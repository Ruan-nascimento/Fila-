package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"fila/internal/contrato"
)

type openapiDoc struct {
	Paths map[string]map[string]struct {
		OperationID string `yaml:"operationId"`
		Authz       []struct {
			Actor string `yaml:"actor"`
			Scope string `yaml:"scope"`
		} `yaml:"x-authz"`
		Reauth bool `yaml:"x-reauth"`
	} `yaml:"paths"`
}

func loadOpenAPI(t *testing.T) openapiDoc {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "contrato", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var doc openapiDoc
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

// Toda rota implementada existe no contrato com o mesmo operationId, e a lista de atores
// autorizados declarada no código é exatamente o x-authz do contrato (negação por padrão).
func TestRoutesMatchContract(t *testing.T) {
	doc := loadOpenAPI(t)
	e := newEnv(t)
	if len(e.api.Routes()) == 0 {
		t.Fatal("nenhuma rota registrada")
	}
	for _, rt := range e.api.Routes() {
		op, ok := doc.Paths[rt.Path][strings.ToLower(rt.Method)]
		if !ok {
			t.Errorf("%s %s não existe no contrato", rt.Method, rt.Path)
			continue
		}
		if op.OperationID != rt.Operation {
			t.Errorf("%s %s: operationId %q no contrato, %q no código", rt.Method, rt.Path, op.OperationID, rt.Operation)
		}
		var want, got []string
		for _, a := range op.Authz {
			want = append(want, a.Actor+"/"+a.Scope)
		}
		for _, g := range rt.Grants {
			got = append(got, g.Actor+"/"+g.Scope)
		}
		slices.Sort(want)
		slices.Sort(got)
		if !slices.Equal(want, got) {
			t.Errorf("%s: x-authz do contrato %v ≠ código %v", rt.Operation, want, got)
		}
	}
}

// Respostas decodificam nos tipos gerados do contrato sem campos a mais.
func strictDecode(t *testing.T, raw []byte, dst any) {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		t.Fatalf("resposta fora do contrato: %v\n%s", err, raw)
	}
}

func TestResponsesMatchContractTypes(t *testing.T) {
	e := newEnv(t)
	loja := e.shop("barbearia-teste", "ana@exemplo.com")
	beto := e.professional(loja.id, "Beto")

	var sv struct{ Data contrato.SessionView }
	strictDecode(t, loja.owner.do("GET", "/v1/session", nil).raw, &sv)
	if sv.Data.Account == nil || sv.Data.Session == nil || len(sv.Data.Memberships) != 1 || sv.Data.CsrfToken == nil {
		t.Fatalf("SessionView incompleta: %+v", sv.Data)
	}
	var anon struct{ Data contrato.SessionView }
	strictDecode(t, e.browser().do("GET", "/v1/session", nil).raw, &anon)
	if anon.Data.Account != nil || anon.Data.Session != nil || anon.Data.CsrfToken != nil || anon.Data.Visitors == nil || anon.Data.Memberships == nil {
		t.Fatalf("SessionView anônima: %+v", anon.Data)
	}

	loja.owner.reauth()
	var inv struct{ Data contrato.Invite }
	strictDecode(t, loja.owner.mustStatus(loja.owner.do("POST", "/v1/establishments/"+loja.id.String()+"/professionals/"+beto.String()+"/invites",
		map[string]any{"email": "beto@exemplo.com"}, "Idempotency-Key", newIdemKey()), 201).raw, &inv)

	var pub struct{ Data contrato.PublicEstablishment }
	strictDecode(t, e.browser().mustStatus(e.browser().do("GET", "/v1/public/slugs/barbearia-teste", nil), 200).raw, &pub)
	if pub.Data.Id.String() != loja.id.String() || pub.Data.Timezone != "America/Sao_Paulo" {
		t.Fatalf("loja pública: %+v", pub.Data)
	}
	if r := e.browser().do("GET", "/v1/public/slugs/nao-existe", nil); r.status != 404 {
		t.Fatalf("slug inexistente: %d", r.status)
	}

	var prob contrato.Problem
	strictDecode(t, e.browser().do("POST", "/v1/auth/login", map[string]any{"email": "x"}).raw, &prob)
	if prob.Code != "validation_failed" || prob.RequestId == "" || prob.Errors == nil {
		t.Fatalf("Problem: %+v", prob)
	}
	var accepted struct {
		Data struct {
			Message string `json:"message"`
		}
	}
	strictDecode(t, e.browser().do("POST", "/v1/auth/password-reset/request", map[string]any{"email": "a@b.co"}).raw, &accepted)
}

// C-SEG-08 / C-SEG-11 — segredos e dados pessoais fora de logs, auditoria e banco em claro.
func TestNoSecretsInLogsAuditOrDatabase(t *testing.T) {
	e := newEnv(t)
	loja := e.shop("barbearia-teste", "ana@exemplo.com")
	maria := e.verifiedCustomer("maria@exemplo.com")
	maria.mustStatus(maria.do("POST", "/v1/auth/password/change", map[string]any{"current_password": goodPassword, "new_password": "outra-senha-forte"}), 204)
	maria.do("POST", "/v1/auth/password-reset/request", map[string]any{"email": "maria@exemplo.com"})
	resetTok := e.lastToken("maria@exemplo.com", "Redefinição")
	sessionCookie := maria.cookie("__Host-fila_session").Value

	logs := e.logs.String()
	for _, secret := range []string{goodPassword, "outra-senha-forte", resetTok, sessionCookie, maria.csrf, "maria@exemplo.com", "ana@exemplo.com"} {
		if strings.Contains(logs, secret) {
			t.Errorf("log contém dado sensível %q", secret[:min(6, len(secret))]+"…")
		}
	}
	assertNotInDatabase(t, e, resetTok)
	assertNotInDatabase(t, e, sessionCookie)
	assertNotInDatabase(t, e, goodPassword)

	rows, err := e.pool.Query(context.Background(), `SELECT row_to_json(a)::text FROM audit_event a`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		n++
		if strings.Contains(s, "@") || strings.Contains(s, "Dona") || strings.Contains(s, "Pessoa") {
			t.Errorf("auditoria com dado pessoal: %s", s)
		}
	}
	if n < 8 {
		t.Fatalf("esperava várias transições auditadas, achei %d", n)
	}
	_ = loja
}

// assertNotInDatabase procura o texto em todas as colunas de texto e bytea do esquema público.
func assertNotInDatabase(t *testing.T, e *env, secret string) {
	t.Helper()
	ctx := context.Background()
	rows, err := e.pool.Query(ctx, `
		SELECT table_name, column_name, data_type FROM information_schema.columns
		WHERE table_schema = 'public' AND data_type IN ('text', 'bytea', 'jsonb', 'character varying')`)
	if err != nil {
		t.Fatal(err)
	}
	type col struct{ table, column, typ string }
	var cols []col
	for rows.Next() {
		var c col
		_ = rows.Scan(&c.table, &c.column, &c.typ)
		cols = append(cols, c)
	}
	rows.Close()
	for _, c := range cols {
		expr := `"` + c.column + `"::text`
		if c.typ == "bytea" {
			expr = `encode("` + c.column + `", 'escape')`
		}
		var n int
		q := `SELECT count(*) FROM "` + c.table + `" WHERE position($1 in ` + expr + `) > 0`
		if err := e.pool.QueryRow(ctx, q, secret).Scan(&n); err != nil {
			t.Fatalf("%s.%s: %v", c.table, c.column, err)
		}
		if n > 0 {
			t.Errorf("segredo encontrado em claro em %s.%s", c.table, c.column)
		}
	}
}
