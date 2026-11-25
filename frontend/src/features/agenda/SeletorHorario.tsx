import { useEffect, useRef, useState } from "react";
import { CalendarClock } from "lucide-react";
import { api } from "../../api/client";
import { call, problemMessage } from "../../api/problem";
import type { components } from "../../api/schema.gen";
import { espera } from "../../tempo";
import { Button, LoadError, Loading, Notice } from "../../ui/ui";
import { cx } from "../../ui/cx";
import { useStreamMessages } from "../../stream/context";
import { horaNoFuso, rotuloDia, somarDias } from "./horario";

export type Availability = components["schemas"]["Availability"];
export type Slot = components["schemas"]["AvailabilitySlot"];

/**
 * Escolha de dia e horário (ADR-0038). Uma consulta por dia escolhido (nunca uma por horário); a
 * consulta anterior é CANCELADA quando a pessoa troca de dia ou de itens, e resposta antiga é
 * ignorada. Os dias com vaga vêm de UMA consulta agregada (getAvailabilityDays) para a janela
 * inteira. Sem consulta repetida em intervalo: voltar à aba revalida só o dia visível.
 * `items` no formato do contrato: "<id>" ou "<id>:<quantidade>".
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
  // Voltou para a aba/aparelho desbloqueado: uma revalidação do dia visível (outra pessoa pode ter
  // reservado). Nada de consulta periódica.
  useEffect(() => {
    const onVisible = () => {
      if (document.visibilityState === "visible") setAttempt((n) => n + 1);
    };
    document.addEventListener("visibilitychange", onVisible);
    return () => document.removeEventListener("visibilitychange", onVisible);
  }, []);
  useEffect(() => {
    if (items.length === 0) return;
    let active = true;
    const ctrl = new AbortController();
    const today = date ?? new Date().toISOString().slice(0, 10);
    void call(() =>
      api.GET("/v1/public/establishments/{establishmentId}/availability", {
        params: {
          path: { establishmentId: estId },
          query: { date: today, items, professional_id: [professionalId] },
        },
        signal: ctrl.signal,
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
      ctrl.abort();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps -- key resume as dependências
  }, [key]);
  // Dias com vaga na janela inteira: uma consulta por seleção (profissional + itens + janela).
  const [vagas, setVagas] = useState<{ key: string; days: Record<string, boolean> } | null>(null);
  const [geracao, setGeracao] = useState(0);
  const janela = data ? `${data.first_date}|${lastDateFor ? lastDateFor(data) : data.last_date}` : "";
  const diasKey = `${estId}|${professionalId}|${items.join(",")}|${janela}|${geracao}`;
  // Outra pessoa reservou/cancelou (evento público, só profissional e data): relê o dia visível e o
  // agregado, uma vez por rajada de eventos.
  const rajada = useRef<number | null>(null);
  useStreamMessages((m) => {
    if (m.type !== "availability.updated") return;
    const h = (m.data ?? {}) as { professional_id?: string | null; date?: string | null };
    if (h.professional_id && h.professional_id !== professionalId) return;
    if (rajada.current) window.clearTimeout(rajada.current);
    rajada.current = window.setTimeout(() => {
      if (!h.date || h.date === date) setAttempt((n) => n + 1);
      setGeracao((n) => n + 1);
    }, 400);
  });
  useEffect(
    () => () => {
      if (rajada.current) window.clearTimeout(rajada.current);
    },
    [],
  );
  useEffect(() => {
    if (!data || items.length === 0) return;
    const [from, ate] = janela.split("|");
    if (!from || !ate) return;
    const to = ate < somarDias(from, 61) ? ate : somarDias(from, 61);
    let active = true;
    const ctrl = new AbortController();
    void call(() =>
      api.GET("/v1/public/establishments/{establishmentId}/availability/days", {
        params: {
          path: { establishmentId: estId },
          query: { from, to, professional_id: professionalId, items },
        },
        signal: ctrl.signal,
      }),
    ).then((out) => {
      if (!active || !out.ok) return; // sem o agregado, os dias aparecem sem marca (a consulta do dia decide)
      setVagas({
        key: diasKey,
        days: Object.fromEntries(out.data.data.days.map((d) => [d.date, d.has_slots])),
      });
    });
    return () => {
      active = false;
      ctrl.abort();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps -- diasKey resume as dependências
  }, [diasKey]);
  const semVaga = (d: string) => vagas?.key === diasKey && vagas.days[d] === false;

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
            aria-label={semVaga(d) ? `${rotuloDia(d, data.first_date)}, sem vaga` : undefined}
            onClick={() => setDate(d)}
            className={cx(
              "grid min-h-11 shrink-0 place-items-center rounded-lg border px-3 text-sm font-semibold",
              d === date
                ? "border-primary bg-select text-accent"
                : semVaga(d)
                  ? "border-line bg-surface-2 text-fg-muted line-through"
                  : "border-line bg-surface text-fg hover:bg-surface-2",
            )}
          >
            {rotuloDia(d, data.first_date)}
            {semVaga(d) && <span className="text-[0.7rem] font-normal no-underline">sem vaga</span>}
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
