package jsonscrub

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRewriteOnlyTheTargetPerson(t *testing.T) {
	tg := Targets{Participations: map[string]bool{"p-sai": true}, Reservations: map[string]bool{"r-sai": true}, Replacement: "Cliente"}
	// Duas pessoas com o nick "waiting" (igual a um estado) e o mesmo nome de reserva.
	doc := `{"data":{"status":"waiting","entries":[
		{"participation_id":"p-sai","nick":"waiting","status":"waiting","amount_cents":4500},
		{"participation_id":"p-fica","nick":"waiting","status":"waiting","amount_cents":12345678901234567}],
		"participation":{"id":"p-sai","nick":"Zé <b>","status":"called"},
		"reservation":{"id":"r-sai","holder_display_name":"Zé","status":"active"},
		"outra_reserva":{"id":"r-fica","holder_display_name":"Zé"}}}`
	out, changed, err := tg.Rewrite([]byte(doc))
	if err != nil || !changed {
		t.Fatalf("deveria mudar: %v", err)
	}
	var v struct {
		Data struct {
			Status  string `json:"status"`
			Entries []struct {
				PID    string      `json:"participation_id"`
				Nick   string      `json:"nick"`
				Status string      `json:"status"`
				Amount json.Number `json:"amount_cents"`
			} `json:"entries"`
			Participation struct{ Nick, Status string } `json:"participation"`
			Reservation   struct {
				Holder string `json:"holder_display_name"`
				Status string `json:"status"`
			} `json:"reservation"`
			Outra struct {
				Holder string `json:"holder_display_name"`
			} `json:"outra_reserva"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		t.Fatal(err)
	}
	d := v.Data
	if d.Status != "waiting" || d.Entries[0].Status != "waiting" || d.Entries[1].Status != "waiting" ||
		d.Participation.Status != "called" || d.Reservation.Status != "active" {
		t.Fatalf("estado trocado: %s", out)
	}
	if d.Entries[0].Nick != "Cliente" || d.Participation.Nick != "Cliente" || d.Reservation.Holder != "Cliente" {
		t.Fatalf("o nome da pessoa excluída ficou: %s", out)
	}
	if d.Entries[1].Nick != "waiting" || d.Outra.Holder != "Zé" {
		t.Fatalf("mexeu em outra pessoa: %s", out)
	}
	if d.Entries[1].Amount.String() != "12345678901234567" || strings.Contains(string(out), `<`) {
		t.Fatalf("número ou texto alterado na regravação: %s", out)
	}
	// Sem nada da pessoa: documento idêntico, byte a byte.
	same := `{"id":"x","nick":"waiting"}`
	if out, changed, _ := tg.Rewrite([]byte(same)); changed || string(out) != same {
		t.Fatal("não deveria mudar")
	}
	if _, changed, _ := tg.Rewrite([]byte("não é json")); changed {
		t.Fatal("texto que não é JSON fica igual")
	}
	if !(Targets{}).Empty() || tg.Empty() || len(tg.IDs()) != 2 {
		t.Fatal("Empty/IDs")
	}
}
