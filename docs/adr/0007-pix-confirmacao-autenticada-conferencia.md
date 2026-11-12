# ADR-0007 — Pix confirmado só pelo provedor; conferência separada

- Situação: Aceita (decisão 07 do anexo + detalhes da etapa 00)
- Data: 2026-11-12

## Contexto

Pagamento é o ponto de maior risco: a rede pode falhar no meio da criação da cobrança, o provedor pode reenviar avisos, o cliente pode pagar duas vezes, e nada do navegador prova pagamento.

## Decisão

- Recebimento Pix só por evidência autenticada do provedor (webhook com autenticidade validada conforme contrato oficial, ou consulta autenticada pelo backend). Retorno do navegador, comprovante e "já paguei" não existem como operação.
- **Uma tentativa ativa por comanda** (`creating`/`pending`/`unknown`), garantida por índice único parcial.
- A tentativa é gravada **antes** de chamar o provedor. Timeout/resposta ambígua → `unknown`; a interface mostra "verificando pagamento" e não permite outra tentativa até o worker reconciliar. Idempotência local não substitui garantia remota.
- Pagamento tardio/duplicado é registrado como `excess` com pendência — nunca descartado nem apagado.
- Conferência é registro separado, feito pela equipe; Pix recebido não volta a "não pago".
- Dinheiro/maquininha: recebimento + conferência na mesma ação, dois registros.
- Estorno: integral por recebimento; sucesso só com evidência; parcial/desconhecido/presencial em revisão documentada pelo dono.
- Chave da API só no backend, via segredo externo; integração de produção desligada por configuração até validação em sandbox. Adaptador do provedor isolado atrás de interface, com testes de contrato usando fixtures sem segredos.
- Capacidades da AbacatePay **não** são presumidas: cada uma está marcada `x-external-pending` e será confirmada na documentação oficial na etapa 08.

## Alternativas consideradas

- Confiar no redirecionamento do provedor: rejeitado (falsificável).
- Criar nova cobrança após timeout: rejeitado (risco de cobrar duas vezes).
- Apagar recebimento duplicado para fechar saldo: proibido pelo anexo.

## Consequências

- O worker precisa rodar mesmo com navegador fechado (reconciliação e alertas).
- Pendências financeiras se tornam parte do painel do dono.
