package outbox

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fila/internal/platform/database"
)

// Published é um evento já publicado (event_log), com a posição definitiva.
type Published struct {
	Position        int64
	EventID         uuid.UUID
	EstablishmentID uuid.UUID
	Audience        string
	ProfessionalID  *uuid.UUID
	AccountID       *uuid.UUID
	VisitorID       *uuid.UUID
	Type            string
	ResourceType    string
	ResourceID      uuid.UUID
	ResourceVersion int64
	Payload         json.RawMessage
	OccurredAt      time.Time
}

// Grants diz o que UMA conexão pode receber (calculado pelo servidor a partir da sessão).
type Grants struct {
	Public         bool
	Owner          bool
	ProfessionalID *uuid.UUID // audiência "professional": só eventos deste profissional
	VisitAccount   *uuid.UUID // audiência "visit": visitas desta conta…
	VisitVisitor   *uuid.UUID // …ou deste visitante
	Account        *uuid.UUID // audiência "account": eventos da própria conta (pontos, etapa 10)
}

func same(a, b *uuid.UUID) bool { return a != nil && b != nil && *a == *b }

// Allows decide se o evento pode ir para a conexão. Negação por padrão: audiência desconhecida
// ou sem o filtro correspondente não passa.
func (g Grants) Allows(e Published) bool {
	switch e.Audience {
	case Public:
		return g.Public
	case Owner:
		return g.Owner
	case Professional:
		return same(g.ProfessionalID, e.ProfessionalID)
	case Visit:
		return same(g.VisitAccount, e.AccountID) || same(g.VisitVisitor, e.VisitorID)
	case Account:
		return same(g.Account, e.AccountID)
	}
	return false
}

// Covers informa se g dá pelo menos tudo o que other dava (para decidir se a conexão continua
// depois de reconferir a sessão).
func (g Grants) Covers(other Grants) bool {
	eq := func(a, b *uuid.UUID) bool { return b == nil || same(a, b) }
	return (g.Public || !other.Public) && (g.Owner || !other.Owner) && eq(g.ProfessionalID, other.ProfessionalID) &&
		eq(g.VisitAccount, other.VisitAccount) && eq(g.VisitVisitor, other.VisitVisitor) && eq(g.Account, other.Account)
}

// Channels lista as audiências concedidas (para o snapshot).
func (g Grants) Channels() []string {
	var out []string
	if g.Public {
		out = append(out, Public)
	}
	if g.VisitAccount != nil || g.VisitVisitor != nil {
		out = append(out, Visit)
	}
	if g.Account != nil {
		out = append(out, Account)
	}
	if g.ProfessionalID != nil {
		out = append(out, Professional)
	}
	if g.Owner {
		out = append(out, Owner)
	}
	return out
}

// sqlFilter traduz Grants para SQL parametrizado (mesma regra de Allows).
func (g Grants) sqlFilter(args *[]any) string {
	arg := func(v any) string {
		*args = append(*args, v)
		return "$" + strconv.Itoa(len(*args))
	}
	var or []string
	if g.Public {
		or = append(or, `audience = 'public'`)
	}
	if g.Owner {
		or = append(or, `audience = 'owner'`)
	}
	if g.ProfessionalID != nil {
		or = append(or, `(audience = 'professional' AND professional_id = `+arg(*g.ProfessionalID)+`)`)
	}
	if g.VisitAccount != nil {
		or = append(or, `(audience = 'visit' AND account_id = `+arg(*g.VisitAccount)+`)`)
	}
	if g.VisitVisitor != nil {
		or = append(or, `(audience = 'visit' AND visitor_id = `+arg(*g.VisitVisitor)+`)`)
	}
	if g.Account != nil {
		or = append(or, `(audience = 'account' AND account_id = `+arg(*g.Account)+`)`)
	}
	if len(or) == 0 {
		return "false"
	}
	return "(" + strings.Join(or, " OR ") + ")"
}

const publishedSelect = `
	SELECT position, event_id, establishment_id, audience, professional_id, account_id, visitor_id, type,
	       resource_type, resource_id, resource_version, payload, occurred_at
	FROM event_log`

func scanPublished(r pgx.CollectableRow) (Published, error) {
	var e Published
	err := r.Scan(&e.Position, &e.EventID, &e.EstablishmentID, &e.Audience, &e.ProfessionalID, &e.AccountID, &e.VisitorID,
		&e.Type, &e.ResourceType, &e.ResourceID, &e.ResourceVersion, &e.Payload, &e.OccurredAt)
	return e, err
}

