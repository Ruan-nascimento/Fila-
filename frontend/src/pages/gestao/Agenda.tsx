import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";
import { Link } from "react-router";
import { CalendarDays, CalendarPlus, Clock, Columns3, List, Scissors, UserCheck } from "lucide-react";
import { api } from "../../api/client";
import { call, problemMessage } from "../../api/problem";
import type { components } from "../../api/schema.gen";
import {
  horaNoFuso,
  itensTexto,
  rotuloDia,
  situacaoReserva,
  somarDias,
  type Appointment,
} from "../../features/agenda/horario";
import { SeletorHorario, type Slot } from "../../features/agenda/SeletorHorario";
import { formatCents } from "../../money";
import { useStreamMessages } from "../../stream/context";
import {
  Badge,
  Button,
  CopyButton,
  EmptyState,
  Field,
  LoadError,
  Loading,
  Notice,
  PageHeader,
  Section,
  SelectField,
  Submit,
} from "../../ui/ui";
import { cx } from "../../ui/cx";
import { useCommand } from "./comando";
import { CommandError, type StaffContext } from "./comum";

type AgendaView = components["schemas"]["AgendaView"];
type AgendaDay = components["schemas"]["AgendaDay"];
type StaffQueue = components["schemas"]["StaffQueue"];
type PublicService = components["schemas"]["PublicService"];

/**
 * Agenda da equipe (ADR-0038): lista cronológica por profissional com horário, nome, serviços e
 * presença; no computador, colunas por profissional (com alternativa em lista). "Hoje" junta a
 * cadeira atual, o próximo horário marcado e o próximo da fila. Nada exige arrastar.
 */
