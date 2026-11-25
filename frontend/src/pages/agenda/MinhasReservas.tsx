import { useState } from "react";
import { Link } from "react-router";
import { CalendarClock, CalendarPlus, MapPin } from "lucide-react";
import { api } from "../../api/client";
import type { components } from "../../api/schema.gen";
import { dataPorExtenso, horaNoFuso, itensTexto, situacaoReserva } from "../../features/agenda/horario";
import { SeletorHorario, type Slot } from "../../features/agenda/SeletorHorario";
import { useMinhasReservas } from "../../features/agenda/reservas";
import { PagamentoReserva } from "../../features/agenda/PagamentoReserva";
import { ContatoVisitante } from "../../features/agenda/ContatoVisitante";
import { useSession } from "../../session/context";
import { formatCents } from "../../money";
import { useStreamMessages } from "../../stream/context";
import { Badge, Button, ConfirmPanel, EmptyState, LoadError, Loading, Notice, Section } from "../../ui/ui";
import { useCommand } from "../gestao/comando";
import { CommandError } from "../gestao/comum";

type Appointment = components["schemas"]["Appointment"];

/** "Minhas reservas" dentro da loja: próximas (com Cheguei, remarcar e cancelar) e anteriores. */
export function MinhasReservas({ shopId, slug }: { shopId: string; slug: string }) {
  const up = useMinhasReservas("upcoming", shopId);
  const { state } = useSession();
  const visitante =
    state.status === "ready" &&
    state.session.account === null &&
    state.session.visitors.some((v) => v.establishment_id === shopId);
  const past = useMinhasReservas("past", shopId);
  useStreamMessages((m) => {
    if (m.type === "appointment.updated") {
      up.reload();
      past.reload();
    }
  });
  if (up.error && !up.list) return <LoadError message={up.error} onRetry={up.reload} />;
  if (!up.list) return <Loading>Carregando reservas…</Loading>;
  return (
    <div className="grid gap-6">
      {visitante && <ContatoVisitante shopId={shopId} />}
      <Section
        title="Próximos horários"
        action={
          <Link
            to={`/loja/${slug}/agendar`}
            className="inline-flex min-h-11 items-center gap-2 font-semibold"
          >
            <CalendarPlus aria-hidden="true" className="size-5" /> Agendar outro
          </Link>
        }
      >
        {up.list.length === 0 ? (
          <EmptyState icon={CalendarClock} title="Nenhum horário marcado">
            <p>Quando você agendar, a reserva aparece aqui.</p>
          </EmptyState>
        ) : (
          <ul className="m-0 grid list-none gap-3 p-0">
            {up.list.map((x) => (
              <li key={x.appointment.id}>
                <Reserva a={x.appointment} onChanged={up.reload} slug={slug} />
              </li>
            ))}
          </ul>
        )}
      </Section>
      {past.list && past.list.length > 0 && (
        <Section title="Anteriores" level={3}>
          <ul className="m-0 grid list-none gap-2 p-0">
            {past.list.map((x) => {
              const s = situacaoReserva(x.appointment);
              return (
                <li
                  key={x.appointment.id}
                  className="flex flex-wrap items-center gap-2 rounded-lg border border-line bg-surface px-3 py-2 text-sm"
                >
                  <span className="font-semibold">
                    {dataPorExtenso(x.appointment.local_date)}, {x.appointment.local_time}
                  </span>
                  <span className="text-fg-muted">{itensTexto(x.appointment)}</span>
                  <Badge tone={s.tom}>{s.texto}</Badge>
                </li>
              );
            })}
          </ul>
        </Section>
      )}
    </div>
  );
}

