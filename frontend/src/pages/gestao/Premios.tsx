import { useCallback, useEffect, useState, type FormEvent } from "react";
import { api } from "../../api/client";
import { call, problemMessage } from "../../api/problem";
import { useStreamMessages } from "../../stream/context";
import { Field, Notice, Submit } from "../../ui/ui";
import {
  reservationStatusText,
  stockText,
  type LoyaltyProgram,
  type Reservation,
  type Reward,
} from "../fidelidade/textos";
import { useCommand } from "./comando";
import { CommandError, type StaffContext } from "./comum";

const when = new Intl.DateTimeFormat("pt-BR", { dateStyle: "short", timeStyle: "short" });
type Filter = "active" | "delivered" | "";

/**
 * Prêmios da loja. Equipe: entregar reservas ativas (antes do vencimento). Dono: também corrigir
 * entrega (com motivo), ligar/desligar o programa e cadastrar prêmios. Quem decide é o servidor.
 */
export function Premios({ ctx }: { ctx: StaffContext }) {
  return (
    <div>
      <Reservas ctx={ctx} />
      {ctx.isOwner && <Programa ctx={ctx} />}
      {ctx.isOwner && <Catalogo ctx={ctx} />}
    </div>
  );
}

function Reservas({ ctx }: { ctx: StaffContext }) {
  const [filter, setFilter] = useState<Filter>("active");
  const [list, setList] = useState<Reservation[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const fetchList = useCallback(
    () =>
      call(() =>
        api.GET("/v1/establishments/{establishmentId}/reward-reservations", {
          params: {
            path: { establishmentId: ctx.estId },
            query: { limit: 50, ...(filter ? { status: filter } : {}) },
          },
        }),
      ),
    [ctx.estId, filter],
  );
  const reload = useCallback(async () => {
    const out = await fetchList();
    if (out.ok) setList(out.data.data);
    else setError(problemMessage(out.problem, out.offline));
  }, [fetchList]);
  useEffect(() => {
    let active = true;
    void fetchList().then((out) => {
      if (!active) return;
      if (out.ok) {
        setList(out.data.data);
        setError(null);
      } else setError(problemMessage(out.problem, out.offline));
    });
    return () => {
      active = false;
    };
  }, [fetchList]);
  useStreamMessages((m) => {
    if (m.type === "stream.snapshot" || m.type === "reward_reservation.updated") void reload();
  });
  const replace = (r: Reservation) =>
    setList((l) =>
      (l ?? []).map((x) => (x.id === r.id ? r : x)).filter((x) => !filter || x.status === filter),
    );

  return (
    <section aria-labelledby="titulo-reservas">
      <h3 id="titulo-reservas">Reservas de prêmio</h3>
      <label htmlFor="filtro-reservas">Mostrar</label>{" "}
      <select
        id="filtro-reservas"
        className="select"
        value={filter}
        onChange={(e) => setFilter(e.target.value as Filter)}
      >
        <option value="active">Para entregar</option>
        <option value="delivered">Entregues</option>
        <option value="">Todas</option>
      </select>
      {error && <Notice kind="erro">{error}</Notice>}
      {list?.length === 0 && <p>Nenhuma reserva aqui.</p>}
      <ul className="cards">
        {(list ?? []).map((r) => (
          <li key={r.id} className="card">
            <h4>
              {r.accepted_terms.name} — {r.holder_display_name ?? "Cliente"}
            </h4>
            <p className="muted">
              {reservationStatusText[r.status]} · {r.accepted_terms.cost_points} pontos · reservado em{" "}
              {when.format(new Date(r.created_at))}
              {r.status === "active" && <> · vence em {when.format(new Date(r.expires_at))}</>}
            </p>
            <p>{r.accepted_terms.usage_instructions}</p>
            {r.status === "active" && (
              <Entregar ctx={ctx} reservation={r} onSaved={replace} onConflict={reload} />
            )}
            {ctx.isOwner && r.status === "delivered" && (
              <Corrigir ctx={ctx} reservation={r} onSaved={replace} onConflict={reload} />
            )}
          </li>
        ))}
      </ul>
    </section>
  );
}

function Entregar({
  ctx,
  reservation,
  onSaved,
  onConflict,
}: {
  ctx: StaffContext;
  reservation: Reservation;
  onSaved: (r: Reservation) => void;
  onConflict: () => Promise<void>;
}) {
  const cmd = useCommand();
  async function deliver() {
    const out = await cmd.run(`entregar:${reservation.id}:${reservation.version}`, (key) =>
      api.POST("/v1/establishments/{establishmentId}/reward-reservations/{reservationId}/deliver", {
        params: {
          path: { establishmentId: ctx.estId, reservationId: reservation.id },
          header: { "Idempotency-Key": key },
        },
        body: { expected_version: reservation.version },
      }),
    );
    if (out.ok) onSaved(out.data.data);
    else if (out.problem?.status === 409) await onConflict();
  }
  return (
    <>
      <CommandError problem={cmd.problem} offline={cmd.offline} />
      <button type="button" className="button" disabled={cmd.busy !== null} onClick={() => void deliver()}>
        Confirmar entrega de {reservation.accepted_terms.name}
      </button>
    </>
  );
}

function Corrigir({
  ctx,
  reservation,
  onSaved,
  onConflict,
}: {
  ctx: StaffContext;
  reservation: Reservation;
  onSaved: (r: Reservation) => void;
  onConflict: () => Promise<void>;
}) {
  const [open, setOpen] = useState(false);
  const [reason, setReason] = useState("");
  const cmd = useCommand();
  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    if (reason.trim().length < 3) return;
    const out = await cmd.run(`corrigir-entrega:${reservation.id}:${reservation.version}`, (key) =>
      api.POST("/v1/establishments/{establishmentId}/reward-reservations/{reservationId}/revert-delivery", {
        params: {
          path: { establishmentId: ctx.estId, reservationId: reservation.id },
          header: { "Idempotency-Key": key },
        },
        body: { expected_version: reservation.version, reason },
      }),
    );
    if (out.ok) onSaved(out.data.data);
    else if (out.problem?.status === 409) await onConflict();
  }
  if (!open)
    return (
      <button type="button" className="button button--secondary" onClick={() => setOpen(true)}>
        Corrigir entrega
      </button>
    );
  return (
    <form noValidate aria-label="Corrigir entrega" onSubmit={onSubmit}>
      <CommandError problem={cmd.problem} offline={cmd.offline} />
      <Field
        label="Motivo da correção"
        hint="Os pontos e a unidade voltam para o cliente."
        required
        minLength={3}
        maxLength={500}
        value={reason}
        onChange={(e) => setReason(e.target.value)}
      />
      <Submit busy={cmd.busy !== null}>Desfazer entrega</Submit>
    </form>
  );
}

