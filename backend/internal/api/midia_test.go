package api_test

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
	"time"

	"fila/internal/contrato"
)

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.NRGBA{uint8(x), uint8(y), 120, 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// upload envia bytes crus (como o navegador faz) com o Content-Type dado.
func (b *browser) upload(query string, ct string, data []byte) resp {
	b.e.t.Helper()
	return b.do("POST", "/v1/media/uploads?"+query, string(data), "Content-Type", ct, "Idempotency-Key", newIdemKey())
}

// uploadReady envia, processa (worker) e devolve o id pronto para associar.
func (b *browser) uploadReady(query string) string {
	b.e.t.Helper()
	r := b.mustStatus(b.upload(query, "image/png", pngBytes(b.e.t, 300, 300)), 202)
	id := r.data()["id"].(string)
	for {
		did, err := b.e.api.Media.ProcessOnce(context.Background())
		if err != nil {
			b.e.t.Fatal(err)
		}
		if !did {
			break
		}
	}
	if st := b.mustStatus(b.do("GET", "/v1/media/uploads/"+id, nil), 200).data()["status"]; st != "ready" {
		b.e.t.Fatalf("envio não ficou pronto: %v", st)
	}
	return id
}

func (b *browser) profile() map[string]any {
	b.e.t.Helper()
	return b.mustStatus(b.do("GET", "/v1/me", nil), 200).data()
}

func firstURL(ref any) string {
	m, _ := ref.(map[string]any)
	vs, _ := m["variants"].([]any)
	if len(vs) == 0 {
		return ""
	}
	return vs[0].(map[string]any)["url"].(string)
}

// Avatar: envio → processamento → associação com versão; entrega privada; outra pessoa e o
// endpoint público não veem; foto de outra pessoa ou de outra finalidade não é associada.
// Cenários: C-CFG-05.
func TestAvatarLifecycleAndPrivacy(t *testing.T) {
	e, _ := newEnvFull(t)
	maria := e.verifiedCustomer("maria@exemplo.com")
	joao := e.verifiedCustomer("joao@exemplo.com")
	before := maria.profile()
	if before["avatar"] != nil {
		t.Fatal("avatar inicial")
	}
	id := maria.uploadReady("purpose=avatar")
	// Envio aceito não muda o perfil.
	if maria.profile()["avatar"] != nil {
		t.Fatal("envio virou avatar sem associação")
	}
	// João não associa a foto da Maria.
	if r := joao.cmd("PUT", "/v1/me/avatar", map[string]any{"asset_id": id, "expected_version": 1}); r.code() != "media_not_attachable" {
		t.Fatalf("foto de outra conta: %d %s", r.status, r.raw)
	}
	if r := joao.do("GET", "/v1/media/uploads/"+id, nil); r.status != 404 {
		t.Fatalf("status do envio alheio: %d", r.status)
	}
	p := maria.mustStatus(maria.cmd("PUT", "/v1/me/avatar", map[string]any{"asset_id": id, "expected_version": before["version"]}), 200).data()
	u := firstURL(p["avatar"])
	if !strings.HasPrefix(u, "/v1/me/avatar/") {
		t.Fatalf("URL do avatar: %v", p["avatar"])
	}
	img := maria.mustStatus(maria.do("GET", u, nil), 200)
	if img.header.Get("Cache-Control") != "private, no-cache, must-revalidate" || img.header.Get("Content-Type") != "image/jpeg" ||
		img.header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("cabeçalhos: %v", img.header)
	}
	if r := maria.do("GET", u, nil, "If-None-Match", img.header.Get("ETag")); r.status != 304 {
		t.Fatalf("304: %d", r.status)
	}
	// Sem sessão, nem o 304 sai.
	if r := e.browser().do("GET", u, nil, "If-None-Match", img.header.Get("ETag")); r.status != 401 {
		t.Fatalf("anônimo: %d", r.status)
	}
	if r := joao.do("GET", "/v1/me/avatar/128", nil); r.status != 404 {
		t.Fatalf("joão viu foto: %d", r.status)
	}
	if r := e.browser().do("GET", "/v1/public/media/"+id+"/128", nil); r.status != 404 {
		t.Fatalf("avatar privado no endpoint público: %d", r.status)
	}
	// Troca antiga não sobrescreve a escolha nova (versão).
	id2 := maria.uploadReady("purpose=avatar")
	if r := maria.cmd("PUT", "/v1/me/avatar", map[string]any{"asset_id": id2, "expected_version": before["version"]}); r.code() != "version_conflict" {
		t.Fatalf("versão velha aceita: %d %s", r.status, r.raw)
	}
	// Avatar privado não vira logo (finalidade).
	loja := e.shop("barbearia-teste", "ana@exemplo.com")
	if r := loja.owner.upload("purpose=avatar&establishment_id="+loja.id.String(), "image/png", pngBytes(t, 50, 50)); r.status != 400 {
		t.Fatalf("avatar com loja: %d", r.status)
	}
	est := loja.owner.mustStatus(loja.owner.do("GET", "/v1/establishments/"+loja.id.String(), nil), 200).data()
	if r := loja.owner.cmd("PUT", "/v1/establishments/"+loja.id.String()+"/logo", map[string]any{"asset_id": id2, "expected_version": est["version"]}); r.code() != "media_not_attachable" {
		t.Fatalf("avatar como logo: %d %s", r.status, r.raw)
	}
	// Remoção: referência sai na hora; limpeza não apaga o que está associado.
	cur := maria.profile()
	maria.mustStatus(maria.cmd("POST", "/v1/me/avatar/remove", map[string]any{"expected_version": cur["version"]}), 200)
	if r := maria.do("GET", u, nil); r.status != 404 {
		t.Fatalf("foto removida ainda servida: %d", r.status)
	}
	id3 := maria.uploadReady("purpose=avatar")
	maria.mustStatus(maria.cmd("PUT", "/v1/me/avatar", map[string]any{"asset_id": id3, "expected_version": maria.profile()["version"]}), 200)
	e.clock.Advance(30 * time.Hour)
	if _, err := e.api.Media.CleanOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	maria.mustStatus(maria.do("GET", firstURL(maria.profile()["avatar"]), nil), 200)
	if n := e.count(t, `SELECT count(*) FROM media_asset WHERE id = $1 AND state = 'purged'`, id); n != 1 {
		t.Fatal("foto removida não foi limpa depois da retenção")
	}
}

