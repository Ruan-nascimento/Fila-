// Package catalogo é o módulo de serviços, preços em centavos, duração, pontos por serviço e profissionais habilitados.
//
// Implementação prevista na etapa 03. Regras: R-CMD-01…05. Transições: T-SV-*
// (ver docs/regras.md e docs/matriz-transicoes.md).
//
// Fronteiras (ADR-0001): o módulo é dono das suas tabelas; outros módulos usam suas funções
// exportadas, nunca suas tabelas diretamente. A plataforma (internal/platform) não importa módulos.
package catalogo
