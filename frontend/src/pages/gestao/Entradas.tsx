import { useState, type FormEvent } from "react";
import {
  ArrowDown,
  ArrowLeftRight,
  ArrowUp,
  Ban,
  ChevronDown,
  ChevronUp,
  Clock,
  Play,
  Scissors,
  UserX,
} from "lucide-react";
import { api } from "../../api/client";
import type { components } from "../../api/schema.gen";
import { Avatar, Badge, Button, Card, Choice, SelectField, Submit, TextArea } from "../../ui/ui";
import { cx } from "../../ui/cx";
import { useInlinePanel } from "../../ui/useInlinePanel";
import { Atendimento } from "./Atendimento";
import { useCommand } from "./comando";
import { CommandError, type StaffContext } from "./comum";

type StaffQueue = components["schemas"]["StaffQueue"];
type Entry = StaffQueue["entries"][number];
type Direcao = "up" | "down";

const time = new Intl.DateTimeFormat("pt-BR", { timeStyle: "short" });

/**
 * Pessoas de uma fila e as ações da equipe. Ordem de leitura (e no celular, de cima para baixo):
 * quem está chamado ou em atendimento e a ação disponível; depois quem aguarda. Em telas largas
 * as duas partes ficam lado a lado. A tela só mostra os botões que fazem sentido; o servidor
 * confere papel, estado, versão e o prazo de tolerância.
 */
