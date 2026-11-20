# ADR-0029 — Mudar de lugar na fila e endereço da loja que acompanha o nome

- Situação: Aceita
- Data: 2026-11-20

## Contexto

Duas mudanças pedidas pelo responsável depois do uso real:

1. **Fila.** A pessoa chamada às vezes avisa o barbeiro que vai atrasar. O barbeiro (ou a dona) precisa descê-la uma posição para que o próximo seja chamado na hora, e poder subir/descer quem aguarda. Até aqui R-FILA-05 proibia qualquer reordenação: a única saída era cancelar ou esperar a tolerância para marcar ausência.
2. **Identidade.** Ao trocar o nome da barbearia, o link, o QR Code e o canto superior esquerdo (web e programa desktop, que mostra a mesma gestão web) precisam mostrar o nome novo; com logo, a logo vai para o canto superior esquerdo. O ADR-0028 tinha deixado o endereço (slug) só para leitura porque trocar invalidaria QR impressos e não havia redirecionamento.

## Decisão

### 1. `moveParticipation` (T-P-11)

- `POST …/participations/{id}/move` com `expected_version` e `direction` (`up`/`down`). Atores: profissional da própria fila e dono. Cliente nunca reordena.
- Troca com a **vizinha** apenas (uma posição por vez). As posições são trocadas sob a trava da fila, usando uma posição livre do `queue_counter` como passo intermediário (a restrição `UNIQUE (queue_id, position)` continua imediata).
- Se a troca envolve a pessoa `called`, ela volta a `waiting` (sem `called_at`) e a outra passa a `called` com **nova** tolerância de 10 minutos. A ordem dos `UPDATE` (primeiro "desachamar", depois chamar) mantém o índice "uma cadeira por fila". Troca com a chamada exige fila `open`, como `callNext`.
- `in_service` nunca se move. As duas participações recebem nova versão, auditoria (T-P-11) e evento `participation.updated`; a fila emite `queue.updated`/`queue.public.updated`.

### 2. Endereço acompanha o nome, endereços antigos continuam válidos

- Tabela `establishment_slug_alias` (módulo estabelecimento): ao trocar o slug, o antigo é guardado. `PublicBySlug` e `BySlug` aceitam o antigo e devolvem a loja com o slug **atual**; a página `/loja/:slug` leva o navegador ao endereço atual (mantendo caminho e `?entrada=`).
- Endereço antigo fica reservado para a loja que o usou (`slug_taken` para outra loja), para que um QR impresso nunca abra a loja errada. A própria loja pode voltar a ele.
- Na tela de configurações o campo "Endereço do link" acompanha o nome (sem acentos, hífens) até o dono editá-lo à mão. O QR é gerado a partir do slug salvo.
- O canto superior esquerdo mostra logo + nome da loja na página pública e na gestão, relidos do servidor depois de salvar (a sessão também é relida, para a lista de lojas).

## Alternativas

- Reordenar arrastando para qualquer posição: mais flexível, porém mais fácil de "furar fila" sem querer e mais difícil de auditar. Uma posição por vez cobre o caso pedido.
- Manter o slug fixo e só mudar o nome exibido: não atende ao pedido (link e QR com o nome antigo).
- Redirecionamento HTTP no servidor: o roteamento de `/loja/*` é da aplicação web (SPA); o redirecionamento no cliente evita duplicar regras no Caddy.

## Consequências

- R-FILA-05 passa a permitir reordenação pela equipe; C-FILA-14 cobre o caso.
- A verificação "endereço livre" de outra loja e a gravação do alias acontecem na transação da alteração; a corrida entre duas lojas disputando o mesmo endereço antigo no mesmo instante é teoricamente possível (sem restrição única entre as duas tabelas) e foi considerada aceitável para o uso atual.
- Supera a pendência "redirects de slugs antigos não implementados" do ADR-0028.
