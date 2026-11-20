# ADR-0028 — Configurações, fotos, Google (só cliente), identidade da loja e QR Code

- Situação: Aceita
- Data: 2026-11-19
- Revisão de partida: `5bc96f0` (branch `feat/dashboard-desktop`); a especificação citava `00bdf42`, que não existe nesta cópia — o código foi reconferido antes de editar.
- Guia técnico e operacional: [configuracoes-e-identidade.md](../configuracoes-e-identidade.md)

## Contexto

O produto precisava de: (1) "Minha conta" para cliente, profissional e dono; (2) fotos de perfil com visibilidade adequada; (3) cadastro e login com Google **só na jornada de cliente**; (4) identidade da loja (logo, 16 paletas, informações públicas); (5) link e QR Code da loja; (6) entrada sem conta no login de cliente. Restrições: monólito Go com módulos donos das próprias tabelas (ADR-0001), sessões opacas em cookie com CSRF (ADR-0006/0017), contrato único (ADR-0008), sessão única do dono e desktop (ADR-0027), CSP de produção `img-src 'self' data:`, `style-src 'self'`, `script-src 'self'`.

## Estado inicial verificado

- Testes do backend com PostgreSQL real: 23/23 pacotes ok. Frontend: 14 arquivos, 95 testes ok; typecheck e lint limpos.
- Não havia armazenamento de objetos, processamento de imagem, OAuth, personalização por loja nem preferência de aparência (o tema seguia só o sistema operacional).
- `qrcode-generator` já existia (usado no Pix, com regra de cobrança junto: não foi reaproveitado o componente, só a biblioteca).
- Sessão de conta já distinguia `customer`/`staff`; `staffActor` recusa sessão de cliente — base natural para o Google nunca abrir a gestão.

## Decisões

### Fotos (módulo novo `midia`, dono de `media_asset`)
- **Processar uma vez, servir variantes prontas.** Envio por rota própria (`uploadMedia`, bytes crus; o limite de 64 KiB das outras rotas não mudou) → staging privado → **worker** com reserva (lease) e no máximo `FILA_MEDIA_WORKERS` imagens simultâneas → variantes → `ready`. Nada de goroutine por envio nem fila em memória.
- Formato real pela assinatura (JPEG, PNG, WebP estáticos); recusa SVG, GIF, APNG, WebP animado, HEIC e desconhecidos; 5 MiB, 12 MP, 8.192 px por eixo conferidos antes de decodificar; EXIF de orientação aplicado; recodificação remove metadados (inclusive GPS); o original é apagado.
- **Saída JPEG (avatar/foto) e PNG (logo com transparência; JPEG se opaca e acima de 120 KiB).** Não há codificador WebP em Go puro mantido; usar cgo/libwebp mudaria o build e a imagem do servidor. Variantes 64/128/256 (pessoa) e 128/256/512 (logo), sem ampliar.
- **Visibilidade pela finalidade:** `avatar` é sempre privado; `professional_photo` e `logo` são públicos. Associar exige mesma pessoa, mesma finalidade e mesma loja (`Claim`). Usar o avatar como foto pública **copia** as variantes para um registro novo (`CopyAsPublic`).
- Entrega privada reautoriza a cada pedido, antes do 304, com `Cache-Control: private, no-cache, must-revalidate`. A equipe vê a foto do cliente **só pela participação ativa** (`getCustomerAvatar`: dono da loja ou profissional da fila). Entrega pública com URL versionada pelo id e `max-age=300`.
- Armazenamento: interface `Storage` com adaptador de pasta (`FileStore`, gravação atômica, chaves aleatórias validadas). **Desligado por padrão** (`FILA_MEDIA_STORAGE=disabled`); em produção exige `FILA_MEDIA_DIR` explícita (volume persistente compartilhado e no backup). Adaptador S3 fica para depois (a interface está pronta).
- Limpeza: envio não associado e imagem removida ficam 24 h; originais órfãos de staging também. Nunca toca imagem associada (atualização condicional).

