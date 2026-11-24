package estabelecimento

import (
	"context"
	"encoding/base64"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fila/internal/midia"
	"fila/internal/platform/busca"
	"fila/internal/platform/database"
	"fila/internal/platform/httpserver"
)

// ---------------------------------------------------------------------------------------------
// listPublicEstablishments: diretório público de barbearias para o cliente encontrar onde entrar
// na fila. Só dados públicos da loja (nome, endereço, logo, localização informada pelo dono) e o
// resumo das filas (abertas, quantas pessoas aguardam, menor espera estimada).
//
// Ordem (estável, com desempate por id):
//   - distance: mais perto primeiro (lojas sem localização no fim). Exige lat/lng do cliente, que
//     vem só nesta consulta: nunca é gravada e não aparece em log (o log registra só a rota).
//   - open: com fila aberta agora primeiro, depois por nome (padrão sem localização).
//   - name: por nome (A–Z).
//
// Paginação por cursor opaco (posição na lista desta ordem). O cursor carrega a busca que o gerou:
// usar com outra busca/ordem é 400 (convencoes.md §7).

type directoryItem struct {
	ID                  uuid.UUID       `json:"id"`
	Slug                string          `json:"slug"`
	Name                string          `json:"name"`
	Palette             string          `json:"palette"`
	PublicAddress       *string         `json:"public_address"`
	Logo                *midia.ImageRef `json:"logo"`
	DistanceMeters      *int            `json:"distance_meters"`
	OpenQueues          int             `json:"open_queues"`
	WaitingCount        int             `json:"waiting_count"`
	ShortestWaitMinutes *int            `json:"shortest_wait_minutes"`

	logoID *uuid.UUID
}

type page struct {
	NextCursor *string `json:"next_cursor"`
	HasMore    bool    `json:"has_more"`
}

const (
	directoryDefaultLimit = 20
	directoryMaxLimit     = 100
	// Raio da Terra (m) para a distância em linha reta (haversine). É "em linha reta", não rota.
	earthRadius = 6371000.0
)

type directoryQuery struct {
	search   string
	sort     string
	lat, lng *float64
	limit    int
	offset   int
}

// fingerprint amarra o cursor à busca que o gerou.
func (d directoryQuery) fingerprint() string {
	coords := ""
	if d.lat != nil {
		coords = fmt.Sprintf("%.5f,%.5f", *d.lat, *d.lng)
	}
	return fmt.Sprintf("%s|%s|%s", d.sort, d.search, coords)
}

func encodeCursor(d directoryQuery, offset int) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(offset) + "|" + d.fingerprint()))
}

func decodeCursor(d directoryQuery, c string) (int, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return 0, false
	}
	off, fp, ok := strings.Cut(string(raw), "|")
	if !ok || fp != d.fingerprint() {
		return 0, false
	}
	n, err := strconv.Atoi(off)
	if err != nil || n < 0 || n > 100000 {
		return 0, false
	}
	return n, true
}

func parseDirectoryQuery(r *http.Request) (directoryQuery, error) {
	q := r.URL.Query()
	d := directoryQuery{limit: directoryDefaultLimit}
	var errs []httpserver.FieldError
	d.search = strings.Join(strings.Fields(q.Get("q")), " ")
	if utf8.RuneCountInString(d.search) > 80 {
		errs = append(errs, httpserver.FieldError{Field: "/q", Code: httpserver.FieldTooLong, Message: "até 80 caracteres"})
	}
	lat, lng := q.Get("lat"), q.Get("lng")
	if (lat == "") != (lng == "") {
		errs = append(errs, httpserver.FieldError{Field: "/lat", Code: httpserver.FieldRequired, Message: "informe lat e lng juntos"})
	} else if lat != "" {
		la, e1 := strconv.ParseFloat(lat, 64)
		ln, e2 := strconv.ParseFloat(lng, 64)
		if e1 != nil || e2 != nil || math.IsNaN(la) || math.IsNaN(ln) || la < -90 || la > 90 || ln < -180 || ln > 180 {
			errs = append(errs, httpserver.FieldError{Field: "/lat", Code: httpserver.FieldOutOfRange, Message: "coordenadas inválidas"})
		} else {
			d.lat, d.lng = &la, &ln
		}
	}
	d.sort = q.Get("sort")
	switch d.sort {
	case "":
		d.sort = "open"
		if d.lat != nil {
			d.sort = "distance"
		}
	case "distance":
		if lat == "" {
			errs = append(errs, httpserver.FieldError{Field: "/sort", Code: httpserver.FieldNotAllowed,
				Message: "ordenar por distância exige lat e lng"})
		}
	case "open", "name":
	default:
		errs = append(errs, httpserver.FieldError{Field: "/sort", Code: httpserver.FieldNotAllowed, Message: "use distance, open ou name"})
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > directoryMaxLimit {
			errs = append(errs, httpserver.FieldError{Field: "/limit", Code: httpserver.FieldOutOfRange, Message: "de 1 a 100"})
		} else {
			d.limit = n
		}
	}
	if len(errs) > 0 {
		return d, httpserver.ErrValidation(errs...)
	}
	if c := q.Get("cursor"); c != "" {
		off, ok := decodeCursor(d, c)
		if !ok {
			return d, httpserver.ErrValidation(httpserver.FieldError{Field: "/cursor", Code: httpserver.FieldInvalidFormat,
				Message: "cursor inválido ou de outra busca"})
		}
		d.offset = off
	}
	return d, nil
}