function Reserva({ a, onChanged, slug }: { a: Appointment; onChanged: () => void; slug?: string }) {
  const cmd = useCommand();
  const [painel, setPainel] = useState<null | "cancelar" | "remarcar">(null);
  const [novo, setNovo] = useState<Slot | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  // Instante em que o painel de cancelar abriu (a janela de cancelamento é conferida nele).
  const [agora, setAgora] = useState(0);
  const s = situacaoReserva(a);
  const path = { establishmentId: a.establishment_id, appointmentId: a.id };
  // O que acontece com o valor pago ao cancelar agora (a política aceita na reserva; o servidor confere).
  const b = a.payment.booking;
  const pago = b?.status === "paid" && b.outcome === null;
  const foraDaJanela =
    a.payment.free_cancel_until !== null && agora > new Date(a.payment.free_cancel_until).getTime();
  const retem = pago && foraDaJanela && b?.late_cancel_policy === "retain";
  const textoCancelar =
    a.status === "held"
      ? "O horário fica livre para outra pessoa e o Pix ainda não pago deixa de valer. Se você pagar depois, o valor é devolvido."
      : !pago
        ? "O horário fica livre para outra pessoa. Reserva gratuita: não há cobrança."
        : retem
          ? `Faltam menos de ${b?.cancel_window_hours ?? 0} h para o horário: pela política aceita na reserva, o valor de ${formatCents(b?.amount.amount_cents ?? 0)} fica com a barbearia.`
          : `O horário fica livre e o valor de ${formatCents(b?.amount.amount_cents ?? 0)} é devolvido pelo Pix (pode levar alguns dias para aparecer).`;
  async function sinalizar() {
    const out = await cmd.run(`cheguei:${a.id}:${a.version}`, (key) =>
      api.POST("/v1/establishments/{establishmentId}/appointments/{appointmentId}/arrival-signal", {
        params: { path, header: { "Idempotency-Key": key } },
        body: { expected_version: a.version },
      }),
    );
    if (out.ok || out.problem?.code === "version_conflict") onChanged();
  }
  async function cancelar() {
    const out = await cmd.run(`cancelar:${a.id}:${a.version}`, (key) =>
      api.POST("/v1/establishments/{establishmentId}/appointments/{appointmentId}/cancel", {
        params: { path, header: { "Idempotency-Key": key } },
        body: retem
          ? { expected_version: a.version, accept_retention: true }
          : { expected_version: a.version },
      }),
    );
    setPainel(null);
    if (out.ok || out.problem?.code === "version_conflict") onChanged();
  }
  async function remarcar() {
    if (!novo) return;
    const out = await cmd.run(`remarcar:${a.id}:${a.version}:${novo.starts_at}`, (key) =>
      api.POST("/v1/establishments/{establishmentId}/appointments/{appointmentId}/reschedule", {
        params: { path, header: { "Idempotency-Key": key } },
        body: { expected_version: a.version, starts_at: novo.starts_at },
      }),
    );
    if (out.ok) {
      setPainel(null);
      setNovo(null);
      onChanged();
    } else if (out.problem?.code === "slot_unavailable") {
      setNovo(null);
      setNotice("Esse horário acabou de ser ocupado. Escolha outro; a reserva atual continua valendo.");
    }
  }
  const items = a.items.map((i) => `${i.service_id ?? i.product_id ?? ""}:${i.quantity}`);
  return (
    <article
      className="grid gap-3 rounded-xl border border-line bg-surface p-4"
      aria-label={`Reserva ${a.local_time}`}
    >
      <div className="flex flex-wrap items-start gap-2">
        <span className="grid flex-1 gap-0.5">
          <span className="text-lg font-bold">
            {dataPorExtenso(a.local_date)}, às {a.local_time}
          </span>
          <span className="text-sm text-fg-muted">
            Com {a.professional.display_name} · {itensTexto(a)} ·{" "}
            {formatCents(b ? a.payment.due_at_store.amount_cents : a.price.amount_cents)} na barbearia
          </span>
        </span>
        <Badge tone={s.tom}>{s.texto}</Badge>
      </div>
      <PagamentoReserva a={a} onChanged={onChanged} />
      {a.status === "held" && painel === null && (
        <div>
          <Button
            variant="secondary"
            onClick={() => {
              setAgora(Date.now());
              setPainel("cancelar");
            }}
          >
            Desistir da reserva
          </Button>
        </div>
      )}
      {a.status === "called" && (
        <Notice kind="aviso" title="Você foi chamado!">
          <p>
            Vá até a cadeira de {a.professional.display_name}
            {a.deadline_at ? ` até ${horaNoFuso(a.deadline_at, a.timezone)}` : ""}.
          </p>
        </Notice>
      )}
      {(a.status === "called" || a.status === "in_service") && slug && (
        <Link to={`/loja/${slug}/visita`} className="font-semibold">
          Acompanhar o atendimento e pagar
        </Link>
      )}
      <CommandError
        problem={cmd.problem?.code === "slot_unavailable" ? null : cmd.problem}
        offline={cmd.offline}
      />
      {(a.status === "confirmed" || a.status === "called") &&
        !a.arrival_signal_at &&
        !a.arrived_at &&
        painel === null && (
          <div className="flex flex-wrap gap-2">
            <Button variant="secondary" disabled={cmd.busy !== null} onClick={() => void sinalizar()}>
              <MapPin aria-hidden="true" className="size-4" /> Cheguei
            </Button>
            {a.status === "confirmed" && (
              <>
                <Button variant="secondary" onClick={() => setPainel("remarcar")}>
                  Remarcar
                </Button>
                <Button
                  variant="secondary"
                  onClick={() => {
                    setAgora(Date.now());
                    setPainel("cancelar");
                  }}
                >
                  Cancelar
                </Button>
              </>
            )}
          </div>
        )}
      {a.arrival_signal_at && !a.arrived_at && (
        <p className="m-0 text-sm">Avisamos a barbearia que você chegou. A equipe confirma a sua presença.</p>
      )}
      {painel === "cancelar" && (
        <ConfirmPanel
          title="Cancelar esta reserva?"
          confirmLabel="Cancelar a reserva"
          cancelLabel="Manter"
          variant="danger"
          busy={cmd.busy !== null}
          onConfirm={() => void cancelar()}
          onCancel={() => setPainel(null)}
        >
          <p>{textoCancelar}</p>
        </ConfirmPanel>
      )}
      {painel === "remarcar" && (
        <div className="grid gap-3">
          <SeletorHorario
            estId={a.establishment_id}
            professionalId={a.professional.id}
            items={items}
            value={novo?.starts_at ?? null}
            initialDate={a.local_date}
            onPick={(sl) => {
              setNotice(null);
              setNovo(sl);
            }}
            notice={notice}
          />
          <div className="flex flex-wrap gap-2">
            <Button disabled={!novo || cmd.busy !== null} onClick={() => void remarcar()}>
              {novo ? `Remarcar para ${novo.local_time}` : "Escolha um horário"}
            </Button>
            <Button variant="secondary" onClick={() => setPainel(null)}>
              Voltar
            </Button>
          </div>
        </div>
      )}
      <p className="m-0 text-xs text-fg-muted">{a.terms}</p>
    </article>
  );
}
