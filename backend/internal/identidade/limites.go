package identidade

import (
	"context"
	"net/http"
	"time"

	"fila/internal/platform/httpserver"
	"fila/internal/platform/ratelimit"
)

// Limites de tentativas (R-CONTA-11, R-VIS-10, R-SEG-04). Os valores estão documentados no
// ADR-0017. Assuntos sempre combinam alvo (conta, código, loja) E origem (IP, navegador):
// nunca só o IP.
var (
	// Senha errada por conta (login, reautenticação, troca de senha, aceite de convite).
	// Ao atingir, novas tentativas para a conta são recusadas até o fim da janela, mesmo com a
	// senha certa (C-ID-09).
	rulePasswordAccount = ratelimit.Rule{Name: "senha_conta", Limit: 10, Window: 15 * time.Minute}
	// Senha errada por IP (protege contra testar uma senha em muitas contas).
	rulePasswordIP = ratelimit.Rule{Name: "senha_ip", Limit: 50, Window: 15 * time.Minute}
	// Pedidos que geram e-mail (cadastro, reenvio, redefinição) por endereço e por IP.
	ruleMailAddress = ratelimit.Rule{Name: "email_endereco", Limit: 3, Window: time.Hour}
	ruleMailIP      = ratelimit.Rule{Name: "email_ip", Limit: 20, Window: time.Hour}
	// Token inválido (verificação, redefinição, convite) por IP.
	ruleTokenIP = ratelimit.Rule{Name: "token_ip", Limit: 20, Window: 15 * time.Minute}
)

func (s *Service) clientIP(r *http.Request) string {
	return httpserver.ClientIP(r, s.cfg.TrustForwardedFor)
}

// checkBlocked devolve 429 se algum assunto já atingiu o limite (sem contar nova tentativa).
func (s *Service) checkBlocked(ctx context.Context, checks ...limitCheck) error {
	for _, c := range checks {
		blocked, retry, err := s.limiter.Exceeded(ctx, c.rule, c.subject)
		if err != nil {
			return err
		}
		if blocked {
			return httpserver.ErrRateLimited(retry)
		}
	}
	return nil
}

// hit registra uma tentativa errada em todos os assuntos.
func (s *Service) hit(ctx context.Context, checks ...limitCheck) error {
	for _, c := range checks {
		if _, err := s.limiter.Hit(ctx, c.rule, c.subject); err != nil {
			return err
		}
	}
	return nil
}

// allow conta o evento em todos os assuntos e devolve 429 se algum passou do limite.
func (s *Service) allow(ctx context.Context, checks ...limitCheck) error {
	var worst time.Duration
	blocked := false
	for _, c := range checks {
		ok, retry, err := s.limiter.Allow(ctx, c.rule, c.subject)
		if err != nil {
			return err
		}
		if !ok {
			blocked = true
			worst = max(worst, retry)
		}
	}
	if blocked {
		return httpserver.ErrRateLimited(worst)
	}
	return nil
}

type limitCheck struct {
	rule    ratelimit.Rule
	subject string
}

func lim(rule ratelimit.Rule, subject string) limitCheck {
	return limitCheck{rule: rule, subject: subject}
}
