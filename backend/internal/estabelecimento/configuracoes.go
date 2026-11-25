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

// Forma de atendimento da loja (ADR-0038). Padrão de migração: fila (nada muda sem adesão).
const (
	ModeQueue    = "queue"
	ModeSchedule = "schedule"
	ModeHybrid   = "hybrid"
)

// Limites da agenda (validados no servidor; mesmos CHECK da migration 00029).
const (
	DefaultSlotStep       = 15
	MinSlotStep           = 5
	MaxSlotStep           = 120
	MaxBufferMinutes      = 60
	DefaultMinLead        = 30
	MaxMinLead            = 1440
	DefaultHorizonDays    = 30
	MinHorizonDays        = 1
	MaxHorizonDays        = 180
	DefaultCheckinWindow  = 15
	MaxCheckinWindow      = 60
	DefaultMaxFutureAppts = 3
	MinMaxFutureAppts     = 1
	MaxMaxFutureAppts     = 20
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
	// Agenda (ADR-0038)
	ServiceMode              string
	SlotStepMinutes          int
	BufferMinutes            int
	MinLeadMinutes           int
	HorizonDays              int
	ScheduleToleranceMinutes *int // nil = herda CallToleranceMinutes
	CheckinWindowMinutes     int
	MaxFutureAppointments    int
	ProfessionalsCanBlock    bool
	AgendaVersion            int64
}

// AppointmentTolerance é a tolerância de chegada das reservas (a da agenda ou, sem ela, a da fila).
func (st Settings) AppointmentTolerance() int {
	if st.ScheduleToleranceMinutes != nil {
		return *st.ScheduleToleranceMinutes
	}
	return st.CallToleranceMinutes
}

// AcceptsQueue / AcceptsSchedule: a forma de atendimento admite NOVAS entradas na fila / reservas.
// Mudar o modo fecha só a admissão correspondente: o que já foi aceito continua sendo atendido.
func (st Settings) AcceptsQueue() bool { return st.ServiceMode != ModeSchedule }
func (st Settings) AcceptsSchedule() bool {
	return st.ServiceMode == ModeSchedule || st.ServiceMode == ModeHybrid
}

func defaultSettings() Settings {
	return Settings{MaxServiceQuantity: DefaultMaxServiceQuantity, ProductsEnabled: true, CatalogVersion: 1,
		VisitorsEnabled: true, CallToleranceMinutes: DefaultToleranceMinutes, ProfessionalsControlAdmission: true, QueueVersion: 1,
		CashEnabled: true, CardEnabled: true, PixEnabled: true, PaymentsVersion: 1,
		ServiceMode: ModeQueue, SlotStepMinutes: DefaultSlotStep, MinLeadMinutes: DefaultMinLead, HorizonDays: DefaultHorizonDays,
		CheckinWindowMinutes: DefaultCheckinWindow, MaxFutureAppointments: DefaultMaxFutureAppts, AgendaVersion: 1}
}

const settingsSelect = `
	SELECT max_service_quantity, products_enabled, catalog_version, visitors_enabled, call_tolerance_minutes,
	       default_queue_capacity, professionals_control_admission, queue_version, cash_enabled, card_enabled, pix_enabled,
	       payments_version, service_mode, slot_step_minutes, buffer_minutes, min_lead_minutes, horizon_days,
	       schedule_tolerance_minutes, checkin_window_minutes, max_future_appointments, professionals_can_block, agenda_version
	FROM establishment_settings WHERE establishment_id = $1`

