import { useCallback, useEffect, useState, type FormEvent } from "react";
import { api } from "../../api/client";
import { call, fieldErrors, problemMessage } from "../../api/problem";
import type { components } from "../../api/schema.gen";
import { Field, Notice, Submit } from "../../ui/ui";
import { useCommand } from "./comando";
import { CommandError, type StaffContext } from "./comum";

type Professional = components["schemas"]["Professional"];

const dateTime = new Intl.DateTimeFormat("pt-BR", { dateStyle: "short", timeStyle: "short" });

export function Profissionais({ ctx }: { ctx: StaffContext }) {
  const [list, setList] = useState<Professional[] | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);

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
    if (out.ok) setList(out.data.data);
    else setLoadError(problemMessage(out.problem, out.offline));
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

  if (loadError) return <Notice kind="erro">{loadError}</Notice>;
  if (!list) return <p role="status">Carregando profissionais…</p>;
  const used = new Set(list.map((p) => p.queue_letter));
  return (
    <div>
      <NovoProfissional ctx={ctx} usedLetters={used} onDone={load} />
      {list.length === 0 ? (
        <p>Nenhum profissional cadastrado.</p>
      ) : (
        <ul className="cards">
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
}: {
  ctx: StaffContext;
  usedLetters: Set<string>;
  onDone: () => Promise<void>;
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
    <form onSubmit={onSubmit} noValidate className="card" aria-labelledby="titulo-novo-pro">
      <h3 id="titulo-novo-pro">Cadastrar profissional</h3>
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
        value={letter}
        error={errors.queue_letter}
        onChange={(e) => setLetter(e.target.value.toUpperCase())}
        hint={`Prefixo das senhas (ex.: A017). Livres: ${free.slice(0, 8).join(", ")}${free.length > 8 ? "…" : ""}`}
      />
      <Submit busy={cmd.busy !== null}>Cadastrar</Submit>
      <p className="muted">A fila do profissional é criada fechada; abra na aba Filas.</p>
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
  const [mode, setMode] = useState<"ver" | "nome" | "desativar" | "convite">("ver");
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
        setMode("ver");
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
  return (
    <li className="card">
      <h3>
        {p.display_name} <span className="muted">(fila {p.queue_letter})</span>
      </h3>
      <p>
        {p.status === "active" ? "✓ Ativo" : "■ Desativado"} ·{" "}
        {p.has_access
          ? "acesso ativado"
          : p.pending_invite
            ? `convite pendente até ${dateTime.format(new Date(p.pending_invite.expires_at))}`
            : "sem acesso"}
      </p>
      {!cmd.problem?.errors?.length && (
        <CommandError
          problem={cmd.problem}
          offline={cmd.offline}
          onRetry={pending ? () => void pending() : undefined}
        />
      )}
      {mode === "ver" && (
        <div className="actions">
          <button type="button" className="button button--secondary" onClick={() => setMode("nome")}>
            Renomear
          </button>
          {p.status === "active" && !p.has_access && (
            <button type="button" className="button button--secondary" onClick={() => setMode("convite")}>
              {p.pending_invite ? "Reenviar convite" : "Convidar"}
            </button>
          )}
          {p.status === "active" ? (
            <button type="button" className="button button--secondary" onClick={() => setMode("desativar")}>
              Desativar
            </button>
          ) : (
            <button
              type="button"
              className="button"
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
            </button>
          )}
        </div>
      )}
      {mode === "nome" && (
        <form
          noValidate
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
          <div className="actions">
            <Submit busy={cmd.busy !== null}>Salvar</Submit>
            <button type="button" className="button button--secondary" onClick={() => setMode("ver")}>
              Cancelar
            </button>
          </div>
        </form>
      )}
      {mode === "convite" && (
        <form
          noValidate
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
            required
            value={email}
            error={errors.email}
            onChange={(e) => setEmail(e.target.value)}
            hint="O convite vale 24 horas e só funciona uma vez."
          />
          <div className="actions">
            <Submit busy={cmd.busy !== null}>Enviar convite</Submit>
            <button type="button" className="button button--secondary" onClick={() => setMode("ver")}>
              Cancelar
            </button>
          </div>
        </form>
      )}
      {mode === "desativar" && (
        <form
          noValidate
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
          <p>
            O profissional perde o acesso na hora e a fila dele fecha. Nada é apagado: atendimentos e comandas
            antigas continuam com você. Não é possível desativar com clientes na fila.
          </p>
          <Field
            label="Motivo"
            required
            minLength={3}
            maxLength={500}
            value={reason}
            error={errors.reason}
            onChange={(e) => setReason(e.target.value)}
          />
          <div className="actions">
            <Submit busy={cmd.busy !== null}>Desativar</Submit>
            <button type="button" className="button button--secondary" onClick={() => setMode("ver")}>
              Cancelar
            </button>
          </div>
        </form>
      )}
    </li>
  );
}
