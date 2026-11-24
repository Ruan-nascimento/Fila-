# Evidências — melhorias da versão 1.0.0 (23/11/2026)

Ambiente: notebook Linux Mint (X11), Intel Core i3-N305 (8 núcleos), 8 GB de RAM, Go 1.27.4, Node 26.13.1, Rust 1.100 (cargo), PostgreSQL 18.6 em contêiner descartável (`fila-teste-v1`). Nada foi executado contra homologação ou produção.

## Testes automatizados

| Conjunto | Resultado |
|---|---|
| Backend `go test -race -count=1 ./...` (PostgreSQL real) | 26 pacotes `ok`, sem corrida |
| `gofmt`, `go vet`, `staticcheck`, `go mod tidy -diff`, tipos gerados do contrato | limpos |
| Testes novos (backend): `TestQuantitiesInOneParticipation`, `TestPartialExecutionReviewAndPoints`, `TestProductsAsComplement`, `TestChangeRequestWithQuantities`, `TestAdmissionAndCapacity` (6 pessoas disputando a última vaga: 1 entra), `TestConfigurableTolerance`, `TestStoreSettingsSections`, `TestReceiptFees`, `TestAnalyticsWithQuantitiesProductsAndFees`, `TestOrderSearchOnServer`, `TestUpgradeTo100KeepsOrdersQuantitiesAndReceipts` | passam |
| Varredura de autorização em todas as rotas (inclui as novas, com categoria e produto reais) | passa |
| Frontend `vitest` | 153 testes passam; `tsc`, `eslint`, `prettier --check`, `vite build` e `check:api` limpos |
| Dashboard do desktop (`test:ui`) | 4 testes passam; `tsc` limpo |
| Rust (`cargo test --lib`) | 7 testes passam (3 novos de segundo plano) |
| `docs/tools/verificar_contrato.py` e `rastrear_evidencias.py --verificar` | OK |

## Desempenho da busca de comandas

Volume: uma loja com 20 000 comandas, participações e itens (inseridos direto no banco descartável de inspeção). Medição das duas consultas que a busca executa (participações por senha/nick + comandas paginadas), 80 execuções com 8 termos (com e sem resultado), no próprio PostgreSQL — sem rede, sem HTTP e sem a montagem das 20 comandas da página:

| Versão | p50 | p95 | máximo |
|---|---|---|---|
| Normalizando na consulta (`translate` por linha) | 259 ms | **329 ms** | 421 ms |
| Coluna normalizada gerada na escrita (migration 00028) | 22 ms | **64 ms** | 90 ms |

A primeira medição ficou acima da meta inicial (300 ms) e levou à migration 00028. Não é garantia universal: depende do hardware e do volume.

## Desktop em segundo plano (Linux)

Roteiro: `desktop-dono/testes/segundo-plano.sh` — build de depuração, painel falso local com SSE a cada 2 s, X aninhado (Xephyr) e D-Bus isolado (sem tocar na sessão da pessoa, sem notificação real). "Fechar" e "Sair do Fila" acionados por ganchos que só existem no build de depuração e passam pelo mesmo caminho do clique.

| Verificação | Resultado |
|---|---|
| Fechar a janela com a preferência ligada | processo continua |
| Página escondida recebe o fluxo | 20 eventos seguidos (ids 8–27, sem lacuna) com `visibilityState = hidden`; temporizadores continuaram |
| Consumo escondida (processo + filhos do WebKit) | CPU 0,7–0,8% de um núcleo em regime (9% no primeiro intervalo, logo após esconder); memória ~440 MiB |
| Segunda instância | sai com código 0 e reabre a janela existente (`IsViewable`) |
| Sair do Fila | processo encerra com código 0 |

**Não verificado:** clique físico no X e no menu da bandeja num ambiente com área de notificação real; exibição real da notificação com a janela escondida (o D-Bus isolado não tem servidor de notificações); suspensão e retorno do computador; perda de internet com a janela escondida; logout/revogação com a janela escondida (o comportamento vem da página, que já trata `stream.session_ended`). **Windows não foi testado** (nem compilado nesta entrega).

## O que não foi validado nesta entrega

- Pix real ou em sandbox com a mudança (só a lógica de custo estimado, com o provedor simulado dos testes).
- Larguras 320/390/768/1024/1440 px e leitores de tela em navegador real: as telas novas seguem os componentes existentes e foram testadas com Testing Library, mas não houve sessão em navegador real.
- Cenário de aceite §41 de ponta a ponta em navegador real (está coberto por partes nos testes de integração listados acima).
