# Operação (etapa 12)

| Documento | Para quê |
|---|---|
| [publicacao-v1.md](publicacao-v1.md) | **V1 na nuvem** (Vercel + Render + Neon + R2 + Resend): estado, checkpoint, custos a autorizar e roteiro por provedor |
| [agenda-2.0.md](agenda-2.0.md) | **Fila 2.0** (agenda, fila e atendimento híbrido): checkpoint por parte, como testar os três modos, testes executados e pendências |
| [variaveis-v1.md](variaveis-v1.md) | Variáveis da V1 por serviço: finalidade, onde configurar e se é segredo |
| [backup-e-recuperacao-v1.md](backup-e-recuperacao-v1.md) | Backup externo cifrado, ensaio de restauração e como recuperar a produção na V1 |
| [implantacao.md](implantacao.md) | Ambientes (homologação e produção), compose, TLS, segredos, banco com menor privilégio, atualizar sem perder fila, reversão, limites e logs |
| [monitoramento.md](monitoramento.md) | Métricas, alertas (severidade, responsável e rotina) e o que falta para avisar pessoas |
| [backup-restauracao.md](backup-restauracao.md) | Backup cifrado, restauração com conciliação do Pix e do diário de exclusões, RPO/RTO medidos |
| [rotinas.md](rotinas.md) | Passo a passo: incidente, provedor indisponível, troca de segredo, falha de migração, manutenção e cada alerta |
| [evidencias.md](evidencias.md) | O que foi executado, com números, e o que não foi |

Arquivos: [`deploy/`](../../deploy/). Decisões: [ADR-0026](../adr/0026-operacao-trava-restauracao-metricas.md).
