package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"fila/internal/platform/config"
)

func TestRedactsSensitiveAttributes(t *testing.T) {
	var buf bytes.Buffer
	log := New(&buf, slog.LevelInfo, "json")
	log.Info("teste",
		"password", "p@ss-1",
		"session_token", "tok-2",
		"Authorization", "Bearer x-3",
		slog.Group("req", "cookie", "c-4"),
		"database", config.Secret("postgres://u:s-5@h/db"),
		"route", "/healthz/live",
	)
	out := buf.String()
	for _, leak := range []string{"p@ss-1", "tok-2", "x-3", "c-4", "s-5"} {
		if strings.Contains(out, leak) {
			t.Fatalf("segredo %q vazou no log: %s", leak, out)
		}
	}
	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatalf("log não é JSON válido: %v", err)
	}
	if m["route"] != "/healthz/live" {
		t.Fatalf("atributo comum deveria permanecer: %v", m)
	}
}

func TestTextFormatAndLevel(t *testing.T) {
	var buf bytes.Buffer
	log := New(&buf, slog.LevelWarn, "text")
	log.Info("não aparece")
	log.Warn("aparece", "secret", "zzz")
	out := buf.String()
	if strings.Contains(out, "não aparece") || !strings.Contains(out, "aparece") || strings.Contains(out, "zzz") {
		t.Fatalf("saída inesperada: %s", out)
	}
}
