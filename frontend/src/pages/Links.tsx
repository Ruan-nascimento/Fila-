import { useEffect, useRef, useState, type FormEvent } from "react";
import { Link } from "react-router";
import { Mail } from "lucide-react";
import { api } from "../api/client";
import { call, problemMessage, type Problem } from "../api/problem";
import { takeFragmentToken } from "../ui/fragment";
import { ButtonLink, Field, Loading, Notice, PageHeader, PasswordField, Submit } from "../ui/ui";
import { useForm } from "../ui/useForm";

/** Lê o token do link uma única vez (e apaga da barra de endereço). */
function useLinkToken(): string | null {
  const [token] = useState(() => takeFragmentToken());
  return token;
}

function MissingToken({ again }: { again: { to: string; label: string } }) {
  return (
    <Notice kind="erro" title="Link incompleto.">
      <p>Abra o link exatamente como chegou no e-mail.</p>
      <p>
        <Link to={again.to}>{again.label}</Link>
      </p>
    </Notice>
  );
}

/** Erro de link usado, vencido ou inválido: sempre com uma saída. */
function LinkError({
  problem,
  offline,
  again,
}: {
  problem: Problem | null;
  offline: boolean;
  again: { to: string; label: string };
}) {
  const message = problemMessage(problem, offline);
  if (problem?.errors?.length) return null;
  return (
    <Notice kind="erro">
      <p>{message}</p>
      <p>
        <Link to={again.to}>{again.label}</Link>
      </p>
    </Notice>
  );
}

export function VerificarEmail() {
  const token = useLinkToken();
  const [state, setState] = useState<"enviando" | "ok" | "erro">("enviando");
  const [problem, setProblem] = useState<Problem | null>(null);
  const [offline, setOffline] = useState(false);
  const sent = useRef(false);

  useEffect(() => {
    // O StrictMode monta duas vezes em desenvolvimento; o token é de uso único.
    if (!token || sent.current) return;
    sent.current = true;
    void call(() => api.POST("/v1/auth/email-verification/confirm", { body: { token } })).then((out) => {
      if (out.ok) {
        setState("ok");
      } else {
        setProblem(out.problem);
        setOffline(out.offline);
        setState("erro");
      }
    });
  }, [token]);

  const again = { to: "/reenviar-verificacao", label: "Pedir um novo link" };
  return (
    <section aria-labelledby="titulo-verificar" className="grid gap-5">
      <PageHeader id="titulo-verificar" title="Confirmação de e-mail" />
      {!token && <MissingToken again={again} />}
      {token && state === "enviando" && <Loading>Confirmando…</Loading>}
      {state === "ok" && (
        <Notice kind="ok" title="E-mail confirmado! Sua conta está ativa.">
          <p className="mt-2">
            <ButtonLink to="/entrar">Entrar</ButtonLink>
          </p>
        </Notice>
      )}
      {state === "erro" && <LinkError problem={problem} offline={offline} again={again} />}
    </section>
  );
}

export function RedefinirSenha() {
  const token = useLinkToken();
  const form = useForm();
  const [password, setPassword] = useState("");
  const [done, setDone] = useState(false);

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    if (!token) return;
    const out = await form.run(() =>
      api.POST("/v1/auth/password-reset/confirm", { body: { token, new_password: password } }),
    );
    if (out.ok) {
      setDone(true);
      setPassword("");
    }
  }

  return (
    <section aria-labelledby="titulo-redefinir" className="grid gap-5">
      <PageHeader
        id="titulo-redefinir"
        title="Criar senha nova"
        description="Defina uma nova senha para acessar sua conta no Fila."
      />
      {!token && <MissingToken again={{ to: "/esqueci-senha", label: "Pedir um novo link" }} />}
      {form.generalError && (
        <div className="grid gap-2">
          {form.generalError}
          <p className="m-0 text-sm">
            <Link to="/esqueci-senha">Pedir um novo link</Link>
          </p>
        </div>
      )}
      {done ? (
        <Notice kind="ok" title="Senha alterada.">
          <p>Por segurança, todas as sessões abertas da conta foram encerradas.</p>
          <p className="mt-2">
            <ButtonLink to="/entrar">Entrar com a senha nova</ButtonLink>
          </p>
        </Notice>
      ) : (
        token && (
          <form onSubmit={onSubmit} noValidate className="grid gap-4">
            <PasswordField
              label="Senha nova"
              autoComplete="new-password"
              required
              minLength={10}
              maxLength={128}
              hint="Pelo menos 10 caracteres."
              value={password}
              error={form.errors.new_password}
              onChange={(e) => setPassword(e.target.value)}
            />
            <Submit busy={form.busy} block>
              Salvar senha
            </Submit>
          </form>
        )
      )}
    </section>
  );
}

export function AceitarConvite() {
  const token = useLinkToken();
  const form = useForm();
  const [name, setName] = useState("");
  const [password, setPassword] = useState("");
  const [done, setDone] = useState(false);

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    if (!token) return;
    const body = name.trim() ? { token, name, password } : { token, password };
    const out = await form.run(() => api.POST("/v1/auth/invites/accept", { body }));
    if (out.ok) {
      setDone(true);
      setPassword("");
    }
  }

  return (
    <section aria-labelledby="titulo-convite" className="grid gap-5">
      <span className="inline-flex size-12 items-center justify-center rounded-xl bg-select text-accent">
        <Mail aria-hidden="true" className="size-6" />
      </span>
      <PageHeader
        id="titulo-convite"
        title="Aceitar convite da equipe"
        description="Você foi convidado para a equipe de uma barbearia. Confirme seus dados e a senha para acessar."
      />
      {!token && <MissingToken again={{ to: "/", label: "Peça ao dono da loja um convite novo" }} />}
      {form.generalError && (
        <div className="grid gap-2">
          {form.generalError}
          <p className="m-0 text-sm text-fg-muted">
            Convite usado, cancelado ou vencido? Peça ao dono da loja para enviar outro.
          </p>
        </div>
      )}
      {done ? (
        <Notice kind="ok" title="Acesso ativado.">
          <p>Para trabalhar na barbearia, entre escolhendo “Equipe da barbearia”.</p>
          <p className="mt-2">
            <ButtonLink to="/entrar?como=equipe">Entrar</ButtonLink>
          </p>
        </Notice>
      ) : (
        token && (
          <form onSubmit={onSubmit} noValidate className="grid gap-4">
            <p className="m-0 text-sm text-fg-muted">
              Se você <strong className="text-fg">já tem conta</strong> com o e-mail que recebeu o convite,
              deixe o nome em branco e digite a senha dessa conta. Se{" "}
              <strong className="text-fg">não tem</strong>, informe seu nome e crie uma senha.
            </p>
            <Field
              label="Nome (só para conta nova)"
              autoComplete="name"
              maxLength={80}
              value={name}
              error={form.errors.name}
              onChange={(e) => setName(e.target.value)}
            />
            <PasswordField
              label="Senha"
              autoComplete="current-password"
              required
              maxLength={128}
              value={password}
              hint="Conta nova: pelo menos 10 caracteres."
              error={form.errors.password}
              onChange={(e) => setPassword(e.target.value)}
            />
            <Submit busy={form.busy} block>
              Aceitar convite
            </Submit>
          </form>
        )
      )}
    </section>
  );
}