export function Entradas({
  ctx,
  queue,
  queues,
  now,
  onDone,
}: {
  ctx: StaffContext;
  queue: StaffQueue;
  queues: StaffQueue[];
  now: Date;
  onDone: () => Promise<void>;
}) {
  const cmd = useCommand();
  const canOperate = ctx.isOwner || ctx.professionalId === queue.professional.id;
  const current = queue.entries.find((e) => e.status === "called" || e.status === "in_service");
  const waiting = queue.entries.filter((e) => e.status === "waiting");
  const busy = Boolean(current);

  async function chamar() {
    const out = await cmd.run(
      `chamar:${queue.queue_id}:${queue.entries.map((e) => e.version).join(".")}`,
      (key) =>
        api.POST("/v1/establishments/{establishmentId}/queues/{queueId}/call-next", {
          params: {
            path: { establishmentId: ctx.estId, queueId: queue.queue_id },
            header: { "Idempotency-Key": key },
          },
        }),
    );
    if (out.ok || out.problem?.status === 409) await onDone();
  }

  async function acao(e: Entry, op: "start" | "no-show") {
    const path =
      op === "start"
        ? ("/v1/establishments/{establishmentId}/participations/{participationId}/start" as const)
        : ("/v1/establishments/{establishmentId}/participations/{participationId}/no-show" as const);
    const out = await cmd.run(`${op}:${e.participation_id}:${e.version}`, (key) =>
      api.POST(path, {
        params: {
          path: { establishmentId: ctx.estId, participationId: e.participation_id },
          header: { "Idempotency-Key": key },
        },
        body: { expected_version: e.version },
      }),
    );
    if (out.ok || out.problem?.status === 409) await onDone();
  }

  // Troca a pessoa de lugar com a vizinha (o servidor confere papel, estado, versão e a fila).
  async function mover(e: Entry, direction: Direcao) {
    const out = await cmd.run(`mover:${e.participation_id}:${e.version}:${direction}`, (key) =>
      api.POST("/v1/establishments/{establishmentId}/participations/{participationId}/move", {
        params: {
          path: { establishmentId: ctx.estId, participationId: e.participation_id },
          header: { "Idempotency-Key": key },
        },
        body: { expected_version: e.version, direction },
      }),
    );
    if (out.ok || out.problem?.status === 409) await onDone();
  }
  const calledNow = current?.status === "called";
  // Trocar com a pessoa chamada é uma nova chamada: só com a fila aberta.
  const canSwapCall = queue.status === "open";

  // Por que não dá para chamar agora (sempre explicado em texto).
  const callBlocked = busy
    ? "A cadeira está ocupada: conclua ou resolva o atendimento atual antes de chamar."
    : queue.status === "paused"
      ? "Fila pausada: chamadas bloqueadas até o dono reabrir."
      : queue.status === "closed"
        ? "Fila fechada."
        : waiting.length === 0
          ? "Ninguém aguardando."
          : null;

  return (
    <div className="@container">
      <CommandError problem={cmd.problem} offline={cmd.offline} />
      <div className="mt-3 grid gap-5 @3xl:grid-cols-[minmax(15rem,20rem)_minmax(0,1fr)] @3xl:items-start">
        {/* Chamado / em atendimento: primeiro na leitura; à direita em telas largas */}
        <section aria-labelledby="titulo-atual" className="grid gap-3 @3xl:col-start-2 @3xl:row-start-1">
          <h2 id="titulo-atual" className="sr-only">
            Atendimento atual
          </h2>
          {current ? (
            <Atual
              ctx={ctx}
              entry={current}
              queue={queue}
              queues={queues}
              now={now}
              canOperate={canOperate}
              busyCmd={cmd.busy !== null}
              onAction={acao}
              onDone={onDone}
              onMove={canSwapCall && waiting.length > 0 ? (d) => void mover(current, d) : undefined}
            />
          ) : (
            <Card className="grid justify-items-start gap-2 p-5">
              <p className="m-0 text-sm text-fg-muted">Cadeira livre</p>
              <p className="m-0 text-lg font-semibold">
                {waiting[0]
                  ? `Próximo: ${waiting[0].ticket_code} · ${waiting[0].nick}`
                  : "Ninguém chamado no momento"}
              </p>
              {waiting[0] && (
                <p className="m-0 text-sm text-fg-muted">{waiting[0].service_names.join(" + ")}</p>
              )}
            </Card>
          )}
        </section>

        {/* Aguardando */}
        <section aria-labelledby="titulo-aguardando" className="grid gap-3 @3xl:col-start-1 @3xl:row-start-1">
          <div className="flex items-center justify-between gap-2">
            <h2 id="titulo-aguardando" className="m-0 text-base font-semibold">
              Aguardando <span className="font-normal text-fg-muted tabular-nums">· {waiting.length}</span>
            </h2>
          </div>
          {canOperate && queue.status !== "closed" && (
            <div className="grid gap-1.5">
              <Button
                icon={Play}
                block
                disabled={cmd.busy !== null || callBlocked !== null}
                onClick={() => void chamar()}
              >
                Chamar próximo
              </Button>
              {callBlocked && <p className="m-0 text-sm text-fg-muted">{callBlocked}</p>}
            </div>
          )}
          {waiting.length === 0 ? (
            <p className="m-0 rounded-xl border border-dashed border-line px-4 py-6 text-center text-sm text-fg-muted">
              Ninguém na fila.
            </p>
          ) : (
            <ol className="m-0 grid list-none gap-2 p-0">
              {waiting.map((e, i) => (
                <Aguardando
                  key={e.participation_id}
                  ctx={ctx}
                  entry={e}
                  position={i + 1}
                  queue={queue}
                  queues={queues}
                  canOperate={canOperate}
                  onDone={onDone}
                  busyCmd={cmd.busy !== null}
                  canUp={i > 0 || (calledNow && canSwapCall)}
                  canDown={i < waiting.length - 1}
                  onMove={(d) => void mover(e, d)}
                />
              ))}
            </ol>
          )}
        </section>
      </div>
    </div>
  );
}

