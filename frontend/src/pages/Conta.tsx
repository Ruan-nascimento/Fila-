import { useState, type FormEvent } from "react";
import { Link, useNavigate } from "react-router";
import { ChevronRight, KeyRound, LogOut, Store, Trash2 } from "lucide-react";
import { api } from "../api/client";
import { call, fieldErrors, problemMessage } from "../api/problem";
import { modoDono } from "./dono/modo";
import { useSession } from "../session/context";
import {
  Avatar,
  Badge,
  Button,
  ButtonLink,
  Card,
  Field,
  Loading,
  Notice,
  PageHeader,
  PasswordField,
  Section,
  Submit,
} from "../ui/ui";
import { useInlinePanel } from "../ui/useInlinePanel";
import { useForm } from "../ui/useForm";
import { ExcluirConta, ExclusaoSolicitada } from "./exclusao/Exclusao";
import type { DeletionRequest } from "./exclusao/textos";
import { useCommand } from "./gestao/comando";
import { CommandError } from "./gestao/comum";

const dateTime = new Intl.DateTimeFormat("pt-BR", { dateStyle: "short", timeStyle: "short" });

function NaoConectado() {
  return (
    <section aria-labelledby="titulo-conta" className="grid gap-4">
      <PageHeader id="titulo-conta" title="Minha conta" />
      <p className="m-0">Você não está conectado.</p>
      <div className="flex flex-wrap gap-2">
        <ButtonLink to="/entrar">Entrar</ButtonLink>
        <ButtonLink to="/cadastro" variant="secondary">
          Criar conta
        </ButtonLink>
      </div>
    </section>
  );
}

export function Conta() {
  const { state, refresh } = useSession();
  const navigate = useNavigate();
  const [logoutError, setLogoutError] = useState<string | null>(null);
  const [leaving, setLeaving] = useState(false);

  if (state.status === "loading") return <Loading />;
  if (state.status === "offline")
    return (
      <Notice kind="erro">
        <p>Sem conexão com o servidor. Verifique a internet e tente de novo.</p>
      </Notice>
    );
  const { account, session, memberships } = state.session;
  if (!account || !session) return <NaoConectado />;

  async function logout() {
    setLogoutError(null);
    setLeaving(true);
    const out = await call(() => api.POST("/v1/auth/logout"));
    setLeaving(false);
    if (!out.ok && out.problem?.status !== 401) {
      setLogoutError(problemMessage(out.problem, out.offline));
      return;
    }
    await refresh();
    navigate(modoDono() ? "/dono" : "/");
  }

  const lojas = new Map<string, { name: string; roles: string[] }>();
  for (const m of memberships) {
    const cur = lojas.get(m.establishment_id) ?? { name: m.establishment_name, roles: [] };
    cur.roles.push(m.role === "owner" ? "dono" : "profissional");
    lojas.set(m.establishment_id, cur);
  }

  return (
    <section aria-labelledby="titulo-conta" className="grid gap-6">
      <PageHeader id="titulo-conta" title="Minha conta" />
      <Card className="flex flex-wrap items-center gap-4 p-5">
        <Avatar name={account.name} className="size-14 text-lg" />
        <dl className="m-0 grid min-w-0 flex-1 gap-0.5">
          <dt className="sr-only">Nome</dt>
          <dd className="m-0 text-xl font-bold">{account.name}</dd>
          <dt className="sr-only">E-mail</dt>
          <dd className="m-0 text-fg-muted">{account.email}</dd>
          <dt className="sr-only">Usando como</dt>
          <dd className="m-0 mt-1">
            <Badge tone="primary">{session.kind === "staff" ? "Equipe da barbearia" : "Cliente"}</Badge>
          </dd>
          <dt className="sr-only">Sessão válida até</dt>
          <dd className="m-0 text-xs text-fg-muted">
            Sessão válida até {dateTime.format(new Date(session.expires_at))}
          </dd>
        </dl>
      </Card>

      {lojas.size > 0 && (
        <Section title="Acesso de equipe">
          <ul className="m-0 grid list-none divide-y divide-line rounded-xl border border-line bg-surface p-0">
            {[...lojas].map(([id, l]) => (
              <li key={id}>
                {session.kind === "staff" ? (
                  <Link
                    to={`/gestao/${id}/filas`}
                    className="flex min-h-11 items-center gap-3 px-4 py-3 text-fg no-underline hover:bg-surface-2"
                  >
                    <Store aria-hidden="true" className="size-5 text-fg-muted" />
                    <span className="flex-1">
                      <span className="block font-medium">{l.name}</span>
                      <span className="text-sm text-fg-muted">{l.roles.join(" e ")}</span>
                    </span>
                    <ChevronRight aria-hidden="true" className="size-5 text-fg-muted" />
                  </Link>
                ) : (
                  <div className="flex items-center gap-3 px-4 py-3">
                    <Store aria-hidden="true" className="size-5 text-fg-muted" />
                    <span>
                      {l.name}: {l.roles.join(" e ")}
                    </span>
                  </div>
                )}
              </li>
            ))}
          </ul>
          {session.kind !== "staff" && (
            <p className="m-0 text-sm text-fg-muted">
              Para usar o acesso de equipe, saia e entre escolhendo “Equipe da barbearia”.
            </p>
          )}
        </Section>
      )}

      <MeuNome current={account.name} onDone={refresh} />
      <TrocarSenha onDone={refresh} />

      <div className="grid gap-2">
        {logoutError && <Notice kind="erro">{logoutError}</Notice>}
        <Button
          variant="secondary"
          icon={LogOut}
          className="justify-self-start"
          disabled={leaving}
          onClick={() => void logout()}
        >
          Sair desta conta
        </Button>
      </div>

      <section aria-labelledby="titulo-zona-exclusao" className="grid gap-2 border-t border-line pt-5">
        <h2 id="titulo-zona-exclusao" className="m-0 text-base font-semibold">
          Excluir conta
        </h2>
        <p className="m-0 text-sm text-fg-muted">
          Remove seus dados pessoais e encerra pontos e prêmios. Antes de confirmar, você vê todos os efeitos.
        </p>
        <ButtonLink to="/conta/excluir" variant="danger-ghost" icon={Trash2} className="justify-self-start">
          Excluir conta
        </ButtonLink>
      </section>
    </section>
  );
}

