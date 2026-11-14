package api_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"fila/internal/api"
	"fila/internal/contrato"
	"fila/internal/fluxo"
	"fila/internal/platform/httpserver"
	"fila/internal/platform/outbox"
)

// ---------------------------------------------------------------------------------------------
// Cliente SSE de teste: lê o fluxo como o navegador (id/event/data, linhas em branco separam).

type sseMsg struct {
	id    string
	event string
	raw   string
	env   map[string]any
}

func (m sseMsg) data() map[string]any { d, _ := m.env["data"].(map[string]any); return d }
func (m sseMsg) resource() map[string]any {
	r, _ := m.env["resource"].(map[string]any)
	return r
}

type sseStream struct {
	t      *testing.T
	status int
	body   []byte
	msgs   chan sseMsg
	pings  atomic.Int64
	cancel context.CancelFunc
	closed chan struct{}
}

func openStream(t *testing.T, client *http.Client, base string, est uuid.UUID, channels, cursor string, headers ...string) *sseStream {
	t.Helper()
	q := url.Values{"establishment_id": {est.String()}, "channels": {channels}}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "GET", base+"/v1/stream?"+q.Encode(), nil)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := client.Do(req)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	s := &sseStream{t: t, status: resp.StatusCode, msgs: make(chan sseMsg, 1000), cancel: cancel, closed: make(chan struct{})}
	t.Cleanup(s.close)
	if resp.StatusCode != 200 {
		s.body, _ = io.ReadAll(resp.Body)
		resp.Body.Close()
		close(s.msgs)
		close(s.closed)
		return s
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" || resp.Header.Get("Cache-Control") != "no-cache, no-transform" ||
		resp.Header.Get("X-Accel-Buffering") != "no" {
		t.Errorf("cabeçalhos do fluxo: %v", resp.Header)
	}
	go func() {
		defer close(s.closed)
		defer close(s.msgs)
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
		var m sseMsg
		for sc.Scan() {
			line := sc.Text()
			switch {
			case line == "":
				if m.event != "" {
					_ = json.Unmarshal([]byte(m.raw), &m.env)
					s.msgs <- m
				}
				m = sseMsg{}
			case strings.HasPrefix(line, ": "):
				s.pings.Add(1)
			case strings.HasPrefix(line, "id: "):
				m.id = line[4:]
			case strings.HasPrefix(line, "event: "):
				m.event = line[7:]
			case strings.HasPrefix(line, "data: "):
				m.raw += line[6:]
			}
		}
	}()
	return s
}

// stream abre o fluxo com os cookies do navegador.
func (b *browser) stream(est uuid.UUID, channels, cursor string, headers ...string) *sseStream {
	b.e.t.Helper()
	client := &http.Client{Transport: b.client.Transport, Jar: b.client.Jar} // sem Timeout: fluxo longo
	return openStream(b.e.t, client, b.e.srv.URL, est, channels, cursor, append([]string{"X-Forwarded-For", b.ip}, headers...)...)
}

func (s *sseStream) close() {
	s.cancel()
	<-s.closed
}

// next devolve a próxima mensagem; ok=false se o fluxo acabou ou o prazo passou.
func (s *sseStream) next(d time.Duration) (sseMsg, bool) {
	select {
	case m, ok := <-s.msgs:
		return m, ok
	case <-time.After(d):
		return sseMsg{}, false
	}
}

// expect espera uma mensagem do tipo (descartando as outras).
func (s *sseStream) expect(event string) sseMsg {
	s.t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case m, ok := <-s.msgs:
			if !ok {
				s.t.Fatalf("fluxo terminou esperando %s", event)
			}
			if m.event == event {
				return m
			}
		case <-deadline:
			s.t.Fatalf("não chegou %s", event)
		}
	}
}

// drain junta o que chegar durante d.
func (s *sseStream) drain(d time.Duration) []sseMsg {
	var out []sseMsg
	deadline := time.After(d)
	for {
		select {
		case m, ok := <-s.msgs:
			if !ok {
				return out
			}
			out = append(out, m)
		case <-deadline:
			return out
		}
	}
}

// ended diz se o servidor encerrou o fluxo dentro de d.
func (s *sseStream) ended(d time.Duration) bool {
	deadline := time.After(d)
	for {
		select {
		case _, ok := <-s.msgs:
			if !ok {
				return true
			}
		case <-deadline:
			return false
		}
	}
}

