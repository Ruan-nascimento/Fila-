import { useCallback, useEffect, useMemo, useState, type FormEvent } from "react";
import { CalendarOff, Copy, Plus, Trash2 } from "lucide-react";
import { api } from "../../api/client";
import { call, problemMessage, type Problem } from "../../api/problem";
import type { components } from "../../api/schema.gen";
import {
  dataNoFuso,
  diasDaSemana,
  horaNoFuso,
  horaParaMinutos,
  minutosParaHora,
  modalidadeTexto,
  rotuloDia,
  somarDias,
  type Modality,
  type WeeklyShift,
} from "../../features/agenda/horario";
import {
  Button,
  Choice,
  ConfirmPanel,
  Field,
  LoadError,
  Loading,
  Notice,
  PageHeader,
  Section,
  SelectField,
  Submit,
} from "../../ui/ui";
import { useUnsavedWarning } from "../../ui/useUnsavedWarning";
import { useCommand } from "./comando";
import { CommandError, type StaffContext } from "./comum";

type Hours = components["schemas"]["AgendaHours"];
type Subject = components["schemas"]["SubjectHours"];
type Exception = components["schemas"]["AgendaException"];

type Linha = { weekday: number; start: string; end: string; modality: Modality };

function linhasDe(shifts: WeeklyShift[]): Linha[] {
  return shifts.map((s) => ({
    weekday: s.weekday,
    start: minutosParaHora(s.start_minute),
    end: minutosParaHora(s.end_minute),
    modality: s.modality,
  }));
}

/** Confere os turnos no aparelho (o servidor confere de novo): formato, fim depois do início, sobreposição. */
function validar(linhas: Linha[]): { shifts: WeeklyShift[]; erros: Record<number, string> } {
  const erros: Record<number, string> = {};
  const shifts: WeeklyShift[] = [];
  linhas.forEach((l, i) => {
    const a = horaParaMinutos(l.start);
    const b = horaParaMinutos(l.end);
    if (a === null || b === null) erros[i] = "Use HH:MM (ex.: 09:00).";
    else if (a >= b)
      erros[i] =
        "O fim precisa ser depois do início. Turno que passa da meia-noite vira dois: até 24:00 e, no dia seguinte, a partir de 00:00.";
    else if (a > 1439) erros[i] = "Início até 23:59.";
    else {
      const sobre = shifts.findIndex(
        (s) => s.weekday === l.weekday && a < s.end_minute && s.start_minute < b,
      );
      if (sobre >= 0) erros[i] = "Turnos sobrepostos no mesmo dia.";
      shifts.push({ weekday: l.weekday, start_minute: a, end_minute: b, modality: l.modality });
    }
  });
  return { shifts, erros };
}

/** "09:00–12:00 · pausa 12:00–13:00 · 13:00–18:00" (prévia legível do dia). */
function resumoDia(linhas: Linha[], weekday: number): string {
  const dia = linhas
    .filter(
      (l) => l.weekday === weekday && horaParaMinutos(l.start) !== null && horaParaMinutos(l.end) !== null,
    )
    .sort((a, b) => (horaParaMinutos(a.start) ?? 0) - (horaParaMinutos(b.start) ?? 0));
  if (dia.length === 0) return "Fechado";
  const partes: string[] = [];
  dia.forEach((l, i) => {
    const prev = dia[i - 1];
    if (prev && prev.end !== l.start) partes.push(`pausa ${prev.end}–${l.start}`);
    partes.push(`${l.start}–${l.end}`);
  });
  return partes.join(" · ");
}

/**
 * Horários (ADR-0038): jornada semanal da loja e de cada profissional, com data de vigência, e as
 * exceções com data (folga, feriado, bloqueio). O dono edita; o profissional vê a própria e, se o
 * dono liberou, bloqueia trechos da própria agenda.
 */
