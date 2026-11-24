# Configurações da loja e do aparelho (versão 1.0.0)

Decisão: [ADR-0034](adr/0034-configuracoes-da-loja-e-custos-de-recebimento.md). Tela: **Gestão → Configurações da loja**, uma seção por assunto, cada uma salva separadamente (`PATCH /settings/{seção}` com a versão da seção). Toda alteração é auditada (T-ST-01…03, T-FE-01, T-Q-08). Sem configuração salva, valem os padrões abaixo.

## Configurações da loja

| Seção | Configuração | Tipo | Padrão | Intervalo | Quem altera | Visibilidade | Quando vale | Efeito sobre o que já existe |
|---|---|---|---|---|---|---|---|---|
| Catálogo | Unidades do mesmo item por pedido (`max_service_quantity`) | inteiro | 10 | 1–50 | dono | pública (`ordering`) | inclusões e aumentos a partir de agora | nenhum: quantidade já aceita acima do limite continua válida, só não cresce |
| Catálogo | Oferecer produtos (`products_enabled`) | sim/não | sim | — | dono | pública | novas inclusões | produtos já em comandas continuam até o fim |
| Catálogo | Categorias (nome, posição, situação) | cadastro | nenhuma | até 50 por tipo; posição 0–9999 | dono | ativas são públicas | na hora | desativar não apaga nem esconde itens (ficam "sem categoria") |
| Filas e atendimento | Entrada como visitante (`visitors_enabled`) | sim/não | sim | — | dono (pede confirmação para desligar) | pública | novas sessões e novas entradas de visitante | visitas ativas continuam, com pagamento e recuperação |
| Filas e atendimento | Tolerância depois da chamada (`call_tolerance_minutes`) | minutos | 10 | 1–60 | dono | privada (cada chamada mostra o próprio prazo) | próximas chamadas | quem já foi chamado mantém o prazo gravado |
| Filas e atendimento | Limite padrão por fila (`default_queue_capacity`) | participações | sem limite | 1–200 ou sem limite | dono | só `entry_state` é público | novas entradas e transferências | ninguém sai da fila; a entrada volta sozinha quando libera vaga |
| Filas (tela da fila) | Limite da fila (`queue.capacity`) | participações | padrão da loja | 1–200 ou "padrão da loja" | dono | idem | idem | idem |
| Filas (tela da fila) | Entrada aberta/fechada (`queue.admission`) | aberta/fechada | aberta | — | dono; profissional na própria fila se permitido | `entry_state` público | na hora | ninguém sai, a ordem não muda; desistência não reabre; abrir uma operação encerrada começa com a entrada aberta |
| Equipe e permissões | Profissionais podem controlar a entrada da própria fila (`professionals_control_admission`) | sim/não | **sim** (mudança desta versão) | — | dono | privada | na hora | não libera limite, transferência, preços, outras filas nem encerrar a operação |
| Pagamentos | Dinheiro (`cash_enabled`) | sim/não | sim | — | dono | pública (`payment_methods`) | novas escolhas | cobrança e recebimentos existentes não mudam; quem já escolheu pode pagar nele com a equipe |
| Pagamentos | Cartão na maquininha (`card_enabled`) | sim/não | sim | — | dono | pública | idem | idem |
| Pagamentos | Pix pela integração (`pix_enabled`) | sim/não | sim (só aparece com a integração ligada no servidor) | — | dono | pública | novas cobranças | cobrança Pix existente segue válida até pagar/expirar |
| Pagamentos | (regra) | — | — | pelo menos um meio utilizável | — | — | — | não salva sem meio utilizável (`no_payment_method`) |
| Pagamentos e custos | Custo de débito / crédito / Pix (`payment_fee_config`) | % em pontos-base + fixo em centavos por transação | não configurado | 0–30% (2 casas); R$ 0–100 | dono | **privada** (nunca para cliente, visitante ou profissional) | recebimentos novos | recebimentos anteriores guardam o custo calculado na hora; antigos sem custo ficam "desconhecido" |
| Fidelidade | Programa de pontos e prêmios | (tela Prêmios) | desligado | — | dono | — | novas visitas | sem mudança nesta versão, exceto pontos por unidade (R-QTD-04) |

Chaves do provedor de pagamento nunca aparecem no painel nem no frontend.

## Preferências do aparelho (não são da loja)

| Preferência | Onde fica | Padrão | Quem muda | Observação |
|---|---|---|---|---|
| Notificações do sistema | navegador/programa (`localStorage`) | ligadas se a permissão foi dada | a pessoa, no sino | permissão só por gesto |
| Som nos avisos | `localStorage` do painel | ligado | a pessoa (no sino); no programa, pelo menu da bandeja | o programa grava o valor na página |
| Conteúdo discreto | `localStorage` | desligado | a pessoa, no sino | a notificação mostra só "Fila / Há uma atualização" |
| Continuar em segundo plano ao fechar a janela | arquivo `segundo-plano.json` do programa | **desligado** | a pessoa, no menu da bandeja | explicação na primeira ativação ([ADR-0037](adr/0037-desktop-em-segundo-plano.md)) |

Iniciar com o sistema operacional **não** existe nesta versão.
