package api_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"fila/internal/contrato"
)

func estPath(id uuid.UUID, suffix string) string {
	return "/v1/establishments/" + id.String() + suffix
}

// C-EST-01 — cada profissional tem uma fila, criada fechada; letra única na loja.
func TestCreateProfessionalCreatesClosedQueue(t *testing.T) {
	e := newEnv(t)
	loja := e.shop("barbearia-teste", "ana@exemplo.com")
	key := newIdemKey()
	r := loja.owner.mustStatus(loja.owner.do("POST", estPath(loja.id, "/professionals"),
		map[string]any{"display_name": "Beto", "queue_letter": "A"}, "Idempotency-Key", key), 201)
	var prof struct{ Data contrato.Professional }
	strictDecode(t, r.raw, &prof)
	if prof.Data.Status != "active" || prof.Data.HasAccess || prof.Data.QueueLetter != "A" || prof.Data.Version != 1 || prof.Data.PendingInvite != nil {
		t.Fatalf("profissional criado: %+v", prof.Data)
	}
	again := loja.owner.mustStatus(loja.owner.do("POST", estPath(loja.id, "/professionals"),
		map[string]any{"display_name": "Beto", "queue_letter": "A"}, "Idempotency-Key", key), 201)
	if again.header.Get("Idempotent-Replayed") != "true" {
		t.Fatal("repetição deveria devolver a resposta guardada")
	}
	var queues struct {
		Data   []contrato.StaffQueue
		Cursor *string
	}
	strictDecode(t, loja.owner.mustStatus(loja.owner.do("GET", estPath(loja.id, "/queues"), nil), 200).raw, &queues)
	if len(queues.Data) != 1 || queues.Data[0].Status != "closed" || queues.Data[0].QueueId != prof.Data.QueueId ||
		queues.Data[0].Professional.Id != prof.Data.Id || len(queues.Data[0].Entries) != 0 || queues.Cursor != nil {
		t.Fatalf("filas: %+v", queues)
	}

	r = loja.owner.cmd("POST", estPath(loja.id, "/professionals"), map[string]any{"display_name": "Outro", "queue_letter": "A"})
	if r.status != 400 || r.code() != "validation_failed" || !strings.Contains(string(r.raw), `"/queue_letter"`) {
		t.Fatalf("letra repetida: %d %s", r.status, r.raw)
	}
	for _, bad := range []map[string]any{
		{"display_name": "X", "queue_letter": "a"},
		{"display_name": "X", "queue_letter": "AB"},
		{"display_name": "", "queue_letter": "B"},
		{"display_name": strings.Repeat("x", 41), "queue_letter": "B"},
		{"display_name": "X", "queue_letter": "B", "status": "inactive"},
	} {
		if r := loja.owner.cmd("POST", estPath(loja.id, "/professionals"), bad); r.status != 400 {
			t.Errorf("%v: %d %s", bad, r.status, r.raw)
		}
	}
	var n int
	_ = e.pool.QueryRow(context.Background(), `SELECT count(*) FROM queue`).Scan(&n)
	if n != 1 {
		t.Fatalf("filas no banco = %d, esperado 1", n)
	}
}