// Entrada maliciosa ou fora dos limites é recusada no envio; limite de envios por conta.
// Cenários: C-CFG-05.
func TestUploadValidation(t *testing.T) {
	e, _ := newEnvFull(t)
	maria := e.verifiedCustomer("maria@exemplo.com")
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"/>`)
	cases := []struct {
		name, ct string
		data     []byte
		status   int
	}{
		{"svg disfarçado", "image/png", svg, 415},
		{"svg declarado", "image/svg+xml", svg, 415},
		{"MIME falso (PNG declarado JPEG)", "image/jpeg", pngBytes(t, 20, 20), 415},
		{"inválida", "image/png", append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 40)...), 422},
		{"grande demais", "image/png", make([]byte, 5<<20+10), 413},
	}
	for _, c := range cases {
		if r := maria.upload("purpose=avatar", c.ct, c.data); r.status != c.status {
			t.Errorf("%s: %d %s", c.name, r.status, truncate(r.raw))
		}
	}
	if r := maria.upload("purpose=avatar&crop_x=0.5", "image/png", pngBytes(t, 20, 20)); r.status != 400 {
		t.Errorf("recorte incompleto: %d", r.status)
	}
	if r := maria.upload("purpose=qualquer", "image/png", pngBytes(t, 20, 20)); r.status != 400 {
		t.Errorf("finalidade desconhecida: %d", r.status)
	}
	if r := e.browser().upload("purpose=avatar", "image/png", pngBytes(t, 20, 20)); r.status != 401 {
		t.Errorf("anônimo: %d", r.status)
	}
	// Limite por minuto (5): o 6º é recusado; login e filas de outras pessoas seguem.
	joao := e.verifiedCustomer("joao@exemplo.com")
	n429 := 0
	for range 6 {
		if r := joao.upload("purpose=avatar", "image/png", pngBytes(t, 20, 20)); r.status == 429 {
			n429++
		}
	}
	if n429 == 0 {
		t.Fatal("sem limite de envios")
	}
	e.browser().login("maria@exemplo.com", goodPassword, "customer")
	maria.mustStatus(maria.upload("purpose=avatar", "image/png", pngBytes(t, 20, 20)), 202)
	if n := e.count(t, `SELECT count(*) FROM media_asset`); n == 0 {
		t.Fatal("nenhum envio registrado")
	}
}

