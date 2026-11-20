import { useCallback, useEffect, useState, type FormEvent } from "react";
import { Mail, Pencil, Plus, UserCheck, UserRound, UserX, X, ImageOff } from "lucide-react";
import { api } from "../../api/client";
import { call, fieldErrors, problemMessage } from "../../api/problem";
import type { components } from "../../api/schema.gen";
import {
  Avatar,
  Badge,
  Button,
  EmptyState,
  Field,
  LoadError,
  Loading,
  PageHeader,
  Submit,
  TextArea,
} from "../../ui/ui";
import { useInlinePanel } from "../../ui/useInlinePanel";
import { useCommand } from "./comando";
import { CommandError, type StaffContext } from "./comum";

type Professional = components["schemas"]["Professional"];

const dateTime = new Intl.DateTimeFormat("pt-BR", { dateStyle: "short", timeStyle: "short" });

export function Profissionais({ ctx }: { ctx: StaffContext }) {
  const [list, setList] = useState<Professional[] | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const novo = useInlinePanel();

  const fetchList = useCallback(
    () =>
      call(() =>
        api.GET("/v1/establishments/{establishmentId}/professionals", {
          params: { path: { establishmentId: ctx.estId } },
        }),
      ),
    [ctx.estId],
  );
  const load = useCallback(async () => {
    const out = await fetchList();
    if (out.ok) {
      setList(out.data.data);
      setLoadError(null);
    } else setLoadError(problemMessage(out.problem, out.offline));
  }, [fetchList]);

  useEffect(() => {
    let active = true;
    void fetchList().then((out) => {
      if (!active) return;
      if (out.ok) setList(out.data.data);
      else setLoadError(problemMessage(out.problem, out.offline));
    });
    return () => {
      active = false;
    };
  }, [fetchList]);

  const header = (
    <PageHeader
      title="Profissionais"
      description="Quem atende na loja, a letra da fila e o acesso de cada um."
      actions={
        list && !novo.open ? (
          <Button id={novo.triggerId} icon={Plus} onClick={() => novo.show("aberto")}>
            Cadastrar profissional
          </Button>
        ) : undefined
      }
    />
  );
  if (loadError && !list)
    return (
      <>
        {header}
        <LoadError message={loadError} onRetry={() => void load()} />
      </>
    );
  if (!list)
    return (
      <>
        {header}
        <Loading>Carregando profissionais…</Loading>
      </>
    );
  const used = new Set(list.map((p) => p.queue_letter));
  return (
    <div className="grid gap-4">
      {header}
      {novo.open && (
        <NovoProfissional
          ctx={ctx}
          usedLetters={used}
          panelId={novo.panelId}
          onKeyDown={novo.onKeyDown}
          onCancel={novo.hide}
          onDone={async () => {
            novo.hide();
            await load();
          }}
        />
      )}
      {list.length === 0 ? (
        <EmptyState icon={UserRound} title="Nenhum profissional cadastrado">
          <p>Cadastre quem atende: cada profissional tem a própria fila.</p>
        </EmptyState>
      ) : (
        <ul className="m-0 grid list-none gap-3 p-0">
          {list.map((p) => (
            <ProfissionalCard key={p.id} ctx={ctx} p={p} onDone={load} />
          ))}
        </ul>
      )}
    </div>
  );
}

function NovoProfissional({
  ctx,
  usedLetters,
  onDone,
  onCancel,
  panelId,
  onKeyDown,
}: {
  ctx: StaffContext;
  usedLetters: Set<string>;
  onDone: () => Promise<void>;
  onCancel: () => void;
  panelId: string;
  onKeyDown: (e: React.KeyboardEvent) => void;
}) {
  const cmd = useCommand();
  const [name, setName] = useState("");
  const [letter, setLetter] = useState("");
  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    const body = { display_name: name, queue_letter: letter.toUpperCase() };
    const out = await cmd.run(`novo:${body.display_name}:${body.queue_letter}`, (key) =>
      api.POST("/v1/establishments/{establishmentId}/professionals", {
        params: { path: { establishmentId: ctx.estId }, header: { "Idempotency-Key": key } },
        body,
      }),
    );
    if (out.ok) {
      setName("");
      setLetter("");
      await onDone();
    }
  }
  const errors = fieldErrors(cmd.problem);
  const free = "ABCDEFGHIJKLMNOPQRSTUVWXYZ".split("").filter((l) => !usedLetters.has(l));
  return (
    <form
      id={panelId}
      onKeyDown={onKeyDown}
      onSubmit={onSubmit}
      noValidate
      className="grid max-w-xl gap-4 rounded-xl border border-line bg-surface p-4 sm:p-5"
      aria-labelledby="titulo-novo-pro"
    >
      <h2 id="titulo-novo-pro" className="m-0 text-base font-semibold">
        Cadastrar profissional
      </h2>
      {!cmd.problem?.errors?.length && <CommandError problem={cmd.problem} offline={cmd.offline} />}
      <Field
        label="Nome exibido"
        required
        maxLength={40}
        value={name}
        error={errors.display_name}
        onChange={(e) => setName(e.target.value)}
        hint="Aparece na fila pública."
      />
      <Field
        label="Letra da fila"
        required
        maxLength={1}
        autoCapitalize="characters"
        className="w-20 text-center font-semibold uppercase"
        value={letter}
        error={errors.queue_letter}
        onChange={(e) => setLetter(e.target.value.toUpperCase())}
        hint={`Prefixo das senhas (ex.: A017). Livres: ${free.slice(0, 8).join(", ")}${free.length > 8 ? "…" : ""}`}
      />
      <p className="m-0 text-sm text-fg-muted">A fila do profissional é criada fechada; abra em Filas.</p>
      <div className="flex flex-wrap gap-2">
        <Submit busy={cmd.busy !== null}>Cadastrar</Submit>
        <Button variant="secondary" onClick={onCancel}>
          Cancelar
        </Button>
      </div>
    </form>
  );
}

