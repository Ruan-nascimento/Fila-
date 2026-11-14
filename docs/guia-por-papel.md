# Guia rápido por papel

Uma página por pessoa. Tudo funciona no celular, no tablet e no computador.

## Cliente sem conta (visitante)

1. Abra o link da loja (ou leia o QR do balcão), digite como quer ser chamado e toque em **Continuar**.
2. **Guarde o código de recuperação** que aparece uma vez. Com ele, você recupera a visita em outro aparelho em "Já estava na loja em outro aparelho?". O aparelho antigo perde o acesso.
3. Escolha o profissional e os serviços e toque em **Entrar na fila**. A tela mostra sua senha, sua posição e a espera estimada, e **atualiza sozinha**.
4. Enquanto aguarda, dá para **alterar os serviços**. Depois de chamado, você **pede** a alteração e o profissional aprova.
5. Quando for chamado, você tem **10 minutos** para chegar. Depois disso, o profissional pode marcar ausência.
6. Ao terminar, escolha como pagar:
   - **dinheiro ou cartão:** pague no balcão;
   - **Pix:** a tela mostra o QR. "Pago" só aparece depois de o banco confirmar e a equipe conferir.
7. Sem internet, a tela avisa ("Sem internet…"). Nada é cobrado duas vezes se você repetir.

## Cliente com conta

Igual ao visitante, mais:

- **Criar conta:** em "Criar conta", com confirmação por e-mail. **Depende de o envio de e-mail estar ligado.**
- **Pontos:** a loja define quantos pontos cada serviço vale. Eles entram depois de pago **e** conferido.
- **Prêmios:** em Pontos, **Reservar** guarda o prêmio por 24 h. Retire no balcão; a equipe confirma a entrega.
- **Minha conta:** histórico, trocar nome e senha, sair, **excluir a conta**. A exclusão dá um protocolo e é concluída quando não há atendimento nem pagamento pendente.

## Profissional (barbeiro)

Entre como **Equipe**: Gestão → **Filas**.

1. **Chamar próximo:** a pessoa é avisada na hora no celular dela.
2. **Iniciar atendimento** quando ela sentar. Se não vier em 10 minutos, aparece **Marcar ausência**.
3. **Concluir atendimento:** marque cada serviço como realizado. A comanda fica aguardando pagamento.
4. Se precisar parar no meio: **Interromper**, com motivo e o que foi feito. A dona revisa o valor.
5. **Receber:**
   - **Dinheiro:** "Receber na loja"; o troco é calculado.
   - **Cartão:** registre a referência.
   - **Pix:** confirme o recebimento do banco ("Conferir") antes de liberar o cliente.
6. Em **Prêmios**, confirme a entrega do prêmio reservado.

Você só vê e mexe na **sua** fila e nos **seus** atendimentos.

## Dono

Tudo do profissional, em todas as filas, mais:

| Aba | O que fazer |
|---|---|
| **Pendências** | Começar o dia aqui: comandas a receber ou a conferir, interrupções a revisar, Pix em verificação e pendências financeiras (excesso, Pix tardio, divergência, estorno), cada uma com "o que fazer". Resolver pede uma nota; nada é apagado. |
| **Comandas** | Revisar interrupções (cobra só o que foi feito por inteiro) e corrigir valor fechado (pede a senha de novo). |
| **Profissionais / Serviços / Loja** | Cadastrar e convidar profissionais (o convite vale 24 h); serviços com preço, duração e pontos; dados da loja. |
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
