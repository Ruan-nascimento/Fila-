package api_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fila/internal/api"
	"fila/internal/contrato"
	"fila/internal/estabelecimento"
	"fila/internal/exclusao"
)

// ---------------------------------------------------------------------------------------------
// Ajudantes da etapa 07

func (b *browser) requestDeletion() resp {
	b.e.t.Helper()
	return b.cmd("POST", "/v1/me/deletion-request", map[string]any{"confirm_effects": true})
}

func (e *env) deletionStatus(protocol string) resp {
	e.t.Helper()
	return e.browser().do("POST", "/v1/deletion-requests/status", map[string]any{"protocol": protocol})
}

func (e *env) processDeletions() int {
	e.t.Helper()
	n, err := e.api.Deletion.ProcessOnce(context.Background())
	if err != nil {
		e.t.Fatal(err)
	}
	return n
}

func accountIDOf(t *testing.T, e *env, email string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := e.pool.QueryRow(context.Background(), `SELECT id FROM account WHERE email_normalized = $1`, email).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

type fakeBenefits struct {
	mu    sync.Mutex
	calls []uuid.UUID
	fail  error
}

func (f *fakeBenefits) CloseForDeletion(_ context.Context, _ pgx.Tx, accountID uuid.UUID, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return f.fail
	}
	f.calls = append(f.calls, accountID)
	return nil
}

// ---------------------------------------------------------------------------------------------

