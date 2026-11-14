package api_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"fila/internal/exclusao"
	"fila/internal/operacao"
	"fila/internal/pagamento/pagamentotest"
	"fila/internal/platform/testdb"
)

// backupAndContinue simula um BACKUP: para a API, copia o banco (CREATE DATABASE … TEMPLATE:
// mesmos dados, outro oid — como pg_restore num banco novo) e religa a API no banco ORIGINAL,
// que segue a vida depois do backup. Devolve a URL da cópia (o backup) e a API religada.
func (e *env) backupAndContinue(t *testing.T) (string, *env) {
	t.Helper()
	ctx := context.Background()
	origURL := e.pool.Config().ConnString()
	e.stop()
	e.pool.Close()
	admin := testdb.URL(t)
	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	src, _ := url.Parse(origURL)
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	name := "fila_t_backup_" + hex.EncodeToString(b)
	if _, err := conn.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()+" TEMPLATE "+
		pgx.Identifier{strings.TrimPrefix(src.Path, "/")}.Sanitize()); err != nil {
		t.Fatalf("backup (cópia do banco): %v", err)
	}
	t.Cleanup(func() {
		if c, err := pgx.Connect(context.Background(), admin); err == nil {
			_, _ = c.Exec(context.Background(), "DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")
			_ = c.Close(context.Background())
		}
	})
	u, _ := url.Parse(admin)
	u.Path = "/" + name
	cont := buildEnv(t, testdb.Pool(t, origURL), e.master, e.clock, e.pix, e.pixURL, nil)
	return u.String(), cont
}

// restoreFrom "restaura" o backup: desliga a API atual e sobe outra sobre o banco do backup. O
// simulador de Pix (o mundo lá fora) continua com tudo o que aconteceu depois do backup.
func (e *env) restoreFrom(t *testing.T, backupURL string) *env {
	t.Helper()
	e.stop()
	return buildEnv(t, testdb.Pool(t, backupURL), e.master, e.clock, e.pix, e.pixURL, nil)
}

func rebind(e *env, bs ...*browser) {
	for _, b := range bs {
		b.e = e
	}
}

