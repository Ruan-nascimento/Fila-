#!/bin/sh
# Roda uma única vez, na criação do volume do PostgreSQL (docker-entrypoint-initdb.d).
# 1. Cria papéis e banco com menor privilégio (papeis.sql), senhas lidas dos segredos.
# 2. Liga TLS com certificado próprio: a aplicação conecta com sslmode=require mesmo na rede
#    interna, e conexões sem TLS de fora do contêiner são recusadas (pg_hba).
set -eu

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname postgres \
	-v senha_migrador="$(cat /run/secrets/pg_senha_migrador)" \
	-v senha_app="$(cat /run/secrets/pg_senha_app)" \
	-v senha_backup="$(cat /run/secrets/pg_senha_backup)" \
	-f /docker-entrypoint-initdb.d/papeis.sql.inc

openssl req -new -x509 -days 3650 -nodes -subj "/CN=postgres" \
	-keyout "$PGDATA/server.key" -out "$PGDATA/server.crt" >/dev/null 2>&1
chmod 600 "$PGDATA/server.key"
psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname postgres -c "ALTER SYSTEM SET ssl = on"

# Só TLS de fora do contêiner; conexões locais (o próprio contêiner) seguem por socket.
cat > "$PGDATA/pg_hba.conf" <<'HBA'
local     all  all                 scram-sha-256
hostssl   all  all  0.0.0.0/0      scram-sha-256
hostssl   all  all  ::/0           scram-sha-256
hostnossl all  all  0.0.0.0/0      reject
hostnossl all  all  ::/0           reject
HBA
