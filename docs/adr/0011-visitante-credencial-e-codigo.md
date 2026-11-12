# ADR-0011 — Credencial de visitante por loja e código de recuperação

- Situação: Aceita (etapa 00, aplicando prazos do anexo)
- Data: 2026-11-12

## Contexto

O visitante não tem conta, mas precisa acompanhar e pagar a visita, inclusive se trocar de aparelho. Nick não pode dar acesso, e o código não pode vazar.

## Decisão

- Cookie `__Host-fila_visitor` com token aleatório de 256 bits (banco guarda hash), vinculado a **uma identidade de visitante por loja** (um navegador pode ter visitantes de lojas diferentes no mesmo cookie).
- Prazos: 7 dias sem uso; 30 dias desde a criação; após encerramento da comanda, até 24 h (sem passar do limite absoluto).
- Código de recuperação: 80 bits aleatórios em base32 Crockford (`XXXX-XXXX-XXXX-XXXX`), exibido uma vez na criação da credencial (imediatamente antes da entrada na fila) com `Cache-Control: no-store`. Guardado como HMAC-SHA256 com chave secreta do servidor (permite busca sem guardar o código).
- Recuperação: código no **corpo** da requisição; válido 30 dias da criação e até 24 h após encerramento; limite de tentativas por loja e origem; cada uso gera novo código e revoga credenciais anteriores (e suas conexões SSE).
- Criação de visitante fica fora da regra de `Idempotency-Key`: repetir cria outra identidade sem visita (inofensiva, expira). Assim nenhuma resposta com segredo precisa ser armazenada.
- Não existe operação de associar visita de visitante a conta.

## Alternativas consideradas

- Código curto (6 dígitos): rejeitado (força bruta viável).
- Código em link: proibido pelo anexo.
- Guardar resposta com o código para replay idempotente: rejeitado (segredo em repouso).

## Consequências

- A equipe resolve no balcão quem perdeu credencial e código.
- Proteção contra várias identidades anônimas é só redução de abuso (R-VIS-10).
