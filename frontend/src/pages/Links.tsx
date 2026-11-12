import { useEffect, useRef, useState, type FormEvent } from "react";
import { Link } from "react-router";
import { api } from "../api/client";
import { call, problemMessage, type Problem } from "../api/problem";
import { takeFragmentToken } from "../ui/fragment";
import { Field, Notice, Submit } from "../ui/ui";
import { useForm } from "../ui/useForm";

/** Lê o token do link uma única vez (e apaga da barra de endereço). */
function useLinkToken(): string | null {
  const [token] = useState(() => takeFragmentToken());
  return token;
}

function MissingToken() {
  return (
    <Notice kind="erro">
      <p>Link incompleto. Abra o link exatamente como chegou no e-mail.</p>
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

  return (
    <section aria-labelledby="titulo-verificar">
      <h2 id="titulo-verificar">Confirmação de e-mail</h2>
      {!token && <MissingToken />}
      {token && state === "enviando" && <p role="status">Confirmando…</p>}
      {state === "ok" && (
        <Notice kind="ok">
          <p>E-mail confirmado! Sua conta está ativa.</p>
          <p>
            <Link to="/entrar">Entrar</Link>
          </p>
        </Notice>
      )}
      {state === "erro" && (
        <Notice kind="erro">
          <p>{problemMessage(problem, offline)}</p>
          <p>
            <Link to="/reenviar-verificacao">Pedir um novo link</Link>
          </p>
        </Notice>
      )}
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
    <section aria-labelledby="titulo-redefinir">
      <h2 id="titulo-redefinir">Criar senha nova</h2>
      {!token && <MissingToken />}
      {form.generalError}
      {done ? (
        <Notice kind="ok">
          <p>Senha alterada. Por segurança, todas as sessões abertas da conta foram encerradas.</p>
          <p>
            <Link to="/entrar">Entrar com a senha nova</Link>
          </p>
        </Notice>
      ) : (
        token && (
          <form onSubmit={onSubmit} noValidate>
            <Field
              label="Senha nova"
              type="password"
              autoComplete="new-password"
              required
              minLength={10}
              maxLength={128}
              hint="Pelo menos 10 caracteres."
              value={password}
              error={form.errors.new_password}
              onChange={(e) => setPassword(e.target.value)}
            />
            <Submit busy={form.busy}>Salvar senha</Submit>
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
    <section aria-labelledby="titulo-convite">
      <h2 id="titulo-convite">Aceitar convite da equipe</h2>
      {!token && <MissingToken />}
      {form.generalError}
      {done ? (
        <Notice kind="ok">
          <p>Acesso ativado. Para trabalhar na barbearia, entre escolhendo “Equipe da barbearia”.</p>
          <p>
            <Link to="/entrar">Entrar</Link>
          </p>
        </Notice>
      ) : (
        token && (
          <form onSubmit={onSubmit} noValidate>
            <p>
              Se você <strong>já tem conta</strong> com o e-mail que recebeu o convite, deixe o nome em branco
              e digite a senha dessa conta. Se <strong>não tem</strong>, informe seu nome e crie uma senha.
            </p>
            <Field
              label="Nome (só para conta nova)"
              autoComplete="name"
              maxLength={80}
              value={name}
              error={form.errors.name}
              onChange={(e) => setName(e.target.value)}
            />
            <Field
              label="Senha"
              type="password"
              autoComplete="current-password"
              required
              maxLength={128}
              value={password}
              hint="Conta nova: pelo menos 10 caracteres."
              error={form.errors.password}
              onChange={(e) => setPassword(e.target.value)}
            />
            <Submit busy={form.busy}>Aceitar convite</Submit>
          </form>
        )
      )}
    </section>
  );
}
