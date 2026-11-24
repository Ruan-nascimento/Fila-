# ADR-0032 — Catálogo simples de produtos e categorias do catálogo (versão 1.0.0)

- Situação: Aceita
- Data: 2026-11-21
- Substitui parcialmente: R-ESC-03 ("venda de produtos/estoque comercial" fora do MVP) — ver [regras.md](../regras.md) §1. O histórico da decisão anterior fica preservado no próprio R-ESC-03 (anotado), não apagado.

## Contexto

A versão 1.0.0 passa a vender itens como pomada e shampoo **como complemento de uma visita**, e os catálogos crescem a ponto de exigir organização. A decisão anterior (R-ESC-03) excluía produtos do MVP porque eles vinham acompanhados de estoque, compras e venda avulsa. O pedido desta versão autoriza só o catálogo simples.

## Decisão

1. **Categoria** (`catalog_category`, migration 00024): pertence à loja e a um tipo (`service` | `product`), com nome (único por tipo, sem diferenciar maiúsculas), posição (0–9999) e situação. Desativar **não apaga nem esconde** itens: para o cliente eles aparecem como "sem categoria". Até 50 categorias por tipo. Serviço ganha `category_id` (opcional) e `display_order`.
2. **Produto** (`product`): nome, descrição curta opcional, categoria, preço em centavos, foto opcional (módulo midia, finalidade nova `product_photo`, pública, só o dono envia), posição e situação. Histórico imutável em `product_revision`. Até 500 produtos por loja.
3. **Sem estoque**: não existe quantidade disponível, lote, validade, compras, inventário, venda avulsa, entrega, nota fiscal nem comissão. Disponível = ativo, definido pelo dono. Nada de "restam N".
4. **Na comanda**: produto é uma linha `kind = 'product'` (ADR-0033) com quantidade, **sem duração** (não entra na fila nem na estimativa) e **sem pontos** nesta versão. Participa do total, das alterações e do fechamento como os serviços. A entrega (`performed_quantity`) é registrada à parte da realização de serviço.
5. **Nunca só produto**: entrar na fila exige pelo menos um serviço. Configuração `products_enabled` (seção Catálogo, padrão ligada) controla novas inclusões; desligar não tira produtos das comandas existentes e esvazia a lista pública.
6. **Permissões**: dono cadastra, edita e desativa; equipe consulta e inclui nas comandas que pode editar; cliente e visitante consultam e escolhem conforme a configuração. Endpoints públicos expõem só itens ativos e nunca dados internos.
7. **Ordenação estável** em todas as listas: posição, nome sem diferenciar maiúsculas, id.

## Alternativas

- Estoque mínimo ("só avisar quando acabar"): exigiria movimentação e conciliação. Fora desta versão.
- Produto como "serviço de duração zero" na mesma tabela: misturaria pontos, habilitação por profissional e duração, e tornaria as regras de cada um ambíguas.

## Consequências

- O modelo permite estoque no futuro (movimentos ligados a `product`) sem reescrever a comanda.
- Os indicadores ganham o detalhamento "products"; períodos anteriores à 1.0.0 ficam vazios.
