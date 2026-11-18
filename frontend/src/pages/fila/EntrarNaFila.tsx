import { useState, type FormEvent } from "react";
import { Clock, PauseCircle, Scissors, SquareX, UsersRound, X } from "lucide-react";
import { api } from "../../api/client";
import type { components } from "../../api/schema.gen";
import { formatCents } from "../../money";
import { Choice, ChoiceGroup, EmptyState, LoadError, Loading, Notice, Submit } from "../../ui/ui";
import { cx } from "../../ui/cx";
import { useCommand } from "../gestao/comando";
import { CommandError } from "../gestao/comum";
import { useStoreData } from "./dadosContexto";
import { queueLine, servicesFor } from "./selecao";

type Visit = components["schemas"]["Visit"];

const queueIcon = { open: Clock, paused: PauseCircle, closed: SquareX } as const;

/**
 * Catálogo e entrada na fila numa lista só: o visitante vê os profissionais e a situação de cada
 * fila; ao escolher um, os serviços dele viram opções com duração e preço, e o total estimado fica
 * junto de "Entrar na fila". O navegador envia só a fila e os serviços: preço e total vêm do
 * servidor (R-CMD-02). Sem identificação, o botão leva ao apelido e a escolha é mantida.
 */
export function EntrarNaFila({
  identified,
  onNeedIdentity,
  onEntered,
}: {
  identified: boolean;
  onNeedIdentity?: () => void;
  onEntered?: (v: Visit) => void;
}) {
  const data = useStoreData();
  const { queues, services, selection, setSelection } = data;
  const cmd = useCommand();
  const [missing, setMissing] = useState(false);

  const queue = queues?.find((q) => q.queue_id === selection.queueId);
  const offered = services ? servicesFor(services, queue) : [];
  const chosen = offered.filter((s) => selection.serviceIds.includes(s.id));
  const total = chosen.reduce((sum, s) => sum + s.price.amount_cents, 0);
  const duration = chosen.reduce((sum, s) => sum + s.duration_minutes, 0);
  const anyOpen = (queues ?? []).some((q) => q.status === "open");

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    if (!selection.queueId || selection.serviceIds.length === 0) {
      setMissing(true);
      return;
    }
    setMissing(false);
    if (!identified) {
      onNeedIdentity?.();
      return;
    }
    const body = { queue_id: selection.queueId, service_ids: selection.serviceIds };
    const out = await cmd.run(`entrar:${body.queue_id}:${[...body.service_ids].sort().join(",")}`, (key) =>
      api.POST("/v1/establishments/{establishmentId}/participations", {
        params: { path: { establishmentId: data.shopId }, header: { "Idempotency-Key": key } },
        body,
      }),
    );
    if (out.ok) {
      setSelection({ queueId: "", serviceIds: [] });
      onEntered?.(out.data.data);
    } else if (out.problem?.status === 409) {
      data.retryQueues();
    }
  }

  return (
    <form onSubmit={onSubmit} noValidate aria-labelledby="titulo-entrar-fila" className="grid gap-6">
      <h2 id="titulo-entrar-fila" className="sr-only">
        Entrar na fila
      </h2>
      {data.selectionNote && (
        <div className="relative">
          <Notice kind="aviso">
            <p className="pr-8">{data.selectionNote}</p>
          </Notice>
          <button
            type="button"
            onClick={data.clearSelectionNote}
            aria-label="Fechar aviso"
            className="absolute top-1 right-1 inline-flex size-11 items-center justify-center rounded-lg text-fg-muted hover:text-fg"
          >
            <X aria-hidden="true" className="size-4" />
          </button>
        </div>
      )}

      {/* Profissionais e situação das filas */}
      <div className="grid gap-2">
        {queues === null ? (
          data.queuesError ? (
            <>
              <h3 className="m-0 text-sm font-semibold">Profissionais</h3>
              <LoadError
                message={`Não foi possível ver as filas agora. ${data.queuesError}`}
                onRetry={data.retryQueues}
              />
            </>
          ) : (
            <>
              <h3 className="m-0 text-sm font-semibold">Profissionais</h3>
              <Loading>Carregando filas…</Loading>
            </>
          )
        ) : queues.length === 0 ? (
          <>
            <h3 className="m-0 text-sm font-semibold">Profissionais</h3>
            <EmptyState icon={UsersRound} title="Nenhuma fila aberta no momento">
              <p>A barbearia ainda não iniciou o atendimento por fila.</p>
            </EmptyState>
          </>
        ) : (
          <ChoiceGroup
            legend="Profissional"
            hint={
              anyOpen ? undefined : "Nenhuma fila aberta no momento. Volte mais tarde ou fale com a loja."
            }
          >
            {queues.map((q) => {
              const Icon = queueIcon[q.status];
              return (
                <Choice
                  key={q.queue_id}
                  type="radio"
                  name="profissional"
                  value={q.queue_id}
                  disabled={q.status !== "open"}
                  checked={selection.queueId === q.queue_id}
                  onChange={() => {
                    setMissing(false);
                    setSelection({ queueId: q.queue_id, serviceIds: selection.serviceIds });
                  }}
                  label={q.professional.display_name}
                  description={
                    <span className="inline-flex items-center gap-1.5">
                      <Icon aria-hidden="true" className="size-4 shrink-0" />
                      {queueLine(q)}
                    </span>
                  }
                />
              );
            })}
          </ChoiceGroup>
        )}
      </div>

      {/* Serviços: catálogo antes da escolha; opções depois dela */}
      <div className="grid gap-2">
        {services === null ? (
          <>
            <h3 className="m-0 text-sm font-semibold">Serviços</h3>
            {data.servicesError ? (
              <LoadError message={data.servicesError} onRetry={data.retryServices} />
            ) : (
              <Loading>Carregando serviços…</Loading>
            )}
          </>
        ) : services.length === 0 ? (
          <>
            <h3 className="m-0 text-sm font-semibold">Serviços</h3>
            <p className="m-0 text-fg-muted">Nenhum serviço disponível no momento.</p>
          </>
        ) : !queue ? (
          <>
            <h3 className="m-0 text-sm font-semibold">Serviços</h3>
            {anyOpen && (
              <p className="m-0 text-sm text-fg-muted">
                Escolha um profissional para selecionar os serviços.
              </p>
            )}
            <ul className="m-0 grid list-none divide-y divide-line rounded-xl border border-line bg-surface p-0">
              {services.map((s) => {
                const names = s.professional_ids
                  .map((id) => queues?.find((q) => q.professional.id === id)?.professional.display_name)
                  .filter(Boolean);
                return (
                  <li key={s.id} className="flex items-start gap-3 px-4 py-3">
                    <Scissors aria-hidden="true" className="mt-0.5 size-5 shrink-0 text-fg-muted" />
                    <div className="min-w-0 flex-1">
                      <p className="m-0 font-medium">{s.name}</p>
                      <p className="m-0 text-sm text-fg-muted">
                        cerca de {s.duration_minutes} min
                        {queues && <> · com {names.join(", ") || "nenhum profissional no momento"}</>}
                      </p>
                    </div>
                    <span className="font-semibold tabular-nums">{formatCents(s.price.amount_cents)}</span>
                  </li>
                );
              })}
            </ul>
          </>
        ) : (
          <ChoiceGroup legend={`Serviços com ${queue.professional.display_name}`}>
            {offered.length === 0 && (
              <p className="m-0 text-fg-muted">Este profissional não tem serviços disponíveis.</p>
            )}
            {offered.map((s) => (
              <Choice
                key={s.id}
                type="checkbox"
                checked={selection.serviceIds.includes(s.id)}
                onChange={(e) => {
                  setMissing(false);
                  setSelection({
                    queueId: selection.queueId,
                    serviceIds: e.target.checked
                      ? [...selection.serviceIds, s.id]
                      : selection.serviceIds.filter((x) => x !== s.id),
                  });
                }}
                label={s.name}
                description={`cerca de ${s.duration_minutes} min`}
                trailing={formatCents(s.price.amount_cents)}
              />
            ))}
            {services.length > offered.length && (
              <p className="m-0 text-sm text-fg-muted">
                {services.length - offered.length === 1
                  ? "1 serviço da loja não é feito por este profissional."
                  : `${services.length - offered.length} serviços da loja não são feitos por este profissional.`}
              </p>
            )}
          </ChoiceGroup>
        )}
      </div>

      {anyOpen && (
        <div
          className={cx(
            "grid gap-3 rounded-xl border border-line bg-surface p-4",
            // No celular, o resumo acompanha a rolagem quando já há algo escolhido.
            chosen.length > 0 && "max-md:sticky max-md:bottom-2 max-md:z-20 max-md:shadow-lg",
          )}
        >
          <CommandError problem={cmd.problem} offline={cmd.offline} />
          {missing && (
            <p role="alert" className="m-0 text-sm font-semibold text-danger">
              Escolha um profissional e pelo menos um serviço.
            </p>
          )}
          <div className="flex items-end justify-between gap-3">
            <div className="grid">
              <span className="text-sm text-fg-muted">
                {chosen.length === 0
                  ? "Nenhum serviço escolhido"
                  : `Total estimado · ${chosen.length} ${chosen.length === 1 ? "serviço" : "serviços"} · ~${duration} min`}
              </span>
              <span className="text-xl font-bold tabular-nums">{formatCents(total)}</span>
            </div>
            <Submit busy={cmd.busy !== null}>Entrar na fila</Submit>
          </div>
          <p className="m-0 text-xs text-fg-muted">
            O valor é confirmado pela loja ao entrar. A espera é uma estimativa e pode mudar.
          </p>
        </div>
      )}
    </form>
  );
}