// C-EXC-01 — exclusão simples: reautenticação, efeitos na hora (sessões, SSE aberto, fila,
// benefícios), protocolo uma vez, "completed" só depois da anonimização, sem dado pessoal.
func TestDeletionSimple(t *testing.T) {
	benefits := &fakeBenefits{}
	e := newEnvDeps(t, func(d *api.Deps) { d.Benefits = benefits })
	b := e.barbearia(t)
	maria := e.verifiedCustomer("maria@exemplo.com")
	acct := accountIDOf(t, e, "maria@exemplo.com")
	pm, om := visitOf(maria.mustStatus(maria.enter(b.id, b.filaBeto, b.corte), 201))
	e.publish()
	stream := maria.stream(b.id, "public,visit", "")
	stream.expect("stream.snapshot")

	if r := maria.cmd("POST", "/v1/me/deletion-request", map[string]any{"confirm_effects": false}); r.status != 400 {
		t.Fatalf("sem confirmar os efeitos: %d", r.status)
	}
	if r := maria.requestDeletion(); r.status != 403 || r.code() != "reauthentication_required" {
		t.Fatalf("sem reautenticação: %d %s", r.status, r.raw)
	}
	// Benefício (pontos/reservas, etapa 10) que falha desfaz TUDO: nada de exclusão pela metade.
	benefits.fail = errors.New("falha simulada")
	maria.reauth()
	if r := maria.requestDeletion(); r.status != 500 {
		t.Fatalf("falha no encerramento de benefícios deveria desfazer: %d", r.status)
	}
	if st, _ := e.partStatus(t, pm["id"].(string)); st != "waiting" || maria.session()["account"] == nil {
		t.Fatal("efeitos aplicados apesar da falha")
	}
	benefits.fail = nil
	key := newIdemKey()
	r := maria.do("POST", "/v1/me/deletion-request", map[string]any{"confirm_effects": true}, "Idempotency-Key", key)
	if r.status != 202 || r.header.Get("Cache-Control") != "no-store" {
		t.Fatalf("pedido: %d %s", r.status, r.raw)
	}
	var created struct{ Data contrato.DeletionRequest }
	strictDecode(t, r.raw, &created)
	protocol := created.Data.Protocol
	if len(protocol) != 32 || created.Data.Status != "processing" || len(created.Data.Blockers) != 0 || created.Data.TargetDays == nil || *created.Data.TargetDays != 30 {
		t.Fatalf("resposta: %s", r.raw)
	}
	// Efeitos imediatos.
	if end := stream.expect("stream.session_ended"); end.data()["reason"] != "session_revoked" {
		t.Fatal("SSE aberto não foi encerrado")
	}
	if maria.session()["account"] != nil {
		t.Fatal("sessão continuou válida")
	}
	if n := e.count(t, `SELECT count(*) FROM account_session WHERE account_id = $1 AND (revoked_at IS NULL OR revoke_reason <> 'account_deletion')`, acct); n != 0 {
		t.Fatalf("%d sessões sem revogação por exclusão", n)
	}
	if st, _ := e.partStatus(t, pm["id"].(string)); st != "cancelled" {
		t.Fatalf("participação: %s", st)
	}
	var closure string
	_ = e.pool.QueryRow(context.Background(), `SELECT closure_reason FROM service_order WHERE id = $1`, om["id"]).Scan(&closure)
	if closure != "cancelled_by_account_deletion" {
		t.Fatalf("comanda: %q", closure)
	}
	if len(benefits.calls) != 1 || benefits.calls[0] != acct {
		t.Fatalf("benefícios encerrados: %v", benefits.calls)
	}
	// Repetição: sessão já revogada → 401, sem efeito novo; a resposta guardada nem tem o protocolo.
	if again := maria.do("POST", "/v1/me/deletion-request", map[string]any{"confirm_effects": true}, "Idempotency-Key", key); again.status != 401 {
		t.Fatalf("repetição: %d", again.status)
	}
	if n := e.count(t, `SELECT count(*) FROM idempotency_record WHERE operation = 'requestAccountDeletion' AND position($1::bytea in response_body) > 0`, []byte(protocol)); n != 0 {
		t.Fatal("protocolo completo guardado na tabela de repetição")
	}
	if n := e.count(t, `SELECT count(*) FROM deletion_request`); n != 1 || len(benefits.calls) != 1 {
		t.Fatalf("efeitos duplicados: %d solicitações, %d encerramentos", n, len(benefits.calls))
	}
	if r := e.browser().login("maria@exemplo.com", goodPassword, "customer"); r.status != 401 {
		t.Fatalf("login durante o processamento: %d", r.status)
	}
	// Situação real pelo protocolo: em processamento até o worker anonimizar.
	if s := e.deletionStatus(protocol); s.data()["status"] != "processing" || strings.Contains(string(s.raw), protocol) {
		t.Fatalf("consulta: %s", s.raw)
	}
	if e.processDeletions() != 1 {
		t.Fatal("worker não concluiu")
	}
	s := e.deletionStatus(protocol)
	if s.data()["status"] != "completed" || s.data()["completed_at"] == nil {
		t.Fatalf("depois do worker: %s", s.raw)
	}
	if e.processDeletions() != 0 {
		t.Fatal("concluiu de novo (não idempotente)")
	}
	// Sem dados pessoais: perfil sobrescrito, nick fora da participação, eventos da conta apagados,
	// evidência só com categorias; comanda continua com valores e pseudônimo.
	var name, email, evidence string
	_ = e.pool.QueryRow(context.Background(), `SELECT name, email FROM account WHERE id = $1`, acct).Scan(&name, &email)
	_ = e.pool.QueryRow(context.Background(), `SELECT evidence::text FROM deletion_request`).Scan(&evidence)
	if name != "Conta excluída" || strings.Contains(email, "maria") || strings.Contains(evidence, "maria") || strings.Contains(evidence, "Pessoa") {
		t.Fatalf("dado pessoal restante: %q %q %s", name, email, evidence)
	}
	if n := e.count(t, `SELECT count(*) FROM participation WHERE account_id = $1 AND nick = 'Pessoa Teste'`, acct); n != 0 {
		t.Fatal("nick continua na participação")
	}
	if n := e.count(t, `SELECT count(*) FROM event_log WHERE account_id = $1`, acct); n != 0 {
		t.Fatal("eventos da conta continuam guardados")
	}
	if n := e.count(t, `SELECT count(*) FROM service_order WHERE id = $1 AND account_id = $2`, om["id"], acct); n != 1 {
		t.Fatal("registro operacional perdido")
	}
	if n := e.count(t, `SELECT count(*) FROM audit_event WHERE action IN ('T-AC-03', 'T-AC-04', 'T-P-10') AND target_id IN ($1, $2)`, acct, pm["id"]); n != 3 {
		t.Fatalf("auditoria: %d", n)
	}
	// O e-mail fica livre: nova conta, sem histórico herdado.
	nova := e.verifiedCustomer("maria@exemplo.com")
	if h := nova.mustStatus(nova.do("GET", "/v1/me/history?establishment_id="+b.id.String(), nil), 200); len(h.body["data"].([]any)) != 0 {
		t.Fatalf("conta nova herdou histórico: %s", h.raw)
	}
	if r := e.deletionStatus("PROTOCOLO-QUE-NAO-EXISTE-0000"); r.status != 404 {
		t.Fatalf("protocolo inexistente: %d", r.status)
	}
}

