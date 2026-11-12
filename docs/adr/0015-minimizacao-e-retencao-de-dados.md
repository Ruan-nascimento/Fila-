# ADR-0015 — Separação de perfil e registros, e retenção

- Situação: Aceita parcialmente (prazos legais pendentes)
- Data: 2026-11-12

## Contexto

Excluir conta não é desativar, mas registros financeiros e operacionais precisam permanecer para reconciliação e para obrigações que ainda serão confirmadas. Não se pode inventar prazo legal.

## Decisão

- Perfil identificável (nome, e-mail, credenciais) fica em tabelas do módulo identidade; registros operacionais/financeiros referenciam apenas um identificador pseudônimo da identidade.
- Anonimização: apaga/sobrescreve perfil e credenciais, mantém o pseudônimo nos registros; rotina idempotente e retomável; evidência sem dados pessoais (protocolo, datas, categorias tratadas).
- Nick do visitante é tratado como dado pessoal: removido dos registros após o fim da necessidade operacional (prazo concreto na matriz de retenção).
- Auditoria nunca guarda e-mail, nick, telefone ou segredo.
- Matriz de retenção por categoria (perfil, operacional, financeiro, auditoria, logs, backups) com finalidade, prazo e descarte será produzida na etapa 07; **prazos legais/fiscais dependem de validação antes de produção** e não são inventados.
- Meta operacional de 30 dias para tratar solicitações, com alerta; não é prazo legal.

## Alternativas consideradas

- Apagar tudo da conta: perderia obrigações financeiras pendentes.
- Manter tudo para sempre: contraria minimização.

## Consequências

- Backups contêm dados de contas excluídas até expirarem; a política de backups entra na matriz de retenção.
- Acesso aos registros financeiros minimizados é restrito ao dono da loja e ao operador.
