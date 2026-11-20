# Configurações, fotos, Google, identidade da loja e QR Code

Guia técnico e operacional do escopo do [ADR-0028](adr/0028-configuracoes-fotos-google-identidade-qr.md). Contrato: [contrato/openapi.yaml](contrato/openapi.yaml) (tags `midia`, `sessao`, `administracao`, `publico`). Transições: T-AC-05…08, T-SS-06, T-SS-08, T-PR-05, T-PR-06 e T-ES-01 em [matriz-transicoes.md](matriz-transicoes.md).

## 1. Onde fica cada coisa

| Tela | Rota | Quem |
|---|---|---|
| Minha conta (dados pessoais, foto, aparência, como entra, sessão, exclusão) | `/conta` | Qualquer conta |
| Meu perfil na barbearia (foto pública do profissional) | dentro de `/conta`, com sessão de equipe | Quem atende na loja |
| Configurações da barbearia (identidade, informações públicas, logo, QR, operação) | `/gestao/:estId/loja` (menu "Configurações da loja") | Dono da loja |
| Login de cliente com loja (nome, logo e cor da loja; Google; entrar sem conta) | `/entrar?loja=:slug&voltar=/loja/:slug` | Público |
| Entrada sem conta pelo QR | `/loja/:slug?entrada=visitante` | Público |

"Minha conta" não depende de loja selecionada e usa a identidade da plataforma; as configurações da loja levam a loja no caminho e o servidor confere o vínculo em cada pedido.

## 2. Fotos: quem vê o quê

| Imagem | Quem altera | Quem vê | Onde é servida |
|---|---|---|---|
| Foto pessoal (avatar) de qualquer conta | A própria pessoa | Ela mesma; a **equipe da loja** (dono ou profissional da fila) **só enquanto a participação dela está ativa** (aguardando, chamada, em atendimento) | `GET /v1/me/avatar/{size}`; `GET /v1/establishments/{e}/participations/{p}/customer-avatar/{size}` |
| Foto pública do profissional | O próprio profissional (publicação explícita) | Qualquer pessoa na página da loja | `GET /v1/public/media/{id}/{size}` |
| Logo da loja | O dono daquela loja | Qualquer pessoa | `GET /v1/public/media/{id}/{size}` |

- Fotos de cliente nunca aparecem na fila pública, em páginas de profissionais, no histórico de outras pessoas nem em eventos SSE públicos. Visitante não tem foto.
- Dono que também atende: a foto pública dele é a do perfil profissional; o avatar administrativo nunca é publicado sozinho.
- "Usar minha foto pessoal" no perfil profissional publica uma **cópia** (registro e arquivos novos) depois de aviso; remover uma não remove a outra.
- O dono pode **retirar a publicação** de uma foto profissional inadequada (motivo obrigatório, auditoria T-PR-06). Isso não mexe no avatar da conta do profissional.
- Sem foto, as telas mostram iniciais (pessoa) ou nome/ícone (loja). Nada depende de foto.

## 3. Arquitetura das imagens

```
navegador ──POST /v1/media/uploads (bytes crus, 202)──▶ API: confere sessão, finalidade, permissão,
                                                       limites, assinatura e dimensões; grava o
                                                       original em staging/ (privado) + media_asset
                                                       'processing'
worker (FILA_MEDIA_WORKERS simultâneas) ──▶ decodifica, aplica EXIF, recorta, reduz, recodifica,
                                            grava v/<id>/<aleatório>-<tam>.<ext>, apaga o original,
                                            marca 'ready'
navegador ──GET /v1/media/uploads/{id} (consulta curta)──▶ 'ready'
navegador ──PUT /v1/me/avatar | /logo | /my-professional-profile/photo (versão)──▶ 'attached'
```