// Pedidos simultâneos da mesma conta: efeitos uma vez só.
func TestDeletionConcurrentRequests(t *testing.T) {
	e := newEnv(t)
	b := e.barbearia(t)
	maria := e.verifiedCustomer("maria@exemplo.com")
	maria.mustStatus(maria.enter(b.id, b.filaBeto, b.corte), 201)
	maria.reauth()
	res := make([]resp, 3)
	var wg sync.WaitGroup
	for i := range res {
		wg.Go(func() { res[i] = maria.requestDeletion() })
	}
	wg.Wait()
	ok := 0
	for _, r := range res {
		switch {
		case r.status == 202:
			ok++
		case r.status == 409 && r.code() == "deletion_in_progress", r.status == 401:
		default:
			t.Fatalf("resultado inesperado: %d %s", r.status, r.raw)
		}
	}
	if ok != 1 || e.count(t, `SELECT count(*) FROM deletion_request`) != 1 ||
		e.count(t, `SELECT count(*) FROM audit_event WHERE action = 'T-P-10'`) != 1 {
		t.Fatalf("efeitos duplicados (%d aceitos)", ok)
	}
}

// C-EXC-02 — atendimento em curso e pagamento pendente: "processing" com os bloqueios reais; a
// equipe continua trabalhando; nada é perdido; conclui só depois de resolvido.
func TestDeletionWithPendingWork(t *testing.T) {
	e := newEnv(t)
	b := e.barbearia(t)
	joao := e.verifiedCustomer("joao@exemplo.com")
	pj, oj := visitOf(joao.mustStatus(joao.enter(b.id, b.filaBeto, b.corte), 201))
	visit := b.betoB.startServing(b.id, b.filaBeto, pj["id"].(string))
	joao.reauth()
	r := joao.mustStatus(joao.requestDeletion(), 202)
	protocol := r.data()["protocol"].(string)
	if bl := r.data()["blockers"].([]any); len(bl) != 1 || bl[0] != "service_in_progress" {
		t.Fatalf("bloqueios: %s", r.raw)
	}
	if st, _ := e.partStatus(t, pj["id"].(string)); st != "in_service" {
		t.Fatal("atendimento em curso foi mexido")
	}
	if e.processDeletions() != 0 {
		t.Fatal("concluiu com atendimento em curso")
	}
	// A equipe conclui normalmente: agora é pendência financeira.
	b.betoB.mustStatus(b.betoB.closeVisit(b.id, visit, "complete", func(string) string { return "performed" }, nil), 200)
	s := e.deletionStatus(protocol)
	if bl := s.data()["blockers"].([]any); s.data()["status"] != "processing" || len(bl) != 1 || bl[0] != "financial_pending" {
		t.Fatalf("depois da conclusão: %s", s.raw)
	}
	if e.processDeletions() != 0 {
		t.Fatal("concluiu com pagamento pendente")
	}
	// A dona vê a pendência da loja (protocolo mascarado, sem nome nem e-mail).
	l := b.owner.mustStatus(b.owner.do("GET", estPath(b.id, "/deletion-requests"), nil), 200)
	list := l.body["data"].([]any)
	if len(list) != 1 || strings.Contains(string(l.raw), protocol) || strings.Contains(string(l.raw), "joao") ||
		list[0].(map[string]any)["blockers"].([]any)[0] != "financial_pending" {
		t.Fatalf("lista da dona: %s", l.raw)
	}
	if r := b.betoB.do("GET", estPath(b.id, "/deletion-requests"), nil); r.status != 403 {
		t.Fatalf("profissional vendo exclusões: %d", r.status)
	}
	// A pendência é resolvida pela equipe registrando o recebimento (etapa 08), nunca pela exclusão. A exclusão não cria pagamento nem apaga a dívida.
	o := b.owner.order(b.id, oj["id"])
	if o["status"] != "awaiting_payment" || total(o) != 4500 {
		t.Fatalf("comanda depois do pedido de exclusão: %v", o)
	}
	b.betoB.mustStatus(b.betoB.receive(b.id, o, map[string]any{"method": "cash", "amount_cents": 4500, "cash_tendered_cents": 5000}), 201)
	if e.processDeletions() != 1 {
		t.Fatal("não concluiu depois de resolvido")
	}
	if s := e.deletionStatus(protocol); s.data()["status"] != "completed" {
		t.Fatalf("situação final: %s", s.raw)
	}
	var status string
	var tot int64
	_ = e.pool.QueryRow(context.Background(), `SELECT status, total_cents FROM service_order WHERE id = $1`, oj["id"]).Scan(&status, &tot)
	if status != "settled" || tot != 4500 {
		t.Fatalf("registro financeiro alterado: %s %d", status, tot)
	}
	if l := b.owner.mustStatus(b.owner.do("GET", estPath(b.id, "/deletion-requests"), nil), 200); len(l.body["data"].([]any)) != 0 {
		t.Fatalf("lista depois de concluída: %s", l.raw)
	}
}