// Worker: original ausente vira falha (nada publicado pela metade).
// Cenários: C-CFG-05.
func TestWorkerFailureNeverPublishes(t *testing.T) {
	e, _ := newEnvFull(t)
	maria := e.verifiedCustomer("maria@exemplo.com")
	r := maria.mustStatus(maria.upload("purpose=avatar", "image/png", pngBytes(t, 80, 80)), 202)
	id := r.data()["id"].(string)
	var key string
	if err := e.pool.QueryRow(context.Background(), `SELECT staging_key FROM media_asset WHERE id = $1`, id).Scan(&key); err != nil {
		t.Fatal(err)
	}
	if err := e.deps.MediaStore.Delete(context.Background(), key); err != nil {
		t.Fatal(err)
	}
	if _, err := e.api.Media.ProcessOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	st := maria.mustStatus(maria.do("GET", "/v1/media/uploads/"+id, nil), 200).data()
	if st["status"] != "failed" || st["failure_reason"] != "processing_error" {
		t.Fatalf("situação: %v", st)
	}
	if r := maria.cmd("PUT", "/v1/me/avatar", map[string]any{"asset_id": id, "expected_version": 1}); r.code() != "media_not_attachable" {
		t.Fatalf("falha associada: %s", r.raw)
	}
}

// A equipe da fila vê a foto do cliente com conta só enquanto a participação está ativa; equipe de
// outra loja e outro profissional não veem; a fila pública nunca mostra.
// Cenários: C-CFG-05.
func TestCustomerAvatarOnlyForQueueStaff(t *testing.T) {
	e, _ := newEnvFull(t)
	b := e.barbearia(t)
	maria := e.verifiedCustomer("maria@exemplo.com")
	id := maria.uploadReady("purpose=avatar")
	maria.mustStatus(maria.cmd("PUT", "/v1/me/avatar", map[string]any{"asset_id": id, "expected_version": maria.profile()["version"]}), 200)
	part, _ := visitOf(maria.mustStatus(maria.enter(b.id, b.filaBeto, b.corte), 201))
	pid := part["id"].(string)

	queues := b.betoB.mustStatus(b.betoB.do("GET", estPath(b.id, "/queues"), nil), 200)
	var u string
	for _, q := range queues.body["data"].([]any) {
		for _, en := range q.(map[string]any)["entries"].([]any) {
			if en.(map[string]any)["participation_id"] == pid {
				u = firstURL(en.(map[string]any)["customer_avatar"])
			}
		}
	}
	if u == "" {
		t.Fatalf("equipe sem a miniatura do cliente: %s", truncate(queues.raw))
	}
	b.betoB.mustStatus(b.betoB.do("GET", u, nil), 200)
	b.owner.mustStatus(b.owner.do("GET", u, nil), 200)
	caio := e.staffMember(b.shop, b.caio, "caio@exemplo.com")
	if r := caio.do("GET", u, nil); r.status == 200 {
		t.Fatal("profissional de outra fila viu a foto")
	}
	outra := e.shop("outra-loja", "bia@exemplo.com")
	if r := outra.owner.do("GET", u, nil); r.status == 200 {
		t.Fatal("dona de outra loja viu a foto")
	}
	if r := e.verifiedCustomer("joao@exemplo.com").do("GET", u, nil); r.status == 200 {
		t.Fatal("outro cliente viu a foto")
	}
	pub := e.browser().mustStatus(e.browser().do("GET", "/v1/public/establishments/"+b.id.String()+"/queues", nil), 200)
	if strings.Contains(string(pub.raw), id) || strings.Contains(string(pub.raw), "customer-avatar") {
		t.Fatal("foto de cliente na fila pública")
	}
	// Encerrada a participação, a equipe perde o acesso.
	maria.mustStatus(maria.partCmd(b.id, pid, "cancel", map[string]any{"expected_version": part["version"]}), 200)
	if r := b.betoB.do("GET", u, nil); r.status != 404 {
		t.Fatalf("acesso depois de encerrar: %d", r.status)
	}
}

