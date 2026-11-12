// Package identidade é o módulo de contas, verificação de e-mail, login e sessões, reautenticação,
// visitantes e código de recuperação, convites de equipe e (na etapa 07) exclusão de conta.
//
// Implementado na etapa 02. Regras: R-CONTA-*, R-VIS-*, R-SEG-02/03. Transições: T-AC-01/02,
// T-SS-*, T-PW-*, T-IN-*, T-VS-*, T-OP-01 (ver docs/regras.md, docs/matriz-transicoes.md e ADR-0017).
//
// Princípios:
//   - O ator vem só dos cookies (Principal), nunca do corpo da requisição.
//   - Segredos (senha, tokens, código) nunca ficam em claro no banco, em logs ou em URLs.
//   - Respostas públicas não revelam se um e-mail existe.
//
// Fronteiras (ADR-0001): o módulo é dono das suas tabelas (account, account_token,
// account_session, invite e, na etapa 02, as de visitante); usa o módulo estabelecimento só
// pelas funções exportadas dele. A plataforma (internal/platform) não importa módulos.
package identidade