- Estados: `processing → ready | failed`, `ready → attached`, `attached → released` (troca/remoção), qualquer um → `purged` (arquivos apagados).
- **Envio aceito não é perfil salvo.** A foto antiga continua até a nova ser associada; a associação exige `expected_version`, então uma troca antiga que termina depois não sobrescreve a escolha mais recente.
- Trabalho persistente: a reserva tem prazo (2 min) e no máximo 3 tentativas; se o worker cair, outro retoma. Só fica `ready` com todas as variantes gravadas.
- Limites de entrada: 5 MiB (conferido no `Content-Length` e na leitura), 12 MP e 8.192 px por eixo (conferidos no cabeçalho, antes de decodificar), formatos JPEG/PNG/WebP **estáticos** pela assinatura. Recusados: SVG, GIF, APNG, WebP animado, HEIC, desconhecidos e conteúdo que não bate com o tipo declarado.
- Variantes: pessoa 64/128/256 px (JPEG q82, quadradas, fundo branco); logo 128/256/512 px (PNG; JPEG q88 se opaca e acima de 120 KiB), inteira e sem cortar a marca. Nunca amplia.
- Recorte: quadrado normalizado (`crop_x`, `crop_y`, `crop_size`) sobre a imagem já orientada; na tela há controles deslizantes (teclado) e "Centralizar".
- Limites por conta: 5 envios/min e 20/dia (configuráveis) e até 10 envios pendentes. Não afetam login nem filas.
- O banco guarda só metadados e chaves aleatórias; nenhum nome de arquivo, e-mail ou base64.

### 3.1 Cache e remoção

| Tipo | Cabeçalhos | Observação |
|---|---|---|
| Pública | `Cache-Control: public, max-age=300`, `ETag`, `nosniff`, `CSP: default-src 'none'; sandbox` | URL muda quando a imagem muda (id no caminho); retirada some em até 5 min de cache |
| Privada | `Cache-Control: private, no-cache, must-revalidate`, `ETag` | Autorização refeita a cada pedido, antes do 304; nunca em CDN |

Remover retira a referência na hora (novos acessos negados). Os arquivos de imagens removidas e de envios nunca associados são apagados depois de **24 horas** pelo worker. Na exclusão de conta, todas as imagens da pessoa são encerradas na mesma transação da anonimização. Cópias que alguém já baixou ou capturas de tela não podem ser recolhidas.

### 3.2 Medições (máquina de desenvolvimento, Intel i3-N305, 8 núcleos)

| O quê | Resultado |
|---|---|
| Foto 4000×3000 (12 MP) JPEG com EXIF de orientação 6 e GPS → 3 variantes | 1,4 s; pico de heap ≈ 42 MiB acima da base; variantes somam ≈ 20 KiB; sem EXIF/GPS na saída |
| Mesma medida antes da otimização (girar a foto inteira) | 5,6 s; ≈ 137 MiB |
| Página da loja no celular (390 px, build de produção), logo + 1 foto profissional | 2 imagens, 2.974 bytes (meta: ≤ 200 KiB para até 8 miniaturas) |
| Deslocamento de layout causado por imagens | 0 (largura/altura fixas) |
| Deslocamento total da página da loja | ≈ 0,17–0,20 **com ou sem imagens**: vem do carregamento das filas e serviços, já existia antes deste trabalho (registrado em [bugs.md](bugs.md)) |

Imagens de teste geradas (gradiente + formas + ruído), não fotos reais; tamanhos em fotos de câmera variam. As medições não valem para outro hardware sem medir de novo.

## 4. Login com Google (só cliente)

### 4.1 Fluxo

1. A tela de cliente chama `POST /v1/auth/google/start` (`intent: login`, loja e retorno opcionais). O servidor valida a origem/CSRF, a loja (inexistente → 404, não inicia) e o retorno (lista fechada: `/conta`, `/loja/{slug}`, `/loja/{slug}/historico`, `/loja/{slug}/pontos`), cria a transação (10 min) e devolve a URL do Google.
2. O Google volta em `GET /v1/auth/google/callback`. A transação é consumida antes de tudo (mesmo navegador, dentro do prazo, uma vez só); depois o código é trocado com o verificador PKCE e o id_token é validado (assinatura, emissor, audiência, expiração, nonce, azp).
3. Resultado, sempre com redirecionamento para rota interna limpa (sem código na barra):
   - identidade conhecida → sessão **de cliente** nova (`auth_method=google`), volta ao retorno;
   - identidade nova, e-mail sem conta → conta só de cliente (ativa se Gmail/Workspace; senão pendente com link de confirmação: `?google=verificar_email`);
   - e-mail que já tem conta → `?google=conta_existente` (entre com a senha e conecte o Google em Minha conta);
   - cancelamento → `?google=cancelado`; falha → `?google=falhou`.

