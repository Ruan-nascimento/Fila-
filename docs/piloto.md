# Plano de piloto e de volta atrás (rollback)

**Situação em 2026-11-14:** o sistema passou na revisão final ([evidencias.md](evidencias.md)), mas **o piloto em produção ainda não pode começar**. Faltam dois itens externos (seção 1), que não dependem de código e precisam de decisão e contratação do responsável. Nada foi publicado nem ativado.

## 1. Antes de começar (bloqueadores)

| # | Item | Por quê | Quem | Situação |
|---|---|---|---|---|
| 1 | **Provedor de e-mail** (SMTP ou API) contratado e integrado | Sem ele, o convite da dona e dos profissionais, a confirmação de conta e a redefinição de senha **não chegam** em produção: ninguém da equipe ativa o acesso. A integração precisa de código novo (um "sender") e das credenciais | Responsável: contratar; desenvolvimento: integrar e testar | **Pendente — bloqueia** |
| 2 | **Servidor de produção**: Linux com Docker, domínio, DNS e portas 80/443 | Hospedar com TLS público (Let's Encrypt) | Responsável | Pendente |
| 3 | **Cópia dos backups para fora do servidor** e cofre para a chave de cifra | Sobreviver à perda do servidor | Responsável + operador | Pendente |
| 4 | **Canal de alertas** (e-mail ou Telegram do operador) | Saber de problema sem olhar a tela | Operador | Pendente (dá para começar com `verificar` em agendamento enviando e-mail pelo servidor) |
| 5 | **Pix:** fazer o checklist com chave de teste da AbacatePay (sandbox) **antes** de ligar a chave real | Nunca foi testado contra a AbacatePay de verdade | Responsável (chave) + desenvolvimento | Pendente. **O piloto pode começar sem Pix** (dinheiro e cartão) |
| 6 | Prazos de retenção revisados com orientação jurídica | LGPD; prazos fiscais | Responsável | Pendente (não impede o piloto técnico; padrões técnicos documentados) |
| 7 | Teste rápido em **celulares reais** (1 Android, 1 iPhone) e no Firefox/Safari | Só Chrome e Edge do computador foram testados | Qualquer pessoa, seguindo o guia | Pendente |

## 2. Escopo do piloto

- **Uma loja**, com a dona e 1 ou 2 profissionais. Clientes reais com aviso visível no balcão: "sistema em teste; a fila de papel continua valendo".
- **Duração:** 2 semanas, em dias normais de atendimento.
- **Pagamento:** dinheiro e cartão. Pix só depois do item 5, por decisão explícita do responsável. Pontos e prêmios: opcional, decidido pela dona.
- **Fora do piloto:** várias lojas, apps instalados (desktop, Android, iOS), notificação push.

## 3. Roteiro de cada dia

| Quando | Quem | O quê |
|---|---|---|
| Antes de abrir | Dona | Gestão → Pendências zerada ou entendida. Abre as filas |
| Antes de abrir | Operador | `fila-admin verificar -backups /backups` saindo com 0 |
| Durante | Equipe | Atende pelo sistema; **fila de papel em paralelo** na primeira semana |
| Fechamento | Dona | Comandas sem pagamento pendente; caixa do sistema confere com o caixa físico; Pendências resolvidas com nota |
| Fechamento | Operador | Backup do dia conferido; alertas do dia lidos |
| Toda semana | Todos | 15 minutos: o que atrapalhou, o que faltou, defeitos (registrar em [bugs.md](bugs.md) com reprodução) |

O roteiro completo (visitante, conta, pedido, chamada, ausência, interrupção, pagamento, pontos, rede caindo, pendências) já foi exercitado em navegador real contra a homologação. Ele está em `frontend/e2e/piloto.spec.ts` e pode ser repetido antes de cada versão nova.

## 4. Como medir

- **Funcionou:** atendimentos registrados ÷ atendimentos do dia (meta ≥ 90% na 2ª semana); diferença de caixa sistema × físico = 0; nenhuma pendência financeira sem explicação.
- **Técnico:** sem alerta crítico sem resposta; nenhuma resposta 5xx inesperada (`fila_http_requests_total{status=~"5.."}`).
- **Pessoas:** a equipe consegue operar só pelo celular? O cliente entende a posição e a espera?

## 5. Quando parar (critérios de interrupção)

**Pare o uso do sistema na hora** (volte só para o papel e avise o operador) se:

- algum valor cobrado estiver errado, ou um pagamento aparecer como recebido sem ter sido;
- alguém vir dados de outra pessoa ou de outra loja;
- uma fila ou comanda sumir.

Depois de parar:

1. O operador trava o worker (`fila-admin manutencao iniciar -motivo …`).
2. Registra o defeito como **crítico** em [bugs.md](bugs.md).
3. O piloto só volta com a correção testada.

## 6. Voltar atrás (rollback)

| Situação | Ação | Tempo medido na homologação |
|---|---|---|
| Versão nova com defeito | `FILA_SEM_BUILD=1 deploy/implantar.sh /etc/fila/producao.env <versão anterior>`. O banco segue compatível ([operacao/implantacao.md](operacao/implantacao.md) §7) | ~15–25 s |
| Dados corrompidos ou migração ruim | Restaurar o backup feito antes da implantação, conciliar e liberar ([operacao/backup-restauracao.md](operacao/backup-restauracao.md)) | API no ar em 14–51 s; worker liberado em 17–54 s (até ~1 GB) |
| Encerrar o piloto | Fila de papel; `fila-admin manutencao iniciar`; os dados continuam consultáveis no sistema (não há exportação no MVP); **não apagar dados** (registros financeiros seguem a política de retenção) | — |

Em qualquer volta atrás, **o dinheiro já recebido não se perde**: pagamentos e estornos do provedor são reconciliados, e dinheiro e cartão estão no caixa físico conferido no fechamento.

## 7. Responsáveis

| Papel | Quem | Responde por |
|---|---|---|
| Responsável pelo produto | o dono do projeto | decisões, contratações (e-mail, servidor, Pix), ativação de cobrança real |
| Operador | responsável técnico | implantação, backups, alertas, rotinas |
| Dona da loja | a dona | pendências, caixa, equipe, decisão de parar |
| Equipe | profissionais | usar o sistema e relatar problemas |
