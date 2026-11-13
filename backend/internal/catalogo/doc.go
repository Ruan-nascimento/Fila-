// Package catalogo é o módulo de serviços (nome, preço em centavos, duração, pontos) e habilitações por profissional.
//
// Etapa 03. Regras: R-CMD-01…05, R-PERF-01. Transições: T-SV-* (ver docs/regras.md,
// docs/matriz-transicoes.md e ADR-0018).
//
// Princípios: preço só em centavos inteiros e só definido pelo dono; cada versão do serviço fica
// em service_revision (imutável); desativar nunca apaga; o catálogo público não mostra pontos,
// versão nem dados internos.
//
// Fronteiras (ADR-0001): dono das tabelas service, service_professional e service_revision;
// usa o módulo estabelecimento só pelas funções exportadas. A plataforma não importa módulos.
package catalogo