// C-EXC-05 — dona única não exclui a conta; depois da transferência controlada, exclui. Dois
// donos pedindo ao mesmo tempo: a loja nunca fica sem dono.
func TestDeletionSoleOwner(t *testing.T) {
	e := newEnv(t)
	loja := e.shop("barbearia-teste", "ana@exemplo.com")
	loja.owner.reauth()
	if r := loja.owner.requestDeletion(); r.status != 409 || r.code() != "sole_owner" {
		t.Fatalf("dona única: %d %s", r.status, r.raw)
	}
	if loja.owner.session()["account"] == nil || e.count(t, `SELECT count(*) FROM deletion_request`) != 0 {
		t.Fatal("recusa deixou efeitos")
	}
	// Transferência controlada (operador): convite de dono para Bia.
	if _, err := e.api.Identity.OperatorInviteOwner(context.Background(), loja.id, "Loja", "bia@exemplo.com", "transferência pedida pela Ana"); err != nil {
		t.Fatal(err)
	}
	tok := e.lastToken("bia@exemplo.com", "Convite")
	e.browser().mustStatus(e.browser().do("POST", "/v1/auth/invites/accept", map[string]any{"token": tok, "name": "Bia", "password": goodPassword}), 204)
	bia := e.browser()
	bia.mustStatus(bia.login("bia@exemplo.com", goodPassword, "staff"), 200)
	bia.reauth()
	loja.owner.reauth()
	res := make([]resp, 2)
	var wg sync.WaitGroup
	for i, who := range []*browser{loja.owner, bia} {
		wg.Go(func() { res[i] = who.requestDeletion() })
	}
	wg.Wait()
	accepted, refused := 0, 0
	for _, r := range res {
		switch {
		case r.status == 202:
			accepted++
		case r.status == 409 && r.code() == "sole_owner":
			refused++
		default:
			t.Fatalf("resultado: %d %s", r.status, r.raw)
		}
	}
	if accepted != 1 || refused != 1 {
		t.Fatalf("dois donos ao mesmo tempo: %d aceitos, %d recusados", accepted, refused)
	}
	if n := e.count(t, `SELECT count(*) FROM staff_membership WHERE establishment_id = $1 AND role = 'owner' AND status = 'active'`, loja.id); n != 1 {
		t.Fatalf("loja com %d donos ativos", n)
	}
}

