import { useCallback, useEffect, useState, type FormEvent } from "react";
import { api } from "../../api/client";
import { call, fieldErrors, problemMessage } from "../../api/problem";
import type { components } from "../../api/schema.gen";
import { centsToInput, formatCents, parseCents } from "../../money";
import { Field, Notice, Submit } from "../../ui/ui";
import { useCommand } from "./comando";
import { CommandError, type StaffContext } from "./comum";

type Service = components["schemas"]["Service"];
type Professional = components["schemas"]["Professional"];
type Pro = { id: string; name: string; active: boolean };

/** Catálogo: a equipe lê; só o dono cria e altera (o servidor confere). */
export function Servicos({ ctx }: { ctx: StaffContext }) {
  const [services, setServices] = useState<Service[] | null>(null);
  const [pros, setPros] = useState<Pro[]>([]);
  const [loadError, setLoadError] = useState<string | null>(null);

  const fetchAll = useCallback(async () => {
    const path = { establishmentId: ctx.estId };
    const s = await call(() =>
      api.GET("/v1/establishments/{establishmentId}/services", { params: { path } }),
    );
    // Só o dono lista profissionais; para o profissional, os nomes não são necessários.
    const p = ctx.isOwner
      ? await call(() => api.GET("/v1/establishments/{establishmentId}/professionals", { params: { path } }))
      : null;
    return { s, p };
  }, [ctx.estId, ctx.isOwner]);

  const apply = useCallback((r: Awaited<ReturnType<typeof fetchAll>>) => {
    if (!r.s.ok) {
      setLoadError(problemMessage(r.s.problem, r.s.offline));
      return;
    }
    setServices(r.s.data.data);
    if (r.p?.ok) {
      setPros(
        r.p.data.data.map((x: Professional) => ({
          id: x.id,
          name: x.display_name,
          active: x.status === "active",
        })),
      );
    }
  }, []);
  const load = useCallback(async () => apply(await fetchAll()), [apply, fetchAll]);

  useEffect(() => {
    let active = true;
    void fetchAll().then((r) => {
      if (active) apply(r);
    });
    return () => {
      active = false;
    };
  }, [fetchAll, apply]);

  if (loadError) return <Notice kind="erro">{loadError}</Notice>;
  if (!services) return <p role="status">Carregando serviços…</p>;
  return (
    <div>
      {ctx.isOwner && <ServicoForm ctx={ctx} pros={pros} onDone={load} />}
      {services.length === 0 ? (
        <p>Nenhum serviço cadastrado.</p>
      ) : (
        <ul className="cards">
          {services.map((s) => (
            <ServicoCard key={s.id} ctx={ctx} s={s} pros={pros} onDone={load} />
          ))}
        </ul>
      )}
    </div>
  );
}

function ServicoCard({
  ctx,
  s,
  pros,
  onDone,
}: {
  ctx: StaffContext;
  s: Service;
  pros: Pro[];
  onDone: () => Promise<void>;
}) {
  const [editing, setEditing] = useState(false);
  const cmd = useCommand();
  const names = s.professional_ids
    .map((id) => pros.find((p) => p.id === id)?.name ?? "profissional")
    .join(", ");
  async function toggle() {
    const op = s.status === "active" ? "deactivate" : "reactivate";
    const path =
      op === "deactivate"
        ? ("/v1/establishments/{establishmentId}/services/{serviceId}/deactivate" as const)
        : ("/v1/establishments/{establishmentId}/services/{serviceId}/reactivate" as const);
    const out = await cmd.run(`${op}:${s.id}:${s.version}`, (key) =>
      api.POST(path, {
        params: { path: { establishmentId: ctx.estId, serviceId: s.id }, header: { "Idempotency-Key": key } },
        body: { expected_version: s.version },
      }),
    );
    if (out.ok || out.problem?.status === 409) await onDone();
  }
  return (
    <li className="card">
      <h3>{s.name}</h3>
      <p>
        {formatCents(s.price.amount_cents)} · {s.duration_minutes} min · {s.points} pontos ·{" "}
        {s.status === "active" ? "✓ Ativo" : "■ Inativo"}
      </p>
      {ctx.isOwner && <p className="muted">Profissionais: {names || "nenhum (ninguém pode atender)"}</p>}
      <CommandError problem={cmd.problem} offline={cmd.offline} />
      {ctx.isOwner && !editing && (
        <div className="actions">
          <button type="button" className="button button--secondary" onClick={() => setEditing(true)}>
            Editar
          </button>
          <button
            type="button"
            className="button button--secondary"
            disabled={cmd.busy !== null}
            onClick={() => void toggle()}
          >
            {s.status === "active" ? "Desativar" : "Reativar"}
          </button>
        </div>
      )}
      {editing && (
        <ServicoForm
          ctx={ctx}
          pros={pros}
          service={s}
          onDone={async () => {
            setEditing(false);
            await onDone();
          }}
          onConflict={onDone}
          onCancel={() => setEditing(false)}
        />
      )}
    </li>
  );
}