function ProfissionalCard({
  ctx,
  p,
  onDone,
}: {
  ctx: StaffContext;
  p: Professional;
  onDone: () => Promise<void>;
}) {
  const cmd = useCommand();
  const panel = useInlinePanel<"nome" | "desativar" | "convite" | "foto">();
  const mode = panel.open;
  const [name, setName] = useState(p.display_name);
  const [reason, setReason] = useState("");
  const [email, setEmail] = useState("");
  const [pending, setPending] = useState<(() => Promise<void>) | null>(null);

  async function act(
    intent: string,
    fn: (key: string) => Promise<{ data?: unknown; error?: unknown; response: Response }>,
  ) {
    const run = async () => {
      const out = await cmd.run(intent, fn);
      if (out.ok) {
        panel.hide();
        setPending(null);
        await onDone();
      } else if (out.problem?.code === "reauthentication_required") {
        setPending(() => run);
      } else if (out.problem?.status === 409) {
        await onDone(); // conflito: mostra os dados atuais
      }
    };
    await run();
  }

  const base = { establishmentId: ctx.estId, professionalId: p.id };
  const errors = fieldErrors(cmd.problem);
  const formBox = "grid max-w-xl gap-4 rounded-xl border border-line bg-surface-2 p-4";
  return (
    <li className="grid gap-3 rounded-xl border border-line bg-surface p-4">
      <div className="flex flex-wrap items-center gap-3">
        <Avatar
          name={p.display_name}
          image={p.photo}
          label={p.photo ? `Foto pública de ${p.display_name}` : undefined}
        />
        <div className="grid min-w-0 flex-1 gap-1">
          <h2 className="m-0 text-base font-semibold">{p.display_name}</h2>
          <div className="flex flex-wrap items-center gap-2 text-sm text-fg-muted">
            <span>Fila {p.queue_letter}</span>
            <Badge tone={p.status === "active" ? "success" : "neutral"}>
              {p.status === "active" ? "Ativo" : "Desativado"}
            </Badge>
            {p.has_access ? (
              <Badge tone="primary">Acesso ativado</Badge>
            ) : p.pending_invite ? (
              <Badge tone="warning">
                Convite pendente até {dateTime.format(new Date(p.pending_invite.expires_at))}
              </Badge>
            ) : (
              <Badge>Sem acesso</Badge>
            )}
          </div>
        </div>
      </div>
      {!cmd.problem?.errors?.length && (
        <CommandError
          problem={cmd.problem}
          offline={cmd.offline}
          onRetry={pending ? () => void pending() : undefined}
        />
      )}
      {!mode && (
        <div className="flex flex-wrap gap-2">
          <Button id={panel.triggerId} variant="secondary" icon={Pencil} onClick={() => panel.show("nome")}>
            Renomear
          </Button>
          {p.status === "active" && !p.has_access && (
            <Button variant="secondary" icon={Mail} onClick={() => panel.show("convite")}>
              {p.pending_invite ? "Reenviar convite" : "Convidar"}
            </Button>
          )}
          {p.pending_invite && !p.has_access && (
            <Button
              variant="ghost"
              icon={X}
              disabled={cmd.busy !== null}
              onClick={() => {
                const invite = p.pending_invite;
                if (!invite) return;
                void act(`revogar-convite:${invite.id}`, (key) =>
                  api.POST("/v1/establishments/{establishmentId}/invites/{inviteId}/revoke", {
                    params: {
                      path: { establishmentId: ctx.estId, inviteId: invite.id },
                      header: { "Idempotency-Key": key },
                    },
                    body: {},
                  }),
                );
              }}
            >
              Cancelar convite
            </Button>
          )}
          {p.photo && (
            <Button variant="ghost" icon={ImageOff} onClick={() => panel.show("foto")}>
              Retirar foto pública
            </Button>
          )}
          {p.status === "active" ? (
            <Button variant="danger-ghost" icon={UserX} onClick={() => panel.show("desativar")}>
              Desativar
            </Button>
          ) : (
            <Button
              icon={UserCheck}
              disabled={cmd.busy !== null}
              onClick={() =>
                void act(`reativar:${p.id}:${p.version}`, (key) =>
                  api.POST("/v1/establishments/{establishmentId}/professionals/{professionalId}/reactivate", {
                    params: { path: base, header: { "Idempotency-Key": key } },
                    body: { expected_version: p.version },
                  }),
                )
              }
            >
              Reativar
            </Button>
          )}
        </div>
      )}
      {mode === "nome" && (
        <form
          id={panel.panelId}
          onKeyDown={panel.onKeyDown}
          noValidate
          aria-label={`Renomear ${p.display_name}`}
          className={formBox}
          onSubmit={(e) => {
            e.preventDefault();
            void act(`nome:${p.id}:${p.version}:${name}`, (key) =>
              api.PATCH("/v1/establishments/{establishmentId}/professionals/{professionalId}", {
                params: { path: base, header: { "Idempotency-Key": key } },
                body: { expected_version: p.version, display_name: name },
              }),
            );
          }}
        >
          <Field
            label="Novo nome"
            required
            maxLength={40}
            value={name}
            error={errors.display_name}
            onChange={(e) => setName(e.target.value)}
          />
          <div className="flex flex-wrap gap-2">
            <Submit busy={cmd.busy !== null}>Salvar</Submit>
            <Button variant="secondary" onClick={panel.hide}>
              Cancelar
            </Button>
          </div>
        </form>
      )}
      {mode === "convite" && (
        <form
          id={panel.panelId}
          onKeyDown={panel.onKeyDown}
          noValidate
          aria-label={`Convidar ${p.display_name}`}
          className={formBox}
          onSubmit={(e) => {
            e.preventDefault();
            void act(`convite:${p.id}:${email}`, (key) =>
              api.POST("/v1/establishments/{establishmentId}/professionals/{professionalId}/invites", {
                params: { path: base, header: { "Idempotency-Key": key } },
                body: { email },
              }),
            );
          }}
        >
          <Field
            label="E-mail do profissional"
            type="email"
            inputMode="email"
            autoComplete="off"
            required
            value={email}
            error={errors.email}
            onChange={(e) => setEmail(e.target.value)}
            hint="O convite vale 24 horas e só funciona uma vez."
          />
          <div className="flex flex-wrap gap-2">
            <Submit busy={cmd.busy !== null}>Enviar convite</Submit>
            <Button variant="secondary" onClick={panel.hide}>
              Cancelar
            </Button>
          </div>
        </form>
      )}
      {mode === "foto" && (
        <form
          id={panel.panelId}
          onKeyDown={panel.onKeyDown}
          noValidate
          aria-label={`Retirar a foto pública de ${p.display_name}`}
          className={formBox}
          onSubmit={(e) => {
            e.preventDefault();
            void act(`retirar-foto:${p.id}:${p.version}`, (key) =>
              api.POST("/v1/establishments/{establishmentId}/professionals/{professionalId}/photo/remove", {
                params: { path: base, header: { "Idempotency-Key": key } },
                body: { expected_version: p.version, reason },
              }),
            );
          }}
        >
          <p className="m-0 text-sm">
            A foto sai da página da loja (fica registrado na auditoria). A foto pessoal da conta do
            profissional não é alterada; ele pode publicar outra depois.
          </p>
          <TextArea
            label="Motivo"
            required
            minLength={3}
            maxLength={500}
            rows={2}
            value={reason}
            error={errors.reason}
            onChange={(e) => setReason(e.target.value)}
          />
          <div className="flex flex-wrap gap-2">
            <Submit busy={cmd.busy !== null}>Retirar foto</Submit>
            <Button variant="secondary" onClick={panel.hide}>
              Cancelar
            </Button>
          </div>
        </form>
      )}
      {mode === "desativar" && (
        <form
          id={panel.panelId}
          onKeyDown={panel.onKeyDown}
          noValidate
          aria-label={`Desativar ${p.display_name}`}
          className="grid max-w-xl gap-4 rounded-xl border border-danger/40 bg-danger-soft p-4"
          onSubmit={(e) => {
            e.preventDefault();
            void act(`desativar:${p.id}:${p.version}`, (key) =>
              api.POST("/v1/establishments/{establishmentId}/professionals/{professionalId}/deactivate", {
                params: { path: base, header: { "Idempotency-Key": key } },
                body: { expected_version: p.version, reason },
              }),
            );
          }}
        >
          <div className="grid gap-1 text-sm">
            <p className="m-0 text-base font-semibold">Desativar {p.display_name}?</p>
            <p className="m-0">
              O profissional perde o acesso na hora e a fila dele fecha. Nada é apagado: atendimentos e
              comandas antigas continuam com você. Não é possível desativar com clientes aguardando, chamados
              ou em atendimento.
            </p>
          </div>
          <TextArea
            label="Motivo"
            required
            minLength={3}
            maxLength={500}
            rows={2}
            value={reason}
            error={errors.reason}
            onChange={(e) => setReason(e.target.value)}
          />
          <div className="flex flex-wrap gap-2">
            <Submit busy={cmd.busy !== null} variant="danger">
              Desativar
            </Submit>
            <Button variant="secondary" onClick={panel.hide}>
              Cancelar
            </Button>
          </div>
        </form>
      )}
    </li>
  );
}