function Atual({
  ctx,
  entry: e,
  queue,
  queues,
  now,
  canOperate,
  busyCmd,
  onAction,
  onDone,
  onMove,
}: {
  ctx: StaffContext;
  entry: Entry;
  queue: StaffQueue;
  queues: StaffQueue[];
  now: Date;
  canOperate: boolean;
  busyCmd: boolean;
  onAction: (e: Entry, op: "start" | "no-show") => Promise<void>;
  onDone: () => Promise<void>;
  onMove?: ((d: Direcao) => void) | undefined;
}) {
  const called = e.status === "called";
  const tolerance = e.tolerance_ends_at ? new Date(e.tolerance_ends_at) : null;
  const toleranceOver = tolerance ? now >= tolerance : false;
  return (
    <Card className={cx("grid gap-4 p-5", called && "border-primary/60")}>
      <div className="grid gap-1">
        <Badge tone="primary" icon={called ? Clock : Scissors}>
          {called ? "Chamado" : "Em atendimento"}
        </Badge>
        <p className="m-0 flex items-center gap-3 text-3xl font-bold tracking-tight">
          {e.customer_avatar && (
            <Avatar name={e.nick} image={e.customer_avatar} size={48} className="size-12" />
          )}
          <span>
            {e.ticket_code} · <span className="break-words">{e.nick}</span>
          </span>
        </p>
        <p className="m-0 text-fg-muted">{e.service_names.join(" + ")}</p>
        {e.has_pending_change_request && (
          <Badge tone="warning" className="mt-1">
            Pedido de alteração aguardando
          </Badge>
        )}
        {called && tolerance && (
          <p className="m-0 mt-1 text-sm">
            Tolerância até <strong className="tabular-nums">{time.format(tolerance)}</strong>
          </p>
        )}
      </div>
      {canOperate && called && (
        <div className="grid gap-2">
          <Button icon={Scissors} block disabled={busyCmd} onClick={() => void onAction(e, "start")}>
            Iniciar atendimento
          </Button>
          <Button
            variant="secondary"
            icon={UserX}
            block
            disabled={busyCmd || !toleranceOver}
            aria-describedby={toleranceOver ? undefined : `ausencia-${e.participation_id}`}
            onClick={() => void onAction(e, "no-show")}
          >
            Marcar ausência
          </Button>
          {!toleranceOver && tolerance && (
            <p id={`ausencia-${e.participation_id}`} className="m-0 text-center text-sm text-fg-muted">
              Disponível a partir de {time.format(tolerance)}.
            </p>
          )}
          {onMove && (
            <Button
              variant="secondary"
              icon={ArrowDown}
              block
              disabled={busyCmd}
              onClick={() => onMove("down")}
            >
              Vai atrasar: chamar o próximo
            </Button>
          )}
        </div>
      )}
      {canOperate && (e.status === "in_service" || called) && (
        <ComandaEntrada ctx={ctx} entry={e} onDone={onDone} defaultOpen={e.status === "in_service"} />
      )}
      {ctx.isOwner && called && (
        <DonoAcoes ctx={ctx} entry={e} queue={queue} queues={queues} onDone={onDone} />
      )}
    </Card>
  );
}

function Aguardando({
  ctx,
  entry: e,
  position,
  queue,
  queues,
  canOperate,
  onDone,
  busyCmd,
  canUp,
  canDown,
  onMove,
}: {
  ctx: StaffContext;
  entry: Entry;
  position: number;
  queue: StaffQueue;
  queues: StaffQueue[];
  canOperate: boolean;
  onDone: () => Promise<void>;
  busyCmd: boolean;
  canUp: boolean;
  canDown: boolean;
  onMove: (d: Direcao) => void;
}) {
  return (
    <li className="grid gap-2 rounded-xl border border-line bg-surface p-3">
      <div className="flex items-start gap-3">
        <span
          aria-hidden="true"
          className="inline-flex size-8 shrink-0 items-center justify-center rounded-lg bg-surface-2 text-sm font-semibold tabular-nums"
        >
          {position}
        </span>
        {/* Foto pessoal: só para a equipe e só com a participação ativa (o servidor confere). */}
        {e.customer_avatar && <Avatar name={e.nick} image={e.customer_avatar} size={32} className="size-8" />}
        <div className="grid min-w-0 flex-1 gap-0.5">
          <p className="m-0 font-semibold">
            {e.ticket_code} · <span className="break-words">{e.nick}</span>
          </p>
          <p className="m-0 text-sm text-fg-muted">{e.service_names.join(" + ")}</p>
          {e.has_pending_change_request && <Badge tone="warning">Pedido de alteração aguardando</Badge>}
        </div>
        {canOperate && (
          <div className="flex shrink-0 gap-1">
            <MoveButton
              icon={ArrowUp}
              label={
                position === 1
                  ? `Subir ${e.ticket_code} · ${e.nick} e chamar agora`
                  : `Subir ${e.ticket_code} · ${e.nick} uma posição`
              }
              disabled={busyCmd || !canUp}
              onClick={() => onMove("up")}
            />
            <MoveButton
              icon={ArrowDown}
              label={`Descer ${e.ticket_code} · ${e.nick} uma posição`}
              disabled={busyCmd || !canDown}
              onClick={() => onMove("down")}
            />
          </div>
        )}
      </div>
      {canOperate && <ComandaEntrada ctx={ctx} entry={e} onDone={onDone} />}
      {ctx.isOwner && <DonoAcoes ctx={ctx} entry={e} queue={queue} queues={queues} onDone={onDone} />}
    </li>
  );
}