// ReadAfter lê, em ordem, até limit eventos DESTA loja com posição > after que a conexão pode
// receber. O filtro de audiência é aplicado no banco (replay nunca devolve evento alheio).
func ReadAfter(ctx context.Context, q database.Querier, estID uuid.UUID, after int64, g Grants, limit int) ([]Published, error) {
	args := []any{estID, after, limit}
	sql := publishedSelect + ` WHERE establishment_id = $1 AND position > $2 AND ` + g.sqlFilter(&args) + ` ORDER BY position LIMIT $3`
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, scanPublished)
}

// readAll lê todos os eventos (de qualquer loja) depois de after — usado pelo Hub.
func readAll(ctx context.Context, q database.Querier, after int64, limit int) ([]Published, error) {
	rows, err := q.Query(ctx, publishedSelect+` WHERE position > $1 ORDER BY position LIMIT $2`, after, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, scanPublished)
}

// Head é o estado do fluxo visto por uma leitura: época, última posição publicada e a mais
// antiga ainda guardada (abaixo dela os eventos já foram apagados pela retenção).
type Head struct {
	Epoch  uuid.UUID
	Last   int64
	Oldest int64
}

// ReadHead lê o estado do fluxo. Dentro de uma transação REPEATABLE READ, vale para o mesmo
// instante dos outros dados lidos nela (snapshot + cursor coerentes, eventos-sse.md §6).
func ReadHead(ctx context.Context, q database.Querier) (Head, error) {
	var h Head
	err := q.QueryRow(ctx, `
		SELECT s.epoch, s.last_position, coalesce((SELECT min(position) FROM event_log), s.last_position + 1)
		FROM event_stream_state s WHERE s.id = 1`).Scan(&h.Epoch, &h.Last, &h.Oldest)
	return h, err
}

// Cursor é a posição de leitura de um cliente: época + posição (não é credencial).
type Cursor struct {
	Epoch    uuid.UUID
	Position int64
}

// Encode devolve o cursor opaco (base64url de 16 bytes de época + 8 de posição).
func (c Cursor) Encode() string {
	b := make([]byte, 24)
	copy(b, c.Epoch[:])
	binary.BigEndian.PutUint64(b[16:], uint64(c.Position))
	return base64.RawURLEncoding.EncodeToString(b)
}

// DecodeCursor lê um cursor; ok=false para texto ilegível.
func DecodeCursor(s string) (Cursor, bool) {
	if len(s) == 0 || len(s) > 128 {
		return Cursor{}, false
	}
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || len(b) != 24 {
		return Cursor{}, false
	}
	var c Cursor
	copy(c.Epoch[:], b[:16])
	pos := binary.BigEndian.Uint64(b[16:])
	if pos > 1<<62 {
		return Cursor{}, false
	}
	c.Position = int64(pos)
	return c, true
}

// Resume decide como continuar a partir de um cursor do cliente:
//   - "" (vazio) → retomar: o cliente recebe só os eventos depois do cursor;
//   - senão, o motivo do snapshot (StreamSnapshot.reason).
func (h Head) Resume(raw string) string {
	if raw == "" {
		return "initial"
	}
	c, ok := DecodeCursor(raw)
	switch {
	case !ok:
		return "cursor_invalid"
	case c.Epoch != h.Epoch:
		return "epoch_changed"
	case c.Position > h.Last:
		return "cursor_invalid" // posição que ainda não existe
	case c.Position+1 < h.Oldest:
		return "cursor_expired" // eventos depois dele já foram apagados (retenção)
	}
	return ""
}

// CurrentCursor devolve o cursor do instante da leitura (SnapshotCursor das listagens).
func CurrentCursor(ctx context.Context, q database.Querier) (string, error) {
	h, err := ReadHead(ctx, q)
	if err != nil {
		return "", err
	}
	return Cursor{Epoch: h.Epoch, Position: h.Last}.Encode(), nil
}

// ForgetAccount apaga os eventos ainda guardados que pertencem à conta (audiências visit e
// account, com a visão da própria pessoa). Usado na anonimização; eventos da equipe expiram com a
// retenção de 24 h. Devolve quantos foram apagados.
func ForgetAccount(ctx context.Context, q database.Querier, accountID uuid.UUID) (int64, error) {
	a, err := q.Exec(ctx, `DELETE FROM event_log WHERE account_id = $1`, accountID)
	if err != nil {
		return 0, err
	}
	b, err := q.Exec(ctx, `DELETE FROM outbox WHERE account_id = $1`, accountID)
	if err != nil {
		return 0, err
	}
	return a.RowsAffected() + b.RowsAffected(), nil
}
