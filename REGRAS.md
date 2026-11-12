# Prompts para construir o MVP de filas — versão web

Versão 2 • 12/11/2026

Documento de regras: https://docs.google.com/document/d/1tWCD-cLcqXPgbG5huI61kZ_Sa4E8t8B8MweziEvdUR4/edit

Este guia substitui a versão anterior. Primeiro construímos o sistema web completo e responsivo. Desktop e aplicativos instaláveis ficam para outra etapa. As decisões de produto do anexo já estão adotadas para o MVP; correções futuras devem ser registradas e preservar histórico.

## Como usar

1. Abra a pasta do projeto no seu assistente de programação.
2. Forneça este arquivo inteiro como contexto.
3. Peça para executar o PROMPT-BASE e somente a etapa atual, começando pela 00.
4. Leia a explicação e confira os resultados reais antes de avançar.
5. Use o prompt de revisão ao terminar cada etapa.

Cada etapa pode conter várias tarefas pequenas. Não peça para construir todas de uma vez. A numeração abaixo é ordem de execução, não renumeração automática do Jira; vínculos e descrições antigos precisam ser conciliados antes de marcar tickets como concluídos.

## Ordem de construção

| Etapa | Entrega | Dependências |
|---|---|---|
| 00 | Regras, decisões e contrato | Nenhuma etapa anterior. |
| 01 | Estrutura e ambiente | 00. |
| 02 | Identidade, visitantes e acesso | 01 e contratos de 00. |
| 03 | Estabelecimento, equipe e serviços | 02. |
| 04 | Fila transacional | 03. |
| 05 | Pedido, serviços e atendimento | 04. |
| 06 | Atualizações SSE confiáveis | 05. |
| 07 | Histórico e exclusão | 02, 05 e 06. |
| 08 | Pagamentos e AbacatePay | 05. |
| 09 | Reconciliação, conferência e estorno | 08 e 06. |
| 10 | Pontos e prêmios | 07 e 09. |
| 11 | Web responsiva completa | 02–10 e contratos atualizados. |
| 12 | Operação, recuperação e implantação | 11. |
| 13 | Revisão final e piloto | 00–12. |

## PROMPT-BASE — Sempre acompanhar a etapa

```text
Atue como engenheiro responsável por implementar e verificar esta etapa do MVP de filas. Leia primeiro o repositório, suas instruções e a especificação deste arquivo. Explique decisões em português simples, pois o responsável é iniciante.

Escopo: backend/ em Go; frontend/ web React + TypeScript + Vite; PostgreSQL; monólito modular; comandos HTTP e atualizações SSE. Interface integralmente responsiva para cliente, profissional e dono. Desktop e apps nativos depois. Não crie componentes de produção fictícios nem funcionalidades fora da etapa.

Use as regras do anexo como base. Diante de conflito com código antigo, descreva o conflito e ajuste com migração compatível. Decisões de arquitetura novas exigem ADR curto: contexto, decisão, alternativas e consequências. Escolha autonomamente detalhes reversíveis; não invente políticas financeiras ou capacidades externas para disfarçar bloqueios.

Antes de editar: identifique dependências, tarefas e critérios observáveis. Depois implemente uma fatia completa e teste. Não apague trabalho existente. Autorize cada operação no servidor com negação por padrão; filtre por estabelecimento e propriedade; valide entradas; use SQL parametrizado e dinheiro em centavos. Nunca confie em preço, identidade ou papel enviados pelo frontend. Nenhum segredo em código, URL, logs ou bundle web.

Proteja invariantes com transações e restrições no banco. Projete comandos repetíveis e disputa concorrente, sem presumir entrega única de rede/eventos. Registre auditoria mínima para ações relevantes sem expor dados pessoais. Eventos de mudança só após persistência, via outbox transacional. Erros devem ser claros para o usuário e úteis para diagnóstico sem vazar dados.

Consulte documentação oficial atual para versões e APIs externas. Não afirme compatibilidade, teste ou segurança sem evidência. Testes devem cobrir comportamento e falhas reais: autorização indevida, concorrência, rede, repetição e recuperação conforme a etapa. Use testes unitários, integração e E2E na proporção necessária; não faça apenas mocks de todo o sistema.

Entregue código, migrações/configuração pertinentes, contrato e documentação atualizados. Ao final informe: o que mudou, arquivos relevantes, como executar, testes realmente realizados, resultados e limitações. Se algo impedir conclusão, indique exatamente o bloqueio e continue o trabalho independente. Não avance silenciosamente para outra etapa nem publique/ative cobranças reais.
```

## PROMPT 00 — Regras, decisões e contrato

Dependências: Nenhuma etapa anterior. Copie com o PROMPT-BASE e mantenha o anexo disponível.

```text
Execute a etapa 00: Regras, decisões e contrato.

Leia integralmente a especificação anexa. Produza docs/regras.md, ADRs numerados e matriz de transições e permissões. Cada transição deve identificar estado inicial/final, ator, pré-condições, efeito, idempotência, auditoria e erro esperado. Separe estados da fila, pedido, pagamento e resgate: terminar o corte libera a cadeira e não significa pagamento confirmado.
Documente as decisões já tomadas sem recolocá-las como dúvidas. Liste apenas limitações externas reais, como capacidades do provedor e retenção legal. Desenhe o contrato OpenAPI e envelopes de erro, paginação, identificadores, dinheiro em centavos e datas UTC. Defina eventos SSE com versão, audiência e estratégia de recuperação. Defina cenários de aceite legíveis, incluindo dois usuários disputando a última vaga/reserva. Não implemente telas ou integrações ainda.
Aceite: nenhuma mudança de estado sem ator autorizado; regras do anexo rastreáveis a cenário e contrato; frontend e backend usando o mesmo contrato.
```

## PROMPT 01 — Estrutura e ambiente

Dependências: 00. Copie com o PROMPT-BASE e mantenha o anexo disponível.

```text
Execute a etapa 01: Estrutura e ambiente.

Crie backend/ para Go e frontend/ para React, TypeScript e Vite. Permita docs/ na raiz. Use monólito modular em Go, PostgreSQL, migrações versionadas e execução local documentada. API e worker podem usar executáveis separados do mesmo backend; não introduza microsserviços, Redis ou filas externas sem necessidade demonstrada.
Separe módulos identidade, estabelecimento, catálogo, fila, atendimento, pagamento, fidelidade e auditoria. Defina interfaces apenas nos limites úteis. Selecione versões suportadas consultando documentação oficial e registre-as em lockfiles. Configure validação, logs estruturados sem segredos, configuração por ambiente e .env.example sem valores reais. Configure readiness/liveness, timeouts e encerramento gracioso. Inclua infraestrutura local do banco e scripts de migração.
Crie CI com formatação, análise estática, testes, build e verificação de dependências. Inclua README com comandos exatos para um iniciante.
Aceite: ambiente limpo sobe com instruções; banco vazio migra; builds passam; configuração inválida falha claramente; nenhum segredo versionado.
```

