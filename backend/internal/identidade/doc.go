// Package identidade é o módulo de contas, verificação de e-mail, login e sessões, reautenticação, visitantes e código de recuperação, convites de profissionais e exclusão de conta.
//
// Implementação prevista na etapa 02 (exclusão na 07). Regras: R-CONTA-*, R-VIS-*, R-EXC-*. Transições: T-AC-*, T-SS-*, T-PW-*, T-IN-*, T-VS-*
// (ver docs/regras.md e docs/matriz-transicoes.md).
//
// Fronteiras (ADR-0001): o módulo é dono das suas tabelas; outros módulos usam suas funções
// exportadas, nunca suas tabelas diretamente. A plataforma (internal/platform) não importa módulos.
package identidade
