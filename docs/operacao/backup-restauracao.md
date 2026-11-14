# Backup e restauração

## 1. Backup

`docker compose … run --rm backup` ([`deploy/backup/backup.sh`](../../deploy/backup/backup.sh)) faz:

1. **Dump:** `pg_dump` em formato custom, com o papel `fila_backup` (só leitura), por TLS.
2. **Conferência:** `pg_restore --list` precisa ler o arquivo inteiro, e um dump sem dados é recusado.
3. **Cifra:** AES-256 com a chave em `segredos/backup/backup_chave` e HMAC-SHA256 do arquivo cifrado no manifesto. A restauração recusa arquivo alterado.
4. **Diário:** copia junto o diário de exclusões.
5. **Marcador:** grava `ULTIMO_BACKUP_OK`, que `fila-admin verificar -backups` acompanha.
6. **Retenção:** apaga backups com mais de `FILA_BACKUP_RETENCAO_DIAS` dias.

| Quando | Como |
|---|---|
| Todo dia (sugestão: 03:00, fora do atendimento) | cron do servidor |
| Antes de toda migração | automático no `implantar.sh` |
| Antes de manutenção arriscada | à mão |

**Cópia fora do servidor:** os arquivos ficam em `FILA_BACKUPS_DIR`. **Copiá-los para outro lugar** (outro servidor, armazenamento de objetos) é obrigatório para sobreviver à perda do servidor. Isso **depende da hospedagem escolhida e não está configurado** (bloqueio externo). Os arquivos já saem cifrados.

**Retenção:** o padrão de 14 dias é técnico, não legal. Contas excluídas continuam nos backups até eles vencerem; a restauração reaplica as exclusões do diário (seção 3). O prazo definitivo depende da validação jurídica pendente ([retencao.md](../retencao.md)).

**Chave de cifra:** guarde uma cópia **fora** do servidor, num cofre de senhas. Sem ela, nenhum backup pode ser restaurado. Ao trocá-la, guarde a antiga enquanto houver backup cifrado com ela.

## 2. Por que restaurar não é só "voltar o banco"

O banco restaurado volta no tempo; o mundo lá fora, não. O que aconteceu depois do backup, e como o sistema trata:

| Aconteceu depois do backup | Risco se nada for feito | O que o sistema faz |
|---|---|---|
| Pix pago | pagamento "desaparece"; o cliente cobrado de novo | a conciliação lista no provedor os pagos desde o último sinal de vida do worker − 1 dia e registra pelo caminho normal: recria a tentativa se a comanda existe; abre a pendência `restored_orphan_charge` se nem a comanda existe |
| Estorno feito | a loja devolveria duas vezes | a pendência `restored_refund_unrecorded` avisa o dono para registrar de novo |
| Tentativa Pix que nunca chegou ao provedor | o worker criaria um Pix "do passado" | a conciliação só consulta: o que não existe no provedor vira falha, e o cliente gera outro Pix se precisar |
| Pedido de exclusão de conta | os dados voltam sem o pedido | o **diário de exclusões** (fora do banco) recria o pedido com o mesmo protocolo e os mesmos efeitos |
| Saída ou troca de senha | a sessão antiga volta a valer | **todas** as sessões e links de e-mail não usados são encerrados; todos entram de novo |
| Eventos de tempo real | navegadores retomariam por cursor de uma linha do tempo que não existe mais | época nova do SSE: todo navegador recebe um retrato novo |
| Reservas vencidas, e-mails, exclusões em andamento | o worker agiria sobre o estado velho antes de conciliar | o worker **trava sozinho** (banco com outro oid) e só volta com `restauracao liberar`, depois da conciliação |
| Entrada na fila, atendimento, recebimento em dinheiro ou cartão | **perdidos** (não há fonte externa) | é a perda máxima (RPO): refeitos no balcão; o dono confere o caixa do dia |

## 3. Passo a passo

