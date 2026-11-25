# Fila Gestão 1.0.0

Primeira versão para uso real, junto com a publicação da versão 1 do Fila.

## O que é

O programa do dono da barbearia para Windows e Linux. Ele abre a gestão do site direto no acesso de dono e traz o Dashboard analítico, que só funciona num computador autorizado. Ele precisa de internet e não instala banco de dados nem servidor no computador.

## Mudanças desde a 0.2.0

- Versão 1.0.0 nos manifestos (`package.json`, `tauri.conf.json`, `Cargo.toml` e lockfiles).
- O workflow gera os checksums SHA-256 de cada instalador e o rascunho de Release traz `SHA256SUMS.txt` com os nomes exatos dos anexos e pode criar um rascunho de Release com estas notas.
- As telas, as regras e a segurança da janela são as mesmas da 0.2.0. As telas vêm do site e se atualizam a cada publicação do site.

## Melhorias (ainda na 1.0.0)

- **Segundo plano:** no menu do ícone da bandeja, *Continuar executando em segundo plano ao fechar a janela*. Ligado, o X só esconde a janela e os avisos continuam; *Sair do Fila* encerra o programa de verdade. Começa desligado.
- Menu da bandeja: *Abrir Fila*, *Som dos avisos*, segundo plano e *Sair do Fila*.
- Uma instância só: abrir o programa de novo traz a janela que já está aberta (inclusive escondida).
- Dashboard: recebido após custos de recebimento (com a cobertura dos dados; não é lucro), unidades de serviço e produtos.

## Próxima versão (Fila 2.0 — ainda sem release)

Estas mudanças estão no código, mas **não** foram publicadas como versão nova do programa (nenhum release é criado por estas entregas):

- Dashboard: aba **Agendamentos** (previstos, concluídos, faltas e taxa de falta, cancelamentos, não pagos no prazo, remarcações, atraso de início, sinais, créditos, taxas, devoluções, ocupação planejada e utilização real, comparação fila × horário marcado e gráfico diário), com detalhamento das reservas.
- Clicar numa reserva abre a **Agenda** da gestão no dia dela (destino novo, data conferida no programa).
- A agenda da gestão em si vem do site e já aparece no programa atual sem atualizar o instalador.

## Como instalar

- **Windows:** execute `Fila-Gestao_1.0.0_x64-setup.exe`. Se aparecer "O Windows protegeu o computador", clique em *Mais informações* e depois em *Executar assim mesmo* (o programa não tem assinatura de código).
- **Linux Mint 22 / Ubuntu 24.04:** `sudo apt install ./Fila-Gestao_1.0.0_amd64.deb`.
- **Conferir o arquivo:** na pasta dos arquivos baixados, `sha256sum -c SHA256SUMS.txt --ignore-missing` (no Windows, `Get-FileHash <arquivo>` no PowerShell e compare com a linha correspondente de `SHA256SUMS.txt`).
- **Ativar o Dashboard:** o operador gera um código com `fila-admin desktop emitir-codigo -email <dono> -nome "PC do balcão"`. O código vale 15 minutos e serve uma única vez.

## Limitações

- Não há assinatura de código: o Windows mostra o aviso do SmartScreen.
- Não há atualização automática do programa. As telas se atualizam pelo site. Mudanças na casca ou no Dashboard exigem instalar uma versão nova.
- Notificações só chegam com o programa aberto (pode estar minimizado ou, com o segundo plano ligado, escondido na bandeja). Computador desligado ou suspenso não recebe avisos.
- Clicar na notificação não reabre a janela: use o ícone da bandeja ou abra o programa de novo.
- Não inicia junto com o sistema operacional.
- No Linux, o cofre de chaves exige o chaveiro da sessão (Secret Service).
- Cada instalador funciona com **uma** origem HTTPS, fixada ao gerar. Trocar o domínio do site exige um instalador novo.
