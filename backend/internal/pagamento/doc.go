// Package pagamento é o módulo de cobrança Pix (adaptador do provedor), recebimentos, conferência, estornos e pendências financeiras.
//
// Etapa 08: cobrança Pix (AbacatePay, criação em duas fases e reconciliação pelo worker),
// recebimento presencial e conferência (ADR-0023, docs/pagamentos-abacatepay.md). Etapa 09:
// webhook, estornos e pendências financeiras. Regras: R-PAG-*. Transições: T-PX-*, T-RC-*, T-RF-*, T-FI-*
// (ver docs/regras.md e docs/matriz-transicoes.md).
//
// Fronteiras (ADR-0001): o módulo é dono das suas tabelas; outros módulos usam suas funções
// exportadas, nunca suas tabelas diretamente. A plataforma (internal/platform) não importa módulos.
package pagamento