export function Horarios({ ctx, mode }: { ctx: StaffContext; mode: components["schemas"]["ServiceMode"] }) {
  const [hours, setHours] = useState<Hours | null>(null);
  const [exc, setExc] = useState<Exception[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [attempt, setAttempt] = useState(0);
  const reload = useCallback(() => setAttempt((n) => n + 1), []);
  useEffect(() => {
    let active = true;
    void Promise.all([
      call(() =>
        api.GET("/v1/establishments/{establishmentId}/agenda/hours", {
          params: { path: { establishmentId: ctx.estId } },
        }),
      ),
      call(() =>
        api.GET("/v1/establishments/{establishmentId}/agenda/exceptions", {
          params: { path: { establishmentId: ctx.estId }, query: {} },
        }),
      ),
    ]).then(([h, e]) => {
      if (!active) return;
      if (h.ok) {
        setHours(h.data.data);
        setError(null);
      } else setError(problemMessage(h.problem, h.offline));
      if (e.ok) setExc(e.data.data);
    });
    return () => {
      active = false;
    };
  }, [ctx.estId, attempt]);
  const [sujeito, setSujeito] = useState("loja");
  if (error && !hours) return <LoadError message={error} onRetry={reload} />;
  if (!hours) return <Loading>Carregando horários…</Loading>;
  const subjects: Subject[] = ctx.isOwner ? [hours.store, ...hours.professionals] : hours.professionals;
  const current =
    subjects.find((x) => (x.professional_id ?? "loja") === sujeito) ?? subjects[0] ?? hours.store;
  return (
    <section aria-labelledby="titulo-horarios" className="grid gap-6">
      <PageHeader
        id="titulo-horarios"
        title="Horários"
        description={`Turnos da semana e folgas. Datas e horas no fuso da loja (${hours.timezone}).`}
      />
      {mode === "queue" && (
        <Notice kind="info" title="A loja atende só por ordem de chegada">
          <p>
            Os horários passam a valer para reservas quando você escolher “Com horário marcado” ou “Os dois”
            em Configurações da loja → Agenda.
          </p>
        </Notice>
      )}
      {ctx.isOwner && (
        <SelectField
          label="Jornada de"
          value={current.professional_id ?? "loja"}
          onChange={(e) => setSujeito(e.target.value)}
          wrapperClassName="max-w-sm"
        >
          {subjects.map((x) => (
            <option key={x.professional_id ?? "loja"} value={x.professional_id ?? "loja"}>
              {x.professional_id ? x.display_name : "Loja (abertura)"}
            </option>
          ))}
        </SelectField>
      )}
      <Jornada
        key={`${current.professional_id ?? "loja"}:${current.current?.id ?? "-"}:${attempt}`}
        ctx={ctx}
        hours={hours}
        subject={current}
        mode={mode}
        onSaved={(h) => {
          setHours(h);
        }}
      />
      <Excecoes ctx={ctx} hours={hours} list={exc} onChanged={reload} />
    </section>
  );
}

function Jornada({
  ctx,
  hours,
  subject,
  mode,
  onSaved,
}: {
  ctx: StaffContext;
  hours: Hours;
  subject: Subject;
  mode: components["schemas"]["ServiceMode"];
  onSaved: (h: Hours) => void;
}) {
  const isStore = subject.professional_id == null;
  const storeShifts = hours.store.current?.shifts ?? [];
  const own = subject.current && !subject.current.inherits_store ? subject.current.shifts : null;
  const [inherit, setInherit] = useState(!isStore && own === null);
  // Configuração inicial do profissional por cópia da loja.
  const [linhas, setLinhas] = useState<Linha[]>(linhasDe(own ?? (isStore ? storeShifts : storeShifts)));
  const [from, setFrom] = useState(hours.today);
  const [dirty, setDirty] = useState(false);
  const [conflitos, setConflitos] = useState<{ id: string; starts_at: string; nick: string }[]>([]);
  useUnsavedWarning(dirty);
  const cmd = useCommand();
  const { erros } = useMemo(() => validar(linhas), [linhas]);
  const editable = ctx.isOwner;
  const change = (next: Linha[]) => {
    setLinhas(next);
    setDirty(true);
  };
  const add = (weekday: number) => {
    const last = linhas.filter((l) => l.weekday === weekday).at(-1);
    change([
      ...linhas,
      { weekday, start: last ? last.end : "09:00", end: last ? "18:00" : "18:00", modality: "both" },
    ]);
  };
  const copiarSegunda = () => {
    const seg = linhas.filter((l) => l.weekday === 1);
    change([
      ...linhas.filter((l) => l.weekday === 0 || l.weekday === 1),
      ...[2, 3, 4, 5, 6].flatMap((d) => seg.map((l) => ({ ...l, weekday: d }))),
    ]);
  };
  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    const v = validar(linhas);
    if (Object.keys(v.erros).length > 0) return;
    setConflitos([]);
    const body = {
      effective_from: from,
      ...(subject.professional_id ? { professional_id: subject.professional_id } : {}),
      ...(inherit ? { inherits_store: true } : { shifts: v.shifts }),
    };
    const out = await cmd.run(`horas:${subject.professional_id ?? "loja"}:${JSON.stringify(body)}`, (key) =>
      api.PUT("/v1/establishments/{establishmentId}/agenda/hours", {
        params: { path: { establishmentId: ctx.estId }, header: { "Idempotency-Key": key } },
        body,
      }),
    );
    if (out.ok) {
      setDirty(false);
      onSaved(out.data.data);
    } else if (out.problem?.code === "schedule_conflicts_appointments") {
      const cur = out.problem.current as
        { appointments?: { id: string; starts_at: string; nick: string }[] } | undefined;
      setConflitos(cur?.appointments ?? []);
    }
  }
  const showModality = mode === "hybrid";
  return (
    <Section
      title={isStore ? "Abertura da loja" : `Jornada de ${subject.display_name}`}
      description={
        isStore
          ? "Turnos de cada dia. O intervalo entre dois turnos é a pausa/almoço. Os profissionais ficam dentro destes horários."
          : "Turnos próprios ficam dentro da abertura da loja. Sem jornada própria, segue a da loja."
      }
    >
      <form onSubmit={(e) => void onSubmit(e)} noValidate aria-label="Jornada semanal" className="grid gap-4">
        {subject.upcoming.length > 0 && (
          <Notice kind="info">
            <p>
              Já há mudança agendada: vale a partir de{" "}
              {subject.upcoming.map((u) => rotuloDia(u.effective_from, hours.today)).join(", ")}.
            </p>
          </Notice>
        )}
        {!isStore && editable && (
          <Choice
            type="checkbox"
            checked={inherit}
            onChange={(e) => {
              setInherit(e.target.checked);
              if (!e.target.checked && linhas.length === 0) setLinhas(linhasDe(storeShifts));
              setDirty(true);
            }}
            label="Seguir a jornada da loja"
            description="Desmarque para definir turnos próprios (começa com uma cópia da loja)."
          />
        )}
        {cmd.problem?.code !== "schedule_conflicts_appointments" && (
          <CommandError problem={cmd.problem} offline={cmd.offline} />
        )}
        {conflitos.length > 0 && (
          <Notice kind="aviso" title="Há reservas fora do novo horário — nada foi alterado">
            <p>Remaneje ou cancele com motivo em Agenda antes de mudar a jornada:</p>
            <ul className="m-0 pl-5">
              {conflitos.map((c) => (
                <li key={c.id}>
                  {rotuloDia(dataNoFuso(new Date(c.starts_at), hours.timezone), hours.today)} às{" "}
                  {horaNoFuso(c.starts_at, hours.timezone)} — {c.nick}
                </li>
              ))}
            </ul>
          </Notice>
        )}
        {!inherit && (
          <div className="grid gap-3">
            {[1, 2, 3, 4, 5, 6, 0].map((d) => {
              const doDia = linhas.map((l, i) => ({ l, i })).filter((x) => x.l.weekday === d);
              return (
                <fieldset key={d} className="m-0 grid gap-2 rounded-xl border border-line bg-surface p-3">
                  <legend className="px-1 text-sm font-semibold">
                    {diasDaSemana[d]}{" "}
                    <span className="font-normal text-fg-muted">· {resumoDia(linhas, d)}</span>
                  </legend>
                  {doDia.map(({ l, i }) => (
                    <div key={i} className="grid gap-1">
                      <div className="flex flex-wrap items-end gap-2">
                        <Field
                          label="Início"
                          value={l.start}
                          inputMode="numeric"
                          disabled={!editable}
                          onChange={(e) =>
                            change(linhas.map((x, j) => (j === i ? { ...x, start: e.target.value } : x)))
                          }
                          wrapperClassName="w-28"
                        />
                        <Field
                          label="Fim"
                          value={l.end}
                          inputMode="numeric"
                          disabled={!editable}
                          onChange={(e) =>
                            change(linhas.map((x, j) => (j === i ? { ...x, end: e.target.value } : x)))
                          }
                          wrapperClassName="w-28"
                        />
                        {showModality && (
                          <SelectField
                            label="Atende"
                            value={l.modality}
                            disabled={!editable}
                            onChange={(e) =>
                              change(
                                linhas.map((x, j) =>
                                  j === i ? { ...x, modality: e.target.value as Modality } : x,
                                ),
                              )
                            }
                            wrapperClassName="w-64"
                          >
                            {(["both", "queue", "schedule"] as Modality[]).map((m) => (
                              <option key={m} value={m}>
                                {modalidadeTexto[m]}
                              </option>
                            ))}
                          </SelectField>
                        )}
                        {editable && (
                          <Button
                            variant="secondary"
                            aria-label={`Remover turno ${l.start}–${l.end} de ${diasDaSemana[d]}`}
                            onClick={() => change(linhas.filter((_, j) => j !== i))}
                          >
                            <Trash2 aria-hidden="true" className="size-4" />
                          </Button>
                        )}
                      </div>
                      {erros[i] && (
                        <p className="m-0 text-sm text-danger" role="alert">
                          {erros[i]}
                        </p>
                      )}
                    </div>
                  ))}
                  {editable && (
                    <div>
                      <Button variant="secondary" onClick={() => add(d)}>
                        <Plus aria-hidden="true" className="size-4" />{" "}
                        {doDia.length ? "Mais um turno" : "Abrir neste dia"}
                      </Button>
                    </div>
                  )}
                </fieldset>
              );
            })}
            {editable && (
              <div>
                <Button variant="secondary" onClick={copiarSegunda}>
                  <Copy aria-hidden="true" className="size-4" /> Copiar segunda para terça a sábado
                </Button>
              </div>
            )}
            <p className="m-0 text-sm text-fg-muted">
              Turno que passa da meia-noite: cadastre até 24:00 num dia e a partir de 00:00 no dia seguinte.
            </p>
          </div>
        )}
        {editable && (
          <div className="flex flex-wrap items-end gap-3">
            <Field
              label="Vale a partir de"
              type="date"
              min={hours.today}
              value={from}
              onChange={(e) => {
                setFrom(e.target.value);
                setDirty(true);
              }}
              hint="Reservas antes desta data não mudam. Hoje ou depois."
              wrapperClassName="w-56"
            />
            <Submit busy={cmd.busy !== null}>Salvar jornada</Submit>
          </div>
        )}
      </form>
    </Section>
  );
}

