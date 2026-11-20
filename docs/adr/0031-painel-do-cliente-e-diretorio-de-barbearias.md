# ADR-0031 — Painel do cliente e diretório de barbearias por distância

- Situação: Aceita
- Data: 2026-11-20

## Contexto

O cliente só chegava a uma barbearia pelo link/QR, e só via a própria fila dentro da página daquela loja. Pedido: um painel para o cliente se situar — procurar barbearias ordenadas pela distância (ou, sem GPS, por outro critério), com busca e paginação — e ver as filas em que já está.

## Decisão

1. **Localização da loja** (`establishment.latitude/longitude`, migração 00022): opcional, informada pelo dono na configuração (localização do aparelho, estando na barbearia, ou digitada). É pública, como o endereço. `updateEstablishment.location` aceita objeto ou `null` (remove).
2. **Diretório público** `GET /v1/public/establishments` (módulo estabelecimento): `q` (nome/endereço, sem acento nem maiúsculas, curingas do LIKE escapados), `sort` = `distance` (exige `lat`/`lng`; lojas sem localização no fim) | `open` (com fila aberta primeiro, depois nome; padrão sem GPS) | `name`. Distância **em linha reta** (haversine em SQL puro; poucas lojas, sem PostGIS). Resumo das filas pela interface `QueueActivity` (o módulo não lê `participation`).
3. **Paginação**: cursor opaco com a posição na lista **e a impressão da busca** (ordem, termo, coordenadas); cursor de outra busca → 400. Posição (offset) em vez de chave porque a ordem por distância depende da consulta; aceitável para um diretório público pequeno (lojas novas no meio podem deslocar uma página).
4. **Privacidade da localização do cliente**: pedida só por gesto ("Mais perto") ou se o navegador já tinha permissão; enviada **arredondada (~100 m)**, só na consulta; nunca gravada; o log de acesso registra a rota, não a query. Negada → explica e usa "abertas agora".
5. **Minhas filas** `GET /v1/my-visits` (módulo fila): conta (até 20 lojas mais recentes) + todas as credenciais de visitante deste navegador (`acesso.Customers.AllCustomerIdentities`), com o mesmo critério de `getCurrentVisit`, loja por loja. O painel relê ao voltar à aba e a cada 30 s (o SSE é por loja; abrir um fluxo por loja no painel não compensa).
6. A página inicial (`/`) passa a ser esse painel; o cabeçalho ganha "Início".

## Alternativas

- PostGIS / índice espacial: desnecessário no tamanho atual; troca simples se o número de lojas crescer.
- Geocodificar o endereço por serviço externo: exigiria chave e enviar endereços a terceiros; o dono informa a posição.
- Um SSE por loja no painel: mais conexões por aparelho (limite de 6 por credencial) para ganho pequeno.

## Consequências

- Lojas sem localização aparecem no fim da ordem por distância e normalmente nas outras ordens.
- Distância é aproximada (linha reta, posição do cliente arredondada).