## PROMPT 02 — Identidade, visitantes e acesso

Dependências: 01 e contratos de 00. Copie com o PROMPT-BASE e mantenha o anexo disponível.

```text
Execute a etapa 02: Identidade, visitantes e acesso.

Implemente conta com verificação de e-mail, login, logout, recuperação de senha, sessões e convites de profissionais. Dono inicial somente por processo controlado. Use hashing de senha apropriado com parâmetros documentados, tokens aleatórios fortes, armazenamento de tokens de recuperação em hash, expiração e uso único. Mensagens públicas não devem revelar se um e-mail existe.
Implemente sessão de visitante e código de recuperação conforme durações do anexo; nick não autentica. A recuperação revoga credenciais anteriores. Não aceite associar visita ativa ou pendente a uma nova conta. Nunca considere possível impedir perfeitamente identidades anônimas em vários dispositivos.
No navegador use cookies Secure e HttpOnly, política SameSite compatível com implantação, proteção CSRF e validação de origem nas mutações. Não armazene token de sessão no localStorage. Autorize estabelecimento e propriedade do recurso no servidor em todas as rotas.
Teste login, expiração, replay do código, logout, reset, convite reutilizado, escalada de papel, acesso a outro estabelecimento e brute force limitado. Use relógio controlável em testes de expiração.
Aceite: visitante recupera somente sua visita com prova válida; nenhuma rota aceita permissões declaradas pelo próprio cliente.
```

## PROMPT 03 — Estabelecimento, equipe e serviços

Dependências: 02. Copie com o PROMPT-BASE e mantenha o anexo disponível.

```text
Execute a etapa 03: Estabelecimento, equipe e serviços.

Implemente configuração da barbearia, profissionais, vínculos, habilitações e catálogo com nome, preço positivo em centavos, duração e pontos inteiros não negativos. Desativação não apaga histórico. Garanta isolamento por estabelecimento inclusive nas consultas indiretas.
Implemente fila aberta/pausada/fechada e suas permissões conforme anexo. Apenas dono abre, pausa, fecha e transfere. Impedir fechamento com pessoas esperando/chamadas sem resolução e desativação de profissional com atendimento ativo. Não bloquear reconciliação de pedidos antigos.
Implemente telas administrativas responsivas básicas com validação de formulário e autorização real no backend.
Teste alterações concorrentes, catálogo desativado, profissional incompatível e consultas cruzadas.
Aceite: catálogo público não expõe dados privados; preço enviado pelo navegador nunca define o total; histórico preserva itens antigos.
```

## PROMPT 04 — Fila transacional

Dependências: 03. Copie com o PROMPT-BASE e mantenha o anexo disponível.

```text
Execute a etapa 04: Fila transacional.

Implemente entrada, saída, chamada, início, ausência, transferência e consulta por profissional. FIFO é definido pelo servidor com ordenação determinística. Uma participação ativa por identidade/estabelecimento; pedido pendente bloqueia nova entrada daquela identidade. Limites contra abuso não devem depender somente do endereço IP.
Use transações, restrições do banco e bloqueio/controle de versão para proteger concorrência. Idempotência de comandos deve ser vinculada ao ator/operação e rejeitar mesma chave com payload diferente. Não use apenas mutex local.
Implemente tolerância fixa de dez minutos e ausência manual após o prazo, sem punição de pontos. Transferência exige dono, consentimento registrado, estado esperando, profissional apto e posição no final da fila de destino. Não forneça reordenação arbitrária.
Exiba ao público senhas, quantidade e estimativa identificada como estimativa; esconda nick, serviços e finanças alheios.
Teste entradas simultâneas, dupla chamada, chamada versus saída, transferência versus início, pausa e pedido pendente.
Aceite: nenhum cliente duplicado por corrida, nenhum atendimento duplo para o mesmo profissional e transições inválidas retornam conflito explicável.
```

## PROMPT 05 — Pedido, serviços e atendimento

Dependências: 04. Copie com o PROMPT-BASE e mantenha o anexo disponível.

```text
Execute a etapa 05: Pedido, serviços e atendimento.

Crie pedido associado à visita com snapshots de nome, preço, duração, pontos e elegibilidade. Recalcule tudo no servidor. Enquanto esperando, cliente altera serviços; após chamada, solicitação depende de aprovação do profissional, uma pendente por vez. Item realizado não pode ser removido pelo cliente. Use versão esperada e retorne conflito sem sobrescrever alteração alheia.
Conclusão exige resolver solicitações e registrar serviços executados; congela total e libera cadeira, mantendo financeiro separado. Alterações de catálogo não modificam itens aceitos.
Interrupção registra motivo e itens executados e aguarda revisão do dono. Só itens integralmente realizados são cobrados pelo preço aceito; parciais/não iniciados valem zero; sem pontos. Pedido zero fecha com motivo sem pagamento fictício.
Após fechamento, ajuste só pelo dono, auditado e condicionado à reconciliação de cobrança pendente/desconhecida.
Grave eventos em outbox na mesma transação da mudança.
Teste pedido concorrente, preço adulterado, serviço removido do catálogo, interrupção positiva/zero e conclusão com solicitação pendente.
Aceite: valor cobrado é explicável item a item; cadeira pode atender próximo enquanto pedido anterior está financeiramente pendente.
```

## PROMPT 06 — Atualizações SSE confiáveis

Dependências: 05. Copie com o PROMPT-BASE e mantenha o anexo disponível.

```text
Execute a etapa 06: Atualizações SSE confiáveis.

Implemente SSE como comunicação servidor→navegador; comandos continuam HTTP. Não use consulta periódica do frontend como atualização normal da fila. Forneça snapshot inicial e mecanismo de retomada sem janela de perda entre snapshot e assinatura. Documente watermark/cursor que respeite visibilidade e ordem de commit; sequência alocada antes do commit não basta por si só.
Entregue outbox com repetição segura, lease/retry, consumidores idempotentes e monitoramento de atraso. Eventos confirmam dados já persistidos. Reconexão com cursor, retenção finita e fallback a snapshot quando cursor expirar. Eventos duplicados não repetem efeitos.
Autorize conexão e replay por audiência; revogação de sessão deve encerrar/revalidar conexão já aberta. Não coloque credenciais duráveis na URL. Defina heartbeat, backoff com jitter, buffers limitados, descarte/desconexão de clientes lentos e configuração do proxy sem buffering. Compatibilize heartbeats e timeouts HTTP.
Implemente reconexão ao voltar à aba ou recuperar rede. Não prometa entrega com navegador fechado. Não abra conexão extra em cada componente; coordene estado na aplicação.
Teste queda de rede, reinício, duplicação, cursor velho, evento durante snapshot, sessão revogada, audiência indevida e dois processos API. Não afirme escala horizontal se só houver memória local.
Aceite: reconexão converge ao estado verdadeiro e nunca vaza dados de outro usuário.
```