/** Seta de subir/descer na fila (alvo de toque de 44 px; o nome completo vai no rótulo acessível). */
function MoveButton({
  icon: Icon,
  label,
  disabled,
  onClick,
}: {
  icon: typeof ArrowUp;
  label: string;
  disabled: boolean;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      aria-label={label}
      title={label}
      disabled={disabled}
      onClick={onClick}
      className="inline-flex size-11 items-center justify-center rounded-lg border border-line text-fg hover:bg-surface-2 disabled:cursor-not-allowed disabled:opacity-40"
    >
      <Icon aria-hidden="true" className="size-5" />
    </button>
  );
}

/** Comanda da pessoa, aberta sob demanda (em atendimento já vem aberta). */
function ComandaEntrada({
  ctx,
  entry,
  onDone,
  defaultOpen = false,
}: {
  ctx: StaffContext;
  entry: Entry;
  onDone: () => Promise<void>;
  defaultOpen?: boolean;
}) {
  // null = segue o padrão (aberta em atendimento); depois do primeiro clique vale a escolha da pessoa.
  const [choice, setChoice] = useState<boolean | null>(null);
  const shown = choice ?? (defaultOpen || entry.status === "in_service");
  const panelId = `comanda-${entry.participation_id}`;
  return (
    <div className={cx("grid gap-2", shown && "border-t border-line pt-3")}>
      <button
        type="button"
        aria-expanded={shown}
        aria-controls={shown ? panelId : undefined}
        onClick={() => setChoice(!shown)}
        className="inline-flex min-h-11 w-fit items-center gap-1 rounded-lg px-2 text-sm font-semibold text-accent hover:bg-select"
      >
        {shown ? (
          <ChevronUp aria-hidden="true" className="size-4" />
        ) : (
          <ChevronDown aria-hidden="true" className="size-4" />
        )}
        {shown ? `Ocultar comanda de ${entry.ticket_code}` : `Ver comanda de ${entry.ticket_code}`}
      </button>
      {shown && (
        <div id={panelId}>
          <Atendimento ctx={ctx} entry={entry} onDone={onDone} />
        </div>
      )}
    </div>
  );
}

