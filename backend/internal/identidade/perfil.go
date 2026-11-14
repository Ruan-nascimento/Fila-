package identidade

import (
	"net/http"

	"github.com/jackc/pgx/v5"

	"fila/internal/platform/httpserver"
	"fila/internal/platform/idempotency"
	"fila/internal/platform/validate"
)

// updateMyProfile: a própria conta altera o nome exibido. O nome guardado em visitas e
// reservas já feitas não muda (é o que a equipe viu naquele momento). Não é transição da matriz
// nem ação financeira/de acesso: não gera auditoria.
func (s *Service) updateMyProfile(w http.ResponseWriter, r *http.Request) error {
	sess, err := requireAccount(r)
	if err != nil {
		return err
	}
	var body struct {
		Name *string `json:"name"`
	}
	raw, err := httpserver.DecodeJSON(r, &body)
	if err != nil {
		return err
	}
	var v validate.Collector
	v.Required("/name", body.Name == nil)
	var name string
	if body.Name != nil {
		var fe *httpserver.FieldError
		name, fe = validate.Text("/name", *body.Name, 1, 80)
		v.Add(fe)
	}
	if err := v.Err(); err != nil {
		return err
	}
	ctx := r.Context()
	cmd := idempotency.Command{ActorKey: "conta:" + sess.AccountID.String(), Operation: "updateMyProfile", Body: raw}
	return s.idem.Run(w, r, cmd, func(tx pgx.Tx) (idempotency.Result, error) {
		now := s.clock.Now()
		var out accountView
		if err := tx.QueryRow(ctx, `UPDATE account SET name = $2, updated_at = $3 WHERE id = $1 AND status = 'active'
			RETURNING id, name, email, status`, sess.AccountID, name, now).Scan(&out.ID, &out.Name, &out.Email, &out.Status); err != nil {
			if err == pgx.ErrNoRows {
				return idempotency.Result{}, httpserver.ErrUnauthenticated()
			}
			return idempotency.Result{}, err
		}
		return idempotency.Result{Status: http.StatusOK, Body: httpserver.Data[accountView]{Data: out}}, nil
	})
}