## PROMPT 07 — Histórico e exclusão

Dependências: 02, 05 e 06. Copie com o PROMPT-BASE e mantenha o anexo disponível.

```text
Execute a etapa 07: Histórico e exclusão.

Implemente histórico privado e paginado para conta verificada, isolado por estabelecimento conforme vínculo. Visitante acessa apenas a visita autorizada dentro da validade; não ganha histórico retroativo ao se cadastrar.
Implemente exclusão com reautenticação e estado de processamento. Revogue sessões imediatamente; cancele espera/chamada, libere reservas não consumidas e encerre benefícios. Atendimento em andamento ou pendência financeira continua sob equipe; não exiba exclusão completa enquanto processamento estiver pendente. Não destrua registros necessários à reconciliação.
Separe perfil identificável de registros operacionais/financeiros minimizados. Implemente rotina repetível de anonimização com evidência sem dados pessoais. Defina matriz de retenção por categoria, tratamento de backups e acesso restrito; prazos legais concretos dependem de validação antes de produção, sem inventá-los. Meta operacional de trinta dias não é prazo legal universal.
Proteja exclusão do único dono com transferência/encerramento. Desligamento da equipe é gerenciado pelo dono.
Teste exclusão repetida, pedido pendente, sessão SSE aberta, reserva ativa, dono único e retomada de job interrompido.
Aceite: interface informa situação real e nenhuma exclusão cria pagamento confirmado ou perde obrigação pendente.
```

## PROMPT 08 — Pagamentos e AbacatePay

Dependências: 05. Copie com o PROMPT-BASE e mantenha o anexo disponível.

```text
Execute a etapa 08: Pagamentos e AbacatePay.

Consulte documentação oficial atual da AbacatePay antes de codificar. Registre endpoints, autenticação, sandbox, idempotência, verificação de recebimento, cancelamento/expiração e estorno realmente suportados. Não invente capabilities. Crie adaptador e testes de contrato com fixtures sem segredos; produção desativada até validação real.
Implemente Pix para total integral, dinheiro e cartão presencial. Não processe dados de cartão. Separe cobrança, recebimento externo e conferência do profissional/dono. Pix só recebido por evidência confiável do provedor; imagem, retorno do navegador e clique do cliente não confirmam pagamento. Dinheiro/cartão presencial têm ação autorizada de recebido e conferido, com registros distintos. Zero não cria cobrança.
Proteja chave da API no backend. Em timeout de criação, registre resultado desconhecido e reconcilie antes de tentar outra cobrança. Idempotência local não substitui garantia remota. Garanta uma tentativa ativa coerente por pedido, tratando pagamentos tardios.
Implemente escolha de método e QR/copia-e-cola responsivos e acessíveis; erros não devem perder pedido.
Teste centavos, total adulterado, duplo clique, timeout ambíguo, cobrança expirada e confirmação indevida.
Aceite: nunca cobrar duas vezes automaticamente por falha de rede; todas as transações vinculadas ao estabelecimento/pedido correto.
```

## PROMPT 09 — Reconciliação, conferência e estorno

Dependências: 08 e 06. Copie com o PROMPT-BASE e mantenha o anexo disponível.

```text
Execute a etapa 09: Reconciliação, conferência e estorno.

Implemente webhook do provedor quando suportado e reconciliação no backend. SSE permanece responsável apenas por atualizar o navegador. Valide autenticidade conforme contrato oficial e confirme cobrança, valor, moeda, destinatário e pedido. Não suponha algoritmo de assinatura inexistente.
Persista recebimento antes de responder sucesso; deduplique eventos, tolere eventos fora de ordem e repetições. Webhook não autenticável exige consulta autenticada antes de reconhecer pagamento. Worker reconsulta situações desconhecidas/pendentes com limites/backoff e alerta casos não resolvidos.
Mudar para dinheiro com Pix ativo exige aviso e tentativa de cancelar quando suportada; se Pix chegar tarde, preserve dois recebimentos e abra tratamento do excesso. Nunca apague evidência financeira para encaixar saldo.
Estorno somente dono, integral por recebimento no MVP, evidência do provedor antes de registrar sucesso; parcial/desconhecido segue revisão manual. Separe intenção, processamento e confirmação. Reconciliação e estorno não dependem da aba do cliente.
Teste replay, assinatura inválida, valor divergente, pagamento duplicado/tardio, corrida confirmação/estorno, timeout e recuperação após reinício.
Aceite: conferência humana não fabrica Pix recebido; repetição não duplica crédito de pontos nem receita; pendências visíveis à equipe.
```

## PROMPT 10 — Pontos e prêmios

Dependências: 07 e 09. Copie com o PROMPT-BASE e mantenha o anexo disponível.

```text
Execute a etapa 10: Pontos e prêmios.

Implemente razão de pontos por conta/estabelecimento com lançamentos imutáveis e chaves únicas de origem. Programa começa desativado. Snapshot de elegibilidade e pontos por serviço; creditamento uma vez somente após conclusão normal e recebimento integral conferido. Visitante, ausência, cancelamento, interrupção e gratuidade não geram pontos. Não há punição por ausência nem expiração de saldo.
Estorno integral do pagamento principal reverte crédito uma vez; devolução de excesso duplicado não reverte compra válida. Saldo negativo por pontos já consumidos é permitido, sem dívida monetária; bloqueie novo resgate sem saldo suficiente.
Prêmio é cortesia separada, não desconto do pedido. Configure custo inteiro, descrição, entrega e quantidade limitada/ilimitada. Uma reserva ativa por conta/barbearia; reserva pontos e unidade por 24 horas. Expiração/cancelamento liberam exatamente uma vez; profissional vinculado ou dono confirma entrega. Preserve condições aceitas da reserva; correção pelo dono exige motivo.
Desativar programa preserva saldos/reservas e direitos já prometidos conforme anexo.
Teste concorrência na última unidade, gasto duplo, entrega versus expiração, repetição de eventos, estorno após resgate e desligamento do programa.
Aceite: saldo e inventário sempre explicáveis pelos movimentos; visitante não ganha benefício criando conta depois.
```

## PROMPT 11 — Web responsiva completa

