package estabelecimento

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fila/internal/acesso"
	"fila/internal/auditoria"
	"fila/internal/platform/database"
	"fila/internal/platform/httpserver"
	"fila/internal/platform/idempotency"
	"fila/internal/platform/validate"
)

// Configurações da loja por seção (ADR-0034, docs/configuracoes-da-loja.md). Cada seção tem a
// própria versão: salvar "Filas e atendimento" não conflita com quem salvou "Pagamentos" ao mesmo
// tempo. Sem linha gravada, valem os padrões abaixo (idênticos aos DEFAULT da migration 00026).
const (
	DefaultMaxServiceQuantity = 10
	MinServiceQuantity        = 1
	MaxServiceQuantity        = 50
	DefaultToleranceMinutes   = 10
	MinToleranceMinutes       = 1
	MaxToleranceMinutes       = 60
	MinQueueCapacity          = 1
	MaxQueueCapacity          = 200
)

// Settings são as configurações efetivas de uma loja.
type Settings struct {
	MaxServiceQuantity            int
	ProductsEnabled               bool
	CatalogVersion                int64
	VisitorsEnabled               bool
	CallToleranceMinutes          int
	DefaultQueueCapacity          *int
	ProfessionalsControlAdmission bool
	QueueVersion                  int64
	CashEnabled                   bool
	CardEnabled                   bool
	PixEnabled                    bool
	PaymentsVersion               int64
}

func defaultSettings() Settings {
	return Settings{MaxServiceQuantity: DefaultMaxServiceQuantity, ProductsEnabled: true, CatalogVersion: 1,
		VisitorsEnabled: true, CallToleranceMinutes: DefaultToleranceMinutes, ProfessionalsControlAdmission: true, QueueVersion: 1,
		CashEnabled: true, CardEnabled: true, PixEnabled: true, PaymentsVersion: 1}
}

const settingsSelect = `
	SELECT max_service_quantity, products_enabled, catalog_version, visitors_enabled, call_tolerance_minutes,
	       default_queue_capacity, professionals_control_admission, queue_version, cash_enabled, card_enabled, pix_enabled,
	       payments_version
	FROM establishment_settings WHERE establishment_id = $1`

func scanSettings(row pgx.Row) (Settings, error) {
	var s Settings
	err := row.Scan(&s.MaxServiceQuantity, &s.ProductsEnabled, &s.CatalogVersion, &s.VisitorsEnabled, &s.CallToleranceMinutes,
		&s.DefaultQueueCapacity, &s.ProfessionalsControlAdmission, &s.QueueVersion, &s.CashEnabled, &s.CardEnabled, &s.PixEnabled,
		&s.PaymentsVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return defaultSettings(), nil
	}
	return s, err
}

// LoadSettings devolve as configurações efetivas da loja (padrões quando nunca foram salvas).
// Leitura simples: quem decide sob concorrência (ex.: capacidade) relê dentro da própria
// transação, depois de travar a fila.
func LoadSettings(ctx context.Context, q database.Querier, estID uuid.UUID) (Settings, error) {
	return scanSettings(q.QueryRow(ctx, settingsSelect, estID))
}

// lockSettings garante a linha e a trava (alteração de uma seção).
func lockSettings(ctx context.Context, tx pgx.Tx, estID uuid.UUID, now time.Time) (Settings, error) {
	if _, err := tx.Exec(ctx, `INSERT INTO establishment_settings (establishment_id, updated_at) VALUES ($1, $2)
		ON CONFLICT DO NOTHING`, estID, now); err != nil {
		return Settings{}, err
	}
	return scanSettings(tx.QueryRow(ctx, settingsSelect+` FOR UPDATE`, estID))
}

// ---------------------------------------------------------------------------------------------
// Visões (EstablishmentSettings)

type catalogSettingsView struct {
	MaxServiceQuantity int   `json:"max_service_quantity"`
	ProductsEnabled    bool  `json:"products_enabled"`
	Version            int64 `json:"version"`
}

type queueSettingsView struct {
	VisitorsEnabled               bool  `json:"visitors_enabled"`
	CallToleranceMinutes          int   `json:"call_tolerance_minutes"`
	DefaultQueueCapacity          *int  `json:"default_queue_capacity"`
	ProfessionalsControlAdmission bool  `json:"professionals_control_admission"`
	Version                       int64 `json:"version"`
}

type paymentSettingsView struct {
	CashEnabled bool `json:"cash_enabled"`
	CardEnabled bool `json:"card_enabled"`
	PixEnabled  bool `json:"pix_enabled"`
	// PixAvailable: a integração Pix está operacional neste servidor (sem ela, Pix não é oferecido
	// mesmo que pix_enabled esteja ligado).
	PixAvailable bool  `json:"pix_available"`
	Version      int64 `json:"version"`
}