### 4.2 Regras

- O botão aparece só no login e no cadastro de **cliente**. Não aparece no acesso da equipe, em `/dono`, no programa desktop nem no convite. Chamar a API direto não ajuda: a sessão do Google é sempre de cliente e o banco recusa sessão de equipe que não seja por senha.
- Conectar exige sessão, reautenticação recente (senha ou código) e uma autenticação Google nova presa à mesma sessão. Identidade de outra conta → `?google=ja_vinculado`.
- Desconectar exige reautenticação e uma senha na conta (`last_access_method` caso contrário). Sessões abertas pelo Google são encerradas.
- Conta só com Google: não tem senha (nada de senha falsa). Não vê "Trocar senha"; "Esqueci minha senha" envia um e-mail explicando que ela entra com o Google; para excluir a conta ou confirmar ações sensíveis, recebe um **código de 8 dígitos** no e-mail (10 min, só nesta sessão, 5 tentativas a cada 15 min).
- Convite de equipe para conta só com Google: o aceite define a primeira senha (o convite prova o e-mail). A gestão continua exigindo login de equipe por senha e a sessão única do dono.
- Visitante ativo que entra com Google: nada é migrado (pontos, histórico, visita); a visita continua na credencial de visitante.

### 4.3 Configurar no Google Cloud (passo a passo)

1. Acesse https://console.cloud.google.com/ e crie (ou escolha) um projeto.
2. **APIs e serviços → Tela de consentimento OAuth** (ou "Google Auth Platform → Branding"): tipo **Externo**; nome do app ("Fila"), e-mail de suporte, logotipo opcional; em **Domínios autorizados** coloque o domínio do site (ex.: `exemplo.com.br`); links de política de privacidade e termos. Escopos: só `openid`, `email` e `profile` (os padrões). Enquanto estiver em "Teste", só os usuários de teste cadastrados conseguem entrar; publique o app para liberar a todos.
3. **APIs e serviços → Credenciais → Criar credenciais → ID do cliente OAuth → Aplicativo da Web.**
4. **URIs de redirecionamento autorizados** (exatos, sem barra no fim):
   - produção: `https://SEU-DOMINIO/v1/auth/google/callback`;
   - homologação: `https://SEU-DOMINIO-DE-HOMOLOGACAO/v1/auth/google/callback`;
   - desenvolvimento: `http://127.0.0.1:5173/v1/auth/google/callback` (e/ou `http://localhost:5173/...` — use sempre o mesmo endereço no navegador).
   Origens JavaScript autorizadas não são necessárias (o fluxo é todo no servidor).
5. Copie o **ID do cliente** e o **secret**. Coloque no servidor, nunca no frontend:
   - desenvolvimento: no `.env` da raiz, `FILA_GOOGLE_CLIENT_ID=...` e `FILA_GOOGLE_CLIENT_SECRET=...`;
   - produção: no gerenciador de segredos da plataforma; o secret pode vir de arquivo com `FILA_GOOGLE_CLIENT_SECRET_FILE=/run/secrets/...`.
   O redirect é `FILA_PUBLIC_BASE_URL + /v1/auth/google/callback`; só use `FILA_GOOGLE_REDIRECT_URL` se precisar de outro valor (ele precisa terminar em `/v1/auth/google/callback`).
6. Reinicie a API. `GET /v1/public/config` deve mostrar `"google_login": true` e o botão aparece no login de cliente.
7. **Validar:** entre com uma conta Google de teste pela loja (`/entrar?loja=...`): deve voltar à loja, logado como cliente; `/gestao` continua pedindo o login de equipe por senha.
8. **Trocar o secret (rotação):** no console, em Credenciais → o cliente → "Adicionar secret"; ponha o novo no servidor e reinicie; confira um login; depois desative o secret antigo no console.

