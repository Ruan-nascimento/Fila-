# Fila Gestão 1.1.0

Versão do programa do dono que acompanha a **Fila 1.1.0** (Fila 2.0: agenda, fila e atendimento híbrido).

## O que é

O programa do dono da barbearia para Windows e Linux. Ele abre a gestão do site direto no acesso de dono e traz o Dashboard analítico, que só funciona num computador autorizado. Ele precisa de internet e não instala banco de dados nem servidor no computador.

## Novidades da 1.1.0

- **Dashboard → Agendamentos:** previstos, concluídos, faltas (com a taxa e o que entra na conta), cancelados pelo cliente e pela loja, não pagos no prazo, remarcações, atraso de início, sinais recebidos e descontados, taxas, devoluções, ocupação planejada e utilização real (só nos dias com jornada) e a comparação fila × horário marcado, com gráfico diário.
- Clicar num número ou num dia lista as reservas; cada uma abre a **Agenda** da gestão no dia dela.
- Indicadores da fila mais corretos: a visita de uma reserva chamada não conta mais como entrada na fila nem reduz a espera média.
- Pelo site (sem precisar deste instalador): agenda, horários, reservas, reserva de balcão, avisos por e-mail, notificações de reserva no sino e a foto do dono na navegação.

## Mantido da 1.0.0

- Segundo plano opcional na bandeja (*Continuar executando em segundo plano ao fechar a janela*), *Abrir Fila*, *Som dos avisos* e *Sair do Fila*.
- Uma instância só; Dashboard com recebido após custos, unidades de serviço e produtos.

## Como instalar

- **Windows:** execute `Fila-Gestao_1.1.0_x64-setup.exe`. Se aparecer "O Windows protegeu o computador", clique em *Mais informações* e depois em *Executar assim mesmo* (o programa não tem assinatura de código).
- **Linux Mint 22 / Ubuntu 24.04:** `sudo apt install ./Fila-Gestao_1.1.0_amd64.deb` (atualiza a 1.0.0 no lugar).
- **Conferir o arquivo:** na pasta dos arquivos baixados, `sha256sum -c SHA256SUMS.txt --ignore-missing` (no Windows, `Get-FileHash <arquivo>` no PowerShell e compare com a linha correspondente de `SHA256SUMS.txt`).
- **Ativar o Dashboard:** o operador gera um código com `fila-admin desktop emitir-codigo -email <dono> -nome "PC do balcão"`. O código vale 15 minutos e serve uma única vez. Quem já ativou na 1.0.0 não precisa ativar de novo.

## Limitações

- Não há assinatura de código: o Windows mostra o aviso do SmartScreen.
- Não há atualização automática do programa. As telas se atualizam pelo site. Mudanças na casca ou no Dashboard exigem instalar uma versão nova.
- Notificações só chegam com o programa aberto (pode estar minimizado ou, com o segundo plano ligado, escondido na bandeja). Computador desligado ou suspenso não recebe avisos.
- Clicar na notificação não reabre a janela: use o ícone da bandeja ou abra o programa de novo.
- Não inicia junto com o sistema operacional.
- No Linux, o cofre de chaves exige o chaveiro da sessão (Secret Service).
- Cada instalador funciona com **uma** origem HTTPS, fixada ao gerar. Trocar o domínio do site exige um instalador novo.
