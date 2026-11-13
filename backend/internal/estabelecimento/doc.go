// Package estabelecimento é o módulo de loja, profissionais, vínculos de equipe e estado das filas (aberta, pausada, fechada).
//
// Etapa 02: loja (criada pelo operador) e vínculos de equipe. Etapa 03: configuração da loja,
// cadastro/desativação de profissionais, fila de cada profissional e sinal de indisponibilidade.
// Regras: R-FILA-01, R-FILA-06…08, R-PERM-*. Transições: T-ES-*, T-PR-*, T-Q-*
// (ver docs/regras.md, docs/matriz-transicoes.md e ADR-0018).
//
// Fronteiras (ADR-0001, ADR-0018): o módulo é dono das tabelas establishment, professional,
// staff_membership e queue. Autorização chega pela interface acesso.Authorizer; participações
// (etapa 04) chegam pela interface Occupancy. A plataforma não importa módulos.
package estabelecimento