function Excecoes({
  ctx,
  hours,
  list,
  onChanged,
}: {
  ctx: StaffContext;
  hours: Hours;
  list: Exception[];
  onChanged: () => void;
}) {
  const nomes = new Map(hours.professionals.map((p) => [p.professional_id ?? "", p.display_name]));
  const [date, setDate] = useState(somarDias(hours.today, 1));
  const [scope, setScope] = useState(ctx.isOwner ? "loja" : (ctx.professionalId ?? ""));
  const [wholeDay, setWholeDay] = useState(true);
  const [start, setStart] = useState("12:00");
  const [end, setEnd] = useState("13:00");
  const [reason, setReason] = useState("");
  const [conflitos, setConflitos] = useState<{ id: string; starts_at: string; nick: string }[]>([]);
  const [removing, setRemoving] = useState<string | null>(null);
  const cmd = useCommand();
  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setConflitos([]);
    const a = horaParaMinutos(start);
    const b = horaParaMinutos(end);
    const body = {
      date,
      reason,
      ...(scope !== "loja" ? { professional_id: scope } : {}),
      ...(wholeDay || a === null || b === null ? {} : { start_minute: a, end_minute: b }),
    };
    const out = await cmd.run(`excecao:${JSON.stringify(body)}`, (key) =>
      api.POST("/v1/establishments/{establishmentId}/agenda/exceptions", {
        params: { path: { establishmentId: ctx.estId }, header: { "Idempotency-Key": key } },
        body,
      }),
    );
    if (out.ok) {
      setReason("");
      onChanged();
    } else if (out.problem?.code === "exception_conflicts_appointments") {
      const cur = out.problem.current as
        { appointments?: { id: string; starts_at: string; nick: string }[] } | undefined;
      setConflitos(cur?.appointments ?? []);
    }
  }
  async function remove(id: string) {
    const out = await cmd.run(`remover-excecao:${id}`, (key) =>
      api.POST("/v1/establishments/{establishmentId}/agenda/exceptions/{exceptionId}/remove", {
        params: { path: { establishmentId: ctx.estId, exceptionId: id }, header: { "Idempotency-Key": key } },
        body: {},
      }),
    );
    setRemoving(null);
    if (out.ok) onChanged();
  }
  const problem: Problem | null =
    cmd.problem?.code === "exception_conflicts_appointments" ? null : cmd.problem;
  return (
    <Section
      title="Folgas, feriados e bloqueios"
      description="Datas em que a loja ou um profissional não atende. O motivo é interno."
    >
      <CommandError problem={problem} offline={cmd.offline} />
      {list.length === 0 ? (
        <p className="m-0 text-sm text-fg-muted">Nenhuma folga ou bloqueio nos próximos 30 dias.</p>
      ) : (
        <ul className="m-0 grid list-none divide-y divide-line rounded-xl border border-line bg-surface p-0">
          {list.map((x) => (
            <li key={x.id} className="grid gap-2 px-4 py-3">
              <div className="flex flex-wrap items-center gap-3">
                <CalendarOff aria-hidden="true" className="size-5 text-fg-muted" />
                <span className="flex-1">
                  <span className="block font-semibold">
                    {rotuloDia(x.date, hours.today)} ·{" "}
                    {x.start_minute == null
                      ? "dia inteiro"
                      : `${minutosParaHora(x.start_minute)}–${minutosParaHora(x.end_minute ?? 0)}`}
                  </span>
                  <span className="text-sm text-fg-muted">
                    {x.professional_id ? nomes.get(x.professional_id) : "Loja inteira"} · {x.reason}
                  </span>
                </span>
                {(ctx.isOwner || x.professional_id === ctx.professionalId) && removing !== x.id && (
                  <Button variant="secondary" onClick={() => setRemoving(x.id)}>
                    Desfazer
                  </Button>
                )}
              </div>
              {removing === x.id && (
                <ConfirmPanel
                  title="Desfazer esta folga/bloqueio?"
                  confirmLabel="Desfazer"
                  busy={cmd.busy !== null}
                  onConfirm={() => void remove(x.id)}
                  onCancel={() => setRemoving(null)}
                >
                  <p>Os horários voltam a ser oferecidos (o histórico fica).</p>
                </ConfirmPanel>
              )}
            </li>
          ))}
        </ul>
      )}
      <form
        onSubmit={(e) => void onSubmit(e)}
        noValidate
        aria-label="Nova folga ou bloqueio"
        className="grid gap-3 rounded-xl border border-line bg-surface p-4"
      >
        <h3 className="m-0 text-base font-semibold">Nova folga ou bloqueio</h3>
        {conflitos.length > 0 && (
          <Notice kind="aviso" title="Há reservas neste período — nada foi bloqueado">
            <ul className="m-0 pl-5">
              {conflitos.map((c) => (
                <li key={c.id}>
                  {horaNoFuso(c.starts_at, hours.timezone)} — {c.nick}
                </li>
              ))}
            </ul>
            <p>Remaneje ou cancele com motivo em Agenda e tente de novo.</p>
          </Notice>
        )}
        <div className="flex flex-wrap items-end gap-3">
          <Field
            label="Data"
            type="date"
            min={hours.today}
            value={date}
            onChange={(e) => setDate(e.target.value)}
            wrapperClassName="w-48"
          />
          <SelectField
            label="Para"
            value={scope}
            onChange={(e) => setScope(e.target.value)}
            wrapperClassName="w-60"
          >
            {ctx.isOwner && <option value="loja">Loja inteira (feriado)</option>}
            {hours.professionals.map((p) => (
              <option key={p.professional_id ?? ""} value={p.professional_id ?? ""}>
                {p.display_name}
              </option>
            ))}
          </SelectField>
        </div>
        <Choice
          type="checkbox"
          checked={wholeDay}
          onChange={(e) => setWholeDay(e.target.checked)}
          label="Dia inteiro"
        />
        {!wholeDay && (
          <div className="flex flex-wrap gap-3">
            <Field
              label="De"
              value={start}
              onChange={(e) => setStart(e.target.value)}
              wrapperClassName="w-28"
            />
            <Field label="Até" value={end} onChange={(e) => setEnd(e.target.value)} wrapperClassName="w-28" />
          </div>
        )}
        <Field
          label="Motivo (interno)"
          value={reason}
          onChange={(e) => setReason(e.target.value)}
          hint="De 3 a 200 caracteres. O cliente não vê."
        />
        <div>
          <Submit busy={cmd.busy !== null}>Bloquear</Submit>
        </div>
      </form>
    </Section>
  );
}
