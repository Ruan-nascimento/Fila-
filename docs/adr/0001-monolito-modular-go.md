# ADR-0001 — Monólito modular em Go

- Situação: Aceita (decisão 01 do anexo)
- Data: 2026-11-12

## Contexto

O MVP precisa de regras fortes de fila e dinheiro, construídas por uma equipe pequena e iniciante. Microsserviços trariam rede, versões e falhas parciais entre serviços sem benefício para uma barbearia piloto.

## Decisão

- Um único módulo Go em `backend/`, organizado por responsabilidade: `identidade`, `estabelecimento`, `catalogo`, `fila`, `atendimento` (comanda), `pagamento`, `fidelidade`, `auditoria`, mais `plataforma` (HTTP, banco, outbox, configuração).
- Dois executáveis do mesmo código: `cmd/api` (HTTP + SSE) e `cmd/worker` (outbox, reconciliação, expirações, exclusões). Mais `cmd/fila-admin` para processos controlados (ADR-0013).
- Módulos conversam por funções Go e transações compartilhadas; interfaces só nos limites úteis (provedor de pagamento, e-mail, relógio).
- Sem Redis ou fila externa enquanto o PostgreSQL atender (outbox no próprio banco).

## Alternativas consideradas

- Microsserviços: rejeitado (complexidade operacional, transações distribuídas).
- Monólito sem módulos: rejeitado (regras de dinheiro misturadas com HTTP dificultam teste e revisão).
- Fila externa (Redis/RabbitMQ): adiada até haver medição que justifique.

## Consequências

- Transações ACID cobrem uma operação inteira (ex.: concluir atendimento + gravar evento).
- Disciplina necessária: um módulo não lê tabelas de outro diretamente; usa funções do módulo dono.
- Escala horizontal da API depende de o SSE ler do banco (ADR-0010), não de memória local.