function Programa({ ctx }: { ctx: StaffContext }) {
  const [program, setProgram] = useState<LoyaltyProgram | null>(null);
  const cmd = useCommand();
  useEffect(() => {
    let active = true;
    void call(() =>
      api.GET("/v1/establishments/{establishmentId}/loyalty-program", {
        params: { path: { establishmentId: ctx.estId } },
      }),
    ).then((out) => {
      if (active && out.ok) setProgram(out.data.data);
    });
    return () => {
      active = false;
    };
  }, [ctx.estId]);
  if (!program) return null;
  const current = program;
  async function toggle() {
    const out = await cmd.run(`programa:${current.version}:${!current.enabled}`, (key) =>
      api.PUT("/v1/establishments/{establishmentId}/loyalty-program", {
        params: { path: { establishmentId: ctx.estId }, header: { "Idempotency-Key": key } },
        body: { expected_version: current.version, enabled: !current.enabled },
      }),
    );
    if (out.ok) setProgram(out.data.data);
  }
  return (
    <section aria-labelledby="titulo-programa" className="card">
      <h3 id="titulo-programa">Programa de pontos</h3>
      <p role="status">
        Situação: <strong>{current.enabled ? "ligado" : "desligado"}</strong>.
      </p>
      <p className="muted">
        Vale para novas visitas e novas reservas. Desligar não apaga saldos, não retira pontos já prometidos e
        mantém as reservas feitas. Os pontos de cada serviço são definidos na aba Serviços.
      </p>
      <CommandError problem={cmd.problem} offline={cmd.offline} />
      <button type="button" className="button" disabled={cmd.busy !== null} onClick={() => void toggle()}>
        {current.enabled ? "Desligar programa" : "Ligar programa"}
      </button>
    </section>
  );
}

function Catalogo({ ctx }: { ctx: StaffContext }) {
  const [list, setList] = useState<Reward[]>([]);
  const [editing, setEditing] = useState<Reward | "new" | null>(null);
  const reload = useCallback(async () => {
    const out = await call(() =>
      api.GET("/v1/establishments/{establishmentId}/rewards", {
        params: { path: { establishmentId: ctx.estId } },
      }),
    );
    if (out.ok) setList(out.data.data);
  }, [ctx.estId]);
  useEffect(() => {
    let active = true;
    void call(() =>
      api.GET("/v1/establishments/{establishmentId}/rewards", {
        params: { path: { establishmentId: ctx.estId } },
      }),
    ).then((out) => {
      if (active && out.ok) setList(out.data.data);
    });
    return () => {
      active = false;
    };
  }, [ctx.estId]);
  return (
    <section aria-labelledby="titulo-premios">
      <h3 id="titulo-premios">Prêmios cadastrados</h3>
      {editing ? (
        <PremioForm
          ctx={ctx}
          reward={editing === "new" ? null : editing}
          onDone={async () => {
            setEditing(null);
            await reload();
          }}
        />
      ) : (
        <button type="button" className="button" onClick={() => setEditing("new")}>
          Novo prêmio
        </button>
      )}
      <ul className="cards">
        {list.map((r) => (
          <li key={r.id} className="card">
            <h4>
              {r.name} — {r.cost_points} pontos {r.status === "inactive" && "(inativo)"}
            </h4>
            <p>{r.description}</p>
            <p className="muted">
              {r.usage_instructions} · {stockText(r)}
            </p>
            {r.status === "active" && (
              <div className="actions">
                <button type="button" className="button button--secondary" onClick={() => setEditing(r)}>
                  Editar {r.name}
                </button>
                <Inativar ctx={ctx} reward={r} onDone={reload} />
              </div>
            )}
          </li>
        ))}
      </ul>
    </section>
  );
}

