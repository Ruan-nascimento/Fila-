import { useEffect, useState } from "react";
import { CalendarClock } from "lucide-react";
import { api } from "../../api/client";
import { call, problemMessage } from "../../api/problem";
import type { components } from "../../api/schema.gen";
import { espera } from "../../tempo";
import { Button, LoadError, Loading, Notice } from "../../ui/ui";
import { cx } from "../../ui/cx";
import { horaNoFuso, rotuloDia, somarDias } from "./horario";

export type Availability = components["schemas"]["Availability"];
export type Slot = components["schemas"]["AvailabilitySlot"];

/**
 * Escolha de dia e horário (ADR-0038). Uma consulta por dia escolhido (nunca uma por horário); a
 * consulta anterior é descartada quando a pessoa troca de dia ou de itens. `items` no formato do
 * contrato: "<id>" ou "<id>:<quantidade>".
 */
export function SeletorHorario({
  estId,
  professionalId,
  items,
  value,
  onPick,
  lastDateFor,
  initialDate,
  notice,
}: {
  estId: string;
  professionalId: string;
  items: string[];
  value: string | null;
  onPick: (slot: Slot, info: Availability) => void;
  /** Último dia permitido para quem está reservando (ex.: visitante = 7 dias). */
  lastDateFor?: (a: Availability) => string;
  initialDate?: string | undefined;
  /** Aviso vindo de fora (ex.: "o horário acabou de ser ocupado"). */
  notice?: string | null | undefined;
}) {
  const [date, setDate] = useState<string | null>(initialDate ?? null);
  const [data, setData] = useState<Availability | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [attempt, setAttempt] = useState(0);
  const key = `${estId}|${professionalId}|${items.join(",")}|${date ?? ""}|${attempt}`;
  const [loadedKey, setLoadedKey] = useState<string | null>(null);
  useEffect(() => {
    if (items.length === 0) return;
    let active = true;
    const today = date ?? new Date().toISOString().slice(0, 10);
    void call(() =>
      api.GET("/v1/public/establishments/{establishmentId}/availability", {
        params: {
          path: { establishmentId: estId },
          query: { date: today, items, professional_id: [professionalId] },
        },
      }),
    ).then((out) => {
      if (!active) return; // resposta antiga (trocou de dia/itens): ignorada
      setLoadedKey(key);
      if (out.ok) {
        setData(out.data.data);
        setError(null);
        if (date === null) setDate(out.data.data.date);
      } else if (out.problem?.code === "validation_failed" && date === null) {
        // Primeira abertura num fuso diferente do da loja: tenta o dia seguinte.
        setDate(somarDias(today, 1));
      } else setError(problemMessage(out.problem, out.offline));
    });
    return () => {
      active = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps -- key resume as dependências
  }, [key]);

  if (items.length === 0) return null;
  if (error && !data) return <LoadError message={error} onRetry={() => setAttempt((n) => n + 1)} />;
  if (!data) return <Loading>Procurando horários…</Loading>;
  const last = lastDateFor ? lastDateFor(data) : data.last_date;
  const days: string[] = [];
  for (let d = data.first_date; d <= last && days.length < 31; d = somarDias(d, 1)) days.push(d);
  const prof = data.professionals.find((p) => p.professional_id === professionalId);
  const loading = loadedKey !== key;
  return (
    <div className="grid gap-3">
      {notice && (
        <Notice kind="aviso">
          <p>{notice}</p>
        </Notice>
      )}
      <div role="group" aria-label="Dia" className="-mx-1 flex gap-1 overflow-x-auto px-1 pb-1">
        {days.map((d) => (
          <button
            key={d}
            type="button"
            aria-pressed={d === date}
            onClick={() => setDate(d)}
            className={cx(
              "min-h-11 shrink-0 rounded-lg border px-3 text-sm font-semibold",
              d === date
                ? "border-primary bg-select text-accent"
                : "border-line bg-surface text-fg hover:bg-surface-2",
            )}
          >
            {rotuloDia(d, data.first_date)}
          </button>
        ))}
      </div>
      <p className="m-0 text-sm text-fg-muted">
        Duração: {espera(data.service_minutes)}
        {data.buffer_minutes > 0 ? ` + ${data.buffer_minutes} min de preparação` : ""}. Horários no fuso da
        loja.
      </p>
      {loading ? (
        <Loading>Procurando horários…</Loading>
      ) : error ? (
        <LoadError message={error} onRetry={() => setAttempt((n) => n + 1)} />
      ) : !prof?.eligible ? (
        <Notice kind="info">
          <p>Este profissional não faz algum dos serviços escolhidos. Volte e escolha outro.</p>
        </Notice>
      ) : prof.slots.length === 0 ? (
        <div className="flex items-center gap-2 rounded-xl border border-line bg-surface-2 p-4 text-sm">
          <CalendarClock aria-hidden="true" className="size-5 text-fg-muted" />
          Nenhum horário livre neste dia para esta duração. Tente outro dia.
        </div>
      ) : (
        <div
          role="group"
          aria-label="Horário"
          className="grid grid-cols-3 gap-2 sm:grid-cols-5 lg:grid-cols-6"
        >
          {prof.slots.map((s) => (
            <Button
              key={s.starts_at}
              variant={value === s.starts_at ? "primary" : "secondary"}
              aria-pressed={value === s.starts_at}
              onClick={() => onPick(s, data)}
            >
              {s.local_time}
            </Button>
          ))}
        </div>
      )}
      {prof?.queue && (
        <p className="m-0 text-sm text-fg-muted">
          {prof.queue.has_capacity && prof.queue.next_start
            ? `Na fila agora, a previsão de início seria às ${horaNoFuso(prof.queue.next_start, data.timezone)} (estimativa).`
            : "Hoje a fila deste profissional não tem mais vaga até o fechamento."}
        </p>
      )}
    </div>
  );
}
