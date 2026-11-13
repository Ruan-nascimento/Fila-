import { useState, type FormEvent } from "react";
import { Link, useNavigate } from "react-router";
import { api } from "../api/client";
import { call, problemMessage } from "../api/problem";
import { useSession } from "../session/context";
import { Field, Notice, Submit } from "../ui/ui";
import { useForm } from "../ui/useForm";
import { ExcluirConta, ExclusaoSolicitada } from "./exclusao/Exclusao";
import type { DeletionRequest } from "./exclusao/textos";

const dateTime = new Intl.DateTimeFormat("pt-BR", { dateStyle: "short", timeStyle: "short" });

export function Conta() {
  const { state, refresh } = useSession();
  const navigate = useNavigate();
  const [logoutError, setLogoutError] = useState<string | null>(null);
  // Depois do pedido de exclusão a sessão acaba; o resultado (com o protocolo) fica nesta tela.
  const [deletion, setDeletion] = useState<DeletionRequest | null>(null);

  if (deletion) return <ExclusaoSolicitada request={deletion} />;
  if (state.status === "loading") return <p role="status">Carregando…</p>;
  if (state.status === "offline")
    return <Notice kind="erro">Sem conexão com o servidor. Verifique a internet e tente de novo.</Notice>;
  const { account, session, memberships } = state.session;
  if (!account || !session) {
    return (
      <section aria-labelledby="titulo-conta">
        <h2 id="titulo-conta">Minha conta</h2>
        <p>Você não está conectado.</p>
        <p>
          <Link to="/entrar">Entrar</Link> · <Link to="/cadastro">Criar conta</Link>
        </p>
      </section>
    );
  }

  async function logout() {
    setLogoutError(null);
    const out = await call(() => api.POST("/v1/auth/logout"));
    if (!out.ok && out.problem?.status !== 401) {
      setLogoutError(problemMessage(out.problem, out.offline));
      return;
    }
    await refresh();
    navigate("/");
  }

  return (
    <section aria-labelledby="titulo-conta">
      <h2 id="titulo-conta">Minha conta</h2>
      <dl className="details">
        <dt>Nome</dt>
        <dd>{account.name}</dd>
        <dt>E-mail</dt>
        <dd>{account.email}</dd>
        <dt>Usando como</dt>
        <dd>{session.kind === "staff" ? "Equipe da barbearia" : "Cliente"}</dd>
        <dt>Sessão válida até</dt>
        <dd>{dateTime.format(new Date(session.expires_at))}</dd>
      </dl>
      {memberships.length > 0 && (
        <>
          <h3>Acesso de equipe</h3>
          <ul>
            {memberships.map((m) => (
              <li key={`${m.establishment_id}-${m.role}`}>
                {m.establishment_name}: {m.role === "owner" ? "dono" : "profissional"}
              </li>
            ))}
          </ul>
          {session.kind !== "staff" && (
            <p className="muted">
              Para usar o acesso de equipe, saia e entre escolhendo “Equipe da barbearia”.
            </p>
          )}
        </>
      )}
      {logoutError && <Notice kind="erro">{logoutError}</Notice>}
      <p>
        <button type="button" className="button button--secondary" onClick={() => void logout()}>
          Sair desta conta
        </button>
      </p>
      <TrocarSenha onDone={refresh} />
      <ExcluirConta
        onRequested={(d) => {
          setDeletion(d);
          void refresh();
        }}
      />
    </section>
  );
}

function TrocarSenha({ onDone }: { onDone: () => Promise<void> }) {
  const form = useForm();
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
      await onDone();
    }
  }

  return (
    <form onSubmit={onSubmit} noValidate aria-labelledby="titulo-senha">
      <h3 id="titulo-senha">Trocar senha</h3>
      <p className="muted">As outras sessões abertas desta conta serão encerradas; esta continua.</p>
      {form.generalError}
      {done && <Notice kind="ok">Senha alterada.</Notice>}
      <Field
        label="Senha atual"
        type="password"
        autoComplete="current-password"
        required
        value={current}
        error={form.errors.current_password}
        onChange={(e) => setCurrent(e.target.value)}
      />
      <Field
        label="Senha nova"
        type="password"
        autoComplete="new-password"
        required
        minLength={10}
        maxLength={128}
        hint="Pelo menos 10 caracteres."
        value={next}
        error={form.errors.new_password}
        onChange={(e) => setNext(e.target.value)}
      />
      <Submit busy={form.busy}>Trocar senha</Submit>
    </form>
  );
}