Dependências: 02–10 e contratos atualizados. Copie com o PROMPT-BASE e mantenha o anexo disponível.

```text
Execute a etapa 11: Web responsiva completa.

Conclua frontend/ com React e TypeScript, usando contrato real e cliente de API tipado. Entregue todas as jornadas de visitante, cliente, profissional e dono em navegador: autenticação, recuperação, catálogo, fila, pedido, pagamento, histórico, fidelidade, equipe, configuração e exclusão.
Organize rotas, componentes, estado de servidor e formulários sem duplicar regra de negócio. Usabilidade deve cobrir carregamento, vazio, erro, autorização negada, conflito, operação pendente, expiração, offline e reconexão SSE. Não marque pagamento ou entrada como confirmados antes da resposta confiável. Reenvio deve reutilizar intenção/idempotência.
Valide larguras 360, 390, 768, 1024 e 1440 px. Sem rolagem horizontal da página; tabelas podem ter área local ou virar cartões. Ações essenciais não dependem de hover. Labels, mensagens de erro vinculadas, foco visível, teclado, zoom 200%, contraste e estado além de cor. Alvos de interação de 44×44 px quando aplicável.
Cliente, barbeiro e dono devem operar no celular. Teste navegadores disponíveis e identifique os não testados; emulação não prova compatibilidade física.
Não crie aplicativo desktop, Expo, Android/iOS, PWA ou push nesta etapa. Preserve contratos para adaptação futura.
Aceite: execução ponta a ponta contra API real, telas legíveis em cada largura, fluxo recuperável após rede cair e nenhum segredo do provedor no bundle.
```

## PROMPT 12 — Operação, recuperação e implantação

Dependências: 11. Copie com o PROMPT-BASE e mantenha o anexo disponível.

```text
Execute a etapa 12: Operação, recuperação e implantação.

Prepare ambiente de homologação e produção com TLS, segredos externos, banco com menor privilégio, migrações controladas e reversão documentada. Configure limites de requisição, timeouts compatíveis com SSE, tamanho de payload e proteção contra abuso. Evite dados pessoais nos logs.
Monitore erros, latência, conexões SSE, atraso da outbox, cobranças desconhecidas, divergências, jobs de exclusão e reservas expiradas. Defina alertas acionáveis e responsáveis.
Implemente backup e exercício real de restauração. Antes de liberar workers após restaurar, reconcilie pagamentos e side effects externos: banco restaurado não apaga cobrança já recebida. Defina e meça objetivos de perda máxima/tempo de recuperação, sem alegar valores não testados.
Documente rotinas de incidente, provedor indisponível, rotação de segredo, falha de migration e manutenção. Registre ambientes, capacidades e limites efetivamente verificados.
Aceite: restauração demonstrada em ambiente isolado; jobs recuperam após reinício; deploy não perde fila; produção não ativa com integração financeira simulada.
```

## PROMPT 13 — Revisão final e piloto

Dependências: 00–12. Copie com o PROMPT-BASE e mantenha o anexo disponível.

```text
Execute a etapa 13: Revisão final e piloto.

Revise implementação contra cada regra do anexo e matriz. Execute testes de domínio, integração real com PostgreSQL, concorrência, contratos, SSE, pagamentos em sandbox e fluxos E2E. Execute detector de corridas Go nos testes pertinentes. Faça revisão de autorização por objeto, estabelecimento, papel, sessão e eventos, incluindo tentativas negativas.
Verifique dependências e vulnerabilidades, CSRF, XSS em nicks/descrições, injeção SQL, enumeração de conta, log de segredo, replay, double spend e exclusão incompleta. Identifique o que não foi testado e requisitos externos pendentes.
Produza docs/evidencias.md com comandos, resultados observados, ambiente, data e links/arquivos. Testes não executados nunca são “aprovados”. Registre bugs com gravidade e reprodução. Corrija bloqueadores de integridade, privacidade e dinheiro antes do piloto.
Conduza roteiro real: visitante entra/recupera, conta entra, pedido muda, barbeiro chama/conclui, pagamento recebe/confere, pontos creditam/resgatam, ausência, interrupção, rede cai/volta e dono revisa pendências.
Aceite: todos os critérios rastreados a evidência; riscos restantes explícitos; guia curto por papel e plano de piloto/rollback entregues. Não publicar automaticamente nem ativar cobrança real sem instrução específica.
```

## PROMPT DE REVISÃO — Depois de cada etapa

```text
Revise a etapa entregue contra a especificação, contratos e critérios de aceite. Leia o código e execute verificações disponíveis; não confunda existência de teste com teste aprovado. Procure erros de regra, autorização, isolamento por estabelecimento, concorrência, idempotência, tratamento de falhas e experiência responsiva. Priorize achados concretos com arquivo/local, reprodução, impacto e correção. Corrija problemas dentro do escopo e repita testes afetados. Não declare produção pronta com integrações simuladas. Entregue evidência, riscos restantes e estado de cada critério: atendido, falhou ou não verificado.
```

## Anexo — Especificação de regras adotadas

Esta é a cópia da versão 2 do documento, usada para executar os prompts sem depender de memória de conversa. Mudanças futuras devem atualizar os dois documentos.

### MVP web — Regras e decisões do sistema de filas

Versão 2 • Base de implementação • 12/11/2026

Esta versão define as regras iniciais de construção, conforme autorização do responsável para escolher e detalhar o funcionamento. Substitui as propostas anteriores conflitantes. As regras poderão ser ajustadas após o piloto, preservando dados e histórico. O documento especifica o produto; não afirma que o aplicativo já foi construído ou testado.

### 1. Objetivo e escopo da primeira versão

Permitir que uma barbearia organize a espera, execute atendimentos, registre serviços, receba pagamentos e ofereça fidelidade. O cliente acompanha sua visita pelo navegador, com conta ou como visitante. O piloto começa em uma barbearia; os dados já serão separados por estabelecimento.

A primeira entrega é exclusivamente web responsiva: todas as funções de cliente, profissional e proprietário devem funcionar em celular, tablet e computador. Usar pelo navegador do computador já faz parte do MVP. Um programa desktop instalável será uma evolução separada, assim como aplicativos Android e iOS.

Incluído: contas e visitantes; catálogo de serviços; fila por profissional; chamada, ausência e transferência; atendimento e comanda; Pix pela AbacatePay; dinheiro e cartão na maquininha; conferência; histórico de contas; pontos e prêmios simples; painel da equipe; registros de alterações e recuperação de falhas.

Fora do MVP: agendamento por horário, produtos vendidos e estoque comercial, cartão online, pagamento dividido entre meios, crédito/fiado, descontos livres, cupons, promoções complexas, sorteios, programa desktop, apps nativos e notificações push. O estoque limitado de prêmios de fidelidade é permitido e não constitui um catálogo de produtos vendidos.