// C-EXC-06 — profissional exclui a conta: perde o acesso na hora; a fila dele fica com a dona,
// que redistribui; depois a exclusão conclui. Alerta da meta de 30 dias (operacional).
func TestDeletionProfessional(t *testing.T) {
	e := newEnv(t)
	b := e.barbearia(t)
	var parts []string
	for _, nick := range []string{"Ana", "Bia"} {
		v := e.visitorIn(b.id, nick)
		p, _ := visitOf(v.mustStatus(v.enter(b.id, b.filaBeto, b.corte), 201))
		parts = append(parts, p["id"].(string))
	}
	b.betoB.reauth()
	r := b.betoB.mustStatus(b.betoB.requestDeletion(), 202)
	if bl := r.data()["blockers"].([]any); len(bl) != 1 || bl[0] != "professional_queue_has_participants" {
		t.Fatalf("bloqueios do profissional: %s", r.raw)
	}
	if q := b.betoB.do("GET", estPath(b.id, "/queues"), nil); q.status != 401 {
		t.Fatalf("profissional manteve acesso: %d", q.status)
	}
	if n := e.count(t, `SELECT count(*) FROM staff_membership WHERE professional_id = $1 AND status = 'active'`, b.beto); n != 0 {
		t.Fatal("vínculo de equipe continua ativo")
	}
	e.clock.Advance(26 * 24 * time.Hour) // perto da meta operacional
	if e.processDeletions() != 0 {
		t.Fatal("concluiu com gente na fila do profissional")
	}
	if !strings.Contains(e.logs.String(), "meta operacional de 30 dias") {
		t.Fatal("sem alerta operacional da meta")
	}
	b.owner.mustStatus(b.owner.login(b.email, goodPassword, "staff"), 200) // 26 dias depois: entra de novo
	l := b.owner.mustStatus(b.owner.do("GET", estPath(b.id, "/deletion-requests"), nil), 200)
	if len(l.body["data"].([]any)) != 1 {
		t.Fatalf("dona não vê a pendência: %s", l.raw)
	}
	b.owner.reauth()
	for _, id := range parts {
		b.owner.mustStatus(b.owner.partCmd(b.id, id, "cancel", map[string]any{"expected_version": 1, "reason": "profissional saiu"}), 200)
	}
	if e.processDeletions() != 1 {
		t.Fatal("não concluiu depois de a dona esvaziar a fila")
	}
}