type settingsView struct {
	Catalog  catalogSettingsView `json:"catalog"`
	Queue    queueSettingsView   `json:"queue"`
	Payments paymentSettingsView `json:"payments"`
}

func (s *Service) view(st Settings) settingsView {
	return settingsView{
		Catalog: catalogSettingsView{MaxServiceQuantity: st.MaxServiceQuantity, ProductsEnabled: st.ProductsEnabled, Version: st.CatalogVersion},
		Queue: queueSettingsView{VisitorsEnabled: st.VisitorsEnabled, CallToleranceMinutes: st.CallToleranceMinutes,
			DefaultQueueCapacity: st.DefaultQueueCapacity, ProfessionalsControlAdmission: st.ProfessionalsControlAdmission,
			Version: st.QueueVersion},
		Payments: paymentSettingsView{CashEnabled: st.CashEnabled, CardEnabled: st.CardEnabled, PixEnabled: st.PixEnabled,
			PixAvailable: s.pixAvailable(), Version: st.PaymentsVersion},
	}
}

func (s *Service) pixAvailable() bool { return s.PixOperational != nil && s.PixOperational() }

// PublicOrdering são as configurações que o cliente precisa ver para montar o pedido e pagar
// (nada financeiro interno): fazem parte da página pública da loja.
type PublicOrdering struct {
	MaxServiceQuantity int      `json:"max_service_quantity"`
	ProductsEnabled    bool     `json:"products_enabled"`
	VisitorsEnabled    bool     `json:"visitors_enabled"`
	PaymentMethods     []string `json:"payment_methods"`
}

// PublicOrderingOf monta as configurações públicas de pedido; pixAvailable diz se a integração
// Pix está operacional neste servidor.
func PublicOrderingOf(st Settings, pixAvailable bool) PublicOrdering {
	methods := []string{}
	if st.PixEnabled && pixAvailable {
		methods = append(methods, "pix")
	}
	if st.CashEnabled {
		methods = append(methods, "cash")
	}
	if st.CardEnabled {
		methods = append(methods, "card")
	}
	return PublicOrdering{MaxServiceQuantity: st.MaxServiceQuantity, ProductsEnabled: st.ProductsEnabled,
		VisitorsEnabled: st.VisitorsEnabled, PaymentMethods: methods}
}

// getSettings: dono da loja (as configurações de pagamento são internas).
func (s *Service) getSettings(w http.ResponseWriter, r *http.Request) error {
	estID, err := pathID(r, "establishmentId")
	if err != nil {
		return err
	}
	if _, err := s.Auth.Owner(r, estID); err != nil {
		return err
	}
	st, err := LoadSettings(r.Context(), s.Pool, estID)
	if err != nil {
		return err
	}
	httpserver.WriteJSON(w, http.StatusOK, httpserver.Data[settingsView]{Data: s.view(st)})
	return nil
}

// ---------------------------------------------------------------------------------------------
// Alteração por seção (PATCH parcial: só os campos enviados mudam)

type catalogSettingsRequest struct {
	ExpectedVersion    *int64 `json:"expected_version"`
	MaxServiceQuantity *int64 `json:"max_service_quantity"`
	ProductsEnabled    *bool  `json:"products_enabled"`
}

type queueSettingsRequest struct {
	ExpectedVersion               *int64      `json:"expected_version"`
	VisitorsEnabled               *bool       `json:"visitors_enabled"`
	CallToleranceMinutes          *int64      `json:"call_tolerance_minutes"`
	DefaultQueueCapacity          optionalInt `json:"default_queue_capacity"`
	ProfessionalsControlAdmission *bool       `json:"professionals_control_admission"`
}

type paymentSettingsRequest struct {
	ExpectedVersion *int64 `json:"expected_version"`
	CashEnabled     *bool  `json:"cash_enabled"`
	CardEnabled     *bool  `json:"card_enabled"`
	PixEnabled      *bool  `json:"pix_enabled"`
}

