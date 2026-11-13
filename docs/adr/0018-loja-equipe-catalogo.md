# ADR-0018 — Loja, equipe e catálogo: limites entre módulos e dependências de etapas futuras

- Situação: Aceita (etapa 03)
- Data: 2026-11-12

## Contexto

A etapa 03 implementa configuração da loja, profissionais, filas (aberta/pausada/fechada) e catálogo. Algumas regras dependem de coisas que só existem em etapas futuras:

- fechar fila só sem ninguém aguardando ou chamado (T-Q-04);
- desativar profissional só sem atendimento ativo (T-PR-02);
- as listas de filas devolverem um cursor de eventos (SSE, etapa 06).

Além disso, os módulos `estabelecimento` e `catalogo` precisam de autorização (dono, membro da equipe). Quem sabe isso é o módulo `identidade`, e `identidade` já depende de `estabelecimento`.

## Decisão

1. **Autorização por interface (`internal/acesso`).**
   - Um pacote sem dependências de domínio define `Authorizer` (`Owner`, `Member`, `RequireReauth`) e o tipo `Actor`.
   - O módulo `identidade` implementa a interface (cookies → sessão → vínculos); `estabelecimento` e `catalogo` só a recebem.
   - Assim não há ciclo de importação, e o papel continua vindo só da sessão (R-PERM-06).
   - Regras comuns: sem sessão → 401; sem vínculo com a loja → 404; vínculo com sessão de cliente ou papel insuficiente → 403.
2. **Ocupação da fila por interface (`estabelecimento.Occupancy`).**
   - As regras de fechar a fila e de desativar o profissional consultam a ocupação por essa interface. O módulo `fila` vai implementá-la na etapa 04.
   - Enquanto não existem participações, a interface é `nil` e as contagens são zero.
   - Os testes injetam uma ocupação simulada para provar que as regras bloqueiam.
3. **Cursor das listas de filas.**
   - Novo esquema `SnapshotCursor` = `StreamCursor | null` em `listStaffQueues` e `listPublicQueues`.
   - Até a etapa 06 o valor é `null`: o cliente relê a lista após cada comando e ao voltar para a aba.
   - Não criamos um cursor falso.
4. **Histórico não se apaga.**
   - Gatilhos recusam `DELETE` em loja, profissional, fila, serviço e revisão de serviço. Desativar é mudar o estado.
   - Cada versão do serviço (preço, duração, pontos, situação, habilitações) fica em `service_revision`, que é imutável.
   - Assim mudanças de catálogo valem só para inclusões futuras, e dá para saber o preço vigente em qualquer momento. O item da comanda guardará a própria cópia na etapa 05 (R-CMD-05).
5. **Fim do vínculo.**
   - Desativar o profissional desativa o vínculo dele (`ended_reason = professional_deactivated`) e fecha a fila.
   - O módulo `identidade` é avisado na mesma transação (`AccessRevoked`): revoga convites pendentes e encerra as sessões de equipe das contas que ficaram sem vínculo.
   - Reativar religa só esse vínculo; a fila continua fechada até o dono abrir.
6. **Detalhes reversíveis.**
   - Letra da fila repetida → `400 validation_failed` (código `duplicate`), como no cenário C-EST-01.
   - Profissional de outra loja ao criar serviço → `400` (validação, T-SV-01); ao trocar habilitações → `404` (T-SV-05).
   - O sinal de indisponibilidade não muda a versão da fila (T-Q-05 só usa Idempotency-Key).
   - O catálogo público mostra só profissionais ativos.

## Alternativas consideradas

- **Pôr os handlers de loja e catálogo dentro de `identidade`:** misturaria responsabilidades e faria o módulo crescer sem limite.
- **Criar já a tabela de participações para checar a ocupação:** anteciparia a etapa 04 sem as regras dela (FIFO, senha, concorrência).
- **Cursor fictício (ex.: `"0"`):** o cliente acharia que existe um fluxo de eventos e tentaria retomá-lo.
- **Apagar registros ao desativar:** quebraria comandas antigas e a reconciliação de pagamentos.

## Consequências

- A etapa 04 precisa ligar `Occupancy` (módulo `fila`) em `api.New`, e rodar de novo os testes de fechamento e desativação com participações reais (C-EST-04, C-EST-05).
- A etapa 06 passa a preencher `cursor` e retira o `null` do esquema, se todas as listas tiverem fluxo.
- Os testes de arquitetura conferem que cada módulo só consulta as próprias tabelas.