function Inativar({
  ctx,
  reward,
  onDone,
}: {
  ctx: StaffContext;
  reward: Reward;
  onDone: () => Promise<void>;
}) {
  const cmd = useCommand();
  async function deactivate() {
    const out = await cmd.run(`inativar:${reward.id}:${reward.version}`, (key) =>
      api.POST("/v1/establishments/{establishmentId}/rewards/{rewardId}/deactivate", {
        params: {
          path: { establishmentId: ctx.estId, rewardId: reward.id },
          header: { "Idempotency-Key": key },
        },
        body: { expected_version: reward.version },
      }),
    );
    if (out.ok || out.problem?.status === 409) await onDone();
  }
  return (
    <>
      <CommandError problem={cmd.problem} offline={cmd.offline} />
      <button
        type="button"
        className="button button--secondary"
        disabled={cmd.busy !== null}
        onClick={() => void deactivate()}
      >
        Inativar {reward.name}
      </button>
    </>
  );
}

function PremioForm({
  ctx,
  reward,
  onDone,
}: {
  ctx: StaffContext;
  reward: Reward | null;
  onDone: () => Promise<void>;
}) {
  const [name, setName] = useState(reward?.name ?? "");
  const [description, setDescription] = useState(reward?.description ?? "");
  const [instructions, setInstructions] = useState(
    reward?.usage_instructions ?? "Peça na recepção. A cortesia não dá prioridade na fila.",
  );
  const [cost, setCost] = useState(reward ? String(reward.cost_points) : "");
  const [limited, setLimited] = useState(reward?.stock.kind === "limited");
  const [units, setUnits] = useState(String(reward?.stock.units_available ?? 0));
  const cmd = useCommand();
  const errors: Record<string, string> = {};
  if (!/^\d+$/.test(cost) || Number(cost) < 1) errors.cost = "Use um número inteiro de pontos (1 ou mais).";
  if (limited && !/^\d+$/.test(units)) errors.units = "Use um número inteiro (0 ou mais).";
  const ready = name.trim() && description.trim() && instructions.trim() && Object.keys(errors).length === 0;

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    if (!ready) return;
    const stock = limited
      ? { kind: "limited" as const, units_available: Number(units) }
      : { kind: "unlimited" as const };
    const fields = { name, description, usage_instructions: instructions, cost_points: Number(cost), stock };
    const out = reward
      ? await cmd.run(`editar-premio:${reward.id}:${reward.version}`, (key) =>
          api.PATCH("/v1/establishments/{establishmentId}/rewards/{rewardId}", {
            params: {
              path: { establishmentId: ctx.estId, rewardId: reward.id },
              header: { "Idempotency-Key": key },
            },
            body: { expected_version: reward.version, ...fields },
          }),
        )
      : await cmd.run(`novo-premio:${name}:${cost}`, (key) =>
          api.POST("/v1/establishments/{establishmentId}/rewards", {
            params: { path: { establishmentId: ctx.estId }, header: { "Idempotency-Key": key } },
            body: fields,
          }),
        );
    if (out.ok) await onDone();
  }
  return (
    <form
      noValidate
      aria-label={reward ? `Editar ${reward.name}` : "Novo prêmio"}
      onSubmit={onSubmit}
      className="card"
    >
      <CommandError problem={cmd.problem} offline={cmd.offline} />
      <Field label="Nome" required maxLength={60} value={name} onChange={(e) => setName(e.target.value)} />
      <Field
        label="Descrição"
        required
        maxLength={500}
        value={description}
        onChange={(e) => setDescription(e.target.value)}
      />
      <Field
        label="Instruções de uso"
        required
        maxLength={500}
        value={instructions}
        onChange={(e) => setInstructions(e.target.value)}
      />
      <Field
        label="Custo em pontos"
        inputMode="numeric"
        required
        value={cost}
        error={cost ? errors.cost : undefined}
        onChange={(e) => setCost(e.target.value)}
      />
      <label className="choice">
        <input type="checkbox" checked={limited} onChange={(e) => setLimited(e.target.checked)} />
        Quantidade limitada
      </label>
      {limited && (
        <Field
          label="Unidades disponíveis"
          inputMode="numeric"
          value={units}
          error={errors.units}
          onChange={(e) => setUnits(e.target.value)}
          hint="Reservas ativas já descontaram as suas unidades."
        />
      )}
      {reward && <p className="muted">Quem já reservou mantém as condições aceitas.</p>}
      <div className="actions">
        <Submit busy={cmd.busy !== null}>{reward ? "Salvar prêmio" : "Criar prêmio"}</Submit>
        <button type="button" className="button button--secondary" onClick={() => void onDone()}>
          Voltar
        </button>
      </div>
    </form>
  );
}
