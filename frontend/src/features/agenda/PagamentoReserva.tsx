import { useEffect, useState } from "react";
import { CircleCheck, QrCode } from "lucide-react";
import { api } from "../../api/client";
import { formatCents } from "../../money";
import { PixAberto } from "../../pages/pagamento/pix";
import { useCommand } from "../../pages/gestao/comando";
import { CommandError } from "../../pages/gestao/comum";
import { Button, Notice } from "../../ui/ui";
import { devolucaoTexto, horaNoFuso, type Appointment } from "./horario";

/** Minutos inteiros até um instante (0 depois dele). Atualiza a cada 15 s. */
function useMinutosAte(iso: string | null | undefined): number | null {
  const [agora, setAgora] = useState(() => Date.now());
  useEffect(() => {
    if (!iso) return;
    const t = setInterval(() => setAgora(Date.now()), 15_000);
    return () => clearInterval(t);
  }, [iso]);
  if (!iso) return null;
  return Math.max(0, Math.ceil((new Date(iso).getTime() - agora) / 60_000));
}

/**
 * Pagamento da reserva (ADR-0039): Pix do sinal ou da taxa enquanto o horário está retido, e
 * depois a situação (pago, descontado na comanda, mantido ou devolvido). A confirmação vem só do
 * banco (evento da reserva); comprovante ou print não confirmam nada.
 */
export function PagamentoReserva({ a, onChanged }: { a: Appointment; onChanged: () => void }) {
  const b = a.payment.booking;
  const cmd = useCommand();
  const minutos = useMinutosAte(a.status === "held" ? a.hold_expires_at : null);
  if (!b) return null;
  const valor = formatCents(b.amount.amount_cents);
  const tipo = b.mode === "deposit" ? "sinal" : "taxa de reserva";

  async function gerarPix() {
    const out = await cmd.run(`pix-reserva:${a.id}`, (key) =>
      api.POST("/v1/establishments/{establishmentId}/appointments/{appointmentId}/pix-charges", {
        params: {
          path: { establishmentId: a.establishment_id, appointmentId: a.id },
          header: { "Idempotency-Key": key },
        },
      }),
    );
    if (
      out.ok ||
      out.problem?.code === "booking_hold_expired" ||
      out.problem?.code === "booking_already_paid"
    )
      onChanged();
  }

  if (a.status === "held") {
    const aberto =
      b.charge && b.charge.br_code && (b.charge.status === "pending" || b.charge.status === "creating");
    return (
      <div className="grid gap-3 rounded-xl border border-line bg-surface p-4">
        <p className="m-0">
          <strong>Pague {valor} pelo Pix</strong> ({tipo}) para garantir o horário
          {a.hold_expires_at && <> até {horaNoFuso(a.hold_expires_at, a.timezone)}</>}
          {minutos !== null && minutos > 0 && <> — faltam {minutos} min</>}. Sem pagamento até lá, o horário é
          liberado.
        </p>
        {b.verifying ? (
          <Notice kind="info" title="Verificando pagamento">
            <p>
              Estamos confirmando o Pix com o banco. Você pode sair desta tela; o horário continua retido até
              o prazo.
            </p>
          </Notice>
        ) : aberto && b.charge ? (
          <PixAberto charge={b.charge} />
        ) : (
          <div>
            <Button
              disabled={cmd.busy !== null}
              aria-busy={cmd.busy !== null}
              onClick={() => void gerarPix()}
            >
              <QrCode aria-hidden="true" className="size-5" />{" "}
              {cmd.busy ? "Gerando Pix…" : `Gerar Pix de ${valor}`}
            </Button>
          </div>
        )}
        <CommandError problem={cmd.problem} offline={cmd.offline} />
      </div>
    );
  }

  const devolucao = devolucaoTexto(b);
  const linhas: string[] = [];
  if (b.status === "paid") {
    linhas.push(`${b.mode === "deposit" ? "Sinal" : "Taxa de reserva"} de ${valor} pago pelo Pix.`);
    if (b.outcome === null && b.mode === "deposit")
      linhas.push(
        `Na barbearia ficam ${formatCents(a.payment.due_at_store.amount_cents)} (o sinal é descontado).`,
      );
    if (b.credit_applied.amount_cents > 0)
      linhas.push(`${formatCents(b.credit_applied.amount_cents)} descontados no atendimento.`);
    if (b.outcome === "kept" && b.mode === "deposit")
      linhas.push("Pela política aceita na reserva, o valor ficou com a barbearia.");
  }
  if (b.status === "expired" && !devolucao)
    linhas.push("O Pix não foi pago no prazo e o horário foi liberado.");
  if (devolucao) linhas.push(devolucao);
  if (linhas.length === 0) return null;
  return (
    <div className="flex gap-2 rounded-lg bg-select p-3 text-sm">
      <CircleCheck aria-hidden="true" className="mt-0.5 size-4 shrink-0 text-accent" />
      <div className="grid gap-1">
        {linhas.map((l) => (
          <p key={l} className="m-0">
            {l}
          </p>
        ))}
      </div>
    </div>
  );
}