### Google (OIDC) — só cliente
- Authorization Code + **PKCE S256** no backend com `go-oidc` v3 e `x/oauth2` (bibliotecas mantidas; nenhum validador JWT artesanal). Discovery/JWKS oficiais, sob demanda.
- Transação de uso único (10 min) com hashes de state, nonce e do **cookie de correlação** HttpOnly (`__Host-fila_oidc`, SameSite=Lax); verificador PKCE cifrado (AES-GCM, subchave própria). Até 5 transações por navegador (várias abas). A transação é **consumida antes** da troca do código (repetição, outro navegador, vencido → recusa).
- Redirect URI fixa (configuração), nunca do Host do pedido. Escopos `openid email profile`; tokens do Google não são guardados nem registrados.
- Chave da identidade: provedor + emissor + `sub` (`external_identity`, únicos). **Nunca vincula por e-mail igual**: colisão vira "conta_existente" (mensagem que não revela detalhes).
- O Google só é autoridade do e-mail com `email_verified` **e** Gmail ou Workspace (`hd`); senão a conta nasce `pending_verification` e recebe o link local.
- **Sessão do Google é sempre de cliente**: `account_session.auth_method='google'` e o banco recusa sessão `staff` que não seja por senha (`account_session_staff_password_ck`). A gestão exige o login de equipe por senha, que segue a sessão única do dono.
- Conta só com Google: `password_hash` NULL (sem hash falso). Login por senha recusado com a mesma resposta de senha errada; "esqueci minha senha" não cria senha (envia e-mail explicativo); troca de senha responde `no_password`.
- **Reautenticação de conta sem senha:** escolher conta no Google não prova presença recente (o Google não garante `prompt=login` nem `auth_time`). Usa **código de 8 dígitos no e-mail verificado**, preso à sessão, 10 min, 5 tentativas/15 min. Vale para exclusão de conta e vínculo/desvínculo.
- Desvincular exige reautenticação e senha existente (`last_access_method`). Convite para conta só com Google define a **primeira senha** (o convite prova o e-mail; T-AC-08); nenhum acesso de equipe vem do Google.

### Identidade da loja
- `establishment.palette` guarda só um id do catálogo de 16 (CHECK no banco); textos públicos opcionais (descrição 280, endereço 200, telefone E.164 normalizado). Auditoria `T-ES-01` registra os **nomes** dos campos alterados, nunca os valores.
- Frontend: catálogo tipado (`features/theme/paletas.ts`) → variáveis CSS `--p-light-*`/`--p-dark-*` + atributo `data-brand`; o CSS escolhe o conjunto pelo tema. Sem classes Tailwind dinâmicas. Prévia isolada (`data-brand` só no quadro). Estados, Google e Pix não são recoloridos.
- Aparência da pessoa (`system|light|dark`) no perfil + `localStorage`; `/tema.js` (arquivo externo, compatível com a CSP) aplica antes do primeiro desenho.

### QR Code e entrada
- O QR é gerado no navegador (`qrcode-generator`, correção M, margem de 4 módulos, preto no branco, sem logo no centro) a partir da **origem pública canônica** (`FILA_PUBLIC_BASE_URL`, exposta em `getPublicConfig`) e do slug. Destinos: `/loja/:slug`, `/loja/:slug?entrada=visitante`, `/entrar?loja=:slug&voltar=/loja/:slug`. Nenhuma tabela nem endpoint de criação.
- Entrar sem conta: com loja conhecida leva à identificação por apelido da loja; sem loja, pede o link/código. Cliente logado que abre o QR de visitante **não é desconectado**: vê aviso e escolhe.

## Conflitos encontrados e como foram resolvidos

| Conflito | Resolução |
|---|---|
| A tela "Dados da loja" já permitia **mudar o slug** (e a API `updateEstablishment` aceita), o que invalida QR impressos; a especificação pede não incluir troca de slug | A nova tela mostra o endereço só para leitura, com o motivo. A API foi mantida (compatibilidade, desktop e operador); redirects de slugs antigos **não** foram implementados — pendência registrada (resolvida no ADR-0029) |
| Especificação sugere WebP | Sem codificador WebP em Go puro: JPEG/PNG (decisão acima) |
| Especificação cita revisão `00bdf42` | Inexistente nesta cópia; usada `5bc96f0` |
| Tema claro usava `purple-700` como primária; o catálogo pede `#9333EA` (purple-600) | Padrão da plataforma = paleta `purple` do catálogo (contraste medido 5,38:1 com branco). No escuro, primária `#C084FC` com texto zinc-950 |
| CSP de produção não aceita `blob:` em imagens | Prévia local por data URL (sem afrouxar a CSP); download do QR por data URL |
| SSE: a especificação permite um evento de invalidação de identidade | Não foi criado evento novo: a página pública relê ao abrir; a foto do profissional reaproveita o evento da fila (`queueChanged`); a gestão relê a paleta depois de salvar (evento local da aba) |

## Consequências

- Novas variáveis: `FILA_GOOGLE_CLIENT_ID`, `FILA_GOOGLE_CLIENT_SECRET` (ou `_FILE`), `FILA_GOOGLE_REDIRECT_URL`, `FILA_GOOGLE_ISSUER` (só fora de produção), `FILA_MEDIA_STORAGE`, `FILA_MEDIA_DIR`, `FILA_MEDIA_UPLOADS_PER_MINUTE`, `FILA_MEDIA_UPLOADS_PER_DAY`, `FILA_MEDIA_WORKERS`.
- Migração aditiva `00019` (com `Down`; contas só com Google ficam bloqueadas se voltar o esquema, porque a versão antiga exige senha).
- Dependências novas: `github.com/coreos/go-oidc/v3`, `golang.org/x/oauth2`, `golang.org/x/image` (backend); `jsqr` só para teste (decodificação independente do QR).
- Pendências externas: credenciais do Google Cloud; provedor de e-mail em produção (o código de reautenticação e a confirmação de e-mail dependem dele, como antes); volume de armazenamento em produção.