### 2. Organização do projeto e decisões técnicas

Haverá duas pastas principais de código: backend/ para o servidor Go e frontend/ para a aplicação web. A pasta docs/ guarda regras e instruções; README e arquivos de infraestrutura podem ficar na raiz. Não usar apps/client, Expo ou React Native nesta primeira versão.

#### Decisão 01 — Backend Go organizado por responsabilidade

Situação: definida para o MVP. O backend será uma aplicação dividida em módulos de identidade, barbearias, catálogo, fila, atendimento, comanda, pagamentos e fidelidade. Motivo: manter a construção simples e organizada. API e processamento em segundo plano podem rodar em processos separados do mesmo projeto. Não começar com microsserviços.

#### Decisão 02 — PostgreSQL como registro oficial

Situação: definida. O estado verdadeiro da fila e do dinheiro fica no PostgreSQL. O navegador apresenta esse estado e solicita mudanças. Motivo: atualizar ou fechar uma página não pode apagar uma visita. São necessários gravação segura, cópias de segurança e recuperação testada.

#### Decisão 03 — Frontend web responsivo

Situação: definida. React com TypeScript e Vite em frontend/. A escolha prioriza interfaces web, sem depender de componentes nativos. O servidor mantém as regras de negócio e oferece uma API independente da interface, facilitando uma adaptação desktop posterior. A tecnologia de empacotamento desktop será escolhida apenas nessa fase.

#### Decisão 04 — SSE para atualizar as telas

Situação: definida. Entrar, sair, chamar e concluir são pedidos HTTP ao servidor. SSE entrega as atualizações para as telas conectadas. Ao reconectar, o navegador recebe a situação atual sem duplicar ações. O aplicativo não promete avisos com aba fechada ou suspensa; ao voltar, sincroniza os dados.

#### Decisão 05 — Separar fila, atendimento, comanda e recebimento

Situação: definida. A fila organiza a espera; o atendimento registra o trabalho; a comanda reúne serviços e valor; os recebimentos registram dinheiro; a conferência registra a revisão pela equipe. Terminar o corte libera a cadeira antes de encerrar financeiramente a comanda.

#### Decisão 06 — Acesso privado e autorização no servidor

Situação: definida. Nick não é senha. Cada visitante tem acesso privado à visita. Conta e equipe têm sessões próprias. Toda ação é conferida pelo servidor conforme pessoa, loja e função. Identificadores difíceis de adivinhar e botões escondidos não substituem essa conferência.

#### Decisão 07 — Pix confirmado e conferência separados

Situação: definida. A confirmação financeira vem da AbacatePay, por mecanismo autenticado suportado na integração. O backend poderá usar webhook de pagamento e consultas de conciliação; SSE continua sendo somente a comunicação com o navegador. Pix recebido aguarda conferência, mas não volta a aparecer como não pago. O piloto usa uma conta recebedora; múltiplas contas dependem da validação do provedor.

### 3. Perfis e dados visíveis

Visitante: informa nick e controla apenas a visita atual, inclusive serviços e pagamento. Não acessa histórico anterior, pontos ou prêmios. Cliente com conta: além da visita, consulta seu histórico e seus benefícios por barbearia. Profissional: atende na própria fila e nas comandas dos atendimentos que executou. Proprietário: configura e acompanha toda a própria loja. Gerente e recepcionista ficam para evolução futura.

Uma pessoa pode ser profissional e cliente, mas cada ação respeita o papel usado. A fila pública mostra profissional, quantidade de pessoas, senhas e previsão aproximada. Não mostra nick de terceiros, telefone, e-mail, serviços ou valores. Cada cliente vê seu próprio nick e comanda; a equipe autorizada vê os dados necessários ao atendimento.

### 4. Jornada do cliente

O cliente abre o link ou QR da barbearia, escolhe usar conta ou continuar como visitante, seleciona um profissional e pelo menos um serviço válido, revisa o total e entra na fila. O servidor confirma a entrada e informa senha, posição e estimativa.

A equipe chama, inicia e conclui o trabalho. O cliente pode solicitar mudanças de serviços conforme a fase. Depois da conclusão, vê o total fechado e escolhe Pix, dinheiro ou maquininha. A comanda só encerra após recebimento integral e conferência, ou por fechamento autorizado sem valor devido. Pontos elegíveis são concedidos uma única vez.

### 5. Regras de conta e visitante

#### Conta e sessões

Cadastro de cliente usa nome, e-mail e senha; o e-mail deve ser verificado para ativar a conta. Recuperação usa link temporário de uso único, válido por 30 minutos. Respostas não revelam se o e-mail existe. Cadastro público nunca concede acesso administrativo. O proprietário inicial é habilitado por processo controlado; ele cadastra profissionais e os convida a ativar o acesso.

Sessão de cliente expira após 7 dias sem uso ou 30 dias desde o login, o que ocorrer primeiro. Sessão da equipe expira após 12 horas, exigindo novo login. Logout revoga a sessão no servidor. Alteração de senha revoga as demais sessões. Ações como excluir conta e alterar acesso de equipe exigem confirmação recente de identidade.

#### Persistência e recuperação do visitante

O navegador guarda uma credencial protegida, não uma cópia oficial da comanda. A sessão expira após 7 dias sem uso ou 30 dias desde sua criação, o que vier primeiro. Quando a comanda for encerrada, cancelada sem cobrança ou encerrada por ausência, a credencial termina em até 24 horas, sem ultrapassar o limite absoluto. Concluir somente o trabalho nunca encerra imediatamente o acesso necessário ao pagamento.

Ao entrar, o visitante recebe um código secreto de recuperação, orientado a guardar. Ele permite recuperar a visita dentro de 30 dias da criação, desde que ainda não tenham passado 24 horas do encerramento. Cada uso troca o código e revoga credenciais anteriores. O servidor guarda o código de forma protegida e limita tentativas. Não colocar esse código em links públicos, registros de tela ou logs.

Sem a sessão e sem o código, o nick não devolve acesso. A equipe pode localizar a visita e resolver atendimento ou recebimento no balcão, sem entregar a comanda online a alguém sem prova de acesso. Expirar a credencial não cancela a visita nem apaga a dívida: o registro permanece para a equipe.

Não permitir cadastro ou associação automática à conta dentro de uma visita iniciada como visitante enquanto houver fila ativa ou comanda pendente. Depois do encerramento, ele pode criar conta para as próximas visitas, sem histórico ou pontos retroativos. Essa regra protege a jornada identificada; não pretende reconhecer a mesma pessoa em outro navegador.

### 6. Operação da barbearia e da fila