| Sintoma | Causa provável e correção |
|---|---|
| Página do Google com `redirect_uri_mismatch` | A URI cadastrada não é idêntica à do servidor (http×https, porta, `localhost`×`127.0.0.1`, barra final). Corrija no console ou `FILA_PUBLIC_BASE_URL` |
| Volta com "Você cancelou no Google" | A pessoa cancelou ou o app está em "Teste" e a conta não é usuária de teste |
| Botão não aparece | `FILA_GOOGLE_CLIENT_ID`/`SECRET` ausentes (recurso desligado); veja `GET /v1/public/config` |
| "Não foi possível entrar com o Google" | Relógio do servidor errado (token "vencido"), secret trocado, ou rede bloqueando `accounts.google.com`/`oauth2.googleapis.com`. O log da API registra só a categoria (`motivo`), nunca código ou token |
| Erro de configuração na partida | Só um dos dois (ID/secret) definido, redirect com outro caminho, ou `FILA_GOOGLE_ISSUER` em produção |

`FILA_GOOGLE_ISSUER` existe só para testes fora de produção (provedor OpenID de teste); homologação e produção recusam outro emissor. Não há login Google simulado em produção.

## 5. Identidade da loja

- Campos: nome público, logo, paleta (16 opções), descrição (até 280), endereço (até 200), telefone/WhatsApp público (normalizado para E.164; brasileiro sem +55 é aceito). Textos são texto simples; nada de HTML, CSS, script, fonte ou URL de logo.
- O endereço do link (slug) acompanha o nome, e o QR Code também; o endereço antigo continua abrindo a loja e leva ao novo (QR impressos continuam valendo — [ADR-0029](adr/0029-mudar-de-lugar-na-fila-e-endereco-acompanha-o-nome.md)). O canto superior esquerdo mostra a logo e o nome da loja. Horários e agendamento não fazem parte deste escopo.
- Prévia isolada na própria tela; a loja publicada só muda depois de salvar. Conflito entre abas mostra aviso e mantém o que foi digitado. A tela avisa antes de fechar com alterações não salvas.
- A cor vale na página da loja, no login/cadastro que vem da loja e na gestão daquela loja. Login global, Minha conta e outras lojas usam a identidade padrão; ao sair ou trocar de loja os tokens são limpos.

- **Localização no mapa** (opcional, seção própria): "Usar a localização deste aparelho" (estando na barbearia) ou latitude/longitude digitadas; "Conferir no mapa" abre o OpenStreetMap; vazio remove. Com ela, a loja aparece em "Mais perto" no painel do cliente ([ADR-0031](adr/0031-painel-do-cliente-e-diretorio-de-barbearias.md)).

### 5.1 Paletas e contraste (WCAG 2.2 AA, medido em `src/features/configuracoes.test.tsx`)

Claro: botão na cor base com texto branco; destaque (links, selecionado) na cor base sobre zinc-50/branco/zinc-100. Escuro: botão no tom 400 com texto zinc-950; destaque no tom 300 sobre zinc-950/900/800. O teste confere também hover, anel de foco (≥ 3:1) e texto sobre o fundo de seleção, nas três superfícies de cada tema.

