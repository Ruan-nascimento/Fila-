# Matriz de retenção de dados

Etapa 07, ADR-0015 e ADR-0022.

## Como ler

- **Implementado hoje**: o que o sistema faz sozinho. É prazo técnico ou operacional, escolhido para o MVP e reversível.
- **Prazo legal**: **pendente de validação antes de produção**, com assessoria jurídica e contábil (LGPD, obrigações fiscais e consumeristas). Nenhum prazo legal foi inventado aqui. A coluna fica "a definir" de propósito.
- A meta de **30 dias** para tratar uma exclusão é **meta operacional**, não prazo legal universal (R-EXC-05).
- Perfil identificável (nome, e-mail, credenciais, nick) fica separado dos registros operacionais e financeiros. Esses registros apontam só para um **pseudônimo**: o identificador interno da conta ou do visitante.

## Matriz

| Categoria | Onde (tabela) | Dado pessoal? | Finalidade | Implementado hoje | Na exclusão da conta | Prazo legal | Acesso |
|---|---|---|---|---|---|---|---|
| Perfil da conta | `account` (nome, e-mail, hash de senha) | Sim | Entrar, avisar por e-mail | Enquanto a conta existir | Sobrescrito pelo worker: nome "Conta excluída", e-mail fictício `…@conta.invalid`, senha inutilizada, estado `deleted`. A linha fica como pseudônimo. | A definir | Titular; operador |
| Sessões e tokens | `account_session`, `account_token` | Não (só hash do token e datas) | Manter o login, links de e-mail | Revogados na expiração ou no logout | Revogados **na hora do pedido** (`account_deletion`); só hash e datas permanecem | A definir | Sistema |
| Convites | `invite` (e-mail convidado) | Sim | Ligar o convite à pessoa certa | Até aceite, revogação ou expiração (24 h) | E-mail sobrescrito nos convites aceitos por ela ou pendentes para o e-mail dela; pendentes são revogados | A definir | Dono da loja; operador |
| Vínculos de equipe | `staff_membership` | Não (pseudônimo) | Permissões | Enquanto ativo | Encerrados na hora do pedido (`account_deletion`) | A definir | Dono da loja |
| Visitante | `visitor` (nick), `visitor_credential`, `visitor_browser` | Nick: sim | Acompanhar a visita sem conta | Credencial até 7 dias sem uso ou 30 dias, e até 24 h após encerrar a visita | Não se aplica (visitante não tem conta). O **nick é trocado por "Visitante"** depois que o acesso expira. | A definir | Titular (cookie); equipe |
| Participação na fila | `participation` (nick = nome da conta ou nick do visitante) | Nick: sim | Atendimento no balcão | Visitante: nick trocado por "Visitante" **30 dias** após a visita terminar com a comanda encerrada | Nick trocado por "Cliente". Senha, horários e estado ficam com o pseudônimo. | A definir | Equipe da loja |
| Comandas, itens, solicitações, correções | `service_order`, `order_item`, `change_request*`, `order_adjustment` | Não (pseudônimo, valores, datas) | Financeiro, reconciliação, explicar o valor cobrado | Indefinido (não há apagamento automático) | **Mantidos** sem perfil. Pendência financeira **bloqueia** a conclusão da exclusão, e nunca é apagada nem dada como paga. | **A definir** (fiscal/contábil) | Dono da loja; profissional (atendimentos próprios); operador |
| Cobranças, recebimentos, conferências, estornos e pendências | `pix_charge`, `receipt`, `receipt_conferral`, `refund`, `financial_issue` | Não (ids, valores, datas; motivos e notas digitados pelo dono) | Financeiro, reconciliação, devolução | Indefinido (não há apagamento; o banco recusa apagar) | **Mantidos** (não contêm perfil). Pendência financeira bloqueia a conclusão da exclusão | **A definir** (fiscal/contábil) | Dono da loja; profissional (atendimentos próprios); operador |
| Avisos do provedor | `provider_event` | Não: **nome, e-mail e documento do pagador não são gravados** (só ids, estado, valor e hash do corpo) | Deduplicar e auditar avisos | Indefinido | Não se aplica (sem dado pessoal) | A definir | Operador |
| Pontos e prêmios (etapa 10) | ainda não existem | — | Fidelidade | — | Saldo encerrado sem conversão em dinheiro; reservas liberadas (gancho já existente, `Benefits`) | A definir | Titular; dono da loja |
| Solicitação de exclusão | `deletion_request`, `deletion_request_block` | Não (HMAC do protocolo, sufixo, pseudônimo) | Provar o tratamento, informar a situação | Indefinido | Evidência: categorias tratadas, contagens e data. **Sem nome, e-mail ou nick.** | A definir | Titular (protocolo); dono da loja vê só as que dependem dela, protocolo pelo final |
| Auditoria | `audit_event` | Não (atores e alvos por pseudônimo; motivos digitados pela equipe) | Rastrear ações relevantes | Indefinido | Mantida (não contém perfil) | A definir | Dono da loja (da própria loja); operador |
| Eventos do fluxo | `outbox`, `event_log` | Sim nas visões da equipe (nick) | Atualizar telas | **24 h** (`event_log`); a outbox só guarda até publicar | Eventos da própria pessoa (audiências `visit` e `account`) apagados na anonimização; os da equipe saem com as 24 h | Não se aplica (dado transitório) | Sistema |
| Respostas repetíveis | `idempotency_record` | Pode conter dados da resposta | Não aplicar duas vezes | **24 h** | A resposta da exclusão é guardada **sem** o protocolo completo | Não se aplica | Sistema |
| Limites de tentativa | `rate_limit` | Não (HMAC do alvo) | Proteção contra abuso | Até a janela vencer | — | Não se aplica | Sistema |
| Caixa de saída de e-mail | `mail_outbox` (cifrado) | Sim (cifrado) | Entregar e-mails | Conteúdo apagado ao enviar; linha removida **7 dias** depois; falha após **48 h** | O e-mail com o protocolo é entregue; depois só ficam os metadados | Não se aplica | Sistema |
| Logs da aplicação | saída padrão (stdout) | Não deveria ter (testes conferem: sem e-mail, nick, senha, token) | Diagnóstico | Depende de onde a operação guardar (etapa 12) | — | A definir (etapa 12) | Operador |
| Backups do banco | fora do sistema (etapa 12) | Sim (cópia de tudo) | Recuperação | **A definir na etapa 12** | Contas excluídas **continuam nos backups até eles expirarem**. Atenção: um backup anterior a uma exclusão traz a conta de volta sem a solicitação. O procedimento de restauração (etapa 12) precisa reaplicar as exclusões feitas depois do backup, por exemplo com uma lista de pseudônimos excluídos guardada fora do banco, e rodar `fila-admin nova-epoca`. **Ainda não implementado.** | **A definir** | Operador, com acesso restrito e registrado |

