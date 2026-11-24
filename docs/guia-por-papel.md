# Guia rápido por papel

Uma página por pessoa. Tudo funciona no celular, no tablet e no computador. Passo a passo completo de cada função: [guia-pratico.md](guia-pratico.md) ([PDF](Guia-Pratico-Fila-1.0.0.pdf)).

## Cliente sem conta (visitante)

1. Abra o link da loja (ou leia o QR do balcão), digite como quer ser chamado e toque em **Continuar**.
2. **Guarde o código de recuperação** que aparece uma vez. Com ele, você recupera a visita em outro aparelho em **Já tenho um código**. O aparelho antigo perde o acesso.
3. Escolha o profissional, os serviços (com quantidade, ex.: 2 cortes) e, se quiser, produtos, e toque em **Entrar na fila**. Um pedido ocupa uma posição. A tela mostra sua senha, sua posição e a espera estimada, e **atualiza sozinha**.
4. Enquanto aguarda, dá para **alterar o pedido**. Depois de chamado, você **pede** a alteração e o profissional aprova. Vai atrasar? Toque em **Vou me atrasar**.
5. Quando for chamado, você tem o prazo da loja (normalmente **10 minutos**) para chegar. Depois disso, o profissional pode marcar ausência.
6. Ao terminar, escolha como pagar:
   - **dinheiro ou cartão:** pague no balcão;
   - **Pix:** a tela mostra o QR. "Pago" só aparece depois de o banco confirmar e a equipe conferir.
7. Sem internet, a tela avisa ("Sem internet…"). Nada é cobrado duas vezes se você repetir.

## Cliente com conta

Igual ao visitante, mais:

- **Criar conta:** em "Criar conta", com confirmação por e-mail. **Depende de o envio de e-mail estar ligado.**
- **Pontos:** a loja define quantos pontos cada serviço vale (por unidade; produtos não pontuam). Eles entram depois de pago **e** conferido.
- **Prêmios:** em Pontos, **Reservar** guarda o prêmio por 24 h. Retire no balcão; a equipe confirma a entrega.
- **Painel (página inicial):** busca de barbearias por distância e as filas em que você está.
- **Minha conta:** histórico, trocar nome, foto e senha, sair, **excluir a conta**. A exclusão dá um protocolo e é concluída quando não há atendimento nem pagamento pendente.

## Profissional (barbeiro)

Entre como **Equipe**: Gestão → **Filas**.

1. **Chamar próximo:** a pessoa é avisada na hora no celular dela. Se ela avisar atraso, **Descer** uma posição chama o próximo.
2. **Iniciar atendimento** quando ela sentar. Se não vier no prazo, aparece **Marcar ausência**.
3. Registre o que fez: **Realizado**, **+1 realizado**, **Todos realizados**; produtos em **Entregar**.
4. **Concluir atendimento** (quantas unidades foram feitas). A comanda fica aguardando pagamento.
5. Se precisar parar no meio: **Interromper**, com motivo e o que foi feito. O dono revisa o valor.
6. **Receber:**
   - **Dinheiro:** "Receber na loja"; o troco é calculado.
   - **Cartão:** escolha débito ou crédito e registre a referência.
   - **Pix:** confirme o recebimento do banco ("Conferir") antes de liberar o cliente.
7. Em **Prêmios**, confirme a entrega do prêmio reservado.
8. Fila lotada? **Fechar entrada** (e depois **Reabrir entrada**), se o dono permitir.

Você só vê e mexe na **sua** fila e nos **seus** atendimentos.

## Dono

Tudo do profissional, em todas as filas, mais:

| Aba | O que fazer |
|---|---|
| **Pendências** | Começar o dia aqui: comandas a receber ou a conferir, interrupções a revisar, Pix em verificação e pendências financeiras (excesso, Pix tardio, divergência, estorno), cada uma com "o que fazer". Resolver pede uma nota; nada é apagado. |
| **Comandas** | Revisar interrupções (cobra só o que foi feito por inteiro) e corrigir valor fechado (pede a senha de novo). |
| **Filas** | Abrir, pausar e encerrar a operação; fechar/reabrir a entrada; limite por fila; transferir e cancelar com motivo; subir/descer. |
| **Catálogo** | Serviços (preço, duração, pontos, quem atende), produtos e categorias. |
| **Profissionais** | Cadastrar e convidar (o convite vale 24 h; dá para reenviar). |
| **Configurações da loja** | Identidade, logo, cor e QR Code; visitantes, tolerância e limite; meios de pagamento e custos de recebimento; permissões da equipe. |
| **Prêmios** | Ligar ou desligar o programa de pontos e cadastrar prêmios (cortesias, não descontos). |
| **Exclusões** | Pedidos de exclusão que dependem da loja (ex.: atendimento em aberto). |
| **Auditoria** | Quem fez o quê, sem dados pessoais. |

**Estornos:** só o dono, sempre do valor inteiro. O Pix é devolvido pela AbacatePay e confirmado pela consulta. Dinheiro e cartão exigem descrever como foi devolvido.

## Operador (responsável técnico)

- **Todo dia:** `fila-admin verificar -backups /backups` deve sair com 0. Leia os alertas ([operacao/monitoramento.md](operacao/monitoramento.md)).
- **Implantar:** `deploy/implantar.sh <ambiente> <versão>`. Ele faz backup, migra e troca sem derrubar a fila.
- **Problema:** [operacao/rotinas.md](operacao/rotinas.md). Na dúvida, `fila-admin manutencao iniciar`: o worker para e o atendimento segue.
- **Restaurar backup:** [operacao/backup-restauracao.md](operacao/backup-restauracao.md). Depois de restaurar, **concilie antes de liberar**.
- **Nunca:** ativar Pix real sem o checklist ([pagamentos-abacatepay.md](pagamentos-abacatepay.md) §5), colocar segredo em arquivo do repositório, ou apagar dados para "consertar" algo.