// Foto profissional: publicação explícita (cópia do avatar), aparece no cartão público, o dono
// retira com motivo e o avatar pessoal continua privado e intacto.
// Cenários: C-CFG-06.
func TestProfessionalPhotoPublication(t *testing.T) {
	e, _ := newEnvFull(t)
	b := e.barbearia(t)
	path := estPath(b.id, "/my-professional-profile")
	me := b.betoB.mustStatus(b.betoB.do("GET", path, nil), 200).data()
	if me["photo"] != nil || me["display_name"] != "Beto" {
		t.Fatalf("perfil profissional: %v", me)
	}
	// Sem avatar, não há o que copiar.
	if r := b.betoB.cmd("PUT", path+"/photo", map[string]any{"from_avatar": true, "expected_version": me["version"]}); r.code() != "media_not_attachable" {
		t.Fatalf("cópia sem avatar: %s", r.raw)
	}
	avatar := b.betoB.uploadReady("purpose=avatar")
	b.betoB.mustStatus(b.betoB.cmd("PUT", "/v1/me/avatar", map[string]any{"asset_id": avatar, "expected_version": b.betoB.profile()["version"]}), 200)
	me = b.betoB.mustStatus(b.betoB.cmd("PUT", path+"/photo", map[string]any{"from_avatar": true, "expected_version": me["version"]}), 200).data()
	pu := firstURL(me["photo"])
	if !strings.HasPrefix(pu, "/v1/public/media/") || strings.Contains(pu, avatar) {
		t.Fatalf("publicação não é cópia independente: %s", pu)
	}
	img := e.browser().mustStatus(e.browser().do("GET", pu, nil), 200)
	if !strings.HasPrefix(img.header.Get("Cache-Control"), "public, max-age=300") {
		t.Fatalf("cache público: %s", img.header.Get("Cache-Control"))
	}
	pub := e.browser().mustStatus(e.browser().do("GET", "/v1/public/establishments/"+b.id.String()+"/queues", nil), 200)
	if !strings.Contains(string(pub.raw), pu) {
		t.Fatal("foto ausente do cartão público")
	}
	// Caio (cliente sem vínculo de profissional nesta loja) não tem perfil profissional.
	if r := e.verifiedCustomer("x@exemplo.com").do("GET", path, nil); r.status != 404 {
		t.Fatalf("cliente no perfil profissional: %d", r.status)
	}
	// Dono retira a publicação com motivo (auditado); o avatar continua.
	prof := b.owner.mustStatus(b.owner.do("GET", estPath(b.id, "/professionals"), nil), 200)
	var version any
	for _, p := range prof.body["data"].([]any) {
		if p.(map[string]any)["id"] == b.beto.String() {
			version = p.(map[string]any)["version"]
		}
	}
	if r := b.owner.cmd("POST", estPath(b.id, "/professionals/"+b.beto.String()+"/photo/remove"), map[string]any{"expected_version": version}); r.status != 400 {
		t.Fatalf("sem motivo: %d", r.status)
	}
	b.owner.mustStatus(b.owner.cmd("POST", estPath(b.id, "/professionals/"+b.beto.String()+"/photo/remove"),
		map[string]any{"expected_version": version, "reason": "foto inadequada"}), 200)
	if r := e.browser().do("GET", pu, nil); r.status != 404 {
		t.Fatalf("foto retirada ainda pública: %d", r.status)
	}
	if b.betoB.profile()["avatar"] == nil {
		t.Fatal("retirada pública apagou o avatar pessoal")
	}
	if n := e.count(t, `SELECT count(*) FROM audit_event WHERE action = 'T-PR-06' AND reason = 'foto inadequada'`); n != 1 {
		t.Fatal("retirada sem auditoria")
	}
}