```bash
ENVF=/etc/fila/producao.env
DC="docker compose -f deploy/compose.yaml --env-file $ENVF --profile ferramentas --profile banco-local"

ls /srv/fila-backups/                                              # escolha o backup (nome sem extensão)
deploy/restaurar.sh $ENVF fila-20261114T030000Z "disco do banco corrompido"
$DC run --rm fila-admin restauracao reconciliar -diario /var/lib/fila/diario
# confira o relatório e as pendências em Gestão → Pendências (dono)
$DC run --rm fila-admin restauracao liberar
```

O que `restaurar.sh` faz:

1. Para API e worker.
2. Confere o HMAC e decifra o backup.
3. Restaura num banco **novo** e troca pelo atual. O atual **fica guardado** como `fila_antes_<carimbo>`: nada é apagado. Apague-o à mão depois de confirmar que a restauração está boa.
4. Migra e aplica as permissões.
5. Trava o worker.
6. Sobe a API, com o worker travado.

Durante a trava:

- a API atende normalmente (todos precisam entrar de novo);
- **o worker não faz nada**: nem Pix, nem e-mail, nem vencimento.

`restauracao reconciliar` recusa rodar sem trava de restauração. Se o provedor não responder, ela para com erro e a trava continua: basta rodar de novo. Cada passo é repetível. `liberar` recusa sem conciliação **feita depois do início da trava**.

**Restauração física** (cópia de arquivos ou PITR do provedor de banco): ela mantém o oid do banco, então o worker não percebe sozinho. Rode `fila-admin restauracao travar -motivo "…"` **antes** de subir o worker.

## 4. Objetivos de recuperação (medidos, não prometidos)

| Medida | Definição | Valor medido | Onde |
|---|---|---|---|
| **RPO** (perda máxima) | o que foi registrado só no banco depois do último backup | **até 24 h** com backup diário (o que o backup cobre). Não perdidos: Pix e estornos (vêm do provedor) e exclusões (diário, em segundos). Perdidos: filas, atendimentos e recebimentos presenciais depois do backup. **Medido:** a entrada na fila feita depois do backup sumiu e o Pix pago depois do backup foi recuperado | exercício da seção 5 |
| **RTO** (tempo até voltar) | do início da restauração até a API no ar / worker liberado | banco pequeno: **14 s / 17 s**. Banco com ~1 GB: **51 s / 54 s**. Fora disso, o tempo humano de conferir as pendências | [evidencias.md](evidencias.md) |

Esses valores são **desta máquina** (notebook, Docker Desktop) e destes volumes. No servidor real, refaça o exercício e registre:

- O RTO cresce com o tamanho do banco: o restore levou 33 s para ~1 GB, com 85 MB de dump cifrado.
- Um RPO menor que 24 h exige backup mais frequente ou arquivamento contínuo do WAL (PITR). Isso **não foi configurado nem testado** e é uma escolha que depende da hospedagem.

## 5. Exercício de restauração (fazer todo mês e antes do piloto)

Automatizado em `frontend/e2e/operacao.spec.ts`, teste "restauração de backup", contra a homologação em Docker:

1. loja e comanda aguardando pagamento;
2. **backup**;
3. depois do backup: Pix pago (o worker registra), pedido de exclusão e alguém entra na fila;
4. `restaurar.sh`;
5. `verificar` mostra `FilaWorkerTravadoRestauracao`;
6. conciliação: 1 pagamento registrado com a tentativa recriada do provedor, 1 exclusão reaplicada;
7. `liberar`;
8. a dona entra de novo e vê o Pix registrado; a exclusão termina; a entrada na fila feita depois do backup não existe (RPO).

Também coberto por testes de integração com banco real:

- `TestRestoreReconcilesBeforeWorkerResumes`: órfã, estorno, tentativa que nunca chegou, sessões, repetição;
- `TestGateDetectsCopiedDatabaseAndRequiresReconciliation`.