// updateCatalogSettings (T-ST-01).
func (s *Service) updateCatalogSettings(w http.ResponseWriter, r *http.Request) error {
	var req catalogSettingsRequest
	return s.updateSection(w, r, "updateCatalogSettings", "T-ST-01", "catalog", &req,
		func(v *validate.Collector) int64 {
			if req.MaxServiceQuantity != nil {
				v.IntRange("/max_service_quantity", req.MaxServiceQuantity, MinServiceQuantity, MaxServiceQuantity)
			}
			return v.Version("/expected_version", req.ExpectedVersion)
		},
		func(st *Settings) (int64, bool) {
			changed := false
			if req.MaxServiceQuantity != nil && int(*req.MaxServiceQuantity) != st.MaxServiceQuantity {
				st.MaxServiceQuantity, changed = int(*req.MaxServiceQuantity), true
			}
			if req.ProductsEnabled != nil && *req.ProductsEnabled != st.ProductsEnabled {
				st.ProductsEnabled, changed = *req.ProductsEnabled, true
			}
			return st.CatalogVersion, changed
		}, nil)
}

// updateQueueSettings (T-ST-02). Tolerância nova vale para chamadas futuras; capacidade vale para
// novas entradas e transferências (quem já está na fila não sai).
func (s *Service) updateQueueSettings(w http.ResponseWriter, r *http.Request) error {
	var req queueSettingsRequest
	return s.updateSection(w, r, "updateQueueSettings", "T-ST-02", "queue", &req,
		func(v *validate.Collector) int64 {
			if req.CallToleranceMinutes != nil {
				v.IntRange("/call_tolerance_minutes", req.CallToleranceMinutes, MinToleranceMinutes, MaxToleranceMinutes)
			}
			if req.DefaultQueueCapacity.Value != nil {
				c := int64(*req.DefaultQueueCapacity.Value)
				v.IntRange("/default_queue_capacity", &c, MinQueueCapacity, MaxQueueCapacity)
			}
			return v.Version("/expected_version", req.ExpectedVersion)
		},
		func(st *Settings) (int64, bool) {
			changed := false
			if req.VisitorsEnabled != nil && *req.VisitorsEnabled != st.VisitorsEnabled {
				st.VisitorsEnabled, changed = *req.VisitorsEnabled, true
			}
			if req.CallToleranceMinutes != nil && int(*req.CallToleranceMinutes) != st.CallToleranceMinutes {
				st.CallToleranceMinutes, changed = int(*req.CallToleranceMinutes), true
			}
			if req.DefaultQueueCapacity.Set && !sameInt(req.DefaultQueueCapacity.Value, st.DefaultQueueCapacity) {
				st.DefaultQueueCapacity, changed = req.DefaultQueueCapacity.Value, true
			}
			if req.ProfessionalsControlAdmission != nil && *req.ProfessionalsControlAdmission != st.ProfessionalsControlAdmission {
				st.ProfessionalsControlAdmission, changed = *req.ProfessionalsControlAdmission, true
			}
			return st.QueueVersion, changed
		},
		// A capacidade padrão muda se as filas aceitam entradas: as telas recebem o estado novo.
		func(ctx context.Context, tx pgx.Tx, estID uuid.UUID) error {
			ids, err := queueIDs(ctx, tx, estID)
			if err != nil {
				return err
			}
			for _, id := range ids {
				if err := s.queueChanged(ctx, tx, estID, id); err != nil {
					return err
				}
			}
			return nil
		})
}

// updatePaymentSettings (T-ST-03). Nunca deixa a loja sem meio utilizável; desligar um meio não
// invalida cobranças nem recebimentos existentes (só impede novas escolhas pelo cliente).
func (s *Service) updatePaymentSettings(w http.ResponseWriter, r *http.Request) error {
	var req paymentSettingsRequest
	return s.updateSection(w, r, "updatePaymentSettings", "T-ST-03", "payments", &req,
		func(v *validate.Collector) int64 { return v.Version("/expected_version", req.ExpectedVersion) },
		func(st *Settings) (int64, bool) {
			changed := false
			if req.CashEnabled != nil && *req.CashEnabled != st.CashEnabled {
				st.CashEnabled, changed = *req.CashEnabled, true
			}
			if req.CardEnabled != nil && *req.CardEnabled != st.CardEnabled {
				st.CardEnabled, changed = *req.CardEnabled, true
			}
			if req.PixEnabled != nil && *req.PixEnabled != st.PixEnabled {
				st.PixEnabled, changed = *req.PixEnabled, true
			}
			return st.PaymentsVersion, changed
		}, nil)
}

