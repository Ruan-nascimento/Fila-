import { useCallback, useEffect, useState, type FormEvent } from "react";
import { Check, Gift, Pencil, Plus, Power } from "lucide-react";
import { api } from "../../api/client";
import { call, problemMessage } from "../../api/problem";
import { useStreamMessages } from "../../stream/context";
import {
  Badge,
  Button,
  Choice,
  EmptyState,
  Field,
  LoadError,
  PageHeader,
  Section,
  Segmented,
  Submit,
  TextArea,
} from "../../ui/ui";
import { useInlinePanel } from "../../ui/useInlinePanel";
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
    <div className="grid gap-8">
      <PageHeader
        title="Prêmios"
        description="Reservas para entregar no balcão. Prêmio é cortesia: não muda a comanda nem a fila."
      />
      <Reservas ctx={ctx} />
      {ctx.isOwner && (
        <div className="grid gap-8 border-t border-line pt-6">
          <Programa ctx={ctx} />
          <Catalogo ctx={ctx} />
        </div>
      )}
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
    if (out.ok) {
      setList(out.data.data);
      setError(null);
    } else setError(problemMessage(out.problem, out.offline));
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
    <Section title="Reservas de prêmio">
      <div className="max-w-md">
        <Segmented
          legend="Mostrar"
          name="filtro-reservas"
          value={filter}
          onChange={setFilter}
          options={[
            { value: "active", label: "Para entregar" },
            { value: "delivered", label: "Entregues" },
            { value: "", label: "Todas" },
          ]}
        />
      </div>
      {error && <LoadError message={error} onRetry={() => void reload()} />}
      {list?.length === 0 && (
        <EmptyState icon={Gift} title="Nenhuma reserva aqui">
          <p>{filter === "active" ? "Nenhum prêmio esperando entrega." : "Nada nesta lista."}</p>
        </EmptyState>
      )}
      <ul className="m-0 grid list-none gap-3 p-0 md:grid-cols-2">
        {(list ?? []).map((r) => (
          <li key={r.id} className="grid gap-3 rounded-xl border border-line bg-surface p-4">
            <div className="flex items-start gap-3">
              <span className="inline-flex size-11 shrink-0 items-center justify-center rounded-xl bg-surface-2 text-fg-muted">
                <Gift aria-hidden="true" className="size-5" />
              </span>
              <div className="grid min-w-0 flex-1 gap-0.5">
                <h3 className="m-0 text-base font-semibold">
                  {r.accepted_terms.name} — {r.holder_display_name ?? "Cliente"}
                </h3>
                <p className="m-0 text-sm text-fg-muted tabular-nums">
                  {r.accepted_terms.cost_points} pontos · reservado em {when.format(new Date(r.created_at))}
                  {r.status === "active" && <> · vence em {when.format(new Date(r.expires_at))}</>}
                </p>
              </div>
              <Badge
                tone={r.status === "active" ? "primary" : r.status === "delivered" ? "success" : "neutral"}
              >
                {reservationStatusText[r.status]}
              </Badge>
            </div>
            <p className="m-0 text-sm">{r.accepted_terms.usage_instructions}</p>
            {r.status === "active" && (
              <Entregar ctx={ctx} reservation={r} onSaved={replace} onConflict={reload} />
            )}
            {ctx.isOwner && r.status === "delivered" && (
              <Corrigir ctx={ctx} reservation={r} onSaved={replace} onConflict={reload} />
            )}
          </li>
        ))}
      </ul>
    </Section>
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
      <Button icon={Check} disabled={cmd.busy !== null} onClick={() => void deliver()}>
        Confirmar entrega de {reservation.accepted_terms.name}
      </Button>
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
  const panel = useInlinePanel();
  const [reason, setReason] = useState("");
  const [tried, setTried] = useState(false);
  const cmd = useCommand();
  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setTried(true);
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
  if (!panel.open)
    return (
      <Button
        id={panel.triggerId}
        variant="secondary"
        className="justify-self-start"
        onClick={() => panel.show("aberto")}
      >
        Corrigir entrega
      </Button>
    );
  return (
    <form
      id={panel.panelId}
      onKeyDown={panel.onKeyDown}
      noValidate
      aria-label="Corrigir entrega"
      onSubmit={onSubmit}
      className="grid gap-3 rounded-xl border border-line bg-surface-2 p-4"
    >
      <CommandError problem={cmd.problem} offline={cmd.offline} />
      <TextArea
        label="Motivo da correção"
        hint="Os pontos e a unidade voltam para o cliente."
        required
        minLength={3}
        maxLength={500}
        rows={2}
        value={reason}
        error={tried && reason.trim().length < 3 ? "Escreva o motivo (pelo menos 3 caracteres)." : undefined}
        onChange={(e) => setReason(e.target.value)}
      />
      <div className="flex flex-wrap gap-2">
        <Submit busy={cmd.busy !== null}>Desfazer entrega</Submit>
        <Button variant="secondary" onClick={panel.hide}>
          Voltar
        </Button>
      </div>
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
    <section aria-labelledby="titulo-programa" className="grid max-w-2xl gap-3">
      <div className="flex flex-wrap items-center gap-2">
        <h2 id="titulo-programa" className="m-0 text-lg font-semibold">
          Programa de pontos
        </h2>
        <Badge tone={current.enabled ? "success" : "neutral"} dot>
          <span role="status">{current.enabled ? "Ligado" : "Desligado"}</span>
        </Badge>
      </div>
      <p className="m-0 text-sm text-fg-muted">
        Vale para novas visitas e novas reservas. Desligar não apaga saldos, não retira pontos já prometidos e
        mantém as reservas feitas. Os pontos de cada serviço ficam em Serviços.
      </p>
      <CommandError problem={cmd.problem} offline={cmd.offline} />
      <Button
        variant={current.enabled ? "secondary" : "primary"}
        icon={Power}
        className="justify-self-start"
        disabled={cmd.busy !== null}
        onClick={() => void toggle()}
      >
        {current.enabled ? "Desligar programa" : "Ligar programa"}
      </Button>
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
    <Section
      title="Prêmios cadastrados"
      action={
        !editing && (
          <Button icon={Plus} onClick={() => setEditing("new")}>
            Novo prêmio
          </Button>
        )
      }
    >
      {editing && (
        <PremioForm
          ctx={ctx}
          reward={editing === "new" ? null : editing}
          onDone={async () => {
            setEditing(null);
            await reload();
          }}
        />
      )}
      {list.length === 0 && !editing && (
        <EmptyState icon={Gift} title="Nenhum prêmio cadastrado">
          <p>Cadastre cortesias que os clientes podem reservar com pontos.</p>
        </EmptyState>
      )}
      {list.length > 0 && (
        <ul className="m-0 grid list-none divide-y divide-line rounded-xl border border-line bg-surface p-0">
          {list.map((r) => (
            <li key={r.id} className="grid gap-2 px-4 py-3">
              <div className="flex flex-wrap items-start justify-between gap-2">
                <div className="grid min-w-0 gap-0.5">
                  <h3 className="m-0 text-base font-semibold">
                    {r.name} — {r.cost_points} pontos {r.status === "inactive" && "(inativo)"}
                  </h3>
                  <p className="m-0 text-sm text-fg-muted">{r.description}</p>
                </div>
                <Badge tone={r.status === "active" ? "neutral" : "neutral"}>Estoque: {stockText(r)}</Badge>
              </div>
              <p className="m-0 text-sm text-fg-muted">{r.usage_instructions}</p>
              {r.status === "active" && (
                <div className="flex flex-wrap gap-2">
                  <Button variant="secondary" icon={Pencil} onClick={() => setEditing(r)}>
                    Editar {r.name}
                  </Button>
                  <Inativar ctx={ctx} reward={r} onDone={reload} />
                </div>
              )}
            </li>
          ))}
        </ul>
      )}
    </Section>
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
      <Button variant="ghost" disabled={cmd.busy !== null} onClick={() => void deactivate()}>
        Inativar {reward.name}
      </Button>
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
  const [tried, setTried] = useState(false);
  const cmd = useCommand();
  const errors: Record<string, string> = {};
  if (!/^\d+$/.test(cost) || Number(cost) < 1) errors.cost = "Use um número inteiro de pontos (1 ou mais).";
  if (limited && !/^\d+$/.test(units)) errors.units = "Use um número inteiro (0 ou mais).";
  const ready = name.trim() && description.trim() && instructions.trim() && Object.keys(errors).length === 0;

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setTried(true);
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
  const required = (v: string) => (tried && !v.trim() ? "Preencha este campo." : undefined);
  return (
    <form
      noValidate
      aria-label={reward ? `Editar ${reward.name}` : "Novo prêmio"}
      onSubmit={onSubmit}
      className="grid max-w-2xl gap-4 rounded-xl border border-line bg-surface p-4 sm:p-5"
    >
      <h3 className="m-0 text-base font-semibold">{reward ? `Editar ${reward.name}` : "Novo prêmio"}</h3>
      <CommandError problem={cmd.problem} offline={cmd.offline} />
      <Field
        label="Nome"
        required
        maxLength={60}
        value={name}
        error={required(name)}
        onChange={(e) => setName(e.target.value)}
      />
      <TextArea
        label="Descrição"
        required
        maxLength={500}
        rows={2}
        value={description}
        error={required(description)}
        onChange={(e) => setDescription(e.target.value)}
      />
      <TextArea
        label="Instruções de uso"
        required
        maxLength={500}
        rows={2}
        value={instructions}
        error={required(instructions)}
        onChange={(e) => setInstructions(e.target.value)}
      />
      <div className="grid gap-4 sm:grid-cols-2 sm:items-start">
        <Field
          label="Custo em pontos"
          inputMode="numeric"
          required
          value={cost}
          error={cost || tried ? errors.cost : undefined}
          onChange={(e) => setCost(e.target.value)}
        />
        <div className="sm:pt-7">
          <Choice
            type="checkbox"
            checked={limited}
            onChange={(e) => setLimited(e.target.checked)}
            label="Quantidade limitada"
          />
        </div>
      </div>
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
      {reward && <p className="m-0 text-sm text-fg-muted">Quem já reservou mantém as condições aceitas.</p>}
      <div className="flex flex-wrap gap-2">
        <Submit busy={cmd.busy !== null}>{reward ? "Salvar prêmio" : "Criar prêmio"}</Submit>
        <Button variant="secondary" onClick={() => void onDone()}>
          Voltar
        </Button>
      </div>
    </form>
  );
}