// C-EXC-03 — anonimização interrompida no meio: nada fica pela metade; a próxima volta conclui
// uma vez só; dois workers ao mesmo tempo não duplicam.
func TestDeletionJobResumes(t *testing.T) {
	var failing atomic.Bool
	failing.Store(true)
	e := newEnvDeps(t, func(d *api.Deps) {
		d.DeletionFault = func(step string) error {
			if step == "depois_das_participacoes" && failing.Load() {
				return errors.New("processo morto (simulado)")
			}
			return nil
		}
	})
	b := e.barbearia(t)
	maria := e.verifiedCustomer("maria@exemplo.com")
	acct := accountIDOf(t, e, "maria@exemplo.com")
	maria.mustStatus(maria.enter(b.id, b.filaBeto, b.corte), 201)
	maria.reauth()
	maria.mustStatus(maria.requestDeletion(), 202)
	if e.processDeletions() != 0 {
		t.Fatal("concluiu apesar da queda")
	}
	var name, status string
	_ = e.pool.QueryRow(context.Background(), `SELECT name, status FROM account WHERE id = $1`, acct).Scan(&name, &status)
	if name != "Pessoa Teste" || status != "deletion_processing" {
		t.Fatalf("anonimização pela metade: %q %q", name, status)
	}
	failing.Store(false)
	var done atomic.Int64
	var wg sync.WaitGroup
	for range 3 {
		wg.Go(func() {
			n, err := e.api.Deletion.ProcessOnce(context.Background())
			if err != nil {
				t.Error(err)
			}
			done.Add(int64(n))
		})
	}
	wg.Wait()
	if done.Load() != 1 || e.count(t, `SELECT count(*) FROM audit_event WHERE action = 'T-AC-04'`) != 1 {
		t.Fatalf("conclusões: %d", done.Load())
	}
	_ = e.pool.QueryRow(context.Background(), `SELECT status FROM account WHERE id = $1`, acct).Scan(&status)
	if status != "deleted" {
		t.Fatalf("estado final: %s", status)
	}
}

// Histórico privado, paginado, isolado por loja e por conta; visitante sem histórico (C-VIS-07).
func TestHistoryPrivateAndPaginated(t *testing.T) {
	e := newEnv(t)
	b := e.barbearia(t)
	outra := e.shop("outra-loja", "bia@exemplo.com")
	maria := e.verifiedCustomer("maria@exemplo.com")
	p1, o1 := visitOf(maria.mustStatus(maria.enter(b.id, b.filaBeto, b.corte, b.barba), 201))
	b.betoB.serve(b.id, b.filaBeto, p1["id"].(string))
	b.betoB.mustStatus(b.betoB.receive(b.id, b.betoB.order(b.id, o1["id"]), map[string]any{"method": "card", "amount_cents": 7500}), 201)
	e.clock.Advance(time.Hour)
	_, o2 := visitOf(maria.mustStatus(maria.enter(b.id, b.filaCaio, b.corte), 201))
	h := maria.mustStatus(maria.do("GET", "/v1/me/history?establishment_id="+b.id.String(), nil), 200)
	var page struct {
		Data []contrato.HistoryEntry
		Page contrato.PageInfo
	}
	strictDecode(t, h.raw, &page)
	if len(page.Data) != 2 || page.Data[0].OrderId.String() != o2["id"] || page.Data[1].ProfessionalName != "Beto" ||
		page.Data[1].Total.AmountCents != 7500 || page.Data[1].ParticipationStatus != "completed" || len(page.Data[1].Items) != 2 {
		t.Fatalf("histórico: %s", h.raw)
	}
	first := maria.mustStatus(maria.do("GET", "/v1/me/history?limit=1&establishment_id="+b.id.String(), nil), 200)
	next := first.body["page"].(map[string]any)["next_cursor"].(string)
	second := maria.mustStatus(maria.do("GET", "/v1/me/history?limit=1&establishment_id="+b.id.String()+"&cursor="+next, nil), 200)
	if second.body["data"].([]any)[0].(map[string]any)["order_id"] != o1["id"] || second.body["page"].(map[string]any)["has_more"] != false {
		t.Fatalf("paginação: %s", second.raw)
	}
	if h := maria.mustStatus(maria.do("GET", "/v1/me/history?establishment_id="+outra.id.String(), nil), 200); len(h.body["data"].([]any)) != 0 {
		t.Fatal("histórico vazou para outra loja")
	}
	for _, who := range []*browser{e.verifiedCustomer("joao@exemplo.com"), b.owner} {
		if h := who.mustStatus(who.do("GET", "/v1/me/history?establishment_id="+b.id.String(), nil), 200); len(h.body["data"].([]any)) != 0 {
			t.Fatalf("outra conta vê histórico alheio: %s", h.raw)
		}
	}
	lia := e.visitorIn(b.id, "Lia")
	if r := lia.do("GET", "/v1/me/history?establishment_id="+b.id.String(), nil); r.status != 401 {
		t.Fatalf("visitante com histórico: %d", r.status)
	}
	if r := maria.do("GET", "/v1/me/history?establishment_id=xyz", nil); r.status != 400 {
		t.Fatalf("loja inválida: %d", r.status)
	}
}

