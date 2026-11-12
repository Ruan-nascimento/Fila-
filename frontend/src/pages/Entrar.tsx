import { useState, type FormEvent } from "react";
import { Link, useNavigate } from "react-router";
import { api } from "../api/client";
import { call, fieldErrors, problemMessage, type Problem } from "../api/problem";
import { useSession } from "../session/context";
import { Field, Notice, Submit } from "../ui/ui";

export function Entrar() {
  const { refresh } = useSession();
  const navigate = useNavigate();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [context, setContext] = useState<"customer" | "staff">("customer");
  const [busy, setBusy] = useState(false);
  const [problem, setProblem] = useState<Problem | null>(null);
  const [offline, setOffline] = useState(false);

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    const out = await call(() => api.POST("/v1/auth/login", { body: { email, password, context } }));
    setBusy(false);
    if (out.ok) {
      setPassword("");
      await refresh();
      navigate("/conta");
      return;
    }
    setProblem(out.problem);
    setOffline(out.offline);
  }

  const errors = fieldErrors(problem);
  return (
    <section aria-labelledby="titulo-entrar">
      <h2 id="titulo-entrar">Entrar</h2>
      {(problem || offline) && !problem?.errors?.length && (
        <Notice kind="erro">
          <p>{problemMessage(problem, offline)}</p>
          {problem?.code === "email_not_verified" && (
            <p>
              <Link to="/reenviar-verificacao">Pedir um novo link de confirmação</Link>
            </p>
          )}
        </Notice>
      )}
      <form onSubmit={onSubmit} noValidate>
        <Field
          label="E-mail"
          type="email"
          autoComplete="email"
          required
          value={email}
          error={errors.email}
          onChange={(e) => setEmail(e.target.value)}
        />
        <Field
          label="Senha"
          type="password"
          autoComplete="current-password"
          required
          value={password}
          error={errors.password}
          onChange={(e) => setPassword(e.target.value)}
        />
        <fieldset className="choices">
          <legend>Entrar como</legend>
          <label className="choice">
            <input
              type="radio"
              name="contexto"
              value="customer"
              checked={context === "customer"}
              onChange={() => setContext("customer")}
            />
            Cliente
          </label>
          <label className="choice">
            <input
              type="radio"
              name="contexto"
              value="staff"
              checked={context === "staff"}
              onChange={() => setContext("staff")}
            />
            Equipe da barbearia
          </label>
        </fieldset>
        <Submit busy={busy}>Entrar</Submit>
      </form>
      <p>
        <Link to="/esqueci-senha">Esqueci minha senha</Link> · <Link to="/cadastro">Criar conta</Link>
      </p>
    </section>
  );
}
