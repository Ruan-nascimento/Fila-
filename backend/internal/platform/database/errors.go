package database

import (
	"context"
	"errors"
	"net"

	"github.com/jackc/pgx/v5/pgconn"
)

// Describe transforma um erro de conexão/consulta em texto seguro para log.
//
// As mensagens originais do driver e do PostgreSQL podem conter o nome do usuário, o nome do
// banco ou o endereço (ex.: `failed to connect to user=... database=...`). Se a URL estiver
// mal montada, o "usuário" pode ser até a senha. Por isso nunca registramos o texto original:
// devolvemos só a categoria do problema e, quando houver, o código SQLSTATE.
func Describe(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "tempo esgotado ao falar com o PostgreSQL"
	}
	if errors.Is(err, context.Canceled) {
		return "operação cancelada"
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "28P01", "28000":
			return "o PostgreSQL recusou o usuário ou a senha da FILA_DATABASE_URL (SQLSTATE " + pgErr.Code + ")"
		case "3D000":
			return "o banco indicado na FILA_DATABASE_URL não existe (SQLSTATE 3D000)"
		case "53300":
			return "o PostgreSQL atingiu o limite de conexões (SQLSTATE 53300)"
		case "57P03":
			return "o PostgreSQL está iniciando ou desligando (SQLSTATE 57P03)"
		default:
			return "erro do PostgreSQL (SQLSTATE " + pgErr.Code + ")"
		}
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return "tempo esgotado ao conectar ao endereço do PostgreSQL"
		}
		return "não foi possível conectar ao endereço do PostgreSQL (ele está rodando?)"
	}
	var connErr *pgconn.ConnectError
	if errors.As(err, &connErr) {
		return "não foi possível conectar ao PostgreSQL (endereço, TLS ou credenciais)"
	}
	return "falha ao acessar o PostgreSQL"
}

// Ping confere a conexão e devolve erro com texto seguro para log (ver Describe).
func Ping(ctx context.Context, pool interface{ Ping(context.Context) error }) error {
	if err := pool.Ping(ctx); err != nil {
		return errors.New(Describe(err))
	}
	return nil
}