Cada profissional tem uma fila. Uma identidade pode ter somente uma participação ativa na mesma loja: aguardando, chamada ou em atendimento. Comanda pendente não ocupa cadeira, mas impede essa identidade de entrar novamente naquela loja até ser resolvida. Sessões anônimas diferentes não comprovam pessoas diferentes; aplicar proteção contra abuso sem prometer impedir toda duplicação anônima.

A ordem é de chegada registrada pelo servidor. Não haverá recurso de furar fila no MVP. Transferência pelo proprietário, com concordância do cliente, entra no final da fila destino, exige compatibilidade de serviços e registra o motivo. Recalcula a estimativa; não troca os preços já aceitos.

Aberta: aceita entrada e chamada. Pausada: bloqueia novas entradas e novas chamadas, mas permite iniciar quem já foi chamado e terminar o atendimento em andamento. Fechada: bloqueia novas entradas e novas chamadas; clientes aguardando ou chamados precisam ser transferidos ou cancelados com motivo antes do fechamento. Um trabalho já iniciado pode ser concluído após fechar.

Somente proprietário abre, pausa, fecha e transfere. Profissional pode sinalizar indisponibilidade ao proprietário e operar a própria fila. Não desativar profissional com cliente aguardando, chamado ou em atendimento sem resolver essas participações. Comandas financeiras anteriores continuam disponíveis para o proprietário.

Ao chamar, inicia uma tolerância fixa de 10 minutos. Não reinicia ao atualizar a página. Se o cliente não chegar, o profissional pode marcar ausência depois do prazo; não há cancelamento automático silencioso. Depois de ausente, retornar significa nova entrada ao final. Antes de marcar ausência, o profissional pode iniciar normalmente se o cliente compareceu.

O cliente pode desistir enquanto aguarda ou foi chamado. Não há multa nem perda de pontos por desistência ou ausência no MVP. Essa decisão substitui a penalidade indefinida da versão anterior. Desistir após o trabalho começar exige conversa e interrupção registrada pela equipe.

### 7. Mudanças de estado e responsáveis

Estados da participação: aguardando, chamado, em atendimento, concluído, cancelado, ausente e interrompido. A tabela define as mudanças permitidas. Quem tenta uma ação com tela antiga recebe conflito e os dados atuais; não se aplica uma mudança inválida.


| Ação / mudança | Quem | Condição | Resultado |
| --- | --- | --- | --- |
| Entrar → aguardando | Cliente/visitante | Fila aberta; ≥1 serviço válido; sem visita ativa ou dívida pendente na loja. | Cria senha e ordem; abre comanda. |
| Aguardando → chamado | Profissional da fila / proprietário | Fila aberta; próximo elegível; profissional livre. | Registra chamada e inicia 10 minutos. |
| Chamado → em atendimento | Profissional da fila / proprietário | Cliente compareceu; ainda não cancelado/ausente. | Inicia trabalho; mantém serviços aceitos. |
| Aguardando/chamado → cancelado | Dono da visita / proprietário | Trabalho não iniciou; motivo se ação da equipe. | Libera fila; encerra sem cobrança; sem perda de pontos. |
| Chamado → ausente | Profissional da fila / proprietário | Passaram 10 minutos; cliente não chegou. | Libera profissional; encerra sem cobrança. |
| Transferir aguardando | Proprietário | Cliente concorda; destino aberto e compatível. | Entra ao fim do destino; mantém preços; audita motivo. |
| Alterar comanda aguardando | Dono da visita | Serviços válidos; mantém ≥1 item. | Atualiza versão, total e estimativa. |
| Pedir alteração após chamada | Dono da visita | Sem outro pedido pendente; item ainda alterável. | Cria solicitação sem alterar total aceito. |
| Aprovar / recusar alteração | Profissional do atendimento / proprietário | Pedido pendente; serviço removido não realizado. | Aplica nova versão ou mantém anterior; avisa decisão. |
| Em atendimento → concluído | Profissional do atendimento / proprietário | Itens revisados; pedidos resolvidos. | Libera cadeira; fecha valor; pagamento separado. |
| Em atendimento → interrompido | Profissional do atendimento / proprietário | Motivo e serviços realizados registrados. | Libera cadeira; proprietário revisa valor; sem pontos. |
| Fechar interrupção | Proprietário | Cobrar só serviços integralmente feitos; revisar motivo. | Fecha valor positivo ou zero; permite acerto financeiro. |
| Registrar Pix recebido | Integração autenticada | Cobrança, valor e recebedor conferem. | Registra uma vez; aguarda conferência. |
| Receber dinheiro/maquininha | Profissional do atendimento / proprietário | Recebimento integral verificado. | Registra recebimento e conferência; encerra se elegível. |
| Conferir Pix / encerrar | Profissional do atendimento / proprietário | Trabalho finalizado ou interrupção revisada; total coberto. | Encerra comanda; concede pontos apenas se elegível. |
| Encerrar sem valor | Equipe autorizada | Cancelamento/ausência ou interrupção revisada com total zero. | Registra motivo; não inventa recebimento nem pontos. |
| Corrigir / estornar | Proprietário | Motivo; versão/recebimentos revisados; valor válido. | Ajuste rastreável; devolução só confirmada após evidência. |

### 8. Serviços, comanda e atendimento interrompido

O catálogo tem nome, preço em reais, duração estimada, pontos por serviço e profissionais habilitados. Preços são calculados pelo servidor em centavos inteiros. Serviço precisa ter preço positivo; não há item livre ou preço digitado pelo cliente. Serviço inativado não aceita nova inclusão, mas itens já aceitos permanecem e são resolvidos pela equipe se não puderem ser executados.

Cada serviço aparece no máximo uma vez por comanda neste MVP. Cliente deve manter ao menos um serviço durante a espera; para remover todos, cancela a participação. Um item registra nome, preço, duração e pontos aceitos na inclusão. Mudanças futuras do catálogo não reescrevem essa visita.

Enquanto aguarda, cliente pode incluir/remover serviços compatíveis e recebe novo total e estimativa. Depois da chamada ou início, cria uma solicitação que o profissional aceita ou recusa; até a decisão, total anterior permanece. Somente uma solicitação pendente por comanda. Serviço marcado como realizado não é removido pelo cliente. Mudança aprovada registra autor, instante e versão.

Concluir exige resolver solicitações pendentes e revisar itens. A comanda fecha uma versão de valor; depois disso não admite edição direta. Para corrigir, o proprietário registra ajuste, motivo e efeito financeiro. Com cobrança externa pendente ou desconhecida, só permite alterar valor após verificar cancelamento/expiração ou tratar recebimento; não cria outra cobrança cegamente.

