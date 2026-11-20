# Backup e recuperação na V1 (Neon + R2 + Render)

Complementa [backup-restauracao.md](backup-restauracao.md), que foi escrito para o servidor próprio com Docker Compose. Os comandos de trava, conciliação, reaplicação de exclusões, troca de época do fluxo e liberação são **os mesmos**. Muda onde ficam as cópias e como se chega até elas.

## O que precisa ser preservado

| O quê | Onde vive | Cópia | Retenção sugerida |
|---|---|---|---|
| Banco | Neon | 1) Histórico/PITR do próprio Neon; 2) dump diário **cifrado** no bucket de backup (`Backup` no GitHub Actions) | Neon: a janela do plano; dumps: 35 dias (regra de ciclo de vida só no prefixo `banco/`) |
| Fotos e logos | Bucket `fila-*-midia` (R2, privado) | Cópia diária, sem apagar nada no destino, para `fila-*-backup/midia/` | Sem expiração automática (nunca use um ciclo de vida genérico por idade nas fotos) |
| Diário de exclusões | Bucket `fila-*-diario` (escrito pelo worker a cada 5 s) | Cópia diária para `fila-*-backup/diario/`; `fila-admin diario baixar` | Sem expiração |
| `FILA_SECRET_KEY` (e anteriores) | Render + cofre do responsável | **Fora** da infraestrutura: gerenciador de senhas do responsável | Enquanto houver dado cifrado com ela |
| Chave privada age dos backups | Só com o responsável (gerenciador de senhas + cópia offline) | — | Enquanto houver backup cifrado com ela |

O backup do Neon **não** inclui as fotos nem o diário. Sem a `FILA_SECRET_KEY`, o banco restaurado perde e-mails na fila, códigos e sessões (que são invalidados de qualquer forma). Sem a chave privada age, os dumps não podem ser abertos.

## Alerta de backup ausente ou atrasado

O workflow `Backup` envia ao monitor externo um ping de sucesso ou `/fail` com falha. Configure esse monitor para esperar **1 sinal a cada 24 h, com tolerância de 2 h**. Se o GitHub deixar de rodar o agendamento (por exemplo, ao desativar workflows agendados num repositório inativo), o monitor percebe a falta do sinal e avisa.

## Ensaio de restauração (fazer antes de abrir e a cada mês)

Isolado: usa um PostgreSQL descartável no Docker, grava e-mails em arquivo (nada é enviado), desliga Pix e fotos e não usa domínio público.

```bash
# 1) baixar o último dump cifrado (credencial R2 de LEITURA do bucket de backup, no terminal)
aws s3 cp --endpoint-url "$R2" "s3://fila-prod-backup/banco/AAAA/MM/DD/<arquivo>.dump.age" .
# 2) baixar o diário de exclusões (opcional) para uma pasta
aws s3 sync --endpoint-url "$R2" "s3://fila-prod-backup/diario/" ./diario-bucket/
go -C backend run ./cmd/fila-admin diario baixar -destino ./diario   # alternativa, com a config do ambiente
# 3) ensaiar (pede FILA_SECRET_KEY sem eco)
scripts/ensaio-restauracao.sh <arquivo>.dump.age ~/caminho/chave-age.txt ./diario
```

Registre a duração (o script mostra o tempo de cada passo) em [evidencias.md](evidencias.md).

## Recuperar a produção

Escolha **um** caminho:

**A. Erro recente (dados apagados ou corrompidos há pouco): restauração a um instante do Neon (PITR).**
1. No Render, **suspenda** `fila-worker-prod` e depois `fila-api-prod` (Suspend). O site responde "indisponível". Pausar o worker sozinho **não** bloqueia escritas da API.
2. No Neon, crie um **branch** a partir do instante anterior ao problema (*Branches → Create branch → Past point in time*). Não sobrescreva o branch principal enquanto não conferir os dados.
3. Confira o branch com o `fila-admin verificar`, apontando para ele com a URL do papel `fila_app` desse branch.
4. Promova o branch a principal (*Set as default*) ou troque a `FILA_DATABASE_URL` da API e do worker para o endpoint dele.
5. Num terminal com a credencial de **migrador** (lida com `read -rs`, como em [publicacao-v1.md](publicacao-v1.md)), rode `migrate up`, `migrate permissoes -app fila_app` e `migrate permissoes -app fila_app -verificar`. Não use o workflow `Publicar` aqui: ele dispara deploy dos serviços suspensos.
6. Reative **só a API** (Resume). A API não executa tarefas de fundo. As sessões antigas do backup voltam a valer até o passo 8.
7. No **Shell da API** (Render → `fila-api-prod` → Shell; os quatro executáveis estão em `./bin`), trave **explicitamente**. Depois de um PITR, não confie só na detecção pelo OID do banco:
   `./bin/fila-admin restauracao travar -motivo "PITR para AAAA-MM-DD HH:MM"`. Só então reative o worker. Ele sobe **travado**.
8. No mesmo Shell, concilie: `./bin/fila-admin restauracao reconciliar`. Sem `-diario`, o diário é baixado sozinho do `FILA_DELETION_JOURNAL_BUCKET`. Isso consulta a AbacatePay, encerra todas as sessões, troca a época do fluxo e reaplica as exclusões.
9. Confira as pendências financeiras no painel do dono e libere: `./bin/fila-admin restauracao liberar`.

**B. Perda do projeto ou da conta Neon: dump cifrado do bucket de backup.**
1. Crie um projeto Neon novo (PostgreSQL 18), o papel `fila_app` e o banco ([publicacao-v1.md](publicacao-v1.md), passo do Neon).
2. Decifre o dump numa máquina confiável (`age -d -i chave.txt -o fila.dump arquivo.dump.age`) e restaure com `pg_restore --no-owner --no-privileges -d "<URL do migrador>" fila.dump`. Apague o `fila.dump` decifrado ao terminar.
3. Aponte a `FILA_DATABASE_URL` dos serviços para o banco novo e siga a partir do passo 5 de A.

**C. Perda de fotos:** sincronize de volta com `aws s3 sync s3://fila-prod-backup/midia/ s3://fila-prod-midia/`. Objetos que o banco não referencia são removidos pela limpeza normal (só `staging/` antigo) ou ficam sem uso.

Nunca ligue uma cópia restaurada às chaves de produção do Resend ou da AbacatePay **fora** destes passos. Ela poderia enviar e-mails ou consultar cobranças reais.