// C-EST-03 — só a dona abre, pausa e fecha; o profissional só sinaliza indisponibilidade.
func TestQueueStatesAndPermissions(t *testing.T) {
	e := newEnv(t)
	loja := e.shop("barbearia-teste", "ana@exemplo.com")
	beto := e.professional(loja.id, "Beto")
	caio := e.professional(loja.id, "Caio")
	q := e.queueOf(beto)
	betoB := e.staffMember(loja, beto, "beto@exemplo.com")
	caioB := e.staffMember(loja, caio, "caio@exemplo.com")
	act := func(b *browser, op string, version int64) resp {
		return b.cmd("POST", estPath(loja.id, "/queues/"+q.String()+"/"+op), map[string]any{"expected_version": version})
	}
	for _, op := range []string{"open", "pause", "close"} {
		if r := act(betoB, op, 1); r.status != 403 || r.code() != "forbidden" {
			t.Fatalf("profissional %s: %d %s", op, r.status, r.raw)
		}
	}
	// Sinal de indisponibilidade: não muda o estado.
	r := betoB.mustStatus(betoB.cmd("POST", estPath(loja.id, "/queues/"+q.String()+"/availability-signal"),
		map[string]any{"unavailable": true, "note": "Almoço até 14h"}), 200)
	var sq struct{ Data contrato.StaffQueue }
	strictDecode(t, r.raw, &sq)
	if sq.Data.Status != "closed" || !sq.Data.UnavailableSignal.Active || sq.Data.UnavailableSignal.Note == nil || sq.Data.Version != 1 {
		t.Fatalf("sinal: %+v", sq.Data)
	}
	for name, b := range map[string]*browser{"dona": loja.owner, "outro profissional": caioB} {
		if r := b.cmd("POST", estPath(loja.id, "/queues/"+q.String()+"/availability-signal"), map[string]any{"unavailable": true}); r.status != 403 {
			t.Fatalf("%s sinalizando a fila de Beto: %d %s", name, r.status, r.raw)
		}
	}
	// Painel da dona mostra o sinal.
	var all struct {
		Data   []contrato.StaffQueue
		Cursor *string
	}
	strictDecode(t, loja.owner.do("GET", estPath(loja.id, "/queues"), nil).raw, &all)
	if len(all.Data) != 2 || !all.Data[0].UnavailableSignal.Active {
		t.Fatalf("painel da dona: %+v", all.Data)
	}
	// Profissional vê só a própria fila.
	var own struct {
		Data   []contrato.StaffQueue
		Cursor *string
	}
	strictDecode(t, betoB.do("GET", estPath(loja.id, "/queues"), nil).raw, &own)
	if len(own.Data) != 1 || own.Data[0].QueueId.String() != q.String() {
		t.Fatalf("profissional deveria ver só a própria fila: %+v", own.Data)
	}

	// Dona: fechada → aberta → pausada → aberta → fechada, com versão esperada.
	open := loja.owner.mustStatus(act(loja.owner, "open", 1), 200)
	if open.data()["status"] != "open" || num(open.data()["version"]) != 2 {
		t.Fatalf("abrir: %s", open.raw)
	}
	if r := act(loja.owner, "open", 2); r.status != 409 || r.code() != "invalid_transition" {
		t.Fatalf("abrir fila aberta: %d %s", r.status, r.raw)
	}
	if r := act(loja.owner, "pause", 1); r.status != 409 || r.code() != "version_conflict" || r.body["current"].(map[string]any)["status"] != "open" {
		t.Fatalf("versão antiga: %d %s", r.status, r.raw)
	}
	loja.owner.mustStatus(act(loja.owner, "pause", 2), 200)
	if r := act(loja.owner, "pause", 3); r.code() != "invalid_transition" {
		t.Fatalf("pausar fila pausada: %s", r.raw)
	}
	loja.owner.mustStatus(act(loja.owner, "open", 3), 200)
	closed := loja.owner.mustStatus(act(loja.owner, "close", 4), 200)
	if closed.data()["status"] != "closed" {
		t.Fatalf("fechar: %s", closed.raw)
	}

	// Fila pública: só dados públicos (sem sinal, nick, serviços, valores).
	pub := e.browser().mustStatus(e.browser().do("GET", "/v1/public/establishments/"+loja.id.String()+"/queues", nil), 200)
	var pq struct {
		Data   []contrato.PublicQueue
		Cursor *string
	}
	strictDecode(t, pub.raw, &pq)
	if len(pq.Data) != 2 || pq.Data[0].EstimatedWait.Kind != "estimate" || pq.Data[0].WaitingCount != 0 {
		t.Fatalf("fila pública: %+v", pq.Data)
	}
	for _, leak := range []string{"Almoço", "unavailable", "@", "points", "price"} {
		if strings.Contains(string(pub.raw), leak) {
			t.Errorf("fila pública expõe %q: %s", leak, pub.raw)
		}
	}
	var audits int
	_ = e.pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_event WHERE target_id = $1 AND action IN ('T-Q-01','T-Q-02','T-Q-03','T-Q-04','T-Q-05')`, q).Scan(&audits)
	if audits != 5 {
		t.Fatalf("auditoria das transições da fila: %d", audits)
	}
}

// T-Q-04 e T-PR-02 — fechar com gente aguardando/chamada e desativar com atendimento ativo são
// recusados. As participações reais chegam na etapa 04; aqui a ocupação é simulada.
func TestCloseAndDeactivateBlockedByOccupancy(t *testing.T) {
	occ := newFakeOccupancy()
	e := newEnvWith(t, occ)
	loja := e.shop("barbearia-teste", "ana@exemplo.com")
	beto := e.professional(loja.id, "Beto")
	q := e.queueOf(beto)
	loja.owner.mustStatus(loja.owner.cmd("POST", estPath(loja.id, "/queues/"+q.String()+"/open"), map[string]any{"expected_version": 1}), 200)

	occ.set(q, beto, 1, 2) // 1 aguardando/chamado; 2 ativos (com 1 em atendimento)
	r := loja.owner.cmd("POST", estPath(loja.id, "/queues/"+q.String()+"/close"), map[string]any{"expected_version": 2})
	if r.status != 409 || r.code() != "queue_has_pending_participants" {
		t.Fatalf("fechar com pendentes: %d %s", r.status, r.raw)
	}
	loja.owner.reauth()
	r = loja.owner.cmd("POST", estPath(loja.id, "/professionals/"+beto.String()+"/deactivate"), map[string]any{"expected_version": 1, "reason": "saiu da equipe"})
	if r.status != 409 || r.code() != "professional_has_active_participations" {
		t.Fatalf("desativar com atendimento: %d %s", r.status, r.raw)
	}
	// Só em atendimento (ninguém aguardando/chamado): fechar é permitido; desativar não.
	occ.set(q, beto, 0, 1)
	loja.owner.mustStatus(loja.owner.cmd("POST", estPath(loja.id, "/queues/"+q.String()+"/close"), map[string]any{"expected_version": 2}), 200)
	if r := loja.owner.cmd("POST", estPath(loja.id, "/professionals/"+beto.String()+"/deactivate"), map[string]any{"expected_version": 1, "reason": "saiu da equipe"}); r.code() != "professional_has_active_participations" {
		t.Fatalf("desativar com cliente em atendimento: %s", r.raw)
	}
	occ.set(q, beto, 0, 0)
	loja.owner.mustStatus(loja.owner.cmd("POST", estPath(loja.id, "/professionals/"+beto.String()+"/deactivate"), map[string]any{"expected_version": 1, "reason": "saiu da equipe"}), 200)
}

// C-EST-05/C-SEG-06/C-EST-07 — desativar revoga o acesso sem apagar nada; reativar devolve o
// vínculo e a fila continua fechada.
func TestDeactivateAndReactivateProfessional(t *testing.T) {
	e := newEnv(t)
	loja := e.shop("barbearia-teste", "ana@exemplo.com")
	beto := e.professional(loja.id, "Beto")
	caio := e.professional(loja.id, "Caio")
	q := e.queueOf(beto)
	betoB := e.staffMember(loja, beto, "beto@exemplo.com")
	// Caio tem convite pendente (ainda não aceitou).
	loja.owner.mustStatus(loja.owner.cmd("POST", estPath(loja.id, "/professionals/"+caio.String()+"/invites"), map[string]any{"email": "caio@exemplo.com"}), 201)
	list := loja.owner.mustStatus(loja.owner.do("GET", estPath(loja.id, "/professionals"), nil), 200)
	var profs struct{ Data []contrato.Professional }
	strictDecode(t, list.raw, &profs)
	if len(profs.Data) != 2 || !profs.Data[0].HasAccess || profs.Data[1].PendingInvite == nil {
		t.Fatalf("lista: %+v", profs.Data)
	}
	loja.owner.mustStatus(loja.owner.cmd("POST", estPath(loja.id, "/queues/"+q.String()+"/open"), map[string]any{"expected_version": 1}), 200)

	e.clock.Advance(11 * 60e9) // reautenticação vencida
	deact := func(id uuid.UUID, body map[string]any) resp {
		return loja.owner.cmd("POST", estPath(loja.id, "/professionals/"+id.String()+"/deactivate"), body)
	}
	if r := deact(beto, map[string]any{"expected_version": 1, "reason": "saiu"}); r.code() != "reauthentication_required" {
		t.Fatalf("sem reautenticação: %s", r.raw)
	}
	loja.owner.reauth()
	if r := deact(beto, map[string]any{"expected_version": 1}); r.status != 400 || !strings.Contains(string(r.raw), `"/reason"`) {
		t.Fatalf("sem motivo: %d %s", r.status, r.raw)
	}
	if r := betoB.cmd("POST", estPath(loja.id, "/professionals/"+beto.String()+"/deactivate"), map[string]any{"expected_version": 1, "reason": "teste"}); r.status != 403 {
		t.Fatalf("profissional desativando: %d", r.status)
	}
	r := loja.owner.mustStatus(deact(beto, map[string]any{"expected_version": 1, "reason": "saiu da equipe"}), 200)
	if r.data()["status"] != "inactive" || r.data()["has_access"] != false {
		t.Fatalf("desativado: %s", r.raw)
	}
	// Beto perde o acesso imediatamente (sessão de equipe encerrada).
	if s := betoB.session(); s["account"] != nil || len(s["memberships"].([]any)) != 0 {
		t.Fatalf("Beto ainda tem acesso: %v", s)
	}
	if r := betoB.do("GET", estPath(loja.id, "/queues"), nil); r.status != 401 {
		t.Fatalf("fila após desativação: %d", r.status)
	}
	var queueStatus string
	_ = e.pool.QueryRow(context.Background(), `SELECT status FROM queue WHERE id = $1`, q).Scan(&queueStatus)
	if queueStatus != "closed" {
		t.Fatalf("fila do desativado deveria ficar fechada, está %s", queueStatus)
	}
	// Histórico: nada é apagado; o banco recusa DELETE.
	if _, err := e.pool.Exec(context.Background(), `DELETE FROM professional WHERE id = $1`, beto); err == nil {
		t.Fatal("o banco deveria recusar apagar profissional")
	}
	// Fila pública some (profissional inativo), mas o registro continua.
	pub := e.browser().do("GET", "/v1/public/establishments/"+loja.id.String()+"/queues", nil)
	if strings.Contains(string(pub.raw), beto.String()) {
		t.Fatal("profissional desativado não aparece na fila pública")
	}
	if r := loja.owner.cmd("POST", estPath(loja.id, "/queues/"+q.String()+"/open"), map[string]any{"expected_version": 3}); r.code() != "professional_inactive" {
		t.Fatalf("abrir fila de desativado: %s", r.raw)
	}

	// Reativar: vínculo volta, fila continua fechada.
	reat := loja.owner.mustStatus(loja.owner.cmd("POST", estPath(loja.id, "/professionals/"+beto.String()+"/reactivate"), map[string]any{"expected_version": 2}), 200)
	if reat.data()["status"] != "active" || reat.data()["has_access"] != true {
		t.Fatalf("reativado: %s", reat.raw)
	}
	_ = e.pool.QueryRow(context.Background(), `SELECT status FROM queue WHERE id = $1`, q).Scan(&queueStatus)
	if queueStatus != "closed" {
		t.Fatal("reativar não abre a fila")
	}
	again := e.browser()
	again.mustStatus(again.login("beto@exemplo.com", goodPassword, "staff"), 200)
}

// C-EST-07 — configuração da loja; equipe lê, só a dona altera; slug único.
func TestEstablishmentConfiguration(t *testing.T) {
	e := newEnv(t)
	loja := e.shop("barbearia-teste", "ana@exemplo.com")
	e.shop("outra-loja", "bia@exemplo.com")
	beto := e.professional(loja.id, "Beto")
	betoB := e.staffMember(loja, beto, "beto@exemplo.com")

	var est struct{ Data contrato.Establishment }
	strictDecode(t, betoB.mustStatus(betoB.do("GET", estPath(loja.id, ""), nil), 200).raw, &est)
	if est.Data.Slug != "barbearia-teste" || est.Data.Version != 1 {
		t.Fatalf("loja: %+v", est.Data)
	}
	if r := betoB.cmd("PATCH", estPath(loja.id, ""), map[string]any{"expected_version": 1, "name": "Minha"}); r.status != 403 {
		t.Fatalf("profissional alterando a loja: %d", r.status)
	}
	if r := betoB.cmd("PATCH", estPath(loja.id, "/professionals/"+beto.String()), map[string]any{"expected_version": 1, "display_name": "Betão"}); r.status != 403 {
		t.Fatalf("profissional mudando o próprio nome: %d", r.status)
	}
	if r := loja.owner.cmd("PATCH", estPath(loja.id, ""), map[string]any{"expected_version": 1, "slug": "outra-loja"}); r.status != 409 || r.code() != "slug_taken" {
		t.Fatalf("slug repetido: %d %s", r.status, r.raw)
	}
	if r := loja.owner.cmd("PATCH", estPath(loja.id, ""), map[string]any{"expected_version": 1, "timezone": "Marte/Base"}); r.status != 400 {
		t.Fatalf("fuso inválido: %d", r.status)
	}
	r := loja.owner.mustStatus(loja.owner.cmd("PATCH", estPath(loja.id, ""),
		map[string]any{"expected_version": 1, "name": "Barbearia Nova", "slug": "barbearia-nova", "timezone": "America/Manaus"}), 200)
	if r.data()["slug"] != "barbearia-nova" || num(r.data()["version"]) != 2 {
		t.Fatalf("atualização: %s", r.raw)
	}
	if r := loja.owner.cmd("PATCH", estPath(loja.id, ""), map[string]any{"expected_version": 1, "name": "Velha"}); r.code() != "version_conflict" {
		t.Fatalf("versão antiga: %s", r.raw)
	}
	e.browser().mustStatus(e.browser().do("GET", "/v1/public/slugs/barbearia-nova", nil), 200)
	if r := e.browser().do("GET", "/v1/public/slugs/barbearia-teste", nil); r.status != 404 {
		t.Fatalf("slug antigo: %d", r.status)
	}

	// Novo nome do profissional aparece na fila pública.
	loja.owner.mustStatus(loja.owner.cmd("PATCH", estPath(loja.id, "/professionals/"+beto.String()), map[string]any{"expected_version": 1, "display_name": "Betão"}), 200)
	pub := e.browser().do("GET", "/v1/public/establishments/"+loja.id.String()+"/queues", nil)
	if !strings.Contains(string(pub.raw), "Betão") {
		t.Fatalf("fila pública sem o novo nome: %s", pub.raw)
	}
}

// Consultas cruzadas: nada de outra loja é visível ou alterável, nem por id direto.
func TestCrossEstablishmentIsolation(t *testing.T) {
	e := newEnv(t)
	lojaA := e.shop("loja-a", "ana@exemplo.com")
	lojaB := e.shop("loja-b", "bia@exemplo.com")
	betoA := e.professional(lojaA.id, "Beto")
	caioB := e.professional(lojaB.id, "Caio")
	qB := e.queueOf(caioB)
	cliente := e.verifiedCustomer("cliente@exemplo.com")

	cases := []struct {
		b            *browser
		method, path string
		body         map[string]any
	}{
		{lojaA.owner, "GET", estPath(lojaB.id, ""), nil},
		{lojaA.owner, "GET", estPath(lojaB.id, "/professionals"), nil},
		{lojaA.owner, "GET", estPath(lojaB.id, "/queues"), nil},
		{lojaA.owner, "PATCH", estPath(lojaA.id, "/professionals/"+caioB.String()), map[string]any{"expected_version": 1, "display_name": "X"}},
		{lojaA.owner, "POST", estPath(lojaA.id, "/queues/"+qB.String()+"/open"), map[string]any{"expected_version": 1}},
		{lojaA.owner, "POST", estPath(lojaB.id, "/queues/"+qB.String()+"/open"), map[string]any{"expected_version": 1}},
		{cliente, "GET", estPath(lojaA.id, "/queues"), nil},
		{cliente, "GET", estPath(lojaA.id, "/professionals"), nil},
	}
	for _, c := range cases {
		var r resp
		if c.method == "GET" {
			r = c.b.do(c.method, c.path, nil)
		} else {
			r = c.b.cmd(c.method, c.path, c.body)
		}
		if r.status != 404 {
			t.Errorf("%s %s: %d (esperado 404) %s", c.method, c.path, r.status, r.raw)
		}
	}
	if r := e.browser().do("GET", estPath(lojaA.id, "/queues"), nil); r.status != 401 {
		t.Errorf("sem sessão: %d", r.status)
	}
	// Dona em sessão de cliente → 403 (vínculo só vale com sessão de equipe).
	anaCliente := e.browser()
	anaCliente.mustStatus(anaCliente.login("ana@exemplo.com", goodPassword, "customer"), 200)
	if r := anaCliente.do("GET", estPath(lojaA.id, "/professionals"), nil); r.status != 403 {
		t.Errorf("dona como cliente: %d", r.status)
	}
	var status string
	_ = e.pool.QueryRow(context.Background(), `SELECT status FROM queue WHERE id = $1`, qB).Scan(&status)
	if status != "closed" {
		t.Fatal("fila de outra loja foi alterada")
	}
	_ = betoA
}

// Alteração concorrente da fila: duas abas da dona com a mesma versão → uma vence.
func TestConcurrentQueueChange(t *testing.T) {
	e := newEnv(t)
	loja := e.shop("barbearia-teste", "ana@exemplo.com")
	q := e.queueOf(e.professional(loja.id, "Beto"))
	var wg sync.WaitGroup
	statuses := make([]int, 4)
	codes := make([]string, 4)
	for i := range statuses {
		wg.Go(func() {
			r := loja.owner.cmd("POST", estPath(loja.id, "/queues/"+q.String()+"/open"), map[string]any{"expected_version": 1})
			statuses[i], codes[i] = r.status, r.code()
		})
	}
	wg.Wait()
	ok := 0
	for i, s := range statuses {
		if s == 200 {
			ok++
		} else if codes[i] != "version_conflict" {
			t.Fatalf("resultado inesperado: %d %s", s, codes[i])
		}
	}
	var version int64
	_ = e.pool.QueryRow(context.Background(), `SELECT version FROM queue WHERE id = $1`, q).Scan(&version)
	if ok != 1 || version != 2 {
		t.Fatalf("sucessos=%d versão=%d (%v)", ok, version, statuses)
	}
}