| Paleta | Claro: branco/primária | Claro: destaque/zinc-100 | Escuro: zinc-950/primária | Escuro: destaque/zinc-800 |
|---|---|---|---|---|
| `purple` (Roxo) | 5,38:1 | 4,90:1 | 7,53:1 | 8,43:1 |
| `violet` (Violeta) | 7,10:1 | 6,46:1 | 7,31:1 | 8,07:1 |
| `indigo` (Índigo) | 7,90:1 | 7,19:1 | 6,67:1 | 7,47:1 |
| `blue` (Azul) | 6,70:1 | 6,10:1 | 7,83:1 | 8,26:1 |
| `sky` (Azul-céu) | 5,93:1 | 5,40:1 | 9,29:1 | 8,93:1 |
| `cyan` (Ciano) | 5,36:1 | 4,87:1 | 11,01:1 | 10,28:1 |
| `teal` (Verde-petróleo) | 5,47:1 | 4,98:1 | 10,69:1 | 10,07:1 |
| `emerald` (Esmeralda) | 5,48:1 | 4,99:1 | 10,35:1 | 9,77:1 |
| `green` (Verde) | 5,02:1 | 4,56:1 | 11,42:1 | 10,61:1 |
| `lime` (Lima) | 4,99:1 | 4,54:1 | 13,19:1 | 11,40:1 |
| `amber` (Âmbar) | 5,02:1 | 4,57:1 | 11,92:1 | 10,33:1 |
| `orange` (Laranja) | 5,18:1 | 4,71:1 | 8,79:1 | 8,83:1 |
| `red` (Vermelho) | 6,47:1 | 5,89:1 | 7,19:1 | 7,85:1 |
| `rose` (Rosa) | 6,29:1 | 5,72:1 | 7,39:1 | 7,88:1 |
| `pink` (Pink) | 6,04:1 | 5,49:1 | 7,51:1 | 8,21:1 |
| `fuchsia` (Fúcsia) | 6,32:1 | 5,75:1 | 8,09:1 | 8,47:1 |

Margens mais apertadas: `lime`, `amber` e `green` no claro sobre zinc-100 (≈ 4,5:1). Erro, sucesso, atenção e pagamento têm tokens próprios e não mudam com a paleta; o botão do Google segue as cores do Google.

## 6. Link, QR Code e entrada

| Destino no painel | URL | O que acontece |
|---|---|---|
| Abrir a barbearia | `/loja/:slug` | Loja, profissionais, opções de entrada |
| Entrar sem conta | `/loja/:slug?entrada=visitante` | Foco no apelido; nada é criado até a pessoa confirmar e entrar na fila |
| Entrar com conta | `/entrar?loja=:slug&voltar=/loja/:slug` | Login da loja (senha, Google quando ligado, cadastro, entrar sem conta) |

- A origem vem de `FILA_PUBLIC_BASE_URL` (mostrada em `GET /v1/public/config`). **Origem errada no QR** = essa variável errada: confira o link em texto embaixo do código antes de imprimir; ele nunca deve ser `localhost`, endereço da API ou do programa desktop em produção.
- QR preto no branco, correção M, margem de 4 módulos, gerado no navegador (nenhum serviço externo). Logo e nome ficam no cartão, fora do código. Baixar PNG (≈1024 px, módulos inteiros), SVG (só o desenho do gerador) ou Imprimir (só o cartão sai na impressão).
- O link e o QR não contêm senha, e-mail, token, convite, código de recuperação nem identificador de cliente.

### 6.1 Matriz de sessão

| Situação | Resultado |
|---|---|
| Sem sessão, QR da loja | Abre a loja; oferece conta e visitante |
| Sem sessão, QR visitante | Abre a identificação por apelido; só cria visitante ao confirmar e só entra na fila ao pedir |
| Sem sessão, QR login | Login da loja; volta para a loja depois da senha, do Google ou do cadastro |
| Cliente logado, QR loja/login | Continua na loja, sem pedir login |
| Cliente logado, QR visitante | Aviso "Você está conectado com sua conta"; nada é trocado; sair é ação explícita em Minha conta |
| Visitante com visita nesta loja | Retoma a visita (aviso "Você já está nesta loja sem conta") |
| Visitante com visita em outra loja | Regra existente de visitantes por loja (uma credencial por loja); nada é apagado |
| Visitante ativo escolhe login/Google | Nada é incorporado (sem conversão de visitante em conta neste escopo) |
| Sessão de equipe/dono | Sem logout automático; a sessão de equipe não age como cliente para Google |
| Loja inexistente/encerrada | "Loja não encontrada"; o Google não inicia; nenhum visitante criado |
| Sessão expirada | Comportamento existente (aviso no topo, entrar de novo) com a loja preservada no retorno |

