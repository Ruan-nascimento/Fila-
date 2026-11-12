# ADR-0017 — Identidade: senhas, tokens, cookies, e-mail e limites de tentativa

- Situação: Aceita (etapa 02)
- Data: 2026-11-12

## Contexto

A etapa 02 implementa contas, sessões, convites e visitantes. O ADR-0006 e o ADR-0011 já fixaram o essencial (sessões opacas em cookie, CSRF, código de recuperação). Faltavam os detalhes que só aparecem ao implementar: parâmetros de hash, de onde vêm as chaves, como o link do e-mail carrega o token, como desenvolver sem HTTPS e sem provedor de e-mail, e quais limites de tentativa usar.

## Decisão

**Senhas.** Argon2id (`golang.org/x/crypto/argon2` v0.58.0) com m = 19 MiB, t = 2, p = 1, sal de 16 bytes e chave de 32 bytes. Esses valores são o mínimo recomendado pela OWASP (Password Storage Cheat Sheet, consultada em 12/11/2026). Nesta máquina de desenvolvimento cada hash leva cerca de 38 ms.
- O hash é guardado no formato PHC (`$argon2id$v=19$m=…,t=…,p=…$sal$hash`). Se os parâmetros mudarem, o hash antigo é recalculado no próximo login.
- A senha é normalizada em NFKC e precisa ter de 10 a 128 caracteres, sem regras de composição (NIST SP 800-63B).
- No máximo N hashes rodam ao mesmo tempo, sendo N o número de CPUs (mínimo 2). Assim uma avalanche de logins não esgota a memória.
- Quando a conta não existe, o servidor calcula um hash de mesmo custo, para o tempo de resposta não revelar se o e-mail existe.

**Tokens.** Sessão, verificação de e-mail, redefinição de senha e convite usam 256 bits aleatórios em base64url. O banco guarda só o SHA-256: sem sal, o que basta porque o segredo é aleatório e não dá para adivinhar por dicionário.

**Chave do servidor.** Uma chave mestra (`FILA_SECRET_KEY`, 32+ bytes) é obrigatória para a API, o worker e o `fila-admin`. Dela saem, via HKDF-SHA256, subchaves separadas para:
- o token CSRF;
- o HMAC do código de visitante;
- o HMAC dos assuntos dos limites de tentativa;
- a cifra dos e-mails na fila de envio.

Trocar a chave invalida tokens CSRF, códigos de visitante e e-mails ainda não enviados. A troca planejada de chave fica para a etapa 12.

**Links por e-mail.** O token vai no fragmento (`/verificar-email#token=…`), nunca na query string. O navegador não envia o fragmento ao servidor, então ele não aparece em logs, proxies nem no cabeçalho Referer (que já é `no-referrer`). A página lê o fragmento, apaga-o da barra de endereço e envia o token no corpo de um POST.

**E-mail.**
- A caixa de saída (`mail_outbox`) é gravada na mesma transação que cria o token. O conteúdo fica cifrado (AES-256-GCM) e é apagado depois do envio.
- O worker entrega em segundo plano. Isso também iguala o tempo de resposta exista ou não a conta.
- Em desenvolvimento, `FILA_MAIL_MODE=file` grava cada e-mail como arquivo em `backend/.correio/` (fora do Git).
- Em produção o padrão é `disabled`, e `file` é recusado. Não há provedor de e-mail real: limitação externa já registrada em `docs/regras.md`.

**Cookies em desenvolvimento.**
- Em produção os cookies são `__Host-fila_session` e `__Host-fila_visitor`, com `Secure; HttpOnly; SameSite=Lax; Path=/` (ADR-0006).
- Navegadores só aceitam o prefixo `__Host-` com `Secure`, e o desenvolvimento local usa `http://`. Por isso, fora de produção, `FILA_COOKIE_SECURE=false` usa os nomes `fila_session` e `fila_visitor`, sem `Secure`. O resto não muda.
- A configuração recusa `false` em produção. Os testes de integração usam HTTPS com `Secure` ligado.

**Origem e CSRF.**
- Todo método não seguro sob `/v1/` exige `Origin` na lista `FILA_ALLOWED_ORIGINS`. A exceção é `/v1/webhooks/`, porque provedores não são navegadores.
- Com sessão presente, também é obrigatório `X-CSRF-Token`: um HMAC dos identificadores das sessões, obtido em `GET /v1/session` e guardado só em memória no frontend.
- O IP do cliente vem da conexão. `X-Forwarded-For` só é usado com `FILA_TRUST_X_FORWARDED_FOR=true`, atrás de um proxy reverso próprio.

