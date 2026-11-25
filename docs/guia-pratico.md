# Fila 1.0.0 — Guia prático

Como usar cada função do Fila, passo a passo, para quem usa o sistema na barbearia: cliente, profissional, dono e quem cuida do servidor.

> **Versão em PDF:** [Guia-Pratico-Fila-1.0.0.pdf](Guia-Pratico-Fila-1.0.0.pdf), para imprimir ou mandar por mensagem. O PDF é gerado a partir deste arquivo (veja o fim deste guia).

O Fila funciona no navegador do celular, do tablet e do computador. O dono pode usar também o programa **Fila Gestão** (Windows e Linux), que abre a mesma gestão e traz o Dashboard com gráficos.

## Sumário

1. [Palavras usadas no sistema](#1-palavras-usadas-no-sistema)
2. [Primeiro dia: deixar a loja pronta](#2-primeiro-dia-deixar-a-loja-pronta)
3. [Cliente](#3-cliente)
4. [Profissional (barbeiro)](#4-profissional-barbeiro)
5. [Dono](#5-dono)
6. [Programa Fila Gestão (computador do dono)](#6-programa-fila-gestão-computador-do-dono)
7. [Notificações](#7-notificações)
8. [Operador (responsável técnico)](#8-operador-responsável-técnico)
9. [Problemas comuns](#9-problemas-comuns)
10. [O que a versão 1.0.0 não faz](#10-o-que-a-versão-100-não-faz)

---

## 1. Palavras usadas no sistema

| Palavra | O que é |
|---|---|
| **Loja** | A barbearia. Tem um endereço próprio no site (ex.: `/loja/minha-barbearia`) e um QR Code. |
| **Fila** | Cada profissional tem a sua. A fila tem **operação** (aberta, pausada, encerrada) e **entrada** (aberta ou fechada para novas pessoas). |
| **Senha** | O código da pessoa na fila (ex.: `A003`). Um pedido ocupa **uma** posição, mesmo com vários serviços. |
| **Pedido / comanda** | Os serviços e produtos escolhidos, com quantidade e preço. Vira a conta a pagar. |
| **Visitante** | Cliente sem conta. Entra só com um apelido e recebe um **código de recuperação**. |
| **Recebimento** | O pagamento registrado: dinheiro, cartão (débito ou crédito) ou Pix. |
| **Conferência** | A equipe confirma que o dinheiro entrou de fato (no Pix, depois do aviso do banco). |
| **Pendência** | Algo financeiro que precisa da atenção do dono (Pix atrasado, valor a mais, estorno…). |
| **Pontos e prêmios** | Programa de fidelidade opcional. Prêmios são cortesias, não descontos. |

---

## 2. Primeiro dia: deixar a loja pronta

Ordem recomendada para o dono, do convite ao primeiro cliente.

1. **Aceite o convite de dono.** O responsável técnico cria a loja e você recebe um e-mail. Abra o link (vale 24 horas e só uma vez), defina nome e senha.
2. **Entre como Equipe.** Em **Entrar**, escolha **Equipe da barbearia**. A gestão abre em **Filas**.
3. **Configure a loja** em **Configurações da loja → Identidade e contato**: nome, logo, cor (16 paletas), descrição, endereço, telefone e, se quiser aparecer na busca por distância, a **localização no mapa** (use a localização do aparelho estando na loja, ou digite latitude e longitude).
4. **Cadastre o catálogo** em **Catálogo**: serviços (preço, duração, pontos, quem atende), produtos opcionais (pomada, bebida) e categorias.
5. **Convide os profissionais** em **Profissionais**. Cada um recebe um e-mail de convite (vale 24 horas) e ganha a própria fila.
6. **Escolha os meios de pagamento** em **Configurações da loja → Pagamentos e custos**: dinheiro, cartão e Pix. Se quiser ver quanto as maquininhas levam, cadastre os custos de débito, crédito e Pix.
7. **Ajuste as filas** em **Configurações da loja → Filas e atendimento**: aceitar visitantes, tolerância depois da chamada (padrão 10 minutos), limite de pessoas por fila.
8. **Fidelidade (opcional)** em **Prêmios**: ligue o programa e cadastre os prêmios.
9. **Divulgue:** em **Configurações da loja → Acesso e divulgação** baixe o **QR Code** (PNG, SVG ou impressão) e cole no balcão.
10. **Abra as filas** em **Filas → Abrir operação**. Pronto: os clientes já podem entrar.

---

## 3. Cliente

### 3.1 Encontrar a barbearia

- **Pelo QR Code do balcão ou pelo link** que a loja divulgou: abre direto a página da loja.
- **Pela página inicial do site** (painel do cliente): busque pelo nome ou endereço e escolha a ordem:
  - **Mais perto**: pede a sua localização (só quando você toca). Ela é arredondada (~100 m), usada só na busca e nunca guardada.
  - **Abertas agora**: lojas com fila aberta primeiro.
  - **A–Z**.
- No mesmo painel aparecem **as filas em que você já está**, em qualquer barbearia.

A página da loja mostra serviços com preço, profissionais e a situação de cada fila (aberta, cheia, entrada encerrada).

### 3.2 Entrar sem conta (visitante)

1. Na página da loja, digite como quer ser chamado e toque em **Continuar**.
2. **Guarde o código de recuperação.** Ele aparece uma única vez. Com ele você recupera a visita em outro aparelho, em **Já tenho um código**. Você recebe um código novo e o aparelho antigo perde o acesso.
3. O dono pode desligar a entrada de visitantes. Nesse caso a página pede para entrar com conta (quem já estava na fila continua normalmente).

### 3.3 Entrar com conta

- **Criar conta:** em **Entrar → Criar conta**, com nome, e-mail e senha. Confirme pelo link que chega por e-mail.
- **Google:** em **Entrar**, **Continuar com o Google** (só para clientes).
- **Esqueci a senha:** em **Entrar → Esqueci minha senha**; o link vale 30 minutos.
- **O e-mail de confirmação não chegou:** **Reenviar confirmação de e-mail**.

Com conta você ganha pontos, vê o histórico e as filas aparecem em qualquer aparelho.

### 3.4 Escolher serviços e entrar na fila

1. Escolha o **profissional**.
2. Escolha os **serviços**: busque pelo nome (sem precisar de acento), filtre pela categoria e use **−** / **+** para a quantidade (ex.: 3 cortes para pai e filhos).
3. Se a loja oferecer, adicione **produtos** (não mudam a espera).
4. Confira o **resumo**: itens, total, duração e o aviso "Este pedido ocupa uma posição na fila".
5. Toque em **Entrar na fila**.

Se a fila estiver cheia ou com a entrada encerrada, a página avisa e não deixa entrar. Escolha outro profissional ou espere a vaga.

### 3.5 Acompanhar a vez

A tela mostra **senha**, **posição** e **espera estimada** e se atualiza sozinha, sem recarregar.

| Quero… | Como |
|---|---|
| Mudar o pedido enquanto espero | **Alterar pedido**: troque serviços, quantidades e produtos à vontade. |
| Mudar depois de chamado ou em atendimento | **Pedir alteração ao profissional**. Ele aceita ou recusa (com motivo) e você é avisado. |
| Avisar que vou atrasar | **Vou me atrasar**. A equipe é avisada e pode chamar o próximo; você continua na fila, uma posição abaixo. |
| Desistir | **Sair da fila**. |

Quando for chamado, a tela mostra **"Chegou a sua vez!"** e o prazo para chegar (a tolerância da loja, normalmente 10 minutos). Passado o prazo, o profissional pode marcar ausência.

### 3.6 Pagar

Ao terminar, a comanda mostra o valor item a item. Escolha:

- **Dinheiro** ou **cartão**: pague no balcão. Se mudar de ideia sobre o Pix, use **Trocar para dinheiro**.
- **Pix**: a tela mostra o QR Code e o código copia-e-cola. **"Pago" só aparece depois que o banco confirma e a equipe confere.**

Se a internet cair, a tela avisa ("Sem internet…"). Repetir um pagamento nunca cobra duas vezes.

### 3.7 Pontos e prêmios (com conta)

- Cada serviço vale os pontos que a loja definiu, **por unidade** (3 cortes = 3 × os pontos). Produtos não pontuam.
- Os pontos entram depois do pagamento **pago e conferido**. Você recebe o aviso "Você ganhou N pontos".
- Na aba **Pontos** da loja, toque em **Reservar** num prêmio. A reserva vale **24 horas**; retire no balcão e a equipe confirma a entrega. Reserva não retirada vence e os pontos voltam.

### 3.8 Horário marcado (lojas com agenda)

Quando a barbearia atende com horário marcado (ou "os dois"), a página da loja mostra **Agendar horário**:

1. **Com quem?** escolha o profissional.
2. Escolha os serviços e as quantidades (3 cortes para a família = 3 × a duração).
3. Escolha o dia (dias **sem vaga** aparecem riscados) e o horário. Os horários estão no fuso da loja.
4. **Revise**: quando, com quem, duração e quanto custa. Se a loja cobra a reserva, a tela mostra **quanto se paga agora pelo Pix** e **quanto fica para a barbearia**, com a regra de cancelamento, antes de pagar.
5. Sem conta: informe um apelido (e guarde o código de recuperação). **Confirmar**.

**Reserva com Pix (sinal ou taxa):** depois de confirmar, toque em **Gerar Pix**. O horário fica guardado por cerca de 18 minutos; a tela confirma sozinha quando o banco avisa (comprovante ou print não confirmam). Sem pagamento no prazo, o horário volta a ficar livre e, se o Pix for pago depois, o valor é devolvido. O **sinal** é descontado do atendimento ("Pague R$ 15 agora; na barbearia ficam R$ 45"); a **taxa de reserva** é cobrada além do atendimento.

Em **Minhas reservas**: **Cheguei** (avisa a equipe a partir da janela de chegada), **Remarcar** e **Cancelar**. Ao cancelar, a tela diz antes o que acontece com o valor pago: dentro do prazo da loja, ele é devolvido; depois, segue a regra que você aceitou ao reservar. "Devolução solicitada" ainda não é "devolvida": o Pix pode levar alguns dias.

**E-mail e outro aparelho (sem conta):** em Minhas reservas, informe um e-mail e digite o código de 6 dígitos que chegar. Você passa a receber confirmação, lembrete e chamada por e-mail e, em outro aparelho, pode abrir **Recuperar pelo e-mail** (na tela de agendar) para receber um link de acesso (vale 30 minutos, uma vez).

### 3.9 Histórico

Na aba **Histórico** da loja (com conta): visitas anteriores, itens com quantidade, valor pago e pontos.

### 3.10 Minha conta

Em **Minha conta** (menu do topo):

- trocar nome, foto e aparência (claro, escuro ou do sistema);
- ver o e-mail e como você entra (senha e/ou Google); **Trocar senha** (encerra as outras sessões);
- **Sair deste aparelho**;
- **Excluir conta**: mostra o que acontece, pede a senha de novo e dá um **protocolo** (também por e-mail). Se houver atendimento ou pagamento pendente, a exclusão fica "em processamento" até a loja resolver. Consulte a situação em **/exclusao** com o protocolo.

---

## 4. Profissional (barbeiro)

### 4.1 Começar

1. Abra o link do convite que chegou por e-mail (vale 24 horas), defina nome e senha.
2. Em **Entrar**, escolha **Equipe da barbearia**. A gestão abre em **Filas**.
3. Em **Minha conta**, no perfil profissional, coloque a foto e o nome que os clientes veem.

Você vê e mexe só na **sua** fila e nos **seus** atendimentos. No celular a navegação fica embaixo (Filas · Comandas · Mais); no computador, na lateral.

### 4.2 Atender, passo a passo

| Passo | Botão | Observação |
|---|---|---|
| 1. Chamar | **Chamar próximo** | A pessoa recebe "Chegou a sua vez!" na hora. Começa a contar a tolerância. |
| 2. Começar | **Iniciar atendimento** | Quando a pessoa sentar na cadeira. |
| 2b. Não veio | **Marcar ausência** | Aparece só depois de vencida a tolerância. |
| 3. Registrar o que fez | **Realizado** / **+1 realizado** / **Todos realizados** | Para cada serviço; com quantidade maior que 1, registre unidade por unidade ou todas de uma vez. |
| 3b. Produto | **Entregar** / **+1 entregue** / **Entregar todos** | Marque os produtos entregues. |
| 4. Terminar | **Concluir atendimento** → **Confirmar conclusão** | Pede quantas unidades foram feitas. Se fez menos que o pedido, informe o motivo. |
| 4b. Parar no meio | **Interromper atendimento** → **Confirmar interrupção** | Com motivo e o que foi feito. O dono revisa o valor. |

Sem nenhum serviço realizado, use **Interromper** em vez de concluir.

### 4.3 Mudanças no pedido

- O cliente pode **pedir alteração** depois de chamado. A comanda mostra o pedido; **aceite** ou **recuse** com motivo.
- Você mesmo pode **Alterar itens da comanda** (com motivo).

### 4.4 Organizar a fila

| Situação | O que fazer |
|---|---|
| A pessoa chamada avisou que vai atrasar | Na fila aparece o selo "Vai atrasar". Use **Descer … uma posição** (o próximo é chamado com prazo novo) ou **Subir … e chamar agora** na pessoa de baixo. |
| Trocar a ordem de quem espera | **Subir** / **Descer** uma posição por vez. Quem está em atendimento não se move. |
| Precisa sair um pouco | **Avisar indisponibilidade**: o dono é avisado e decide (pausar, transferir). Só o dono abre, pausa e encerra a operação. |
| Fila lotada | **Fechar entrada**: ninguém novo entra, ninguém sai. **Reabrir entrada** quando quiser (se o dono permitir que o profissional faça isso). |

### 4.5 Receber

Na comanda concluída, **Receber na loja**:

- **Dinheiro:** digite o valor entregue; o troco é calculado.
- **Cartão:** escolha **débito** ou **crédito** e registre a referência da maquininha.
- **Pix:** o cliente paga pelo QR. Quando o banco confirmar, **confira** o recebimento antes de liberar o cliente.

### 4.6 Prêmios

Em **Prêmios**, confirme a entrega do prêmio que o cliente reservou (ex.: **Confirmar entrega de Pomada**). Errou? **Corrigir entrega**.

---

## 5. Dono

O dono faz tudo o que o profissional faz, em **todas** as filas, e mais o que está abaixo. No computador a navegação lateral tem três grupos:

| Grupo | Itens |
|---|---|
| **Operação** | Filas, Comandas, Prêmios |
| **Cadastros** | Catálogo, Profissionais, Configurações da loja |
| **Administração** | Pendências, Exclusões, Auditoria |

> **Comece o dia por Pendências.** Lá está tudo o que precisa de decisão.

### 5.1 Filas

| Ação | Botão | Efeito |
|---|---|---|
| Começar o expediente | **Abrir operação** | A fila passa a aceitar e chamar pessoas. |
| Pausa (almoço) | **Pausar operação** / **Retomar** | Ninguém novo entra e ninguém é chamado; quem já foi chamado pode ser atendido. |
| Fim do dia | **Encerrar operação** | Só sem ninguém aguardando ou chamado (cancele com motivo antes, se preciso). Atendimento em andamento pode ser concluído. |
| Parar de aceitar gente | **Fechar entrada** / **Reabrir entrada** | Ninguém sai, a ordem não muda. Desistências não reabrem sozinhas. |
| Limitar o tamanho | Limite da fila | 1–200 pessoas, ou o padrão da loja. Ao liberar vaga, a entrada volta sozinha. |
| Mandar para outro profissional | **Transferir participação** → **Confirmar transferência** | Com a concordância do cliente. |
| Tirar alguém da fila | **Cancelar participação** → **Confirmar cancelamento** | Exige motivo; o cliente é avisado. |
| Mudar a ordem | **Subir** / **Descer** | Uma posição por vez (veja 4.4). |
| Achar alguém | Busca na fila | Por nome ou senha. |

### 5.2 Comandas

- Lista com busca e filtros (a receber, a conferir, interrompidas, fechadas…). Toque numa comanda para ver o detalhe.
- **Revisar interrupção:** decida, linha por linha, quantas unidades foram feitas por inteiro. Só isso é cobrado.
- **Corrigir valor** de uma comanda fechada: pede a sua senha de novo e fica registrado.

### 5.3 Pendências

Comandas a receber ou a conferir, interrupções a revisar, Pix em verificação e pendências financeiras (valor a mais, Pix que chegou depois, divergência, estorno). Cada item diz **o que fazer**. **Marcar como resolvida** pede uma nota; nada é apagado.

### 5.4 Estornos

Só o dono, sempre do **valor inteiro**: **Pedir estorno** → **Confirmar estorno**.

- **Pix:** a devolução é feita pela AbacatePay e confirmada automaticamente.
- **Dinheiro e cartão:** descreva como o valor foi devolvido.

### 5.5 Catálogo

Três abas, com busca, filtros, ordenação e posição de exibição:

- **Serviços:** nome, preço, duração (entra na espera estimada), pontos de fidelidade, categoria, posição e **quem atende**.
- **Produtos:** itens vendidos junto com o atendimento (não mudam a espera nem pontuam).
- **Categorias:** organizam a página da loja em "chips". Desativar uma categoria não apaga nada; os itens ficam "sem categoria".

### 5.6 Profissionais

Cadastrar, **convidar** por e-mail (convite vale 24 horas; **Reenviar convite** se expirou), renomear e desativar. Cada profissional tem a própria fila.

### 5.7 Configurações da loja

Cada seção salva separadamente. Se outra aba ou aparelho alterou a mesma seção, a tela avisa antes de sobrescrever.

| Seção | O que ajustar |
|---|---|
| **Identidade e contato** | Nome, endereço do link (acompanha o nome; endereços antigos continuam funcionando e levam ao novo), descrição, endereço, telefone, localização no mapa, logo, cor e **Acesso e divulgação** (link e QR Code). |
| **Catálogo** | Máximo de unidades do mesmo item por pedido (padrão 10) e se a loja oferece produtos. |
| **Filas e atendimento** | Aceitar visitantes (pede confirmação para desligar), tolerância depois da chamada (1–60 min, padrão 10) e limite padrão por fila. |
| **Pagamentos e custos** | Liga/desliga dinheiro, cartão e Pix (pelo menos um precisa ficar ligado). Custos de débito, crédito e Pix (% + valor fixo) e o **resumo dos últimos 30 dias**. Os custos são estimativas suas, só para os indicadores; o cliente paga o mesmo valor. |
| **Fidelidade** | Atalho para o programa de pontos. |
| **Equipe e permissões** | Se o profissional pode fechar e reabrir a entrada da própria fila (padrão: sim). |

**QR Code** (em Acesso e divulgação), três versões: abrir a loja, entrar sem conta e entrar com conta. Cada uma pode ser copiada, baixada em PNG ou SVG, ou impressa. O QR usa o endereço atual da loja.

### 5.7.1 Agenda, horários e cobrança da reserva

- **Configurações da loja → Agenda:** forma de atendimento (por ordem de chegada, com horário marcado ou os dois — a tela mostra antes quantas reservas e pessoas na fila existem), grade de horários, preparação entre clientes, antecedência, até quantos dias à frente, tolerância e janela de chegada.
- **Cobrança da reserva** (só quando a instalação libera e o Pix está ligado): gratuita, **sinal** ou **taxa**; valor; até quantas horas antes o cancelamento devolve o valor; o que fazer na falta ou cancelamento tardio (devolver é o padrão; reter depende de liberação); lembrete por e-mail. Mudanças valem para reservas novas.
- **Horários:** jornada da loja e de cada profissional, com pausas, folgas, feriados e bloqueios. Mudança que deixaria reserva de fora é recusada com a lista.
- **Agenda → Hoje / Por dia:** cada reserva mostra a situação e o pagamento ("Aguardando Pix de R$ 15", "Sinal de R$ 15 pago · na barbearia R$ 30"). **Registrar chegada**, **Chamar**, **Remarcar**, **Cancelar** (com motivo; com valor pago, a devolução é pedida) e **Reserva de balcão** (gratuita; o cliente recebe um código).
- Chamado o horário, a visita aparece em **Filas** com o selo "Horário marcado" e segue o fluxo de sempre; a comanda mostra **"Sinal pago na reserva (descontado)"**.
- **Pendências** de reserva: Pix pago depois do prazo (a devolução é automática) e **sobra do sinal** (o atendimento custou menos que o sinal): devolva pelo app do banco e clique em **Confirmar devolução** com a descrição (pede a senha de novo).
- Uma chamada que venceu enquanto o sistema estava fora do ar não vira falta sozinha: decida em Filas (iniciar, ausência ou cancelar).

### 5.8 Prêmios (fidelidade)

Ligue ou desligue o programa e cadastre os prêmios (cortesias). Os pontos de cada serviço ficam no **Catálogo**. Reservas não retiradas vencem em 24 horas e devolvem os pontos.

### 5.9 Exclusões

Pedidos de exclusão de conta de clientes que dependem da loja (atendimento em aberto, pagamento pendente) e o que falta resolver. Itens perto da meta de 30 dias ficam em destaque.

### 5.10 Auditoria

Quem fez o quê e quando: chamadas, cancelamentos, correções de valor, mudanças de configuração. Sem dados pessoais.

### 5.11 Uma sessão de dono por vez

A conta de dono fica aberta em **um** lugar por vez (navegador, programa, outro computador). Ao entrar em outro lugar, o sistema pergunta:

- **Continuar neste aparelho e encerrar a outra sessão**: a outra sessão termina na hora, mesmo que o aparelho esteja desligado.
- **Cancelar e manter a outra sessão**: nada muda.

Abas do mesmo navegador não disputam. Profissionais e clientes não são afetados.

### 5.12 Mais de uma loja

Se a sua conta for de mais de uma loja, use **Trocar de loja** no topo da gestão.

---

## 6. Programa Fila Gestão (computador do dono)

O programa abre a mesma gestão do site, já no acesso de dono, e traz o **Dashboard** com gráficos, que só funciona num computador autorizado.

### 6.1 Instalar

Baixe na página de [Releases do GitHub](https://github.com/Ruan-nascimento/Fila-/releases/latest):

- **Windows:** `Fila-Gestao_1.0.0_x64-setup.exe`. Não pede administrador. Se aparecer "O Windows protegeu o computador", clique em *Mais informações → Executar assim mesmo* (o programa não tem assinatura de código).
- **Linux Mint 22 / Ubuntu 24.04:** `sudo apt install ./Fila-Gestao_1.0.0_amd64.deb`.
- **Conferir o arquivo (opcional):** `sha256sum -c SHA256SUMS.txt --ignore-missing` (no Windows, `Get-FileHash <arquivo>` e compare com `SHA256SUMS.txt`).

### 6.2 Ativar o Dashboard

1. Peça ao operador um **código de ativação** (vale 15 minutos e uma única vez).
2. Abra o programa, entre com e-mail e senha de dono.
3. Clique em **Dashboard** no topo da navegação lateral, digite o código e clique em **Ativar**. Nas próximas vezes abre direto.

### 6.3 Usar o Dashboard

- Escolha a loja, o período e a comparação. **Limpar filtros** volta ao padrão.
- Clique numa barra, ponto ou célula para ver os registros que formaram o número. **Ver tabela** mostra os mesmos dados em tabela.
- Indicadores: atendimentos, unidades de serviço, produtos, espera, recebido e **recebido após custos** (com a cobertura dos dados; não é lucro).
- **Filas** (no topo) volta ao balcão; **Dashboard** (na gestão) volta aos gráficos. Alternar não pede senha.
- **Agendamentos:** previstos, concluídos, faltas (com a taxa e o que entra na conta), cancelados pelo cliente e pela loja, não pagos no prazo, remarcações, atraso de início, sinais recebidos e descontados, taxas, devoluções, ocupação planejada e utilização real (só nos dias com jornada) e a comparação fila × horário marcado. Clique num número ou num dia para ver as reservas; cada uma abre a Agenda no dia.

### 6.4 Segundo plano e bandeja

Menu do ícone na bandeja do sistema:

- **Abrir Fila**: mostra a janela.
- **Som dos avisos**: liga ou desliga.
- **Continuar executando em segundo plano ao fechar a janela**: ligado, o **X** só esconde a janela e os avisos continuam chegando. Começa desligado.
- **Sair do Fila**: encerra o programa de verdade.

Abrir o programa de novo traz a janela que já está aberta (mesmo escondida).

### 6.5 Computador perdido ou trocado

Peça ao operador para **revogar** aquele computador (ele deixa de receber os gráficos na hora). Para derrubar também a sessão aberta lá, entre em outro aparelho e escolha **Continuar neste aparelho**, ou troque a senha em **Minha conta**. Para usar um computador novo, peça um código novo.

---

## 7. Notificações

### 7.1 Ligar

Toque em **Ativar notificações** (convite no topo da gestão e na visita do cliente, ou no **sino**). O navegador só pergunta depois desse toque. "Agora não" não insiste mais naquele aparelho.

No **sino** você também escolhe, por aparelho: notificações ligadas, **som** e **conteúdo discreto** (a notificação mostra só "Fila / Há uma atualização").

### 7.2 O que avisa

| Para quem | Avisos |
|---|---|
| **Cliente** | Faltam N pessoas / Você é o próximo · **Chegou a sua vez!** · A barbearia chamou o próximo · Atendimento começou, terminou ou foi interrompido · Marcado como ausente · Senha cancelada (com motivo) · Transferido de fila · Pedido de mudança aceito ou recusado · Pagamento confirmado · Você ganhou N pontos |
| **Equipe** | Nova pessoa na fila · **Fulano avisou que vai atrasar** · Fulano pediu mudança na comanda · Alguém saiu da fila |

O que você mesmo fez não gera aviso.

### 7.3 Onde aparecem

- Com a página à vista: aviso no canto da tela e o número no sino.
- Página em segundo plano: notificação do sistema (se você permitiu).
- **Ver histórico** (no sino): tudo dos últimos 30 dias neste aparelho, com busca, inclusive o que foi limpo do sino. Fica só no aparelho.

### 7.4 Limites

- Não há push: os avisos chegam enquanto a página da loja ou da gestão está aberta (pode estar em segundo plano). Com o navegador fechado não chegam; ao voltar, a tela sincroniza.
- iPhone/iPad: os avisos aparecem só dentro da página.
- O navegador bloqueia som antes do primeiro toque na página.

---

## 8. Operador (responsável técnico)

Detalhes completos em [operacao/](operacao/README.md). Os comandos abaixo usam o `fila-admin` (no servidor, ou `go -C backend run ./cmd/fila-admin …` no repositório).

| Tarefa | Comando ou lugar |
|---|---|
| Criar loja e convidar o dono | `fila-admin bootstrap -nome "Barbearia" -slug barbearia -email dono@exemplo.com` |
| Trocar o dono | `fila-admin transfer-owner -slug barbearia -email novo@exemplo.com -motivo "…"` |
| Encerrar a loja | `fila-admin close-establishment -slug barbearia -motivo "…"` (sem fila nem comanda pendente) |
| Código de ativação do desktop | `fila-admin desktop emitir-codigo -email dono@exemplo.com -nome "PC do balcão"` |
| Listar / revogar computadores | `fila-admin desktop listar -email …` / `fila-admin desktop revogar -instalacao <id>` |
| Saúde geral | `fila-admin verificar` (sai com 0 quando está tudo bem) |
| Atraso do tempo real | `fila-admin eventos` |
| Problema sério | `fila-admin manutencao iniciar`: o worker para e o atendimento segue ([rotinas](operacao/rotinas.md)) |
| Publicar versão nova | GitHub Actions → **Publicar** (backup, migrações, API e worker na mesma revisão) |
| Backup | GitHub Actions → **Backup** (diário); restauração em [backup-e-recuperacao-v1.md](operacao/backup-e-recuperacao-v1.md) |
| Instaladores do desktop | GitHub Actions → **Fila Gestão (instaladores)** |
| Monitoramento | [monitoramento.md](operacao/monitoramento.md) |

**Nunca:** ligar Pix real sem o checklist ([pagamentos-abacatepay.md](pagamentos-abacatepay.md) §5), colocar segredo em arquivo do repositório, ou apagar dados para "consertar" algo. Depois de restaurar um backup, **concilie antes de liberar**.

---

## 9. Problemas comuns

| Situação | O que fazer |
|---|---|
| A tela não atualiza sozinha | Confira a internet. Recarregue a página: ela sincroniza com o servidor. |
| "Pedido não confirmado" | A página ficou desatualizada (a sessão mudou em outra aba). Recarregue. |
| E-mail de confirmação ou convite não chegou | Veja o spam. Use **Reenviar confirmação de e-mail** ou **Pedir um novo link**; o dono tem **Reenviar convite** em Profissionais. Convites valem 24 horas. |
| Perdi o celular estando na fila como visitante | Em outro aparelho, abra a loja → **Já tenho um código** e digite o código de recuperação. |
| "A fila está cheia…" | A fila chegou ao limite. Escolha outro profissional ou espere; a entrada volta sozinha quando abre vaga. |
| "Acesso de dono aberto em outro lugar" | Veja 5.11. Escolha continuar aqui ou manter a outra sessão. |
| "Sua sessão foi encerrada porque você entrou em outro aparelho" | Alguém (talvez você) entrou com a conta de dono em outro lugar. Se não foi você, troque a senha. |
| Programa diz "Este computador não está mais autorizado" | Peça um código novo ao operador e clique em **Ativar de novo com um código**. |
| Pix pago mas aparece "em verificação" | Aguarde alguns segundos: o banco confirma e a equipe confere. Se demorar, o dono vê em **Pendências**. |
| Notificação não aparece | Veja se foram ativadas no sino e liberadas no navegador. A página precisa estar aberta. |

---

## 10. O que a versão 1.0.0 não faz

- Agendamento com hora marcada, estoque, venda avulsa sem atendimento, comissão e nota fiscal.
- Dividir pagamento, parcelar ou pagar com cartão pela internet.
- Notificação push com o navegador fechado.
- Atualização automática do programa Fila Gestão (as telas se atualizam pelo site; o Dashboard e a bandeja exigem instalar a versão nova) e iniciar junto com o sistema.
- Clicar na notificação do programa não reabre a janela: use o ícone da bandeja.

---

### Sobre este guia

- Fonte: `docs/guia-pratico.md`. PDF: `docs/Guia-Pratico-Fila-1.0.0.pdf`.
- Para gerar o PDF de novo depois de editar: `python docs/tools/gerar_guia_pdf.py` (precisa de Python com o pacote `markdown` e do Google Chrome ou Chromium instalado).
- Guias detalhados: [guia-por-papel.md](guia-por-papel.md) (resumo de uma página), [configuracoes-da-loja.md](configuracoes-da-loja.md), [notificacoes.md](notificacoes.md), [desktop/guia-do-dono.md](desktop/guia-do-dono.md), [configuracoes-e-identidade.md](configuracoes-e-identidade.md).