/** Criação (sem `service`) ou edição. Preço digitado em reais e enviado em centavos inteiros. */
function ServicoForm({
  ctx,
  pros,
  service,
  onDone,
  onConflict,
  onCancel,
}: {
  ctx: StaffContext;
  pros: Pro[];
  service?: Service;
  onDone: () => Promise<void>;
  /** Conflito de versão: relê a lista (o formulário continua aberto com o aviso). */
  onConflict?: () => Promise<void>;
  onCancel?: () => void;
}) {
  const cmd = useCommand();
  const [name, setName] = useState(service?.name ?? "");
  const [price, setPrice] = useState(service ? centsToInput(service.price.amount_cents) : "");
  const [duration, setDuration] = useState(service ? String(service.duration_minutes) : "");
  const [points, setPoints] = useState(service ? String(service.points) : "0");
  const [selected, setSelected] = useState<string[]>(service?.professional_ids ?? []);
  const [localErrors, setLocalErrors] = useState<Record<string, string>>({});

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    const cents = parseCents(price);
    const errs: Record<string, string> = {};
    if (cents === null || cents < 1) errs.price_cents = "Informe um valor maior que zero, ex.: 45,00";
    if (!/^\d+$/.test(duration)) errs.duration_minutes = "Informe os minutos (número inteiro).";
    if (!/^\d+$/.test(points)) errs.points = "Informe um número inteiro de pontos (0 ou mais).";
    setLocalErrors(errs);
    if (Object.keys(errs).length > 0 || cents === null) return;
    const fields = { name, price_cents: cents, duration_minutes: Number(duration), points: Number(points) };
    const path = { establishmentId: ctx.estId };
    let ok: boolean;
    let conflict = false;
    if (!service) {
      const out = await cmd.run(`criar:${JSON.stringify(fields)}:${selected.join(",")}`, (key) =>
        api.POST("/v1/establishments/{establishmentId}/services", {
          params: { path, header: { "Idempotency-Key": key } },
          body: { ...fields, professional_ids: selected },
        }),
      );
      ok = out.ok;
    } else {
      const sp = { ...path, serviceId: service.id };
      const out = await cmd.run(`editar:${service.id}:${service.version}:${JSON.stringify(fields)}`, (key) =>
        api.PATCH("/v1/establishments/{establishmentId}/services/{serviceId}", {
          params: { path: sp, header: { "Idempotency-Key": key } },
          body: { expected_version: service.version, ...fields },
        }),
      );
      ok = out.ok;
      if (!out.ok && out.problem?.status === 409) conflict = true;
      const changedPros = [...selected].sort().join(",") !== [...service.professional_ids].sort().join(",");
      if (out.ok && changedPros) {
        const res = await cmd.run(`habilitar:${service.id}:${out.data.data.version}`, (key) =>
          api.PUT("/v1/establishments/{establishmentId}/services/{serviceId}/professionals", {
            params: { path: sp, header: { "Idempotency-Key": key } },
            body: { expected_version: out.data.data.version, professional_ids: selected },
          }),
        );
        ok = res.ok;
        if (!res.ok && res.problem?.status === 409) conflict = true;
      }
      if (conflict && onConflict) await onConflict();
    }
    if (ok) {
      if (!service) {
        setName("");
        setPrice("");
        setDuration("");
        setPoints("0");
        setSelected([]);
      }
      await onDone();
    }
  }

  const errors = { ...fieldErrors(cmd.problem), ...localErrors };
  return (
    <form
      onSubmit={onSubmit}
      noValidate
      className="card"
      aria-label={service ? `Editar ${service.name}` : "Novo serviço"}
    >
      {!service && <h3>Novo serviço</h3>}
      {!cmd.problem?.errors?.length && <CommandError problem={cmd.problem} offline={cmd.offline} />}
      <Field
        label="Nome"
        required
        maxLength={60}
        value={name}
        error={errors.name}
        onChange={(e) => setName(e.target.value)}
      />
      <Field
        label="Preço (R$)"
        inputMode="decimal"
        required
        value={price}
        error={errors.price_cents}
        hint="Ex.: 45,00. Vale só para novos atendimentos; os já aceitos mantêm o preço antigo."
        onChange={(e) => setPrice(e.target.value)}
      />
      <Field
        label="Duração (minutos)"
        inputMode="numeric"
        required
        value={duration}
        error={errors.duration_minutes}
        onChange={(e) => setDuration(e.target.value)}
      />
      <Field
        label="Pontos de fidelidade"
        inputMode="numeric"
        required
        value={points}
        error={errors.points}
        hint="Use 0 se o programa de pontos não estiver em uso."
        onChange={(e) => setPoints(e.target.value)}
      />
      {pros.length > 0 && (
        <fieldset className="choices">
          <legend>Profissionais que fazem este serviço</legend>
          {pros.map((p) => (
            <label key={p.id} className="choice">
              <input
                type="checkbox"
                checked={selected.includes(p.id)}
                onChange={(e) =>
                  setSelected((cur) => (e.target.checked ? [...cur, p.id] : cur.filter((x) => x !== p.id)))
                }
              />
              {p.name}
              {!p.active && " (desativado)"}
            </label>
          ))}
        </fieldset>
      )}
      <div className="actions">
        <Submit busy={cmd.busy !== null}>{service ? "Salvar" : "Criar serviço"}</Submit>
        {onCancel && (
          <button type="button" className="button button--secondary" onClick={onCancel}>
            Cancelar
          </button>
        )}
      </div>
    </form>
  );
}