// C-OPS-01 — banco restaurado não "desfaz" pagamento recebido: o worker fica travado até a
// conciliação com o provedor, que registra o que aconteceu depois do backup, encerra sessões
// ressuscitadas, troca a época do fluxo e reaplica as exclusões do diário. Repetir não duplica.
func TestRestoreReconcilesBeforeWorkerResumes(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	b := e.barbearia(t)
	journal := t.TempDir()

	// --- Antes do backup -------------------------------------------------------------------
	maria := e.verifiedCustomer("maria@exemplo.com") // O1: Pix criado antes; pago depois do backup
	p1, o1 := visitOf(maria.mustStatus(maria.enter(b.id, b.filaBeto, b.corte), 201))
	b.betoB.serve(b.id, b.filaBeto, p1["id"].(string))
	c1 := maria.mustStatus(maria.createPix(b.id, maria.order(b.id, o1["id"]), newIdemKey()), 201).data()

	joao := e.verifiedCustomer("joao@exemplo.com") // O2: aguardando; Pix criado e pago depois do backup
	p2, o2 := visitOf(joao.mustStatus(joao.enter(b.id, b.filaBeto, b.corte), 201))
	b.betoB.serve(b.id, b.filaBeto, p2["id"].(string))

	pedro := e.verifiedCustomer("pedro@exemplo.com") // O4: pago e conferido; estornado depois do backup
	p4, o4 := visitOf(pedro.mustStatus(pedro.enter(b.id, b.filaBeto, b.corte), 201))
	b.betoB.serve(b.id, b.filaBeto, p4["id"].(string))
	c4 := pedro.mustStatus(pedro.createPix(b.id, pedro.order(b.id, o4["id"]), newIdemKey()), 201).data()
	e.pix.Set(e.remote(c4["id"]).ID, "PAID", 0)
	e.reconcile()
	ord4 := b.betoB.order(b.id, o4["id"])
	b.betoB.mustStatus(b.betoB.confer(b.id, ord4, receiptsOf(ord4)[0]["id"]), 200)

	tiago := e.verifiedCustomer("tiago@exemplo.com") // O5: criação do Pix nunca chegou ao provedor
	p5, o5 := visitOf(tiago.mustStatus(tiago.enter(b.id, b.filaBeto, b.corte), 201))
	b.betoB.serve(b.id, b.filaBeto, p5["id"].(string))
	e.pix.SetCreateMode(pagamentotest.CreateDownBefore, 0)
	c5 := tiago.mustStatus(tiago.createPix(b.id, tiago.order(b.id, o5["id"]), newIdemKey()), 202).data()
	e.pix.SetCreateMode(pagamentotest.CreateOK, 0)
	if c5["status"] != "unknown" {
		t.Fatalf("cenário: tentativa deveria ficar desconhecida: %v", c5)
	}

	sofia := e.verifiedCustomer("sofia@exemplo.com")                     // pede exclusão depois do backup
	clara := e.verifiedCustomer("clara@exemplo.com")                     // sessão aberta no backup
	if _, err := operacao.Gate(ctx, e.pool, e.clock.Now()); err != nil { // worker rodando: sinal de vida
		t.Fatal(err)
	}

	backupURL, a := e.backupAndContinue(t)
	rebind(a, maria, joao, pedro, sofia, clara, b.owner, b.betoB, tiago)

	// --- Depois do backup (o mundo segue) --------------------------------------------------
	a.clock.Advance(2 * time.Hour)
	a.pix.Set(a.remote(c1["id"]).ID, "PAID", 0)
	c2 := joao.mustStatus(joao.createPix(b.id, joao.order(b.id, o2["id"]), newIdemKey()), 201).data()
	a.pix.Set(a.remote(c2["id"]).ID, "PAID", 0)
	lucas := a.verifiedCustomer("lucas@exemplo.com") // O3: visita inteira depois do backup
	p3, o3 := visitOf(lucas.mustStatus(lucas.enter(b.id, b.filaBeto, b.barba), 201))
	b.betoB.serve(b.id, b.filaBeto, p3["id"].(string))
	c3 := lucas.mustStatus(lucas.createPix(b.id, lucas.order(b.id, o3["id"]), newIdemKey()), 201).data()
	a.pix.Set(a.remote(c3["id"]).ID, "PAID", 0)
	a.pix.MarkRefunded(a.remote(c4["id"]).ID) // estorno feito no provedor
	sofia.reauth()
	protocol := sofia.mustStatus(sofia.requestDeletion(), 202).data()["protocol"].(string)
	rafa := a.verifiedCustomer("rafa@exemplo.com") // conta nova (não existe no backup) que pede exclusão
	rafa.reauth()
	rafa.mustStatus(rafa.requestDeletion(), 202)
	if n, err := (&exclusao.Journal{Dir: journal}).Export(ctx, a.pool); err != nil || n != 2 {
		t.Fatalf("diário: %d %v", n, err)
	}
	creates := a.pix.Creates()

	// --- Restauração ----------------------------------------------------------------------
	r := a.restoreFrom(t, backupURL)
	rebind(r, maria, joao, pedro, sofia, clara, b.owner, b.betoB, lucas, tiago)
	r.clock.Advance(time.Hour)
	st, err := operacao.Gate(ctx, r.pool, r.clock.Now())
	if err != nil || !st.RestoreHold() {
		t.Fatalf("banco restaurado deveria travar o worker sozinho: %+v %v", st, err)
	}
	if _, err := operacao.Release(ctx, r.pool, r.clock.Now()); !errors.Is(err, operacao.ErrNotReconciled) {
		t.Fatalf("liberar antes de conciliar: %v", err)
	}
	// Antes da conciliação, as sessões do backup valem (uma sessão encerrada depois do backup, numa
	// saída ou troca de senha, voltaria a valer: por isso a conciliação encerra todas).
	if clara.session()["account"] == nil {
		t.Fatal("cenário: a sessão de Clara deveria estar viva no banco restaurado")
	}
	var lines []string
	deps := operacao.RestoreDeps{Pool: r.pool, Clock: r.clock, Log: r.deps.Log, Payments: r.api.Payments,
		Deletion: r.api.Deletion, JournalDir: journal}
	if err := operacao.Reconcile(ctx, deps, func(l string) { lines = append(lines, l) }); err != nil {
		t.Fatalf("conciliação: %v", err)
	}
	t.Log(strings.Join(lines, "\n"))

	// O1: Pix criado antes do backup e pago depois → pagamento registrado.
	// O2: Pix que o backup não tinha → tentativa recriada (created_by restore) e pagamento registrado.
	for _, o := range []any{o1["id"], o2["id"]} {
		if n := r.count(t, `SELECT count(*) FROM receipt WHERE order_id = $1 AND method = 'pix' AND classification = 'principal'`, o); n != 1 {
			t.Fatalf("comanda %v deveria ter o Pix registrado: %d", o, n)
		}
	}
	if n := r.count(t, `SELECT count(*) FROM pix_charge WHERE id = $1 AND created_by = 'restore'`, c2["id"]); n != 1 {
		t.Fatal("tentativa do Pix de João deveria ter sido recriada a partir do provedor")
	}
	// O3: nem a comanda existe no backup → pendência órfã, sem comanda, com a referência do provedor.
	if n := r.count(t, `SELECT count(*) FROM financial_issue WHERE kind = 'restored_orphan_charge' AND order_id IS NULL
		AND source_id = $1 AND provider_reference = $2 AND amount_cents = 3000`, c3["id"], r.remote(c3["id"]).ID); n != 1 {
		t.Fatal("cobrança paga sem comanda no backup deveria virar pendência órfã")
	}
	// O4: estornado no provedor depois do backup → pendência para o dono registrar.
	if n := r.count(t, `SELECT count(*) FROM financial_issue WHERE kind = 'restored_refund_unrecorded' AND order_id = $1`, o4["id"]); n != 1 {
		t.Fatal("estorno feito no provedor deveria virar pendência")
	}
	// Nenhuma cobrança nova foi criada no provedor durante a conciliação: a tentativa de Tiago, que
	// nunca chegou lá, vira falha (o cliente gera outro Pix), em vez de criar um Pix do passado.
	if r.pix.Creates() != creates || chargeStatus(r, c5["id"].(string)) != "failed" {
		t.Fatalf("a conciliação não pode criar cobrança: %d → %d, tentativa %s", creates, r.pix.Creates(), chargeStatus(r, c5["id"].(string)))
	}
	// Exclusão de Sofia reaplicada com o MESMO protocolo; a de Rafa não tem conta no backup.
	if s := r.deletionStatus(protocol); s.status != 200 || s.data()["status"] != "processing" {
		t.Fatalf("protocolo de Sofia depois da restauração: %d %s", s.status, s.raw)
	}
	if !strings.Contains(strings.Join(lines, "\n"), "1 reaplicado(s); 1 de conta inexistente") {
		t.Fatalf("relatório do diário: %v", lines)
	}
	// Todas as sessões do backup encerradas: Clara (e todos) precisam entrar de novo.
	if clara.session()["account"] != nil {
		t.Fatal("sessão do banco restaurado continuou valendo depois da conciliação")
	}
	// O dono vê as pendências (entrando de novo); a órfã aparece sem comanda.
	b.owner.login("ana@exemplo.com", goodPassword, "staff")
	var orphan map[string]any
	for _, is := range r.issues(b.owner, b.id.String(), "open") {
		if is["kind"] == "restored_orphan_charge" {
			orphan = is
		}
	}
	if orphan == nil || orphan["order_id"] != nil || orphan["provider_reference"] == nil {
		t.Fatalf("pendência órfã na API: %v", orphan)
	}

	// Repetir a conciliação não duplica nada.
	counts := func() [3]int {
		return [3]int{r.count(t, `SELECT count(*) FROM receipt`), r.count(t, `SELECT count(*) FROM financial_issue`),
			r.count(t, `SELECT count(*) FROM deletion_request`)}
	}
	before := counts()
	if _, err := operacao.Hold(ctx, r.pool, operacao.HoldRestore, "repetindo a conciliação", r.clock.Now()); err != nil {
		t.Fatal(err)
	}
	if err := operacao.Reconcile(ctx, deps, nil); err != nil {
		t.Fatal(err)
	}
	if after := counts(); after != before {
		t.Fatalf("repetição duplicou: %v → %v", before, after)
	}
	if st, err := operacao.Release(ctx, r.pool, r.clock.Now()); err != nil || st.Held() {
		t.Fatalf("liberar depois de conciliar: %+v %v", st, err)
	}
	if st, _ := operacao.Gate(ctx, r.pool, r.clock.Now()); st.Held() {
		t.Fatal("liberado continua travado")
	}
	// Sem trava de restauração, a conciliação recusa (não mexe em sessões nem pagamentos à toa).
	if err := operacao.Reconcile(ctx, deps, nil); !errors.Is(err, operacao.ErrNoRestoreHold) {
		t.Fatalf("conciliação sem trava: %v", err)
	}
}