var quietLog = slog.New(slog.NewTextHandler(io.Discard, nil))

// publish faz o papel do worker: publica a outbox inteira no event_log.
func (e *env) publish() {
	e.t.Helper()
	p := &outbox.Publisher{Pool: e.pool, Clock: e.clock, Log: quietLog}
	// Transações de OUTROS testes no mesmo servidor PostgreSQL seguram o horizonte (xmin é do
	// servidor inteiro); por isso espera a outbox esvaziar em vez de parar na primeira volta vazia.
	deadline := time.Now().Add(15 * time.Second)
	for {
		n, err := p.PublishOnce(context.Background())
		if err != nil {
			e.t.Fatal(err)
		}
		if n > 0 {
			continue
		}
		var left int
		_ = e.pool.QueryRow(context.Background(), `SELECT count(*) FROM outbox`).Scan(&left)
		if left == 0 {
			return
		}
		if time.Now().After(deadline) {
			e.t.Fatalf("%d eventos não publicados", left)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func channelsOf(v any) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}

// ---------------------------------------------------------------------------------------------

// Snapshot autorizado + eventos ao vivo, cada um com a visão da própria audiência.
// Cenários: C-SSE-01.
func TestStreamSnapshotAndLiveEvents(t *testing.T) {
	e := newEnv(t)
	b := e.barbearia(t)
	e.publish()
	own := b.owner.stream(b.id, "public,owner", "")
	snap := own.expect("stream.snapshot")
	var env struct{ Data contrato.EventEnvelope }
	strictDecode(t, []byte(`{"data":`+snap.raw+`}`), &env)
	var sd contrato.StreamSnapshot
	strictDecode(t, mustJSON(snap.data()), &sd)
	if sd.Reason != "initial" || len(sd.GrantedChannels) != 2 || len(sd.DeniedChannels) != 0 || sd.StaffQueues == nil || len(*sd.StaffQueues) != 2 ||
		sd.PublicQueues == nil || snap.id == "" {
		t.Fatalf("snapshot da dona: %s", snap.raw)
	}
	anon := e.browser().stream(b.id, "public,owner,visit", "")
	as := anon.expect("stream.snapshot").data()
	if g, d := channelsOf(as["granted_channels"]), channelsOf(as["denied_channels"]); len(g) != 1 || g[0] != "public" || len(d) != 2 || as["staff_queues"] != nil {
		t.Fatalf("anônimo: %v", as)
	}
	maria := e.verifiedCustomer("maria@exemplo.com")
	mv := maria.stream(b.id, "public,visit", "")
	if mv.expect("stream.snapshot").data()["visit"] != nil {
		t.Fatal("visita antes de entrar")
	}
	maria.mustStatus(maria.enter(b.id, b.filaBeto, b.corte), 201)
	e.publish()
	p := own.expect("participation.updated")
	if p.env["audience"] != "owner" || p.data()["identity_kind"] != "account" || p.id == "" {
		t.Fatalf("participação para a dona: %s", p.raw)
	}
	if q := own.expect("queue.updated"); !strings.Contains(q.raw, "Pessoa Teste") {
		t.Fatalf("fila da equipe sem nick: %s", q.raw)
	}
	mp := mv.expect("participation.updated")
	if mp.env["audience"] != "visit" || mp.data()["identity_kind"] != nil {
		t.Fatalf("participação para a cliente: %s", mp.raw)
	}
	pub := anon.expect("queue.public.updated")
	if strings.Contains(pub.raw, "Pessoa") || strings.Contains(pub.raw, "Corte") || pub.env["audience"] != "public" {
		t.Fatalf("evento público com dado privado: %s", pub.raw)
	}
	for _, m := range anon.drain(300 * time.Millisecond) {
		if m.env["audience"] != "public" {
			t.Fatalf("anônimo recebeu %s/%v", m.event, m.env["audience"])
		}
	}
	// A dona abre e pausa a fila (módulo estabelecimento): também vira evento.
	b.owner.mustStatus(b.owner.cmd("POST", estPath(b.id, "/queues/"+b.filaCaio.String()+"/pause"), map[string]any{"expected_version": 2}), 200)
	e.publish()
	if q := anon.expect("queue.public.updated"); q.data()["status"] != "paused" {
		t.Fatalf("pausa sem evento: %s", q.raw)
	}
	// Listagens devolvem o cursor do mesmo instante (SnapshotCursor).
	if r := b.owner.mustStatus(b.owner.do("GET", estPath(b.id, "/queues"), nil), 200); r.body["cursor"] == nil {
		t.Fatal("lista sem cursor")
	}
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

// Retomada pelo cursor (parâmetro e Last-Event-ID); cursor ilegível, de outra época ou vencido
// recebe snapshot novo.
// Cenários: C-SSE-07.
func TestStreamResumeAndCursorFallbacks(t *testing.T) {
	e := newEnv(t)
	b := e.barbearia(t)
	e.publish()
	first := e.browser().stream(b.id, "public", "")
	cursor := first.expect("stream.snapshot").id
	first.close()
	watch := e.browser().stream(b.id, "public", "") // prova de que o Hub já leu os eventos
	watch.expect("stream.snapshot")
	for _, nick := range []string{"Ana", "Bia"} {
		v := e.visitorIn(b.id, nick)
		v.mustStatus(v.enter(b.id, b.filaBeto, b.corte), 201)
	}
	e.publish()
	watch.expect("queue.public.updated")
	watch.expect("queue.public.updated") // daqui em diante só o replay pelo cursor entrega estes dois
	for name, s := range map[string]*sseStream{
		"parâmetro":     e.browser().stream(b.id, "public", cursor),
		"Last-Event-ID": e.browser().stream(b.id, "public", "", "Last-Event-ID", cursor),
	} {
		m1, _ := s.next(3 * time.Second)
		m2, _ := s.next(3 * time.Second)
		if m1.event != "queue.public.updated" || m2.event != "queue.public.updated" || m1.id == m2.id {
			t.Fatalf("%s: retomada deveria reenviar os 2 eventos sem snapshot: %v / %v", name, m1.event, m2.event)
		}
		if num(m2.resource()["version"]) <= num(m1.resource()["version"]) {
			t.Fatalf("%s: versões fora de ordem", name)
		}
		s.close()
	}
	reason := func(c string) string {
		s := e.browser().stream(b.id, "public", c)
		defer s.close()
		return s.expect("stream.snapshot").data()["reason"].(string)
	}
	if r := reason("lixo-que-nao-e-cursor"); r != "cursor_invalid" {
		t.Fatalf("cursor ilegível: %s", r)
	}
	head, _ := outbox.ReadHead(context.Background(), e.pool)
	if r := reason(outbox.Cursor{Epoch: head.Epoch, Position: head.Last + 5}.Encode()); r != "cursor_invalid" {
		t.Fatalf("cursor do futuro: %s", r)
	}
	// Retenção: eventos de mais de 24 h apagados → cursor anterior vence.
	_, _ = e.pool.Exec(context.Background(), `UPDATE event_log SET published_at = now() - interval '25 hours'`)
	if _, err := outbox.Prune(context.Background(), e.pool, time.Now()); err != nil {
		t.Fatal(err)
	}
	if r := reason(cursor); r != "cursor_expired" {
		t.Fatalf("cursor vencido: %s", r)
	}
	if _, err := outbox.NewEpoch(context.Background(), e.pool); err != nil {
		t.Fatal(err)
	}
	if r := reason(outbox.Cursor{Epoch: head.Epoch, Position: head.Last}.Encode()); r != "epoch_changed" {
		t.Fatalf("época trocada: %s", r)
	}
	if r := e.browser().do("GET", "/v1/stream?establishment_id="+b.id.String()+"&channels=tudo", nil); r.status != 400 {
		t.Fatalf("canal inválido: %d", r.status)
	}
	if r := e.browser().do("GET", "/v1/stream?establishment_id="+uuid.NewString()+"&channels=public", nil); r.status != 404 {
		t.Fatalf("loja inexistente: %d", r.status)
	}
}

// Evento publicado enquanto o snapshot é montado (antes e depois de fixar a posição): o cliente
// converge ao estado verdadeiro, sem perder nada.
// Cenários: C-SSE-02.
func TestStreamEventDuringSnapshotConverges(t *testing.T) {
	for _, stage := range []string{"registered", "in_snapshot"} {
		t.Run(stage, func(t *testing.T) {
			var armed atomic.Bool
			var e *env
			var b barbearia
			e = newEnvDeps(t, func(d *api.Deps) {
				d.SnapshotHook = func(s string) {
					if s == stage && armed.CompareAndSwap(true, false) {
						v := e.visitorIn(b.id, "Durante")
						v.mustStatus(v.enter(b.id, b.filaBeto, b.corte), 201)
						e.publish()
					}
				}
			})
			b = e.barbearia(t)
			v := e.visitorIn(b.id, "Antes")
			v.mustStatus(v.enter(b.id, b.filaBeto, b.corte), 201)
			e.publish()
			armed.Store(true)
			s := e.browser().stream(b.id, "public", "")
			// Modelo do cliente: snapshot e depois eventos, aplicando só versão maior.
			state := map[string]map[string]any{}
			known := map[string]int64{}
			for _, q := range s.expect("stream.snapshot").data()["public_queues"].([]any) {
				m := q.(map[string]any)
				state[m["queue_id"].(string)] = m
			}
			after := e.visitorIn(b.id, "Depois")
			after.mustStatus(after.enter(b.id, b.filaBeto, b.corte), 201)
			e.publish()
			for _, m := range s.drain(time.Second) {
				id := m.resource()["id"].(string)
				if v := num(m.resource()["version"]); v > known[id] {
					known[id], state[id] = v, m.data()
				}
			}
			truth := e.browser().mustStatus(e.browser().do("GET", "/v1/public/establishments/"+b.id.String()+"/queues", nil), 200)
			for _, q := range truth.body["data"].([]any) {
				want := q.(map[string]any)
				got := state[want["queue_id"].(string)]
				if num(got["waiting_count"]) != num(want["waiting_count"]) || len(got["tickets"].([]any)) != len(want["tickets"].([]any)) {
					t.Fatalf("cliente não convergiu: %v, verdade %v", got, want)
				}
			}
			if num(state[b.filaBeto.String()]["waiting_count"]) != 3 {
				t.Fatalf("esperava 3 aguardando: %v", state[b.filaBeto.String()])
			}
		})
	}
}

// Audiência indevida: cliente só recebe a própria visita; profissional só a própria fila; replay
// com cursor de outra pessoa não revela nada além do que a sessão pode ver.
// Cenários: C-SSE-05.
func TestStreamAudienceIsolation(t *testing.T) {
	e := newEnv(t)
	b := e.barbearia(t)
	caio := e.staffMember(b.shop, b.caio, "caio@exemplo.com")
	e.publish()
	ownerCursor := b.owner.stream(b.id, "owner", "").expect("stream.snapshot").id
	maria := e.verifiedCustomer("maria@exemplo.com")
	ms := maria.stream(b.id, "visit,owner,professional", "")
	if d := channelsOf(ms.expect("stream.snapshot").data()["denied_channels"]); len(d) != 2 {
		t.Fatalf("cliente pedindo canais da equipe: %v", d)
	}
	cs := caio.stream(b.id, "professional", "")
	cs.expect("stream.snapshot")
	lia := e.visitorIn(b.id, "Lia")
	pl, _ := visitOf(lia.mustStatus(lia.enter(b.id, b.filaBeto, b.corte), 201))
	pm, _ := visitOf(maria.mustStatus(maria.enter(b.id, b.filaBeto, b.corte), 201))
	e.publish()
	got := ms.drain(time.Second)
	if len(got) == 0 {
		t.Fatal("cliente não recebeu a própria visita")
	}
	for _, m := range got {
		if strings.Contains(m.raw, pl["id"].(string)) || strings.Contains(m.raw, "Lia") || m.env["audience"] != "visit" {
			t.Fatalf("cliente recebeu dado de outra visita: %s", m.raw)
		}
	}
	if extra := cs.drain(500 * time.Millisecond); len(extra) != 0 {
		t.Fatalf("Caio recebeu eventos da fila de Beto: %s", extra[0].raw)
	}
	pc, _ := visitOf(lia.mustStatus(lia.cmd("POST", estPath(b.id, "/participations/"+pl["id"].(string)+"/cancel"), map[string]any{"expected_version": 1}), 200))
	_ = pc
	l2 := e.visitorIn(b.id, "Lu")
	l2.mustStatus(l2.enter(b.id, b.filaCaio, b.corte), 201)
	e.publish()
	if q := cs.expect("queue.updated"); q.env["audience"] != "professional" || !strings.Contains(q.raw, "Lu") {
		t.Fatalf("Caio deveria receber a própria fila: %s", q.raw)
	}
	// Replay com o cursor da dona (emprestado): só o que a sessão da Maria pode ver.
	rs := maria.stream(b.id, "visit", ownerCursor)
	for _, m := range rs.drain(time.Second) {
		if m.event == "stream.snapshot" {
			continue
		}
		if m.env["audience"] != "visit" || !strings.Contains(m.raw, pm["id"].(string)) && m.resource()["type"] == "participation" {
			t.Fatalf("replay vazou: %s", m.raw)
		}
	}
}

// Sessão revogada encerra a conexão já aberta: logout, fim do acesso de equipe e expiração
// (reconferência periódica).
// Cenários: C-SSE-06.
func TestStreamSessionRevoked(t *testing.T) {
	e := newEnvDeps(t, func(d *api.Deps) {
		d.StreamLimits = fluxo.Limits{Heartbeat: time.Second, Revalidate: 300 * time.Millisecond, MaxLifetime: time.Minute,
			WriteTimeout: 5 * time.Second, Buffer: 256, PerCredential: 6}
	})
	b := e.barbearia(t)
	maria := e.verifiedCustomer("maria@exemplo.com")
	ms := maria.stream(b.id, "public,visit", "")
	ms.expect("stream.snapshot")
	maria.mustStatus(maria.do("POST", "/v1/auth/logout", nil), 204)
	if end := ms.expect("stream.session_ended"); end.data()["reason"] != "session_revoked" {
		t.Fatalf("fim de sessão: %s", end.raw)
	}
	if !ms.ended(2 * time.Second) {
		t.Fatal("conexão continuou aberta depois do logout")
	}
	// Fim do acesso de equipe (profissional desativado pela dona).
	caio := e.staffMember(b.shop, b.caio, "caio@exemplo.com")
	cs := caio.stream(b.id, "professional", "")
	cs.expect("stream.snapshot")
	b.owner.reauth()
	b.owner.mustStatus(b.owner.cmd("POST", estPath(b.id, "/professionals/"+b.caio.String()+"/deactivate"), map[string]any{"expected_version": 1, "reason": "saiu da loja"}), 200)
	cs.expect("stream.session_ended")
	// Expiração (12 h de sessão de equipe): percebida na reconferência periódica.
	own := b.owner.stream(b.id, "owner", "")
	own.expect("stream.snapshot")
	e.clock.Advance(13 * time.Hour)
	own.expect("stream.session_ended")
	if !own.ended(2 * time.Second) {
		t.Fatal("sessão vencida continuou recebendo")
	}
}

// Dois processos de API sobre o mesmo banco entregam a mesma sequência; reinício de um deles e
// queda da conexão: o cliente volta pelo cursor em outro processo sem perder eventos.
// Cenários: C-SSE-08.
func TestStreamTwoAPIProcessesAndRestart(t *testing.T) {
	e := newEnv(t)
	b := e.barbearia(t)
	e.publish()
	second, err := api.New(e.deps, nil)
	if err != nil {
		t.Fatal(err)
	}
	srv2 := httptest.NewTLSServer(httpserver.Chain(second.Handler(), quietLog))
	hubCtx, stopHub := context.WithCancel(context.Background())
	go second.Run(hubCtx)
	defer stopHub()
	client2 := srv2.Client()
	s2 := openStream(t, client2, srv2.URL, b.id, "public", "", "X-Forwarded-For", "198.51.100.7")
	s2.expect("stream.snapshot")
	s1 := e.browser().stream(b.id, "public", "")
	s1.expect("stream.snapshot")
	v := e.visitorIn(b.id, "Ana") // comando no processo 1
	v.mustStatus(v.enter(b.id, b.filaBeto, b.corte), 201)
	e.publish()
	m1, m2 := s1.expect("queue.public.updated"), s2.expect("queue.public.updated")
	if m1.id != m2.id || m1.raw != m2.raw {
		t.Fatalf("processos entregaram sequências diferentes:\n%s\n%s", m1.raw, m2.raw)
	}
	// Reinício do processo 2: a conexão cai; o cliente reconecta no processo 1 com o cursor.
	second.Stream.Shutdown()
	srv2.Close()
	if !s2.ended(3 * time.Second) {
		t.Fatal("conexão não terminou com o processo")
	}
	for _, nick := range []string{"Bia", "Cris"} {
		v := e.visitorIn(b.id, nick)
		v.mustStatus(v.enter(b.id, b.filaBeto, b.corte), 201)
	}
	e.publish()
	s1.expect("queue.public.updated") // o Hub do processo 1 já passou por eles: só o replay entrega
	s1.expect("queue.public.updated")
	back := e.browser().stream(b.id, "public", m2.id)
	r1, _ := back.next(3 * time.Second)
	r2, _ := back.next(3 * time.Second)
	if r1.event != "queue.public.updated" || r2.event != "queue.public.updated" || num(r2.data()["waiting_count"]) != 3 {
		t.Fatalf("retomada depois do reinício: %s | %s", r1.raw, r2.raw)
	}
	// Queda da rede do lado do cliente: o servidor solta a conexão (sem vazamento).
	s1.close()
	back.close()
	deadline := time.Now().Add(3 * time.Second)
	for e.api.Hub.Count() != 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if n := e.api.Hub.Count(); n != 0 {
		t.Fatalf("%d conexões presas no servidor", n)
	}
}

// Prazos HTTP curtos (leitura/escrita de 1 s) não derrubam o fluxo: cada mensagem tem o próprio
// prazo e o heartbeat mantém a conexão; vida máxima encerra (o cliente volta com o cursor);
// limite de conexões por credencial.
func TestStreamTimeoutsHeartbeatAndLimits(t *testing.T) {
	e := newEnvDeps(t, func(d *api.Deps) {
		d.StreamLimits = fluxo.Limits{Heartbeat: 200 * time.Millisecond, Revalidate: time.Minute, MaxLifetime: 3 * time.Second,
			WriteTimeout: time.Second, Buffer: 256, PerCredential: 2}
	})
	b := e.barbearia(t)
	srv := httptest.NewUnstartedServer(httpserver.Chain(e.api.Handler(), quietLog))
	srv.Config.ReadTimeout, srv.Config.WriteTimeout = time.Second, time.Second
	srv.StartTLS()
	defer func() { e.api.Stream.Shutdown(); srv.Close() }()
	s := openStream(t, srv.Client(), srv.URL, b.id, "public", "", "X-Forwarded-For", "198.51.100.9")
	s.expect("stream.snapshot")
	time.Sleep(2 * time.Second)
	if s.pings.Load() < 5 {
		t.Fatalf("heartbeats em 2 s: %d", s.pings.Load())
	}
	v := e.visitorIn(b.id, "Ana")
	v.mustStatus(v.enter(b.id, b.filaBeto, b.corte), 201)
	e.publish()
	s.expect("queue.public.updated") // passou do WriteTimeout global e continua entregando
	if !s.ended(3 * time.Second) {
		t.Fatal("vida máxima não encerrou a conexão")
	}
	// Anônimos no mesmo IP (Wi-Fi da loja) não contam como uma credencial: só o teto por IP vale.
	for i := range 3 {
		if a := openStream(t, srv.Client(), srv.URL, b.id, "public", "", "X-Forwarded-For", "198.51.100.10"); a.status != 200 {
			t.Fatalf("anônimo %d no mesmo IP: %d", i, a.status)
		}
	}
	// Com credencial (a visitante Ana), o limite por credencial vale (2 neste teste).
	if a, c, d := v.stream(b.id, "public,visit", ""), v.stream(b.id, "public,visit", ""), v.stream(b.id, "public,visit", ""); a.status != 200 || c.status != 200 || d.status != 429 {
		t.Fatalf("limite por credencial: %d %d %d", a.status, c.status, d.status)
	}
}

// Duplicata não repete efeito: o mesmo evento entregue duas vezes (replay + ao vivo, ou dois
// processos) tem o mesmo event_id e versão; o cliente aplica só versão maior.
func TestStreamDuplicatesAreHarmless(t *testing.T) {
	e := newEnv(t)
	b := e.barbearia(t)
	e.publish()
	s := e.browser().stream(b.id, "public", "")
	cursor := s.expect("stream.snapshot").id
	v := e.visitorIn(b.id, "Ana")
	v.mustStatus(v.enter(b.id, b.filaBeto, b.corte), 201)
	e.publish()
	live := s.expect("queue.public.updated")
	again := e.browser().stream(b.id, "public", cursor).expect("queue.public.updated")
	if live.env["event_id"] != again.env["event_id"] || live.id != again.id || live.raw != again.raw {
		t.Fatal("o mesmo evento chegou diferente na repetição")
	}
}
