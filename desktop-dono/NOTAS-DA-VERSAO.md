# Fila Gestão 1.0.0

Primeira versão para uso real, junto com a publicação da versão 1 do Fila.

## O que é

O programa do dono da barbearia para Windows e Linux. Ele abre a gestão do site direto no acesso de dono e traz o Dashboard analítico, que só funciona num computador autorizado. Ele precisa de internet e não instala banco de dados nem servidor no computador.

## Mudanças desde a 0.2.0

- Versão 1.0.0 nos manifestos (`package.json`, `tauri.conf.json`, `Cargo.toml` e lockfiles).
- O workflow gera os checksums SHA-256 de cada instalador (`SHA256SUMS-*.txt`) e pode criar um rascunho de Release com estas notas.
- As telas, as regras e a segurança da janela são as mesmas da 0.2.0. As telas vêm do site e se atualizam a cada publicação do site.

## Como instalar

- **Windows:** execute `Fila Gestão_1.0.0_x64-setup.exe`. Se aparecer "O Windows protegeu o computador", clique em *Mais informações* e depois em *Executar assim mesmo* (o programa não tem assinatura de código).
- **Linux Mint 22 / Ubuntu 24.04:** `sudo apt install ./fila-gestao_1.0.0_amd64.deb`.
- **Conferir o arquivo:** compare o resultado de `sha256sum <arquivo>` (no Windows, `Get-FileHash <arquivo>` no PowerShell) com a linha correspondente em `SHA256SUMS-*.txt`.
- **Ativar o Dashboard:** o operador gera um código com `fila-admin desktop emitir-codigo -email <dono> -nome "PC do balcão"`. O código vale 15 minutos e serve uma única vez.

## Limitações

- Não há assinatura de código: o Windows mostra o aviso do SmartScreen.
- Não há atualização automática do programa. As telas se atualizam pelo site. Mudanças na casca ou no Dashboard exigem instalar uma versão nova.
- Notificações só chegam com o programa aberto. Ele pode estar minimizado.
- No Linux, o cofre de chaves exige o chaveiro da sessão (Secret Service).
- Cada instalador funciona com **uma** origem HTTPS, fixada ao gerar. Trocar o domínio do site exige um instalador novo.