func scanSettings(row pgx.Row) (Settings, error) {
	var s Settings
	err := row.Scan(&s.MaxServiceQuantity, &s.ProductsEnabled, &s.CatalogVersion, &s.VisitorsEnabled, &s.CallToleranceMinutes,
		&s.DefaultQueueCapacity, &s.ProfessionalsControlAdmission, &s.QueueVersion, &s.CashEnabled, &s.CardEnabled, &s.PixEnabled,
		&s.PaymentsVersion, &s.ServiceMode, &s.SlotStepMinutes, &s.BufferMinutes, &s.MinLeadMinutes, &s.HorizonDays,
		&s.ScheduleToleranceMinutes, &s.CheckinWindowMinutes, &s.MaxFutureAppointments, &s.ProfessionalsCanBlock, &s.AgendaVersion)
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

type agendaSettingsView struct {
	ServiceMode              string `json:"service_mode"`
	SlotStepMinutes          int    `json:"slot_step_minutes"`
	BufferMinutes            int    `json:"buffer_minutes"`
	MinLeadMinutes           int    `json:"min_lead_minutes"`
	HorizonDays              int    `json:"horizon_days"`
	ScheduleToleranceMinutes *int   `json:"schedule_tolerance_minutes"`
	CheckinWindowMinutes     int    `json:"checkin_window_minutes"`
	MaxFutureAppointments    int    `json:"max_future_appointments"`
	ProfessionalsCanBlock    bool   `json:"professionals_can_block"`
	// Available: a agenda está liberada neste servidor (FILA_AGENDA). Sem ela, o modo fica "fila".
	Available bool  `json:"available"`
	Version   int64 `json:"version"`
}

type settingsView struct {
	Catalog  catalogSettingsView `json:"catalog"`
	Queue    queueSettingsView   `json:"queue"`
	Payments paymentSettingsView `json:"payments"`
	Agenda   agendaSettingsView  `json:"agenda"`
}

func (s *Service) view(st Settings) settingsView {
	return settingsView{
		Catalog: catalogSettingsView{MaxServiceQuantity: st.MaxServiceQuantity, ProductsEnabled: st.ProductsEnabled, Version: st.CatalogVersion},
		Queue: queueSettingsView{VisitorsEnabled: st.VisitorsEnabled, CallToleranceMinutes: st.CallToleranceMinutes,
			DefaultQueueCapacity: st.DefaultQueueCapacity, ProfessionalsControlAdmission: st.ProfessionalsControlAdmission,
			Version: st.QueueVersion},
		Payments: paymentSettingsView{CashEnabled: st.CashEnabled, CardEnabled: st.CardEnabled, PixEnabled: st.PixEnabled,
			PixAvailable: s.pixAvailable(), Version: st.PaymentsVersion},
		Agenda: agendaSettingsView{ServiceMode: st.ServiceMode, SlotStepMinutes: st.SlotStepMinutes, BufferMinutes: st.BufferMinutes,
			MinLeadMinutes: st.MinLeadMinutes, HorizonDays: st.HorizonDays, ScheduleToleranceMinutes: st.ScheduleToleranceMinutes,
			CheckinWindowMinutes: st.CheckinWindowMinutes, MaxFutureAppointments: st.MaxFutureAppointments,
			ProfessionalsCanBlock: st.ProfessionalsCanBlock, Available: s.AgendaEnabled, Version: st.AgendaVersion},
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
	// ServiceMode: forma de atendimento (queue | schedule | hybrid) que a página pública oferece.
	ServiceMode string `json:"service_mode"`
}

// PublicOrderingOf monta as configurações públicas de pedido; pixAvailable diz se a integração
// Pix está operacional neste servidor. Sem a agenda liberada no servidor, o modo público é fila.
func PublicOrderingOf(st Settings, pixAvailable, agendaEnabled bool) PublicOrdering {
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
	mode := st.ServiceMode
	if !agendaEnabled {
		mode = ModeQueue
	}
	return PublicOrdering{MaxServiceQuantity: st.MaxServiceQuantity, ProductsEnabled: st.ProductsEnabled,
		VisitorsEnabled: st.VisitorsEnabled, PaymentMethods: methods, ServiceMode: mode}
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
		if section == "agenda" && next.ServiceMode != st.ServiceMode && next.ServiceMode != ModeQueue && !s.AgendaEnabled {
			return idempotency.Result{}, &httpserver.Error{Status: 409, Code: "agenda_unavailable", Title: "Agenda ainda não liberada",
				Detail: "A agenda ainda não foi liberada para esta instalação. A loja continua atendendo por ordem de chegada.", Current: s.view(st)}
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
		       agenda_version   = agenda_version   + CASE WHEN $11 = 'agenda'   THEN 1 ELSE 0 END,
		       updated_at = $12,
		       service_mode = $13, slot_step_minutes = $14, buffer_minutes = $15, min_lead_minutes = $16, horizon_days = $17,
		       schedule_tolerance_minutes = $18, checkin_window_minutes = $19, max_future_appointments = $20,
		       professionals_can_block = $21
		WHERE establishment_id = $1`,
		estID, st.MaxServiceQuantity, st.ProductsEnabled, st.VisitorsEnabled, st.CallToleranceMinutes, st.DefaultQueueCapacity,
		st.ProfessionalsControlAdmission, st.CashEnabled, st.CardEnabled, st.PixEnabled, section, now,
		st.ServiceMode, st.SlotStepMinutes, st.BufferMinutes, st.MinLeadMinutes, st.HorizonDays, st.ScheduleToleranceMinutes,
		st.CheckinWindowMinutes, st.MaxFutureAppointments, st.ProfessionalsCanBlock)
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

// ---------------------------------------------------------------------------------------------
// Agenda (T-ST-04, ADR-0038)

type agendaSettingsRequest struct {
	ExpectedVersion          *int64      `json:"expected_version"`
	ServiceMode              *string     `json:"service_mode"`
	SlotStepMinutes          *int64      `json:"slot_step_minutes"`
	BufferMinutes            *int64      `json:"buffer_minutes"`
	MinLeadMinutes           *int64      `json:"min_lead_minutes"`
	HorizonDays              *int64      `json:"horizon_days"`
	ScheduleToleranceMinutes optionalInt `json:"schedule_tolerance_minutes"`
	CheckinWindowMinutes     *int64      `json:"checkin_window_minutes"`
	MaxFutureAppointments    *int64      `json:"max_future_appointments"`
	ProfessionalsCanBlock    *bool       `json:"professionals_can_block"`
}

// updateAgendaSettings (T-ST-04). Mudar a forma de atendimento fecha só a admissão correspondente:
// reservas e entradas já aceitas continuam valendo e são atendidas normalmente. Regras novas valem
// para reservas FUTURAS (cada reserva guarda a própria tolerância, margem e janela de chegada).
func (s *Service) updateAgendaSettings(w http.ResponseWriter, r *http.Request) error {
	var req agendaSettingsRequest
	return s.updateSection(w, r, "updateAgendaSettings", "T-ST-04", "agenda", &req,
		func(v *validate.Collector) int64 {
			if req.ServiceMode != nil && *req.ServiceMode != ModeQueue && *req.ServiceMode != ModeSchedule && *req.ServiceMode != ModeHybrid {
				v.Add(&httpserver.FieldError{Field: "/service_mode", Code: httpserver.FieldNotAllowed, Message: "use queue, schedule ou hybrid"})
			}
			if req.SlotStepMinutes != nil {
				v.IntRange("/slot_step_minutes", req.SlotStepMinutes, MinSlotStep, MaxSlotStep)
			}
			if req.BufferMinutes != nil {
				v.IntRange("/buffer_minutes", req.BufferMinutes, 0, MaxBufferMinutes)
			}
			if req.MinLeadMinutes != nil {
				v.IntRange("/min_lead_minutes", req.MinLeadMinutes, 0, MaxMinLead)
			}
			if req.HorizonDays != nil {
				v.IntRange("/horizon_days", req.HorizonDays, MinHorizonDays, MaxHorizonDays)
			}
			if req.ScheduleToleranceMinutes.Value != nil {
				t := int64(*req.ScheduleToleranceMinutes.Value)
				v.IntRange("/schedule_tolerance_minutes", &t, MinToleranceMinutes, MaxToleranceMinutes)
			}
			if req.CheckinWindowMinutes != nil {
				v.IntRange("/checkin_window_minutes", req.CheckinWindowMinutes, 0, MaxCheckinWindow)
			}
			if req.MaxFutureAppointments != nil {
				v.IntRange("/max_future_appointments", req.MaxFutureAppointments, MinMaxFutureAppts, MaxMaxFutureAppts)
			}
			return v.Version("/expected_version", req.ExpectedVersion)
		},
		func(st *Settings) (int64, bool) {
			changed := false
			setInt := func(dst *int, v *int64) {
				if v != nil && int(*v) != *dst {
					*dst, changed = int(*v), true
				}
			}
			if req.ServiceMode != nil && *req.ServiceMode != st.ServiceMode {
				st.ServiceMode, changed = *req.ServiceMode, true
			}
			setInt(&st.SlotStepMinutes, req.SlotStepMinutes)
			setInt(&st.BufferMinutes, req.BufferMinutes)
			setInt(&st.MinLeadMinutes, req.MinLeadMinutes)
			setInt(&st.HorizonDays, req.HorizonDays)
			setInt(&st.CheckinWindowMinutes, req.CheckinWindowMinutes)
			setInt(&st.MaxFutureAppointments, req.MaxFutureAppointments)
			if req.ScheduleToleranceMinutes.Set && !sameInt(req.ScheduleToleranceMinutes.Value, st.ScheduleToleranceMinutes) {
				st.ScheduleToleranceMinutes, changed = req.ScheduleToleranceMinutes.Value, true
			}
			if req.ProfessionalsCanBlock != nil && *req.ProfessionalsCanBlock != st.ProfessionalsCanBlock {
				st.ProfessionalsCanBlock, changed = *req.ProfessionalsCanBlock, true
			}
			return st.AgendaVersion, changed
		},
		// A forma de atendimento muda o que as filas aceitam: as telas recebem o estado novo.
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

// AgendaImpactView: o que a troca de forma de atendimento afeta agora (mostrado ANTES de salvar).
type agendaImpactView struct {
	FutureAppointments int `json:"future_appointments"`
	WaitingInQueues    int `json:"waiting_in_queues"`
}

// getAgendaImpact: quantas reservas futuras e quantas pessoas na fila existem (dono).
func (s *Service) getAgendaImpact(w http.ResponseWriter, r *http.Request) error {
	estID, err := pathID(r, "establishmentId")
	if err != nil {
		return err
	}
	if _, err := s.Auth.Owner(r, estID); err != nil {
		return err
	}
	ctx := r.Context()
	var v agendaImpactView
	if s.AgendaImpact != nil {
		if v.FutureAppointments, err = s.AgendaImpact(ctx, s.Pool, estID, s.Clock.Now()); err != nil {
			return err
		}
	}
	ids, err := queueIDs(ctx, s.Pool, estID)
	if err != nil {
		return err
	}
	for _, id := range ids {
		n, err := s.pendingInQueue(ctx, s.Pool, id)
		if err != nil {
			return err
		}
		v.WaitingInQueues += n
	}
	httpserver.WriteJSON(w, http.StatusOK, httpserver.Data[agendaImpactView]{Data: v})
	return nil
}