func (s *Service) listPublicEstablishments(w http.ResponseWriter, r *http.Request) error {
	d, err := parseDirectoryQuery(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	var items []directoryItem
	var more bool
	err = database.InSnapshot(ctx, s.Pool, func(tx pgx.Tx) error {
		var err error
		items, more, err = s.directory(ctx, tx, d)
		return err
	})
	if err != nil {
		return err
	}
	pg := page{HasMore: more}
	if more {
		c := encodeCursor(d, d.offset+len(items))
		pg.NextCursor = &c
	}
	w.Header().Set("Cache-Control", "no-store")
	httpserver.WriteJSON(w, http.StatusOK, struct {
		Data []directoryItem `json:"data"`
		Page page            `json:"page"`
	}{Data: items, Page: pg})
	return nil
}

func (s *Service) directory(ctx context.Context, tx pgx.Tx, d directoryQuery) ([]directoryItem, bool, error) {
	args := []any{}
	arg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	where := "e.status = 'active'"
	if d.search != "" {
		pat := arg(busca.Contains(d.search))
		where += fmt.Sprintf(" AND (%s LIKE %s OR %s LIKE %s)", busca.FoldSQL("e.name"), pat, busca.FoldSQL("coalesce(e.public_address, '')"), pat)
	}
	distance := "NULL::double precision"
	if d.lat != nil {
		la, ln := arg(*d.lat), arg(*d.lng)
		// Haversine em SQL puro (sem extensão): poucas lojas, sem índice espacial necessário.
		distance = fmt.Sprintf(`CASE WHEN e.latitude IS NULL THEN NULL ELSE 2 * %f * asin(sqrt(
			power(sin(radians(e.latitude - %s) / 2), 2) +
			cos(radians(%s)) * cos(radians(e.latitude)) * power(sin(radians(e.longitude - %s) / 2), 2))) END`,
			earthRadius, la, la, ln)
	}
	order := map[string]string{
		"distance": "distance ASC NULLS LAST, oq.open_queues > 0 DESC, lower(e.name), e.id",
		"open":     "oq.open_queues > 0 DESC, lower(e.name), e.id",
		"name":     "lower(e.name), e.id",
	}[d.sort]
	sql := fmt.Sprintf(`
		SELECT e.id, e.slug, e.name, e.palette, e.public_address, e.logo_asset_id, %s AS distance, oq.open_queues
		FROM establishment e
		CROSS JOIN LATERAL (SELECT count(*)::int AS open_queues FROM queue q JOIN professional p ON p.id = q.professional_id
		                     WHERE q.establishment_id = e.id AND q.status = 'open' AND p.status = 'active') oq
		WHERE %s
		ORDER BY %s
		LIMIT %s OFFSET %s`, distance, where, order, arg(d.limit+1), arg(d.offset))
	rows, err := tx.Query(ctx, sql, args...)
	if err != nil {
		return nil, false, err
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (directoryItem, error) {
		var it directoryItem
		var dist *float64
		err := row.Scan(&it.ID, &it.Slug, &it.Name, &it.Palette, &it.PublicAddress, &it.logoID, &dist, &it.OpenQueues)
		if dist != nil {
			m := int(math.Round(*dist))
			it.DistanceMeters = &m
		}
		return it, err
	})
	if err != nil {
		return nil, false, err
	}
	more := len(items) > d.limit
	if more {
		items = items[:d.limit]
	}
	if items == nil {
		items = []directoryItem{}
	}
	if err := s.fillDirectory(ctx, tx, items); err != nil {
		return nil, false, err
	}
	return items, more, nil
}

// fillDirectory completa logo e resumo das filas abertas (pessoas aguardando e menor espera).
func (s *Service) fillDirectory(ctx context.Context, tx pgx.Tx, items []directoryItem) error {
	if len(items) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, len(items))
	var logos []uuid.UUID
	for i, it := range items {
		ids[i] = it.ID
		if it.logoID != nil {
			logos = append(logos, *it.logoID)
		}
	}
	assets, err := midia.Attached(ctx, tx, logos, "public")
	if err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `
		SELECT q.id, q.establishment_id FROM queue q JOIN professional p ON p.id = q.professional_id
		WHERE q.establishment_id = ANY($1) AND q.status = 'open' AND p.status = 'active'`, ids)
	if err != nil {
		return err
	}
	queueOf := map[uuid.UUID]uuid.UUID{}
	var queueIDs []uuid.UUID
	for rows.Next() {
		var qid, eid uuid.UUID
		if err := rows.Scan(&qid, &eid); err != nil {
			rows.Close()
			return err
		}
		queueOf[qid] = eid
		queueIDs = append(queueIDs, qid)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	snaps := map[uuid.UUID]PublicSnapshot{}
	if s.Activity != nil && len(queueIDs) > 0 {
		if snaps, err = s.Activity.PublicSnapshots(ctx, tx, queueIDs, s.Clock.Now().Truncate(time.Second)); err != nil {
			return err
		}
	}
	byEst := map[uuid.UUID]*directoryItem{}
	for i := range items {
		byEst[items[i].ID] = &items[i]
		if items[i].logoID != nil {
			if a, ok := assets[*items[i].logoID]; ok {
				items[i].Logo = a.PublicRef()
			}
		}
	}
	for _, qid := range queueIDs {
		it := byEst[queueOf[qid]]
		snap := snaps[qid] // fila sem ninguém: zero aguardando, espera zero
		it.WaitingCount += snap.WaitingCount
		if it.ShortestWaitMinutes == nil || snap.EstimateMinutes < *it.ShortestWaitMinutes {
			m := snap.EstimateMinutes
			it.ShortestWaitMinutes = &m
		}
	}
	return nil
}
