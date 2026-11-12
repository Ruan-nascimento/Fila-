package identidade

import (
	"context"
	"fmt"
	"time"

	"fila/internal/platform/database"
	"fila/internal/platform/mail"
)

// Links enviados por e-mail levam o token no fragmento (#token=...): o navegador não envia o
// fragmento ao servidor, então ele não aparece em logs de acesso, proxies nem no cabeçalho
// Referer. A página lê o fragmento, apaga-o da barra de endereço e envia o token no corpo
// de um POST (ADR-0017).
func (s *Service) link(path, token string) string {
	return s.cfg.PublicBaseURL + path + "#token=" + token
}

func (s *Service) mailVerification(ctx context.Context, q database.Querier, to, name, token string, now time.Time) error {
	body := fmt.Sprintf(`Olá, %s.

Para ativar sua conta na Fila, confirme seu e-mail abrindo o link abaixo (válido por 24 horas):

%s

Se você não criou esta conta, ignore este e-mail.`, name, s.link("/verificar-email", token))
	return s.outbox.Enqueue(ctx, q, "verificacao_email", mail.Message{To: to, Subject: "Confirme seu e-mail", Body: body}, now)
}

func (s *Service) mailAlreadyRegistered(ctx context.Context, q database.Querier, to string, now time.Time) error {
	body := fmt.Sprintf(`Olá.

Alguém tentou criar uma conta na Fila com este e-mail, mas ele já tem uma conta.
Se foi você, entre com sua senha. Se esqueceu a senha, use "Esqueci minha senha":

%s

Se não foi você, ignore este e-mail. Nada foi alterado.`, s.cfg.PublicBaseURL+"/esqueci-senha")
	return s.outbox.Enqueue(ctx, q, "conta_existente", mail.Message{To: to, Subject: "Você já tem uma conta", Body: body}, now)
}

func (s *Service) mailPasswordReset(ctx context.Context, q database.Querier, to, token string, now time.Time) error {
	body := fmt.Sprintf(`Olá.

Recebemos um pedido para redefinir a senha da sua conta na Fila.
Abra o link abaixo em até 30 minutos (ele só funciona uma vez):

%s

Ao redefinir, todas as sessões abertas da conta serão encerradas.
Se você não pediu, ignore este e-mail: sua senha continua a mesma.`, s.link("/redefinir-senha", token))
	return s.outbox.Enqueue(ctx, q, "redefinicao_senha", mail.Message{To: to, Subject: "Redefinição de senha", Body: body}, now)
}

func (s *Service) mailInvite(ctx context.Context, q database.Querier, to, establishmentName, role, token string, now time.Time) error {
	papel := "profissional"
	if role == "owner" {
		papel = "responsável (dono)"
	}
	body := fmt.Sprintf(`Olá.

Você foi convidado para acessar a loja "%s" na Fila como %s.
Para aceitar, abra o link abaixo em até 24 horas (ele só funciona uma vez):

%s

Se você já tem conta com este e-mail, informe a senha dela. Se não tem, a conta será criada.
Se não esperava este convite, ignore este e-mail.`, establishmentName, papel, s.link("/convite", token))
	return s.outbox.Enqueue(ctx, q, "convite_"+role, mail.Message{To: to, Subject: "Convite para a equipe", Body: body}, now)
}
