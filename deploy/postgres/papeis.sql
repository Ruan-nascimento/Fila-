-- Papéis do banco com menor privilégio (rodar UMA vez, como administrador do PostgreSQL).
-- Uso com psql (as senhas vêm de arquivos, nunca escritas aqui):
--   psql -v ON_ERROR_STOP=1 -v senha_migrador="$(cat …)" -v senha_app="$(cat …)" -v senha_backup="$(cat …)" -f papeis.sql
-- No contêiner postgres do compose, deploy/postgres/init/01-papeis.sh faz isso sozinho.
--
--   fila_migrador  dono do banco e das tabelas; só o comando migrate usa.
--   fila_app       api, worker e fila-admin: só lê e escreve dados (permissões finas aplicadas
--                  depois de cada migração por: migrate permissoes -app fila_app).
--   fila_backup    só leitura de tudo (pg_dump).

CREATE ROLE fila_migrador LOGIN PASSWORD :'senha_migrador';
CREATE ROLE fila_app LOGIN PASSWORD :'senha_app';
CREATE ROLE fila_backup LOGIN PASSWORD :'senha_backup';
GRANT pg_read_all_data TO fila_backup;

-- Limites de proteção da aplicação: uma consulta travada não segura o banco para sempre.
ALTER ROLE fila_app SET statement_timeout = '30s';
ALTER ROLE fila_app SET lock_timeout = '10s';
ALTER ROLE fila_app SET idle_in_transaction_session_timeout = '60s';

CREATE DATABASE fila OWNER fila_migrador ENCODING 'UTF8' TEMPLATE template0;
REVOKE ALL ON DATABASE fila FROM PUBLIC;
GRANT CONNECT ON DATABASE fila TO fila_migrador, fila_app, fila_backup;