// Identidade da loja: paleta do catálogo, textos públicos, telefone normalizado, logo; dono de
// outra loja não altera; conflito entre abas; nada privado no público.
// Cenários: C-CFG-07.
func TestStoreBranding(t *testing.T) {
	e, _ := newEnvFull(t)
	loja := e.shop("barbearia-teste", "ana@exemplo.com")
	outra := e.shop("outra-loja", "bia@exemplo.com")
	p := "/v1/establishments/" + loja.id.String()
	est := loja.owner.mustStatus(loja.owner.do("GET", p, nil), 200).data()
	if est["palette"] != "purple" || est["public_url"] != origin+"/loja/barbearia-teste" {
		t.Fatalf("padrão: %v", est)
	}
	v := est["version"]
	for name, body := range map[string]map[string]any{
		"paleta fora do catálogo": {"expected_version": v, "palette": "#ff0000"},
		"descrição longa":         {"expected_version": v, "public_description": strings.Repeat("a", 281)},
		"telefone inválido":       {"expected_version": v, "public_phone": "abc"},
		"campo extra (css)":       {"expected_version": v, "css": "body{}"},
	} {
		if r := loja.owner.cmd("PATCH", p, body); r.status != 400 {
			t.Errorf("%s: %d", name, r.status)
		}
	}
	saved := loja.owner.mustStatus(loja.owner.cmd("PATCH", p, map[string]any{"expected_version": v, "palette": "teal",
		"public_description": "Cortes clássicos <b>e</b> barba", "public_address": "Rua A, 10", "public_phone": "(11) 99999-8888"}), 200).data()
	if saved["public_phone"] != "+5511999998888" || saved["palette"] != "teal" {
		t.Fatalf("salvo: %v", saved)
	}
	// Outra aba com a versão antiga: conflito visível (com o atual).
	if r := loja.owner.cmd("PATCH", p, map[string]any{"expected_version": v, "palette": "red"}); r.code() != "version_conflict" {
		t.Fatalf("conflito: %s", r.raw)
	}
	// Dono de outra loja não altera esta.
	if r := outra.owner.cmd("PATCH", p, map[string]any{"expected_version": saved["version"], "palette": "red"}); r.status != 404 {
		t.Fatalf("outra loja alterou: %d", r.status)
	}
	if r := outra.owner.upload("purpose=logo&establishment_id="+loja.id.String(), "image/png", pngBytes(t, 50, 50)); r.status != 404 {
		t.Fatalf("logo em outra loja: %d", r.status)
	}
	logo := loja.owner.uploadReady("purpose=logo&establishment_id=" + loja.id.String())
	// A logo enviada para a loja A não serve na loja B.
	ob := outra.owner.mustStatus(outra.owner.do("GET", "/v1/establishments/"+outra.id.String(), nil), 200).data()
	if r := outra.owner.cmd("PUT", "/v1/establishments/"+outra.id.String()+"/logo", map[string]any{"asset_id": logo, "expected_version": ob["version"]}); r.code() != "media_not_attachable" {
		t.Fatalf("logo de outra loja: %s", r.raw)
	}
	withLogo := loja.owner.mustStatus(loja.owner.cmd("PUT", p+"/logo", map[string]any{"asset_id": logo, "expected_version": saved["version"]}), 200).data()
	pub := e.browser().mustStatus(e.browser().do("GET", "/v1/public/slugs/barbearia-teste", nil), 200)
	d := pub.data()
	if d["palette"] != "teal" || d["description"] != "Cortes clássicos <b>e</b> barba" || d["phone"] != "+5511999998888" ||
		firstURL(d["logo"]) != firstURL(withLogo["logo"]) {
		t.Fatalf("público: %v", d)
	}
	for _, private := range []string{"ana@exemplo.com", "version", "public_url"} {
		if strings.Contains(string(pub.raw), private) {
			t.Errorf("público vazou %q", private)
		}
	}
	// Profissional não altera a identidade da loja.
	beto := e.professional(loja.id, "Beto")
	bb := e.staffMember(loja, beto, "beto@exemplo.com")
	if r := bb.cmd("PATCH", p, map[string]any{"expected_version": withLogo["version"], "palette": "red"}); r.status != 403 {
		t.Fatalf("profissional alterou a loja: %d", r.status)
	}
	if n := e.count(t, `SELECT count(*) FROM audit_event WHERE action = 'T-ES-01' AND reason LIKE 'campos:%palette%'`); n != 1 {
		t.Fatal("auditoria sem os campos alterados")
	}
	if n := e.count(t, `SELECT count(*) FROM audit_event WHERE reason LIKE '%99999%' OR reason LIKE '%Rua A%'`); n != 0 {
		t.Fatal("auditoria com valores")
	}
}

