# Fila Gestão no computador: passo a passo

Há dois papéis neste guia:

- **Operador**: quem cuida do servidor e roda o `fila-admin`.
- **Dono**: quem usa o programa na loja.

Num MVP, muitas vezes são a mesma pessoa.

## 1. Autorizar o primeiro computador

**Operador**, na pasta raiz do repositório, no servidor ou numa máquina com acesso ao banco e ao `.env` de produção:

```bash
go -C backend run ./cmd/fila-admin desktop emitir-codigo -email dono@suabarbearia.com.br -nome "PC do balcão"
```

Com o binário já instalado no servidor, use `fila-admin desktop emitir-codigo …`.

A saída mostra `código de ativação: XXXX-XXXX-XXXX-XXXX`. O código:

- vale **15 minutos** e **uma única vez**;
- substitui códigos anteriores ainda não usados.

Passe o código ao dono por um canal privado, como telefone ou mensagem direta. Não poste em grupo. O nome dado em `-nome` aparece no aviso de sessão ("aberto no programa Fila Gestão (PC do balcão)").

## 2. Instalar

Baixe a versão mais recente em [Releases](https://github.com/Ruan-nascimento/Fila-/releases/latest) (`desktop-v1.0.0`).

- **Windows:** dê dois cliques em `Fila-Gestao_1.0.0_x64-setup.exe`. Não pede administrador. Se o SmartScreen avisar, escolha *Mais informações → Executar assim mesmo*. O instalador não tem assinatura de código.
- **Linux Mint 22 / Ubuntu 24.04:** `sudo apt install ./Fila-Gestao_1.0.0_amd64.deb`.
- **Conferir o arquivo:** `sha256sum -c SHA256SUMS.txt --ignore-missing` (no Windows, `Get-FileHash <arquivo>` e compare com `SHA256SUMS.txt`).

## 3. Entrar como dono e abrir o Dashboard

1. Abra **Fila Gestão** e entre com o e-mail e a senha de dono.
2. Na gestão, clique em **Dashboard**, no topo da navegação lateral.
3. Na primeira vez, o programa pede o **código de ativação**. Digite o código e clique em **Ativar**.
   - O programa cria uma chave só deste computador e a guarda no cofre de senhas do sistema: Gerenciador de Credenciais no Windows, chaveiro da sessão no Linux.
   - Nas próximas vezes, o Dashboard abre direto.
4. Use **Filas**, no topo do Dashboard, para voltar ao balcão. Use **Dashboard** na gestão para voltar aos gráficos. É a mesma sessão: alternar não pede senha nem gera aviso.

No Dashboard:

- escolha a loja (se a conta for dona de mais de uma), o período e a comparação;
- os filtros ativos aparecem destacados, com **Limpar filtros**;
- clique numa barra, ponto ou célula para ver os registros que formaram o número;
- **Ver tabela** mostra os mesmos números do gráfico em tabela.

## 4. Só uma sessão de dono por vez

Se a conta de dono já estiver aberta em outro lugar (navegador, outro computador, janela anônima), o login mostra:

> **Acesso de dono aberto em outro lugar**: Continuar neste aparelho e encerrar a outra sessão / Cancelar e manter a outra sessão

- **Cancelar**: nada muda; a outra sessão continua.
- **Continuar**: a outra sessão termina na hora, mesmo que aquele computador esteja desligado ou perdido. Quem estava lá vê "Sua sessão foi encerrada porque você entrou em outro aparelho" e os números somem da tela.
- Se a internet cair bem na hora de confirmar, aparece **Verificar de novo**. Ele repete a mesma confirmação sem encerrar nada duas vezes.
- Abas do mesmo navegador não disputam a sessão. Profissionais e clientes não são afetados. Outro dono da mesma loja, com a própria conta, também não.

## 5. Computador perdido, roubado ou trocado

**Operador:**

```bash
go -C backend run ./cmd/fila-admin desktop listar -email dono@suabarbearia.com.br
go -C backend run ./cmd/fila-admin desktop revogar -instalacao <id da lista>
```

Depois da revogação, aquele computador não recebe mais nenhum gráfico. Para o dono derrubar também a sessão de gestão que estiver aberta lá:

- entre em outro aparelho e escolha **Continuar neste aparelho**; ou
- troque a senha em **Minha conta**: as outras sessões são encerradas.

Para **substituir** por um computador novo, emita um código novo (passo 1) e ative no computador novo. Se o programa disser "Este computador não está mais autorizado", clique em **Ativar de novo com um código**.

## 6. O que a autorização garante (e o que não garante)

- O servidor só entrega os gráficos quando há, ao mesmo tempo:
  - a sessão de dono ativa;
  - o vínculo de dono com a loja;
  - a prova de posse da chave de um computador autorizado.
- O navegador comum continua com a gestão normal, mas nunca com os gráficos.
- A garantia é sobre a **chave** e a **sessão**, não sobre o programa em si. Quem tiver controle total do computador autorizado, com a sessão do dono aberta, consegue usar a chave. Por isso, revogue computadores perdidos e não deixe o PC da loja destravado sem ninguém por perto.

## 7. Bandeja e segundo plano (1.0.0)

Menu do ícone na bandeja do sistema: **Abrir Fila**, **Som dos avisos**, **Continuar executando em segundo plano ao fechar a janela** e **Sair do Fila**.

- Com o segundo plano ligado, o X só esconde a janela e os avisos continuam chegando. Começa desligado.
- **Sair do Fila** encerra o programa de verdade.
- Abrir o programa de novo traz a janela que já está aberta, mesmo escondida.
- Clicar na notificação não reabre a janela: use o ícone da bandeja.
- O programa não inicia junto com o sistema e não se atualiza sozinho (as telas da gestão se atualizam pelo site; o Dashboard e a bandeja exigem instalar a versão nova).

Passo a passo de todas as funções: [Guia prático](../guia-pratico.md), seção 6.