// updateSection é a parte comum: dono, Idempotency-Key, versão da seção, auditoria.
func (s *Service) updateSection(w http.ResponseWriter, r *http.Request, op, action, section string, req any,
	check func(v *validate.Collector) int64, apply func(st *Settings) (version int64, changed bool),
	after func(ctx context.Context, tx pgx.Tx, estID uuid.UUID) error) error {
	estID, err := pathID(r, "establishmentId")
	if err != nil {
		return err
	}
	actor, err := s.Auth.Owner(r, estID)
	if err != nil {
		return err
	}
	body, err := httpserver.DecodeJSON(r, req)
	if err != nil {
		return err
	}
	var v validate.Collector
	expected := check(&v)
	if err := v.Err(); err != nil {
		return err
	}
	ctx := r.Context()
	cmd := idempotency.Command{ActorKey: actorKey(actor), Operation: op, Body: body}
	return s.idem.Run(w, r, cmd, func(tx pgx.Tx) (idempotency.Result, error) {
		now := s.Clock.Now()
		st, err := lockSettings(ctx, tx, estID, now)
		if err != nil {
			return idempotency.Result{}, err
		}
		next := st
		version, changed := apply(&next)
		if version != expected {
			return idempotency.Result{}, versionConflict(s.view(st))
		}
		if !changed {
			return idempotency.Result{Status: http.StatusOK, Body: httpserver.Data[settingsView]{Data: s.view(st)}}, nil
		}
		if section == "payments" && !next.CashEnabled && !next.CardEnabled && !(next.PixEnabled && s.pixAvailable()) {
			return idempotency.Result{}, &httpserver.Error{Status: 409, Code: "no_payment_method", Title: "A loja precisa de um meio de pagamento",
				Detail: "Mantenha pelo menos um meio que funcione: dinheiro, cartão ou Pix com a integração ligada.", Current: s.view(st)}
		}
		if err := saveSettings(ctx, tx, estID, section, next, now); err != nil {
			return idempotency.Result{}, err
		}
		if err := auditoria.Record(ctx, tx, auditoria.Event{EstablishmentID: &estID, OccurredAt: now, ActorKind: auditoria.ActorOwner,
			ActorID: auditoria.Ptr(actor.AccountID), Action: action, TargetType: "establishment_settings", TargetID: estID,
			ToState: section, RequestID: httpserver.RequestIDFrom(ctx)}); err != nil {
			return idempotency.Result{}, err
		}
		if after != nil {
			if err := after(ctx, tx, estID); err != nil {
				return idempotency.Result{}, err
			}
		}
		saved, err := LoadSettings(ctx, tx, estID)
		if err != nil {
			return idempotency.Result{}, err
		}
		return idempotency.Result{Status: http.StatusOK, Body: httpserver.Data[settingsView]{Data: s.view(saved)}}, nil
	})
}

// saveSettings grava a linha inteira (já travada) e sobe só a versão da seção alterada.
func saveSettings(ctx context.Context, tx pgx.Tx, estID uuid.UUID, section string, st Settings, now time.Time) error {
	_, err := tx.Exec(ctx, `
		UPDATE establishment_settings SET
		       max_service_quantity = $2, products_enabled = $3, visitors_enabled = $4, call_tolerance_minutes = $5,
		       default_queue_capacity = $6, professionals_control_admission = $7, cash_enabled = $8, card_enabled = $9,
		       pix_enabled = $10,
		       catalog_version  = catalog_version  + CASE WHEN $11 = 'catalog'  THEN 1 ELSE 0 END,
		       queue_version    = queue_version    + CASE WHEN $11 = 'queue'    THEN 1 ELSE 0 END,
		       payments_version = payments_version + CASE WHEN $11 = 'payments' THEN 1 ELSE 0 END,
		       updated_at = $12
		WHERE establishment_id = $1`,
		estID, st.MaxServiceQuantity, st.ProductsEnabled, st.VisitorsEnabled, st.CallToleranceMinutes, st.DefaultQueueCapacity,
		st.ProfessionalsControlAdmission, st.CashEnabled, st.CardEnabled, st.PixEnabled, section, now)
	return err
}

func queueIDs(ctx context.Context, q database.Querier, estID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := q.Query(ctx, `SELECT id FROM queue WHERE establishment_id = $1 ORDER BY id`, estID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
}

func sameInt(a, b *int) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// optionalInt distingue "campo ausente" de "null" (desligar o limite) num PATCH.
type optionalInt struct {
	Set   bool
	Value *int
}

func (o *optionalInt) UnmarshalJSON(b []byte) error {
	o.Set = true
	if string(b) == "null" {
		o.Value = nil
		return nil
	}
	var n int
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	o.Value = &n
	return nil
}

// AdmissionAllowedFor informa se o ator pode abrir/fechar a ENTRADA da fila do profissional
// professionalID: o dono sempre; o profissional só a própria fila e só com a permissão ligada.
func AdmissionAllowedFor(actor acesso.Actor, professionalID uuid.UUID, st Settings) bool {
	if actor.IsOwner() {
		return true
	}
	return st.ProfessionalsControlAdmission && actor.ProfessionalID != nil && *actor.ProfessionalID == professionalID
}