**Limites de tentativa.** Os contadores ficam no PostgreSQL, em janelas fixas, e o assunto é guardado só como HMAC (nunca e-mail nem IP em claro).

| Regra | Limite | Janela | Conta |
|---|---|---|---|
| Senha errada por conta (login, reautenticação, troca, aceite de convite) | 10 | 15 min | Ao atingir, recusa mesmo com a senha certa |
| Senha errada por IP | 50 | 15 min | Protege contra testar uma senha em muitas contas |
| Pedidos que geram e-mail, por endereço / por IP | 3 / 20 | 1 h | Cadastro, reenvio, redefinição |
| Token inválido por IP | 20 | 15 min | Verificação, redefinição, convite |
| Criar visitante por loja+IP / por loja+navegador | 20 / 5 | 1 h | Reduz abuso; não identifica pessoas |
| Código de visitante errado por loja+IP / por loja | 10 / 200 | 15 min / 1 h | Código tem 80 bits |

Consequência aceita: alguém que conhece o e-mail de uma pessoa pode bloquear o login dela por até 15 minutos errando a senha de propósito. É o custo de não depender só do IP (R-CONTA-11). A janela fixa permite até 2× o limite na virada da janela.

**Regras de conta.**
- Cadastro repetido de e-mail pendente nunca troca a senha: só reenvia o link. Assim, quem não controla o e-mail não define a senha de quem controla.
- Concluir uma redefinição de senha prova o e-mail, então uma conta pendente passa a ativa.
- Aceitar convite com conta pendente usa a senha informada: o convite chegou ao e-mail.
- Cada navegador tem uma sessão de conta por vez; entrar de novo encerra a anterior.
- O "último uso" da sessão é gravado no máximo uma vez por minuto.
- O cookie dura até o limite absoluto (30 dias para cliente, 12 h para equipe). A inatividade de 7 dias é conferida no servidor a cada uso.

**Autorização.**
- Sem sessão → 401.
- Sem vínculo com a loja da rota → 404, para não revelar que a loja existe.
- Com vínculo, mas sessão de cliente ou papel insuficiente → 403.
- Cada rota declara no código os mesmos pares ator/escopo do `x-authz`, e um teste compara as duas listas.

**Visitante (ADR-0011, detalhes).**
- O cookie identifica o navegador. Cada credencial liga o navegador a um visitante de uma loja, no máximo uma ativa por loja.
- O limite absoluto de 30 dias conta da criação do visitante. Assim, recuperar não estende o prazo além do anexo.
- `endVisitorSession` encerra todas as credenciais de visitante do navegador. A rota não tem loja, e o objetivo é "sair deste aparelho".
- O visitante não tem coluna de conta: associar visita a conta é impossível por construção (R-VIS-09).

**Escopo.**
- A tabela `professional` desta etapa é mínima (nome e situação), só para os convites. A etapa 03 acrescenta fila, letra e cadastro pela interface, com migrações novas.
- Até lá, profissionais só existem em testes ou inseridos manualmente.
- Os eventos `stream.session_ended` entram na etapa 06, junto com a outbox de eventos e o SSE. Hoje não há conexão SSE para encerrar, e a revogação já vale na requisição seguinte.

## Alternativas consideradas

- **bcrypt:** limite de 72 bytes e sem custo de memória. Argon2id já era a escolha do ADR-0006.
- **Parâmetros maiores (RFC 9106: 64 MiB, t = 3, p = 4):** mais seguros, mas custosos para o servidor pequeno do piloto. Podem ser adotados depois sem migração, graças ao recálculo no login.
- **Token na query string:** vaza em logs, histórico e Referer.
- **E-mail síncrono na requisição:** tempo de resposta revelaria a existência da conta, e uma queda perderia o e-mail.
- **Limites em memória:** burlados com vários processos.
- **Cookies `Secure` em `http://localhost`:** o comportamento varia entre navegadores e não foi possível verificar em todos. A configuração explícita é previsível.

## Consequências

- `FILA_SECRET_KEY` precisa ser gerada (`go -C backend run ./cmd/fila-admin gerar-chave`) e guardada com o mesmo cuidado da senha do banco.
- Sem provedor de e-mail, verificação, redefinição e convites não chegam às pessoas em produção. A interface não afirma que o e-mail foi entregue.
- O worker precisa estar rodando para os e-mails saírem, também em desenvolvimento.