## 7. Configuração, ativação e volta atrás

| Variável | Padrão | Para quê |
|---|---|---|
| `FILA_MEDIA_STORAGE` | `disabled` | `filesystem` liga fotos e logos |
| `FILA_MEDIA_DIR` | `.midia` (fora de produção) | Pasta persistente, não pública, compartilhada por API e worker; **obrigatória em produção** e precisa entrar no backup |
| `FILA_MEDIA_UPLOADS_PER_MINUTE` / `_PER_DAY` | 5 / 20 | Limites por conta |
| `FILA_MEDIA_WORKERS` | 2 | Imagens processadas ao mesmo tempo pelo worker |
| `FILA_GOOGLE_CLIENT_ID` / `FILA_GOOGLE_CLIENT_SECRET` (`_FILE`) | vazio | Ambos definidos ligam o Google |
| `FILA_GOOGLE_REDIRECT_URL` | `FILA_PUBLIC_BASE_URL/v1/auth/google/callback` | Só se precisar de outro valor |
| `FILA_GOOGLE_ISSUER` | `https://accounts.google.com` | Só testes fora de produção |

**Ordem de implantação:** migrar (`00019`, aditiva) → subir API e worker (recursos desligados, nada muda para ninguém) → frontend → ligar fotos (criar o volume, `FILA_MEDIA_STORAGE=filesystem` + `FILA_MEDIA_DIR` nos dois processos) → ligar Google (credenciais). Cores, informações públicas, QR e "entrar sem conta" funcionam sem fotos e sem Google.

**Volta atrás:** remover as variáveis desliga fotos (as telas voltam às iniciais; nada é apagado) e Google (o botão some; contas criadas pelo Google continuam guardadas, mas, sem senha, só voltam a entrar quando o Google for religado). Reverter a migração (`migrate down`) bloqueia contas só com Google e apaga identidades, paletas e referências de imagem: só em último caso, com backup.

**Produção com Docker (deploy/):** monte o mesmo volume em `api` e `worker` (ex.: `/var/lib/fila/midia`) e inclua-o no backup; sem isso, deixe as fotos desligadas.

## 8. Checklist de teste manual

**Dono**
- [ ] Gestão → Configurações da loja: escolher outra cor, ver a prévia mudar sem a loja mudar; salvar; abrir a loja numa janela anônima e ver a cor, a descrição e o telefone.
- [ ] Enviar logo (PNG transparente), ver "Prévia — ainda não salva", salvar; ver a logo na loja e no cartão do QR. Remover.
- [ ] Em duas abas, salvar na primeira e depois na segunda: aparece o aviso de conflito e o texto digitado fica.
- [ ] QR: trocar o destino (link e código mudam juntos), copiar link, baixar PNG/SVG, imprimir (só o cartão). Ler o QR com o celular.
- [ ] Profissionais: retirar a foto pública de alguém (pede motivo).
- [ ] Programa desktop: abrir a gestão, salvar uma configuração permitida e continuar logado.

**Profissional**
- [ ] Minha conta → Meu perfil na barbearia: publicar foto (aviso "ficará pública"), ver no cartão; "Usar minha foto pessoal" (confirmação); despublicar.
- [ ] Ver a miniatura do cliente com conta na fila; depois de concluir/cancelar, a miniatura some.

**Cliente**
- [ ] Minha conta: trocar nome, foto (enquadrar pelo teclado), aparência (claro/escuro/sistema).
- [ ] Com Google configurado: entrar pela loja com Google e voltar à loja; conectar/desconectar em Minha conta (pede senha).
- [ ] Conta só com Google: não aparece "Trocar senha"; excluir a conta com o código do e-mail.

**Visitante**
- [ ] QR "Entrar sem conta": o apelido já vem focado; nada é criado até confirmar.
- [ ] No login global, "Entrar sem conta" pede o link/código da loja.
- [ ] Logado como cliente, abrir o QR de visitante: aparece o aviso e a conta continua.