Se interrompido, o profissional registra motivo e quais serviços foram concluídos; o proprietário revisa. Serviços integralmente feitos mantêm o preço aceito; não iniciados ou parcialmente feitos têm cobrança zero no MVP. Não há preço proporcional improvisado. O proprietário fecha o valor, que pode ser zero, e o profissional fica livre. O estado continua interrompido, mesmo após o acerto financeiro.

Comanda com total zero é encerrada pela equipe com motivo, sem criar Pix fictício nem lançar dinheiro. Comanda cancelada/ausente antes do trabalho encerra sem cobrança e sem pontos. Atendimento interrompido não gera pontos, mesmo que algum serviço tenha sido cobrado.

### 9. Pagamento e fechamento financeiro

Um único meio cobre o total no fluxo normal: Pix integrado, dinheiro ou cartão presencial. Não há parcelamento, divisão de pagamento, fiado ou captura de dados de cartão no sistema. Na maquininha, o sistema registra somente meio, valor, responsável e referência opcional sem dados sensíveis.

O cliente escolhe o meio; somente o servidor cria cobrança Pix da própria comanda fechada. Clicar “já paguei”, enviar comprovante ou voltar da página do provedor não confirma dinheiro recebido. Pix exige confirmação autenticada correspondente à cobrança, ao valor e à conta do estabelecimento.

Dinheiro/maquininha: profissional responsável ou proprietário registra recebimento integral e confirma a conferência na mesma ação, mantendo os dois fatos registrados. Pix: o sistema registra o recebimento e a equipe depois confere. Recebimento incompleto ou divergente não encerra automaticamente; fica em revisão pelo proprietário. Troco em dinheiro é exibido como valor entregue menos total, mas receita registrada é somente o total devido.

Repetir clique ou receber aviso duplicado não cria outro recebimento, cobrança ou crédito de pontos. Se a resposta do provedor for desconhecida, mostrar “verificando pagamento” e consultar antes de permitir repetir. A consulta deve continuar mesmo que o navegador feche.

Mudar do Pix para dinheiro/maquininha com Pix ainda válido exige alerta à equipe e tentativa de cancelamento quando suportada. Se o cliente pagar os dois, registrar ambos, avisar duplicidade e abrir devolução do excesso. Nenhum recebimento pode ser apagado para esconder o problema.

Somente proprietário inicia estorno, com motivo e valor limitado ao recebido ainda não devolvido. O fluxo automático do MVP cobre estorno integral de um recebimento. Casos parciais ou resultado desconhecido ficam em revisão, com documentação e confirmação efetiva antes de atualizar o saldo. A primeira transação não é apagada.

O piloto integra sandbox antes de dinheiro real. Versão da API, dados obrigatórios, confirmação, expiração, cancelamento e estorno precisam ser validados na documentação e na conta usada. Esses são requisitos de integração, não dúvidas de produto. Modelo financeiro multi-loja precisa de validação do provedor antes da expansão.

### 10. Pontos e prêmios simples

Programa desligado por padrão. Proprietário ativa e define pontos inteiros por serviço, inicialmente zero. Somente contas verificadas acumulam pontos na respectiva loja. A elegibilidade do programa e a identidade são registradas ao entrar na fila; desligar o programa depois não retira um crédito já prometido naquela visita. Mudança vale para novas visitas.

Cada item guarda os pontos aceitos na inclusão. Atendimento concluído, pagamento integral e conferência concedem a soma dos serviços efetivamente realizados, uma única vez. Visitas canceladas, ausentes, interrompidas, gratuitas ou iniciadas como visitante não geram crédito. Não há validade/expiração dos pontos no MVP.

Saldo é explicado por um extrato de créditos, reservas, consumos, liberações e reversões. Estorno integral da cobrança principal reverte o crédito daquela comanda uma vez. Devolver apenas um recebimento duplicado não reverte pontos da compra legítima. Se pontos já foram usados, saldo pode ficar negativo; novos resgates ficam bloqueados até haver saldo suficiente, sem cobrança de dinheiro por esse saldo.

Prêmio no MVP é uma cortesia entregue e registrada separadamente, sem desconto automático na comanda, sem horário reservado e sem conceder pontos. Proprietário define nome, descrição, custo inteiro em pontos, disponibilidade limitada ou ilimitada e instruções de uso. Cortesia que exija atendimento deve explicar que não garante prioridade na fila.

Cliente pode manter uma reserva de prêmio ativa por loja. Reserva trava pontos e uma unidade por 24 horas; vencimento ou cancelamento libera ambos exatamente uma vez. Profissional da loja ou proprietário confirma entrega presencial; consumo é definitivo salvo correção pelo proprietário, com motivo. Reserva guarda as condições aceitas, mesmo se o catálogo mudar.

Desligar programa impede novos acúmulos em novas visitas e novas reservas, mas mantém consulta de saldo e honra reservas existentes. Saldo fica preservado para reativação; prêmio reservado pode ser cancelado pelo cliente. Não criar expiração retroativa.

### 11. Permissões da primeira versão

Cada pessoa atua somente nos recursos permitidos. O proprietário não acessa outras lojas nem o histórico pessoal do cliente em outros estabelecimentos. Profissional desativado perde imediatamente o acesso operacional e as conexões privadas.


| Ação | Visitante | Cliente | Profissional | Proprietário |
| --- | --- | --- | --- | --- |
| Ver fila pública | Sim | Sim | Sim | Sim |
| Entrar / desistir | Própria visita | Própria visita | Como cliente | Como cliente |
| Ver comanda | Visita atual | Própria | Atendimento próprio | Própria loja |
| Alterar serviços | Pelas regras | Pelas regras | Atendimento próprio | Própria loja |
| Chamar / iniciar / concluir | Não | Não | Própria fila | Própria loja |
| Confirmar recebimento | Não | Não | Atendimento próprio | Própria loja |
| Transferir / abrir / pausar / fechar | Não | Não | Não | Própria loja |
| Preços / equipe / configurações | Não | Não | Não | Própria loja |
| Estornar / corrigir / fechar interrupção | Não | Não | Não | Própria loja |
| Histórico pessoal | Não | Próprio | Como cliente | Como cliente |
| Consultar / reservar prêmio | Não | Saldo próprio | Como cliente | Como cliente |
| Confirmar entrega de prêmio | Não | Não | Na própria loja | Na própria loja |
| Configurar pontos / prêmios | Não | Não | Não | Própria loja |
| Solicitar exclusão | Não tem conta | Própria conta | Própria conta | Própria conta |

### 12. Exclusão da conta e tratamento de dados