// Perfil: aparência, conflito de versão e campos protegidos (e-mail, papel, verificação).
// Cenários: C-CFG-07.
func TestMyProfileSettings(t *testing.T) {
	e, _ := newEnvFull(t)
	maria := e.verifiedCustomer("maria@exemplo.com")
	p := maria.profile()
	if p["appearance"] != "system" || p["has_password"] != true || p["email_verified"] != true {
		t.Fatalf("perfil: %v", p)
	}
	for _, body := range []map[string]any{
		{"appearance": "neon"}, {"name": "M", "email": "x@y.com"}, {"name": "M", "role": "owner"},
		{"name": "M", "email_verified": true}, {"name": "M", "memberships": []any{}},
	} {
		if r := maria.cmd("PATCH", "/v1/me", body); r.status != 400 {
			t.Errorf("%v: %d", body, r.status)
		}
	}
	saved := maria.mustStatus(maria.cmd("PATCH", "/v1/me", map[string]any{"appearance": "dark", "expected_version": p["version"]}), 200).data()
	if saved["appearance"] != "dark" || saved["name"] != p["name"] {
		t.Fatalf("aparência: %v", saved)
	}
	r := maria.cmd("PATCH", "/v1/me", map[string]any{"name": "Outra aba", "expected_version": p["version"]})
	if r.code() != "version_conflict" || r.body["current"].(map[string]any)["appearance"] != "dark" {
		t.Fatalf("conflito: %s", r.raw)
	}
	if maria.profile()["email"] != "maria@exemplo.com" {
		t.Fatal("e-mail mudou")
	}
}

// Respostas novas decodificam nos tipos gerados do contrato, sem campos a mais.
func TestNewResponsesMatchContract(t *testing.T) {
	e, _ := newEnvFull(t)
	b := e.barbearia(t)
	var prof struct{ Data contrato.MyProfile }
	id := b.betoB.uploadReady("purpose=avatar")
	strictDecode(t, b.betoB.mustStatus(b.betoB.cmd("PUT", "/v1/me/avatar", map[string]any{"asset_id": id, "expected_version": 1}), 200).raw, &prof)
	var up struct{ Data contrato.MediaUpload }
	strictDecode(t, b.betoB.mustStatus(b.betoB.do("GET", "/v1/media/uploads/"+id, nil), 200).raw, &up)
	var mp struct {
		Data contrato.MyProfessionalProfile
	}
	strictDecode(t, b.betoB.mustStatus(b.betoB.do("GET", estPath(b.id, "/my-professional-profile"), nil), 200).raw, &mp)
	var est struct{ Data contrato.Establishment }
	strictDecode(t, b.owner.mustStatus(b.owner.do("GET", "/v1/establishments/"+b.id.String(), nil), 200).raw, &est)
	var cfg struct{ Data contrato.PublicConfig }
	strictDecode(t, e.browser().mustStatus(e.browser().do("GET", "/v1/public/config", nil), 200).raw, &cfg)
	var sv struct{ Data contrato.SessionView }
	strictDecode(t, b.betoB.do("GET", "/v1/session", nil).raw, &sv)
	if !cfg.Data.GoogleLogin || !cfg.Data.MediaUploads {
		t.Fatalf("config: %+v", cfg.Data)
	}
}