export function Agenda({ ctx, initialTab = "hoje" }: { ctx: StaffContext; initialTab?: "hoje" | "dia" }) {
  const [view, setView] = useState<AgendaView | null>(null);
  const [queues, setQueues] = useState<StaffQueue[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [date, setDate] = useState<string | null>(null);
  const [prof, setProf] = useState<string>(ctx.isOwner ? "" : (ctx.professionalId ?? ""));
  const [layout, setLayout] = useState<"colunas" | "lista">("colunas");
  const [aba, setAba] = useState<"hoje" | "dia">(initialTab);
  const [attempt, setAttempt] = useState(0);
  const reload = useCallback(() => setAttempt((n) => n + 1), []);
  useEffect(() => {
    let active = true;
    void Promise.all([
      call(() =>
        api.GET("/v1/establishments/{establishmentId}/agenda", {
          params: {
            path: { establishmentId: ctx.estId },
            query: { ...(date ? { from: date } : {}), ...(prof ? { professional_id: prof } : {}) },
          },
        }),
      ),
      call(() =>
        api.GET("/v1/establishments/{establishmentId}/queues", {
          params: { path: { establishmentId: ctx.estId } },
        }),
      ),
    ]).then(([a, q]) => {
      if (!active) return;
      if (a.ok) {
        setView(a.data.data);
        setError(null);
        if (date === null) setDate(a.data.data.today);
      } else setError(problemMessage(a.problem, a.offline));
      if (q.ok) setQueues(q.data.data);
    });
    return () => {
      active = false;
    };
  }, [ctx.estId, date, prof, attempt]);
  // Atualizações do fluxo da página: agrupa eventos em sequência e relê só o dia visível.
  const timer = useRef<number | null>(null);
  useStreamMessages((m) => {
    if (m.type !== "appointment.updated" && m.type !== "queue.updated" && m.type !== "stream.snapshot")
      return;
    if (timer.current) window.clearTimeout(timer.current);
    timer.current = window.setTimeout(reload, 400);
  });
  useEffect(
    () => () => {
      if (timer.current) window.clearTimeout(timer.current);
    },
    [],
  );

  if (error && !view) return <LoadError message={error} onRetry={reload} />;
  if (!view || !date) return <Loading>Carregando agenda…</Loading>;
  const days = view.days.filter((d) => d.date === date);
  const allProfs = view.days.filter(
    (d, i, arr) => arr.findIndex((x) => x.professional_id === d.professional_id) === i,
  );
  return (
    <section aria-labelledby="titulo-agenda" className="grid gap-5">
      <PageHeader
        id="titulo-agenda"
        title="Agenda"
        description={
          view.mode === "queue"
            ? "A loja atende por ordem de chegada. Reservas antigas continuam aparecendo aqui até serem atendidas."
            : "Horários marcados por profissional. Datas e horas no fuso da loja."
        }
        actions={
          ctx.isOwner && view.mode !== "queue" ? (
            <Link
              to={`/gestao/${ctx.estId}/horarios`}
              className="inline-flex min-h-11 items-center gap-2 font-semibold"
            >
              <Clock aria-hidden="true" className="size-5" /> Horários e folgas
            </Link>
          ) : undefined
        }
      />
      <div role="tablist" aria-label="Visão" className="flex gap-1 border-b border-line">
        {(["hoje", "dia"] as const).map((t) => (
          <button
            key={t}
            role="tab"
            type="button"
            aria-selected={aba === t}
            onClick={() => {
              setAba(t);
              if (t === "hoje") setDate(view.today);
            }}
            className={cx(
              "-mb-px min-h-11 border-b-2 px-3 text-sm font-semibold",
              aba === t ? "border-primary text-accent" : "border-transparent text-fg-muted hover:text-fg",
            )}
          >
            {t === "hoje" ? "Hoje" : "Por dia"}
          </button>
        ))}
      </div>
      {aba === "hoje" ? (
        <Hoje
          ctx={ctx}
          days={view.days.filter((d) => d.date === view.today)}
          queues={queues}
          timezone={view.timezone}
          onChanged={reload}
        />
      ) : (
        <>
          <div className="flex flex-wrap items-end gap-3">
            <div role="group" aria-label="Dia" className="-mx-1 flex gap-1 overflow-x-auto px-1">
              {Array.from({ length: 7 }, (_, i) => somarDias(view.today, i)).map((d) => (
                <button
                  key={d}
                  type="button"
                  aria-pressed={d === date}
                  onClick={() => setDate(d)}
                  className={cx(
                    "min-h-11 shrink-0 rounded-lg border px-3 text-sm font-semibold",
                    d === date
                      ? "border-primary bg-select text-accent"
                      : "border-line bg-surface hover:bg-surface-2",
                  )}
                >
                  {rotuloDia(d, view.today)}
                </button>
              ))}
            </div>
            <Field
              label="Outra data"
              type="date"
              value={date}
              onChange={(e) => e.target.value && setDate(e.target.value)}
              wrapperClassName="w-44"
            />
            {ctx.isOwner && (
              <SelectField
                label="Profissional"
                value={prof}
                onChange={(e) => setProf(e.target.value)}
                wrapperClassName="w-52"
              >
                <option value="">Todos</option>
                {allProfs.map((d) => (
                  <option key={d.professional_id} value={d.professional_id}>
                    {d.display_name}
                  </option>
                ))}
              </SelectField>
            )}
            <div className="hidden gap-1 lg:flex" role="group" aria-label="Formato">
              <Button
                variant={layout === "colunas" ? "primary" : "secondary"}
                aria-pressed={layout === "colunas"}
                onClick={() => setLayout("colunas")}
              >
                <Columns3 aria-hidden="true" className="size-4" /> Colunas
              </Button>
              <Button
                variant={layout === "lista" ? "primary" : "secondary"}
                aria-pressed={layout === "lista"}
                onClick={() => setLayout("lista")}
              >
                <List aria-hidden="true" className="size-4" /> Lista
              </Button>
            </div>
          </div>
          {days.length === 0 ? (
            <EmptyState icon={CalendarDays} title="Nenhum profissional ativo">
              <p>Cadastre profissionais para usar a agenda.</p>
            </EmptyState>
          ) : (
            <div
              className={cx(
                "grid gap-4",
                layout === "colunas" &&
                  days.length > 1 &&
                  "lg:grid-cols-[repeat(auto-fit,minmax(18rem,1fr))]",
              )}
            >
              {days.map((d) => (
                <ColunaProfissional
                  key={d.professional_id}
                  ctx={ctx}
                  day={d}
                  timezone={view.timezone}
                  onChanged={reload}
                />
              ))}
            </div>
          )}
          {ctx.isOwner && view.mode !== "queue" && (
            <Balcao ctx={ctx} profs={allProfs} today={view.today} onDone={reload} />
          )}
        </>
      )}
    </section>
  );
}

function turnosTexto(d: AgendaDay, tz: string): string {
  if (d.shifts.length === 0) return "Sem turno neste dia";
  return d.shifts.map((s) => `${horaNoFuso(s.starts_at, tz)}–${horaNoFuso(s.ends_at, tz)}`).join(" · ");
}

function ColunaProfissional({
  ctx,
  day,
  timezone,
  onChanged,
}: {
  ctx: StaffContext;
  day: AgendaDay;
  timezone: string;
  onChanged: () => void;
}) {
  return (
    <Section
      level={3}
      title={day.display_name}
      description={turnosTexto(day, timezone)}
      className="content-start"
    >
      {day.blocks.length > 0 && (
        <ul className="m-0 grid list-none gap-1 p-0 text-sm">
          {day.blocks.map((b) => (
            <li key={b.id}>
              <Badge tone="neutral">
                {b.start_minute == null
                  ? "Folga o dia inteiro"
                  : `Bloqueado ${String(Math.floor(b.start_minute / 60)).padStart(2, "0")}:${String(b.start_minute % 60).padStart(2, "0")}`}
              </Badge>
            </li>
          ))}
        </ul>
      )}
      {day.appointments.length === 0 ? (
        <p className="m-0 rounded-xl border border-dashed border-line p-4 text-sm text-fg-muted">
          Nenhum horário marcado.
        </p>
      ) : (
        <ol className="m-0 grid list-none gap-2 p-0">
          {day.appointments.map((a) => (
            <li key={a.id}>
              <CartaoReserva ctx={ctx} a={a} timezone={timezone} onChanged={onChanged} />
            </li>
          ))}
        </ol>
      )}
    </Section>
  );
}

/** Cartão de uma reserva com as ações possíveis agora (o servidor decide; aqui só esconde o que não se aplica). */
export function CartaoReserva({
  ctx,
  a,
  timezone,
  onChanged,
  compact,
}: {
  ctx: StaffContext;
  a: Appointment;
  timezone: string;
  onChanged: () => void;
  compact?: boolean;
}) {
  const cmd = useCommand();
  const [painel, setPainel] = useState<null | "cancelar" | "remarcar">(null);
  const [reason, setReason] = useState("");
  const [novo, setNovo] = useState<Slot | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const sit = situacaoReserva(a);
  const est = ctx.estId;
  async function act(op: "arrival" | "call") {
    const opts = (key: string) => ({
      params: { path: { establishmentId: est, appointmentId: a.id }, header: { "Idempotency-Key": key } },
      body: { expected_version: a.version },
    });
    const out = await cmd.run(`${op}:${a.id}:${a.version}`, (key) =>
      op === "arrival"
        ? api.POST("/v1/establishments/{establishmentId}/appointments/{appointmentId}/arrival", opts(key))
        : api.POST("/v1/establishments/{establishmentId}/appointments/{appointmentId}/call", opts(key)),
    );
    if (out.ok || out.problem?.code === "version_conflict") onChanged();
  }
  async function cancelar(e: FormEvent) {
    e.preventDefault();
    const out = await cmd.run(`cancelar:${a.id}:${a.version}:${reason}`, (key) =>
      api.POST("/v1/establishments/{establishmentId}/appointments/{appointmentId}/cancel", {
        params: { path: { establishmentId: est, appointmentId: a.id }, header: { "Idempotency-Key": key } },
        body: { expected_version: a.version, reason },
      }),
    );
    if (out.ok) {
      setPainel(null);
      onChanged();
    }
  }
  async function remarcar(e: FormEvent) {
    e.preventDefault();
    if (!novo) return;
    const out = await cmd.run(`remarcar:${a.id}:${a.version}:${novo.starts_at}:${reason}`, (key) =>
      api.POST("/v1/establishments/{establishmentId}/appointments/{appointmentId}/reschedule", {
        params: { path: { establishmentId: est, appointmentId: a.id }, header: { "Idempotency-Key": key } },
        body: { expected_version: a.version, starts_at: novo.starts_at, reason },
      }),
    );
    if (out.ok) {
      setPainel(null);
      setNovo(null);
      onChanged();
    } else if (out.problem?.code === "slot_unavailable") {
      setNovo(null);
      setNotice("Esse horário acabou de ser ocupado. Escolha outro; a reserva atual continua como estava.");
    }
  }
  const items = a.items.map((i) => `${i.service_id ?? i.product_id ?? ""}:${i.quantity}`);
  const ativa = a.status === "confirmed";
  return (
    <article
      aria-label={`${a.local_time} ${a.nick ?? ""}`}
      className={cx(
        "grid gap-2 rounded-xl border bg-surface p-3",
        a.store_late ? "border-warning/60" : "border-line",
      )}
    >
      <div className="flex flex-wrap items-start gap-2">
        <span className="text-lg font-bold tabular-nums">{a.local_time}</span>
        <span className="grid min-w-0 flex-1">
          <span className="truncate font-semibold">{a.nick}</span>
          <span className="text-sm text-fg-muted">
            <Scissors aria-hidden="true" className="mr-1 inline size-3.5" />
            {itensTexto(a)} · {formatCents(a.price.amount_cents)} · até {horaNoFuso(a.ends_at, timezone)}
          </span>
        </span>
        <Badge tone={sit.tom}>{sit.texto}</Badge>
      </div>
      {a.origin === "counter" && !compact && <p className="m-0 text-xs text-fg-muted">Reserva de balcão</p>}
      <CommandError
        problem={cmd.problem?.code === "slot_unavailable" ? null : cmd.problem}
        offline={cmd.offline}
      />
      {ativa && painel === null && (
        <div className="flex flex-wrap gap-2">
          {!a.arrived_at && (
            <Button variant="secondary" disabled={cmd.busy !== null} onClick={() => void act("arrival")}>
              <UserCheck aria-hidden="true" className="size-4" /> Registrar chegada
            </Button>
          )}
          <Button disabled={cmd.busy !== null} onClick={() => void act("call")}>
            Chamar
          </Button>
          <Button variant="secondary" onClick={() => setPainel("remarcar")}>
            Remarcar
          </Button>
          <Button variant="secondary" onClick={() => setPainel("cancelar")}>
            Cancelar
          </Button>
        </div>
      )}
      {(a.status === "called" || a.status === "in_service") && (
        <p className="m-0 text-sm">
          A visita está em <Link to={`/gestao/${est}/filas`}>Filas</Link>
          {a.deadline_at && a.status === "called"
            ? ` · prazo de chegada até ${horaNoFuso(a.deadline_at, timezone)}`
            : ""}
          .
        </p>
      )}
      {painel === "cancelar" && (
        <form onSubmit={(e) => void cancelar(e)} className="grid gap-2">
          <Field
            label="Motivo do cancelamento"
            value={reason}
            onChange={(e) => setReason(e.target.value)}
            hint="Obrigatório para a equipe (3 a 500 caracteres)."
          />
          <div className="flex gap-2">
            <Submit busy={cmd.busy !== null}>Cancelar a reserva</Submit>
            <Button variant="secondary" onClick={() => setPainel(null)}>
              Voltar
            </Button>
          </div>
        </form>
      )}
      {painel === "remarcar" && (
        <form onSubmit={(e) => void remarcar(e)} className="grid gap-3">
          <SeletorHorario
            estId={est}
            professionalId={a.professional.id}
            items={items}
            value={novo?.starts_at ?? null}
            onPick={(s) => {
              setNotice(null);
              setNovo(s);
            }}
            initialDate={a.local_date}
            notice={notice}
          />
          <Field
            label="Motivo"
            value={reason}
            onChange={(e) => setReason(e.target.value)}
            hint="Obrigatório para a equipe; o cliente recebe a atualização na página da reserva."
          />
          <div className="flex gap-2">
            <Button type="submit" disabled={!novo || cmd.busy !== null} aria-busy={cmd.busy !== null}>
              {novo ? `Remarcar para ${novo.local_time}` : "Escolha um horário"}
            </Button>
            <Button variant="secondary" onClick={() => setPainel(null)}>
              Voltar
            </Button>
          </div>
        </form>
      )}
    </article>
  );
}

/** Hoje: cadeira atual, próximo horário marcado e próximo da fila, por profissional. */
function Hoje({
  ctx,
  days,
  queues,
  timezone,
  onChanged,
}: {
  ctx: StaffContext;
  days: AgendaDay[];
  queues: StaffQueue[];
  timezone: string;
  onChanged: () => void;
}) {
  if (days.length === 0) return <p className="m-0 text-fg-muted">Nenhum profissional ativo.</p>;
  return (
    <div className="grid gap-4 lg:grid-cols-[repeat(auto-fit,minmax(18rem,1fr))]">
      {days.map((d) => {
        const q = queues.find((x) => x.professional.id === d.professional_id);
        const chair = q?.entries.find((e) => e.status === "in_service" || e.status === "called");
        const nextFifo = q?.entries.find((e) => e.status === "waiting");
        const proximas = d.appointments.filter((a) => a.status === "confirmed");
        return (
          <Section
            key={d.professional_id}
            level={3}
            title={d.display_name}
            description={turnosTexto(d, timezone)}
            className="content-start"
          >
            <dl className="m-0 grid gap-2 text-sm">
              <div className="rounded-lg bg-surface-2 p-3">
                <dt className="font-semibold">Cadeira agora</dt>
                <dd className="m-0">
                  {chair
                    ? `${chair.ticket_code} ${chair.nick} · ${chair.status === "in_service" ? "em atendimento" : "chamado"}${chair.origin === "appointment" ? " · horário marcado" : ""}`
                    : "Livre"}
                </dd>
              </div>
              <div className="rounded-lg bg-surface-2 p-3">
                <dt className="font-semibold">Próximo da fila</dt>
                <dd className="m-0">
                  {nextFifo
                    ? `${nextFifo.ticket_code} ${nextFifo.nick} · ${nextFifo.service_names.join(", ")}`
                    : "Ninguém aguardando"}
                </dd>
              </div>
            </dl>
            {proximas.length === 0 ? (
              <p className="m-0 text-sm text-fg-muted">Sem horários marcados pendentes hoje.</p>
            ) : (
              <ol className="m-0 grid list-none gap-2 p-0">
                {proximas.map((a) => (
                  <li key={a.id}>
                    <CartaoReserva ctx={ctx} a={a} timezone={timezone} onChanged={onChanged} compact />
                  </li>
                ))}
              </ol>
            )}
            {q && (
              <Link to={`/gestao/${ctx.estId}/filas/${q.queue_id}`} className="text-sm font-semibold">
                Abrir a fila de {d.display_name}
              </Link>
            )}
          </Section>
        );
      })}
    </div>
  );
}

/** Reserva de balcão (dono): o cliente recebe a própria identidade recuperável (código de uma vez). */
function Balcao({
  ctx,
  profs,
  today,
  onDone,
}: {
  ctx: StaffContext;
  profs: AgendaDay[];
  today: string;
  onDone: () => void;
}) {
  const [open, setOpen] = useState(false);
  const [services, setServices] = useState<PublicService[]>([]);
  const [nick, setNick] = useState("");
  const [prof, setProf] = useState(profs[0]?.professional_id ?? "");
  const [svc, setSvc] = useState<Record<string, number>>({});
  const [slot, setSlot] = useState<Slot | null>(null);
  const [created, setCreated] = useState<{ code: string; until: string; when: string } | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const cmd = useCommand();
  useEffect(() => {
    if (!open) return;
    void call(() =>
      api.GET("/v1/public/establishments/{establishmentId}/services", {
        params: { path: { establishmentId: ctx.estId } },
      }),
    ).then((out) => {
      if (out.ok) setServices(out.data.data);
    });
  }, [open, ctx.estId]);
  const items = Object.entries(svc)
    .filter(([, n]) => n > 0)
    .map(([id, n]) => `${id}:${n}`);
  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    if (!slot) return;
    const body = {
      professional_id: prof,
      starts_at: slot.starts_at,
      items: Object.entries(svc)
        .filter(([, n]) => n > 0)
        .map(([id, n]) => ({ service_id: id, quantity: n })),
      counter_customer: { nick },
    };
    const out = await cmd.run(`balcao:${JSON.stringify(body)}`, (key) =>
      api.POST("/v1/establishments/{establishmentId}/appointments", {
        params: { path: { establishmentId: ctx.estId }, header: { "Idempotency-Key": key } },
        body,
      }),
    );
    if (out.ok) {
      const d = out.data.data;
      setCreated({
        code: d.counter_recovery_code ?? "",
        until: d.counter_recovery_code_valid_until ?? "",
        when: `${rotuloDia(d.local_date, today)} às ${d.local_time}`,
      });
      setSlot(null);
      setSvc({});
      setNick("");
      onDone();
    } else if (out.problem?.code === "slot_unavailable") {
      setSlot(null);
      setNotice("Esse horário acabou de ser ocupado. Escolha outro; os serviços continuam escolhidos.");
    }
  }
  if (!open)
    return (
      <div>
        <Button variant="secondary" onClick={() => setOpen(true)}>
          <CalendarPlus aria-hidden="true" className="size-4" /> Reserva de balcão
        </Button>
      </div>
    );
  return (
    <Section
      title="Reserva de balcão"
      description="Para quem marca pessoalmente ou por telefone. O cliente recebe um código para acompanhar a reserva."
    >
      {created && (
        <Notice kind="ok" title={`Reserva confirmada: ${created.when}`}>
          <p>
            Entregue este código ao cliente (ele aparece só agora). Na página da loja, “Já tenho um código”
            abre a reserva:
          </p>
          <p className="flex flex-wrap items-center gap-2 font-mono text-lg font-bold">
            {created.code} <CopyButton text={created.code} />
          </p>
          <p>Sem aviso por e-mail: combine o horário com o cliente.</p>
        </Notice>
      )}
      <form onSubmit={(e) => void onSubmit(e)} className="grid gap-3">
        <CommandError
          problem={cmd.problem?.code === "slot_unavailable" ? null : cmd.problem}
          offline={cmd.offline}
        />
        <div className="flex flex-wrap gap-3">
          <Field
            label="Nome do cliente"
            value={nick}
            onChange={(e) => setNick(e.target.value)}
            hint="Como a equipe vai chamar (até 30 caracteres)."
            wrapperClassName="w-64"
          />
          <SelectField
            label="Profissional"
            value={prof}
            onChange={(e) => setProf(e.target.value)}
            wrapperClassName="w-52"
          >
            {profs.map((p) => (
              <option key={p.professional_id} value={p.professional_id}>
                {p.display_name}
              </option>
            ))}
          </SelectField>
        </div>
        <fieldset className="m-0 grid gap-2 border-0 p-0">
          <legend className="mb-1 text-sm font-semibold">Serviços</legend>
          {services
            .filter((s) => s.professional_ids.includes(prof))
            .map((s) => (
              <label key={s.id} className="flex items-center gap-3 text-sm">
                <input
                  type="number"
                  min={0}
                  max={10}
                  value={svc[s.id] ?? 0}
                  onChange={(e) => {
                    setSlot(null);
                    setSvc({ ...svc, [s.id]: Math.max(0, Math.min(10, Number(e.target.value) || 0)) });
                  }}
                  className="w-16 rounded-lg border border-line bg-surface px-2 py-1"
                  aria-label={`Quantidade de ${s.name}`}
                />
                {s.name} · {formatCents(s.price.amount_cents)} · {s.duration_minutes} min
              </label>
            ))}
        </fieldset>
        {items.length > 0 && prof && (
          <SeletorHorario
            key={`${prof}|${items.join(",")}`}
            estId={ctx.estId}
            professionalId={prof}
            items={items}
            value={slot?.starts_at ?? null}
            onPick={(s) => {
              setNotice(null);
              setSlot(s);
            }}
            lastDateFor={(a) =>
              somarDias(a.first_date, 29) < a.last_date ? somarDias(a.first_date, 29) : a.last_date
            }
            notice={notice}
          />
        )}
        <div className="flex gap-2">
          <Button
            type="submit"
            disabled={!slot || nick.trim() === "" || cmd.busy !== null}
            aria-busy={cmd.busy !== null}
          >
            {slot ? `Confirmar às ${slot.local_time}` : "Escolha serviço e horário"}
          </Button>
          <Button variant="secondary" onClick={() => setOpen(false)}>
            Fechar
          </Button>
        </div>
      </form>
      <p className="m-0 text-xs text-fg-muted">
        A reserva é gratuita; o cliente paga na barbearia o que for feito.
      </p>
    </Section>
  );
}