// Minimização: nick de visitante sai das visitas encerradas depois do prazo operacional.
func TestVisitorNickMinimization(t *testing.T) {
	e := newEnv(t)
	b := e.barbearia(t)
	lia := e.visitorIn(b.id, "Lia")
	pl, _ := visitOf(lia.mustStatus(lia.enter(b.id, b.filaBeto, b.corte), 201))
	lia.mustStatus(lia.partCmd(b.id, pl["id"].(string), "cancel", map[string]any{"expected_version": 1}), 200)
	if n, _ := e.api.Deletion.MinimizeVisitorNicks(context.Background(), exclusao.VisitorNickRetention); n != 0 {
		t.Fatal("removeu nick antes do prazo")
	}
	e.clock.Advance(31 * 24 * time.Hour)
	if n, err := e.api.Deletion.MinimizeVisitorNicks(context.Background(), exclusao.VisitorNickRetention); err != nil || n < 2 {
		t.Fatalf("minimização: %d %v", n, err)
	}
	if e.count(t, `SELECT count(*) FROM participation WHERE nick = 'Lia'`)+e.count(t, `SELECT count(*) FROM visitor WHERE nick = 'Lia'`) != 0 {
		t.Fatal("nick continua guardado")
	}
}

// Dois donos ao mesmo tempo, de forma determinística: enquanto a exclusão do primeiro não
// confirma, a checagem do segundo ESPERA (trava dos vínculos de dono) e depois se vê como único.
func TestSoleOwnerCheckWaitsForOtherOwner(t *testing.T) {
	e := newEnv(t)
	loja := e.shop("barbearia-teste", "ana@exemplo.com")
	if _, err := e.api.Identity.OperatorInviteOwner(context.Background(), loja.id, "Loja", "bia@exemplo.com", "segunda dona"); err != nil {
		t.Fatal(err)
	}
	tok := e.lastToken("bia@exemplo.com", "Convite")
	e.browser().mustStatus(e.browser().do("POST", "/v1/auth/invites/accept", map[string]any{"token": tok, "name": "Bia", "password": goodPassword}), 204)
	ana, bia := accountIDOf(t, e, "ana@exemplo.com"), accountIDOf(t, e, "bia@exemplo.com")
	ctx := context.Background()
	first, err := e.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Rollback(ctx) }()
	if sole, err := estabelecimento.SoleOwnerOf(ctx, first, ana); err != nil || len(sole) != 0 {
		t.Fatalf("Ana não é a única: %v %v", sole, err)
	}
	if _, err := estabelecimento.EndMembershipsForDeletion(ctx, first, ana, time.Now()); err != nil {
		t.Fatal(err)
	}
	got := make(chan []uuid.UUID, 1)
	go func() {
		_ = pgx.BeginFunc(ctx, e.pool, func(tx pgx.Tx) error {
			sole, err := estabelecimento.SoleOwnerOf(ctx, tx, bia)
			if err != nil {
				t.Error(err)
			}
			got <- sole
			return nil
		})
	}()
	select {
	case <-got:
		t.Fatal("a checagem da Bia não esperou a exclusão da Ana confirmar")
	case <-time.After(300 * time.Millisecond):
	}
	if err := first.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if sole := <-got; len(sole) != 1 || sole[0] != loja.id {
		t.Fatalf("Bia deveria se ver como única dona: %v", sole)
	}
}