Excluir não significa apenas desativar. Ao solicitar, confirmar a identidade, revogar sessões e bloquear novas ações da conta. Visitas aguardando/chamadas são canceladas; reservas não consumidas são liberadas; saldo de fidelidade é encerrado sem conversão em dinheiro. Esses efeitos são mostrados antes da confirmação.

Se há atendimento em curso ou recebimento pendente, a solicitação fica em processamento, com acesso operacional somente da equipe para resolver o atendimento. Enviar protocolo para o e-mail verificado quando o serviço de e-mail estiver disponível; não mostrar “exclusão concluída” enquanto o processo estiver pendente. Não bloquear recebimento tardio ou conciliação.

Perfil, credenciais e identificadores pessoais sem necessidade de retenção devem ser removidos ou anonimizados. Registros financeiros/operacionais necessários são separados do perfil e mantêm somente os dados justificáveis, com acesso restrito. Não manter todos os dados para sempre nem prometer apagar informações que precisem permanecer.

Meta operacional: tratar a solicitação em até 30 dias; alertar proprietário/operador em pendências. É uma meta do produto, não uma declaração de prazo legal. Antes de produção, definir por categoria finalidade, prazo de retenção e descarte com orientação aplicável à operação, inclusive backups. Não inventar prazo fiscal neste documento.

Profissional pode pedir exclusão da conta pessoal; o proprietário redistribui sua operação. Proprietário único deve transferir administração ou encerrar a loja com pendências tratadas antes de excluir a conta administrativa. Nunca deixar loja ativa sem responsável.

### 13. Web responsiva desde a primeira tela

Toda função do cliente e da equipe deve ser utilizável a partir de 360 pixels de largura, em celular, tablet e computador. Validar pelo menos 360, 390, 768, 1024 e 1440 pixels. Em telas estreitas, converter tabelas densas em cartões/listas ou oferecer rolagem localizada identificada; a página inteira não deve exigir rolagem horizontal.

Formulários com rótulos, erros junto aos campos, foco visível, navegação por teclado e leitura por tecnologia assistiva. Ações não dependem de passar o mouse. Botões principais com área de toque de pelo menos 44 × 44 pixels. Estados não são informados apenas por cor. Texto ampliado a 200% deve continuar utilizável.

Cliente: acesso, seleção, confirmação, posição/chamada, comanda, pagamento, histórico, pontos e perfil. Profissional: fila, chamada, serviços, conclusão e recebimentos autorizados. Proprietário: catálogo/equipe, operação, pendências, estornos, fidelidade e solicitações de exclusão.

Cada tela tem carregamento, vazio, erro e reconexão. Ao perder internet, mostrar dados antigos como desatualizados e impedir falsa confirmação. Não reenviar comando financeiro antigo automaticamente ao reconectar. Ao retornar à aba, sincronizar antes de permitir ação sobre estado antigo.

Testar Chrome, Firefox, Edge e Safari atuais disponíveis, incluindo navegador móvel real quando possível. Registrar versões e o que não foi testado. Não declarar suporte a um navegador só porque passou em outro. Instalação PWA e funcionamento financeiro offline não fazem parte do MVP.

### 14. Segurança, qualidade e recuperação

Negar acesso por padrão e verificar permissão em cada ação e consulta, inclusive SSE e retomada de eventos. Impedir vazamento entre lojas e clientes. Nick é texto, nunca HTML executável. Valores, pontos e propriedade são recalculados ou verificados no servidor. Limitar tentativas, tamanho de campos e volume de requisições.

Sessões web em cookies protegidos, com proteção contra pedidos forjados e origens autorizadas. Nenhuma chave de pagamento no frontend. Senhas, tokens, códigos de recuperação e informações sensíveis não entram nos logs. Convites da equipe têm uso único e expiram em 24 horas.

Comandos de negócio e eventos de atualização são gravados juntos antes de notificar. Repetições são tratadas sem efeito duplicado; versões impedem sobrescrita de comanda. O banco garante concorrência, e o worker retoma tarefas após falhas. Testar reconexão sem perder mudança entre a leitura inicial e o início dos eventos.

Desenvolvimento, teste e produção usam dados e credenciais separados. Dados fictícios nos testes. Revisar permissões, dependências, migrações e registros. Fazer backup e testar restauração isolada; conciliar pagamentos externos antes de reativar tarefas restauradas. Definir metas de carga e recuperação com medições antes do piloto.

### 15. Critérios para considerar o MVP pronto

Uma visita real de teste completa seleção, entrada, chamada, serviço, pagamento e conferência. Visitante e conta funcionam; equipe opera pelo celular e computador. Ninguém acessa visita ou loja alheia ao alterar identificadores. Duas chamadas simultâneas não atendem duas pessoas na mesma cadeira.

Cancelar após chamada libera fila sem penalidade; ausência respeita dez minutos; interrupção fecha valor por regra definida. Concluir corte não encerra acesso de pagamento do visitante. Pix duplicado/tardio fica registrado e tratado. Dois resgates concorrentes não gastam o mesmo saldo ou estoque.

Estado após reconexão corresponde ao banco; sessão revogada não continua recebendo dados. Layout passa pelas larguras previstas; comandos de build, testes e verificação estão documentados. Sandbox financeiro e restauração têm evidências. Falhas críticas de acesso, dinheiro ou perda de dados impedem lançamento.

### 16. Sequência de entrega e evolução

Ordem dos prompts: 00 consolidar regras; 01 fundação backend/frontend; 02 identidade/equipe; 03 loja/catálogo; 04 fila; 05 atendimento/comanda; 06 SSE; 07 contas/histórico/exclusão; 08 criação Pix; 09 recebimento/conferência; 10 fidelidade; 11 web responsiva ponta a ponta; 12 operação/recuperação; 13 revisão e piloto.

As regras desta versão são padrões para começar, não perguntas que precisam ser refeitas em toda etapa. Registrar alterações futuras em uma nova versão, com impacto em dados, telas e testes. Dependências externas reais — credenciais, suporte do provedor, domínio e política de retenção — continuam sendo verificadas antes de produção.

Desktop instalável vem depois do piloto web. Deve reutilizar a API e o máximo possível do frontend, sem duplicar regras de negócio nem acessar banco diretamente. A escolha entre tecnologias desktop será uma decisão futura. Android/iOS, PWA, push, agenda, produtos e campanhas também ficam em versões posteriores.

Mudanças principais desta revisão: web como única plataforma inicial; pastas backend/ e frontend/; React/TypeScript/Vite; fim da penalidade de pontos por desistência; recuperação e prazo do visitante definidos; interrupção com cobrança objetiva; fidelidade simples; exclusão separada de desativação. O guia de prompts foi alinhado. O backlog existente do Jira precisa ser reconciliado antes de executar itens antigos que mencionem Expo, apps nativos ou regras substituídas.