/** Área separada da exclusão: efeitos, confirmação, senha e, no fim, o protocolo. */
export function ExcluirContaPagina() {
  const { state, refresh } = useSession();
  // Depois do pedido de exclusão a sessão acaba; o resultado (com o protocolo) fica nesta tela.
  const [deletion, setDeletion] = useState<DeletionRequest | null>(null);
  if (deletion) return <ExclusaoSolicitada request={deletion} />;
  if (state.status === "loading") return <Loading />;
  if (state.status !== "ready" || !state.session.account) return <NaoConectado />;
  return (
    <section aria-labelledby="titulo-excluir-pagina" className="grid gap-5">
      <PageHeader
        id="titulo-excluir-pagina"
        title="Excluir conta"
        back={{ to: "/conta", label: "Minha conta" }}
        description="Você pode excluir sua conta do Fila. A ação é permanente."
      />
      <ExcluirConta
        onRequested={(d) => {
          setDeletion(d);
          void refresh();
        }}
      />
    </section>
  );
}

/** Nome exibido: muda para as próximas visitas (as já feitas guardam o nome da época). */
function MeuNome({ current, onDone }: { current: string; onDone: () => Promise<void> }) {
  const [name, setName] = useState(current);
  const [done, setDone] = useState(false);
  const cmd = useCommand();
  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setDone(false);
    if (name.trim() === "" || name.trim() === current) return;
    const out = await cmd.run(`nome:${name.trim()}`, (key) =>
      api.PATCH("/v1/me", { params: { header: { "Idempotency-Key": key } }, body: { name: name.trim() } }),
    );
    if (out.ok) {
      setDone(true);
      await onDone();
    }
  }
  const errors = fieldErrors(cmd.problem);
  return (
    <form onSubmit={onSubmit} noValidate aria-labelledby="titulo-nome" className="grid max-w-xl gap-3">
      <h2 id="titulo-nome" className="m-0 text-base font-semibold">
        Meu nome
      </h2>
      {!cmd.problem?.errors?.length && <CommandError problem={cmd.problem} offline={cmd.offline} />}
      {done && (
        <Notice kind="ok">
          <p>Nome atualizado.</p>
        </Notice>
      )}
      <Field
        label="Nome exibido"
        autoComplete="name"
        required
        maxLength={80}
        hint="Aparece para a equipe nas próximas visitas."
        value={name}
        error={errors.name}
        onChange={(e) => setName(e.target.value)}
      />
      <div>
        <Submit busy={cmd.busy !== null}>Salvar nome</Submit>
      </div>
    </form>
  );
}

function TrocarSenha({ onDone }: { onDone: () => Promise<void> }) {
  const form = useForm();
  const panel = useInlinePanel();
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [done, setDone] = useState(false);

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setDone(false);
    const out = await form.run(() =>
      api.POST("/v1/auth/password/change", { body: { current_password: current, new_password: next } }),
    );
    if (out.ok) {
      setCurrent("");
      setNext("");
      setDone(true);
      panel.hide();
      await onDone();
    }
  }

  return (
    <section aria-labelledby="titulo-senha" className="grid max-w-xl gap-3">
      <h2 id="titulo-senha" className="m-0 text-base font-semibold">
        Senha
      </h2>
      {done && (
        <Notice kind="ok">
          <p>Senha alterada.</p>
        </Notice>
      )}
      {!panel.open ? (
        <Button
          id={panel.triggerId}
          variant="secondary"
          icon={KeyRound}
          className="justify-self-start"
          onClick={() => {
            setDone(false);
            panel.show("aberto");
          }}
        >
          Trocar senha
        </Button>
      ) : (
        <form
          id={panel.panelId}
          onKeyDown={panel.onKeyDown}
          onSubmit={onSubmit}
          noValidate
          aria-label="Trocar senha"
          className="grid gap-4 rounded-xl border border-line bg-surface p-4"
        >
          <p className="m-0 text-sm text-fg-muted">
            As outras sessões abertas desta conta serão encerradas; esta continua.
          </p>
          {form.generalError}
          <PasswordField
            label="Senha atual"
            autoComplete="current-password"
            required
            value={current}
            error={form.errors.current_password}
            onChange={(e) => setCurrent(e.target.value)}
          />
          <PasswordField
            label="Senha nova"
            autoComplete="new-password"
            required
            minLength={10}
            maxLength={128}
            hint="Pelo menos 10 caracteres."
            value={next}
            error={form.errors.new_password}
            onChange={(e) => setNext(e.target.value)}
          />
          <div className="flex flex-wrap gap-2">
            <Submit busy={form.busy}>Trocar senha</Submit>
            <Button variant="secondary" onClick={panel.hide}>
              Cancelar
            </Button>
          </div>
        </form>
      )}
    </section>
  );
}
