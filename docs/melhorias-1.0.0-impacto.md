# Matriz de impacto — melhorias da versão 1.0.0

Legenda: **alterada** · **validada sem alteração** (conferida, nada mudou) · **não aplicável** (com motivo). "Teste" cita o teste automatizado principal.

| Área | Situação | O que mudou / por quê | Teste |
|---|---|---|---|
| Página pública da loja | alterada | `ordering` (limite, produtos, visitante, meios); `entry_state` das filas com texto "cheia" / "entrada encerrada" | `TestStoreSettingsSections`, `loja.test.tsx` |
| Pesquisa e seleção | alterada | busca local sem acento, categorias em chips, quantidade com −/+/digitação, produtos opcionais | `SeletorItens`, `catalogo.test.tsx`, `fila.test.tsx` |
| Entrada como visitante | alterada | pode ser desligada para novas entradas; visita ativa preservada; tela leva à conta | `TestStoreSettingsSections` |
| Cliente com conta | validada sem alteração | mesmo fluxo; pontos por unidade (R-QTD-04) | `TestPartialExecutionReviewAndPoints` |
| Resumo da seleção | alterada | linhas com quantidade × preço, duração, unidades e o aviso "Este pedido ocupa uma posição na fila…" | `loja.test.tsx` |
| Participação na fila | alterada | um pedido = uma participação; capacidade e entrada fechada | `TestQuantitiesInOneParticipation`, `TestAdmissionAndCapacity` |
| Previsão de espera | alterada | soma quantidade × duração (produtos fora) | `TestQuantitiesInOneParticipation` |
| Aviso de chamada | alterada | prazo pela tolerância gravada na chamada | `TestConfigurableTolerance` |
| Alteração de serviços | alterada | quantidade absoluta, preço aceito mantido, histórico | `TestQuantitiesInOneParticipation`, `atendimento.test.tsx` |
| Pedidos de mudança | alterada | `add_items`, `set_quantities` | `TestChangeRequestWithQuantities` |
| Atendimento do profissional | alterada | "+1 realizado", "Todos", entregar produto, concluir/interromper por unidades | `TestPartialExecutionReviewAndPoints` |
| Gestão do dono (filas) | alterada | Fechar/Reabrir entrada, Pausar/Retomar/Encerrar operação, limite, carga, localizar na fila | `TestAdmissionAndCapacity`, `gestao.test.tsx` |
| Catálogo da gestão | alterada | abas Serviços/Produtos/Categorias, busca, filtros, ordenação, posição | `TestProductsAsComplement`, `gestao.test.tsx` |
| Comanda | alterada | quantidade, subtotal, unidades, produto; invariantes no banco | `explained()` em todos os testes de comanda |
| Recebimento presencial | alterada | cartão exige débito/crédito; meio desligado recusado (salvo o já escolhido) | `TestReceiptFees`, `pagamento.test.tsx` |
| Pix | alterada | nova cobrança recusada com Pix desligado; custo estimado gravado ao confirmar | `TestReceiptFees` (estimativa); Pix real **não** revalidado nesta entrega |
| Revisão de interrupção | alterada | dono decide unidades completas por linha | `TestPartialExecutionReviewAndPoints` |
| Estornos | alterada (cálculo) | resumo não deduz a taxa duas vezes; devolução de taxa só quando informada | `TestReceiptFees` (sem estorno com taxa devolvida: o provedor não informa hoje) |
| Histórico do cliente | alterada | itens com quantidade e unidades cobradas | `TestHistoryPrivateAndPaginated` |
| Fidelidade | alterada | pontos = unidades de serviço cobradas × pontos; produto não pontua | `TestPartialExecutionReviewAndPoints` |
| Configurações | alterada | seções com versão própria, custos, resumo dos 30 dias | `TestStoreSettingsSections`, `TestReceiptFees` |
| Auditoria | alterada | novas ações T-CT, T-PD, T-ST, T-FE, T-Q-06/07/08, T-O-14 | `TestStoreSettingsSections` |
| Notificações | alterada | som e conteúdo discreto por aparelho | `notificacoes.test.tsx` (regras existentes) |
| Desktop | alterada | bandeja, segundo plano, instância única, Sair do Fila | `segundo_plano::testes`, `testes/segundo-plano.sh` |
| Indicadores | alterada | unidades × participações, produtos, custos, recebido após custos, cobertura | `TestAnalyticsWithQuantitiesProductsAndFees` |
| Exclusão de conta | validada sem alteração | anonimizar o nick atualiza a coluna de busca gerada | `TestDeletionLeavesNoPersonalDataAnywhere`, `TestDeletionSimple` |
| Diretório de barbearias | validada sem alteração | só passou a usar `platform/busca` | `TestPublicDirectory` |
| Agendamento, estoque, venda avulsa, comissão, nota fiscal | não aplicável | fora do escopo desta versão (ADR-0032) | — |
| Divisão de pagamento, parcelamento, cartão online | não aplicável | fora do escopo; não havia parcelamento no código | — |
