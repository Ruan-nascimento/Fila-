import { useState, type FormEvent } from "react";
import { Link, useNavigate, useSearchParams } from "react-router";
import { Store, UserRound } from "lucide-react";
import { api } from "../api/client";
import { call, fieldErrors, problemMessage, type Problem } from "../api/problem";
import { useSession } from "../session/context";
import { safeReturn } from "../ui/retorno";
import { Field, Notice, PageHeader, PasswordField, Segmented, Submit } from "../ui/ui";

export function Entrar() {
  const { refresh } = useSession();
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const voltar = safeReturn(params.get("voltar"));
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [context, setContext] = useState<"customer" | "staff">(
    params.get("como") === "equipe" ? "staff" : "customer",
  );
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
      // Volta para a loja de onde veio (contexto mantido); equipe vai para a gestão.
      navigate(voltar ?? (context === "staff" ? "/gestao" : "/conta"));
      return;
    }
    setProblem(out.problem);
    setOffline(out.offline);
  }

  const errors = fieldErrors(problem);
  return (
    <section aria-labelledby="titulo-entrar" className="grid gap-5">
      <PageHeader
        id="titulo-entrar"
        title="Entrar"
        description="Acesse sua conta para acompanhar a fila ou atender clientes."
      />
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
      <form onSubmit={onSubmit} noValidate className="grid gap-4">
        <Segmented
          legend="Entrar como"
          name="contexto"
          value={context}
          onChange={setContext}
          options={[
            { value: "customer", label: "Cliente", icon: UserRound },
            { value: "staff", label: "Equipe da barbearia", icon: Store },
          ]}
        />
        <Field
          label="E-mail"
          type="email"
          autoComplete="email"
          inputMode="email"
          required
          value={email}
          error={errors.email}
          onChange={(e) => setEmail(e.target.value)}
        />
        <PasswordField
          label="Senha"
          autoComplete="current-password"
          required
          value={password}
          error={errors.password}
          onChange={(e) => setPassword(e.target.value)}
        />
        <Submit busy={busy} block>
          Entrar
        </Submit>
      </form>
      <p className="m-0 flex flex-wrap justify-center gap-x-4 gap-y-1 text-sm">
        <Link to="/esqueci-senha" className="inline-flex min-h-11 items-center font-semibold">
          Esqueci minha senha
        </Link>
        <Link
          to={voltar ? `/cadastro?voltar=${encodeURIComponent(voltar)}` : "/cadastro"}
          className="inline-flex min-h-11 items-center font-semibold"
        >
          Criar conta de cliente
        </Link>
      </p>
      {voltar?.startsWith("/loja/") && (
        <p className="m-0 text-center text-sm text-fg-muted">
          Depois de entrar, você volta para a loja. <Link to={voltar}>Voltar sem entrar</Link>
        </p>
      )}
    </section>
  );
}
