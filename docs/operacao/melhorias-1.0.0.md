# Melhorias da versão 1.0.0 — checkpoint, publicação e guia rápido

Worktree `../Fila-melhorias`, branch `melhorias-1.0.0`, criada de `publicacao-v1` (`07b5d1a`, que já tem a migration `00023` aplicada em produção). **Nada foi commitado.** A versão pública continua **1.0.0**.

Decisões: [ADR-0032](../adr/0032-produtos-simples-e-categorias.md) a [ADR-0037](../adr/0037-desktop-em-segundo-plano.md). Configurações: [configuracoes-da-loja.md](../configuracoes-da-loja.md). Impacto: [melhorias-1.0.0-impacto.md](../melhorias-1.0.0-impacto.md). Evidências: [evidencias/melhorias-1.0.0.md](../evidencias/melhorias-1.0.0.md).

## 1. Situação

| Etapa | Situação |
|---|---|
| Migrations 00024–00028 | feitas; atualização a partir da 00023 com dados testada |
| Quantidades, execução parcial, produtos, categorias | backend, contrato, web e testes |
| Configurações por seção, custos de recebimento, débito/crédito | backend, contrato, web e testes |
| Entrada × operação, capacidade, tolerância | backend, contrato, web e testes (inclui concorrência) |
| Busca (comandas no servidor; listas locais) | feita e medida |
| Indicadores (unidades, produtos, custos, cobertura) | backend e Dashboard do desktop |
| Desktop em segundo plano | feito; testado no Linux (ver evidências); Windows não testado |
| Documentação (regras, matrizes, cenários, contrato, SSE) | atualizada; verificadores OK |

## 2. Pendências objetivas

1. Commit, PR e revisão (aguardam autorização).
2. Navegador real: larguras 320–1440 px, teclado e leitor de tela nas telas novas; cenário §41 completo em homologação.
3. Desktop: teste manual com bandeja real (Linux Mint/Cinnamon) e **Windows** (compilar, instalar, X, bandeja, notificação escondida, suspensão).
4. Pix em sandbox depois desta mudança (custo estimado gravado na confirmação).
5. Provedor não informa taxa efetiva: `receipt_fee.actual_*` fica sem uso até existir esse dado.
6. Clique na notificação do desktop não reabre a janela (limitação do plugin); usar a bandeja.

## 3. Publicação (ordem)

Todas as migrations são **aditivas** e compatíveis com o código anterior (colunas novas com padrão; restrições conferidas na migração). A API nova aceita os payloads antigos (`service_ids`, cartão sem modalidade).

1. **Backup** do banco (workflow `Backup`) e conferência de que terminou.
2. **Migrations** (workflow `Publicar` → `migrate up`, depois `migrate permissoes`): 00024 → 00028. A 00028 reescreve `participation` e `order_item` (colunas geradas): em bancos pequenos leva segundos, mas trava essas tabelas durante a reescrita — publicar fora do horário de atendimento.
3. **API e worker** na mesma revisão (mesmo workflow).
4. **Web (Vercel)** depois da API: a web nova envia `items`, `card_type` e chama rotas novas que a API antiga não tem.
5. **Desktop**: só a bandeja/segundo plano exige nova compilação e distribuição do instalador; o resto do desktop é a própria web (atualiza sozinho). O Dashboard novo (custos, produtos) também exige o instalador novo; o antigo continua funcionando com a API nova (campos extras são ignorados).

**Recuperação:** voltar a API/web para a revisão anterior funciona com o banco novo (colunas novas têm padrão; chamada sem `tolerance_minutes` vale 10 minutos, como antes), **exceto** para comandas abertas com quantidade > 1 ou produtos: o código antigo recalcula o total como uma unidade por linha e o banco recusa a gravação (nada fica errado, mas aquela comanda não pode ser editada até a versão nova voltar). Não rodar `migrate down` em produção — restaurar do backup se for preciso voltar o esquema.

## 4. Guia rápido das novidades

- **Dono:** Gestão → *Catálogo* (abas Serviços, Produtos, Categorias). Gestão → *Configurações da loja* → seções Catálogo (limite de unidades, produtos), Filas e atendimento (visitante, tolerância, limite), Pagamentos e custos (meios, custos de débito/crédito/Pix, resumo dos 30 dias), Equipe e permissões. Na tela da fila: *Fechar/Reabrir entrada*, *Pausar/Retomar/Encerrar operação* e o limite daquela fila.
- **Profissional:** na comanda em atendimento, *+1 realizado* / *Todos realizados* e *Entregar* (produto); concluir pede quantas unidades foram feitas. Pode fechar e reabrir a entrada da própria fila (se o dono permitir).
- **Cliente:** escolhe quantidades (−/+), busca serviços, adiciona produtos opcionais; o resumo mostra o total e avisa que o pedido ocupa uma posição.
- **Programa do computador:** menu do ícone na bandeja → *Continuar executando em segundo plano ao fechar a janela*; *Sair do Fila* encerra de verdade.

## 5. Como testar manualmente

1. Dono cadastra 2 serviços, 1 produto com categoria e custo de débito 2,5% + R$ 0,50; tolerância 3 min; limite 1 na fila do Beto.
2. Cliente busca "corte", escolhe 3 cortes + 1 pomada, confere total e duração, entra.
3. Outro cliente tenta entrar: "A fila está cheia…". Dono fecha a entrada; o primeiro desiste; a entrada continua fechada. Beto reabre.
4. Beto chama (prazo de 3 min), inicia, registra 2 de 3 cortes, entrega a pomada, conclui com 3 (ou com 2 e motivo).
5. Recebe no débito; Configurações → Pagamentos e custos mostra o custo estimado; o cliente com conta recebe os pontos.
6. Desktop: liga o segundo plano, fecha a janela, gera uma entrada pelo celular, confere a notificação; *Sair do Fila*.

## 6. Como retomar

`git -C ../Fila-melhorias status`; testes com o contêiner descartável `fila-teste-v1` (porta 55432) — **nunca** o `fila-postgres-1`.