/** Cancelar (com motivo) e transferir (com consentimento) — só o dono. */
function DonoAcoes({
  ctx,
  entry,
  queue,
  queues,
  onDone,
}: {
  ctx: StaffContext;
  entry: Entry;
  queue: StaffQueue;
  queues: StaffQueue[];
  onDone: () => Promise<void>;
}) {
  const cmd = useCommand();
  const panel = useInlinePanel<"cancelar" | "transferir">();
  const mode = panel.open;
  const [reason, setReason] = useState("");
  const [target, setTarget] = useState("");
  const [consent, setConsent] = useState(false);
  const [tried, setTried] = useState(false);
  const targets = queues.filter(
    (q) => q.queue_id !== queue.queue_id && q.status === "open" && q.professional.status === "active",
  );
  const base = { establishmentId: ctx.estId, participationId: entry.participation_id };
  const ready = reason.trim().length >= 3 && (mode === "cancelar" || (target !== "" && consent));

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setTried(true);
    // Sem motivo, ou transferência sem destino ou sem a concordância marcada, nada é enviado.
    if (!ready) return;
    const out =
      mode === "cancelar"
        ? await cmd.run(`cancelar:${entry.participation_id}:${entry.version}`, (key) =>
            api.POST("/v1/establishments/{establishmentId}/participations/{participationId}/cancel", {
              params: { path: base, header: { "Idempotency-Key": key } },
              body: { expected_version: entry.version, reason },
            }),
          )
        : await cmd.run(`transferir:${entry.participation_id}:${entry.version}:${target}`, (key) =>
            api.POST("/v1/establishments/{establishmentId}/participations/{participationId}/transfer", {
              params: { path: base, header: { "Idempotency-Key": key } },
              body: {
                expected_version: entry.version,
                target_queue_id: target,
                reason,
                customer_consent: { given: true, method: "in_person_verbal" },
              },
            }),
          );
    if (out.ok) {
      panel.hide();
      setReason("");
      setTried(false);
      await onDone();
    } else if (out.problem?.status === 409) await onDone();
  }

  if (!mode) {
    return (
      <div className="flex flex-wrap gap-2">
        {entry.status === "waiting" && targets.length > 0 && (
          <Button
            id={panel.triggerId}
            variant="secondary"
            icon={ArrowLeftRight}
            onClick={() => panel.show("transferir")}
          >
            Transferir
          </Button>
        )}
        <Button variant="danger-ghost" icon={Ban} onClick={() => panel.show("cancelar")}>
          Cancelar participação
        </Button>
      </div>
    );
  }
  const targetName = targets.find((q) => q.queue_id === target)?.professional.display_name;
  return (
    <form
      id={panel.panelId}
      onKeyDown={panel.onKeyDown}
      onSubmit={onSubmit}
      noValidate
      className="grid gap-4 rounded-xl border border-line bg-surface-2 p-4"
      aria-label={mode === "cancelar" ? "Cancelar participação" : "Transferir participação"}
    >
      <p className="m-0 font-semibold">
        {mode === "cancelar"
          ? `Cancelar ${entry.ticket_code} · ${entry.nick}`
          : `Transferir ${entry.ticket_code} · ${entry.nick}`}
      </p>
      <CommandError problem={cmd.problem} offline={cmd.offline} />
      {mode === "transferir" && (
        <>
          <SelectField label="Fila de destino" value={target} onChange={(e) => setTarget(e.target.value)}>
            <option value="">Escolha…</option>
            {targets.map((q) => (
              <option key={q.queue_id} value={q.queue_id}>
                {q.professional.display_name}
              </option>
            ))}
          </SelectField>
          <p className="m-0 rounded-lg bg-surface px-3 py-2 text-sm">
            {targetName
              ? `${entry.nick} vai para o fim da fila de ${targetName}`
              : "A pessoa vai para o fim da fila escolhida"}
            , recebe outra senha e mantém os preços registrados na comanda.
          </p>
          <Choice
            type="checkbox"
            checked={consent}
            onChange={(e) => setConsent(e.target.checked)}
            label="O cliente concordou pessoalmente com a transferência"
          />
        </>
      )}
      {mode === "cancelar" && (
        <p className="m-0 text-sm text-fg-muted">
          A pessoa sai da fila e a comanda é encerrada sem cobrança.
        </p>
      )}
      <TextArea
        label="Motivo"
        required
        minLength={3}
        maxLength={500}
        rows={2}
        value={reason}
        onChange={(e) => setReason(e.target.value)}
      />
      {tried && !ready && (
        <p role="alert" className="m-0 text-sm font-semibold text-danger">
          Preencha o motivo{mode === "transferir" ? ", o destino e a concordância" : ""} para confirmar.
        </p>
      )}
      <div className="flex flex-wrap gap-2">
        <Submit busy={cmd.busy !== null} variant={mode === "cancelar" ? "danger" : "primary"}>
          {mode === "cancelar" ? "Confirmar cancelamento" : "Confirmar transferência"}
        </Submit>
        <Button variant="secondary" onClick={panel.hide}>
          Voltar
        </Button>
      </div>
    </form>
  );
}