## Acesso restrito aos registros minimizados

- Comandas e itens: o dono vê os da própria loja; o profissional, só os atendimentos próprios; nunca outras lojas (R-PERM-02, testado).
- O histórico do cliente é só da própria conta (`/v1/me/history`). O dono não tem rota para o histórico de um cliente.
- O operador acessa o banco direto. Esse acesso deve ser nominal e registrado, a definir na etapa 12.

## Rotinas repetíveis (worker)

| Rotina | Frequência | Repetível | Evidência |
|---|---|---|---|
| Processar exclusões (reavaliar bloqueios, anonimizar sem bloqueio) | 1 min | Sim. Uma transação por solicitação: interrupção = nada feito. `SKIP LOCKED` entre workers. | `deletion_request.evidence` + auditoria `T-AC-04` (ator sistema) |
| Trocar nick de visitante por "Visitante" | 1 h | Sim | Contagem no log do worker |
| Apagar eventos com mais de 24 h | 10 min | Sim | Contagem no log |
| Limpeza técnica (limites, respostas repetíveis, e-mails entregues) | 10 min | Sim | Contagem no log |

## Pendências antes de produção

1. Prazos legais por categoria (fiscal, consumidor, LGPD), com assessoria. Em especial, quanto tempo guardar comandas, recebimentos e auditoria.
2. Política de backups: frequência, retenção, criptografia e procedimento de restauração com reaplicação das exclusões feitas depois do backup (etapa 12; ainda não implementado).
3. Guarda e acesso aos logs na hospedagem escolhida (etapa 12).
4. Revisar o prazo operacional do nick de visitante (30 dias) com a operação da loja.
