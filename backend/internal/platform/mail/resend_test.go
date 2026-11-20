package mail_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"fila/internal/platform/mail"
)

func TestResendSenderRequestAndReceipt(t *testing.T) {
	id := uuid.MustParse("019a0000-0000-7000-8000-000000000001")
	var got struct {
		From    string   `json:"from"`
		To      []string `json:"to"`
		Subject string   `json:"subject"`
		Text    string   `json:"text"`
		ReplyTo string   `json:"reply_to"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/emails" {
			t.Errorf("pedido inesperado %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer re_teste_123" {
			t.Error("Authorization ausente ou errado")
		}
		if r.Header.Get("Idempotency-Key") != "fila-mail/"+id.String() {
			t.Errorf("Idempotency-Key = %q", r.Header.Get("Idempotency-Key"))
		}
		if r.Header.Get("User-Agent") == "" {
			t.Error("User-Agent ausente")
		}
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &got)
		_, _ = w.Write([]byte(`{"id":"re-msg-1"}`))
	}))
	defer srv.Close()

	s := mail.ResendSender{APIKey: "re_teste_123", From: "Fila <nao-responda@mail.exemplo.com>",
		ReplyTo: "contato@exemplo.com", BaseURL: srv.URL}
	pid, err := s.SendReceipt(context.Background(), id, mail.Message{To: "ana@exemplo.com", Subject: "Oi", Body: "Corpo"})
	if err != nil || pid != "re-msg-1" {
		t.Fatalf("pid=%q err=%v", pid, err)
	}
	if got.From != s.From || len(got.To) != 1 || got.To[0] != "ana@exemplo.com" || got.Subject != "Oi" ||
		got.Text != "Corpo" || got.ReplyTo != "contato@exemplo.com" {
		t.Fatalf("corpo enviado incorreto: %+v", got)
	}
}

func TestResendSenderClassifiesErrorsWithoutPersonalData(t *testing.T) {
	cases := []struct {
		status    int
		body      string
		permanent bool
		category  string
	}{
		{429, `{"name":"rate_limit_exceeded","message":"ana@exemplo.com"}`, false, "limite do provedor"},
		{500, `{"message":"ana@exemplo.com"}`, false, "provedor indisponível"},
		{503, ``, false, "provedor indisponível"},
		{401, `{"name":"missing_api_key"}`, false, "credencial ou remetente recusado"},
		{403, `{"name":"validation_error","message":"domain not verified"}`, false, "credencial ou remetente recusado"},
		{422, `{"name":"validation_error","message":"Invalid to: ana@exemplo.com"}`, true, "recusado pelo provedor"},
		{400, `{"name":"validation_error"}`, true, "recusado pelo provedor"},
		{409, `{"name":"concurrent_idempotent_requests"}`, false, "envio concorrente em andamento"},
		{409, `{"name":"invalid_idempotent_request"}`, true, "chave de idempotência em conflito"},
	}
	for _, c := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(c.status)
			_, _ = w.Write([]byte(c.body))
		}))
		s := mail.ResendSender{APIKey: "re_x", From: "Fila <a@b.co>", BaseURL: srv.URL}
		_, err := s.SendReceipt(context.Background(), uuid.New(), mail.Message{To: "ana@exemplo.com", Subject: "s", Body: "token-secreto"})
		srv.Close()
		if err == nil {
			t.Fatalf("HTTP %d: esperava erro", c.status)
		}
		var perm *mail.Permanent
		if errors.As(err, &perm) != c.permanent {
			t.Errorf("HTTP %d: permanente=%v, esperado %v", c.status, !c.permanent, c.permanent)
		}
		if got := mail.ErrorCategory(err); got != c.category {
			t.Errorf("HTTP %d: categoria %q, esperada %q", c.status, got, c.category)
		}
		if msg := err.Error(); strings.Contains(msg, "@") || strings.Contains(msg, "token") || strings.Contains(msg, "re_x") {
			t.Errorf("HTTP %d: erro expõe dado sensível: %q", c.status, msg)
		}
	}
}

func TestResendSenderTimeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	s := mail.ResendSender{APIKey: "re_x", From: "Fila <a@b.co>", BaseURL: srv.URL}
	err := s.Send(ctx, uuid.New(), mail.Message{To: "a@b.co", Subject: "s", Body: "b"})
	var perm *mail.Permanent
	if err == nil || errors.As(err, &perm) || mail.ErrorCategory(err) != "tempo esgotado" {
		t.Fatalf("timeout deveria ser falha transitória 'tempo esgotado': %v", err)
	}
}
