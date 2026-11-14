import { useState, type FormEvent } from "react";
import { Link } from "react-router";
import { api } from "../api/client";
import { Field, Notice, Submit } from "../ui/ui";
import { useForm } from "../ui/useForm";

type Sent = { message: string } | null;

/** Resposta genérica: o servidor não revela se o e-mail existe (R-CONTA-02). */
function SentNotice({ sent }: { sent: Sent }) {
  if (!sent) return null;
  return (
    <Notice kind="ok">
      <p>{sent.message}</p>
      {import.meta.env.DEV && (
        <p>Em desenvolvimento, os e-mails ficam na pasta backend/.correio (veja o README).</p>
      )}
    </Notice>
  );
}

export function Cadastro() {
  const form = useForm();
  const [name, setName] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [sent, setSent] = useState<Sent>(null);

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    const out = await form.run(() => api.POST("/v1/auth/register", { body: { name, email, password } }));
    if (out.ok) {
      setSent(out.data.data);
      setPassword("");
    }
  }

  return (
    <section aria-labelledby="titulo-cadastro">
      <h2 id="titulo-cadastro">Criar conta de cliente</h2>
      <p>Com conta você acompanha seu histórico e, quando a barbearia ativar, acumula pontos.</p>
      {form.generalError}
      <SentNotice sent={sent} />
      {!sent && (
        <form onSubmit={onSubmit} noValidate>
          <Field
            label="Nome"
            autoComplete="name"
            required
            maxLength={80}
            value={name}
            error={form.errors.name}
            onChange={(e) => setName(e.target.value)}
          />
          <Field
            label="E-mail"
            type="email"
            autoComplete="email"
            required
            value={email}
            error={form.errors.email}
            onChange={(e) => setEmail(e.target.value)}
          />
          <Field
            label="Senha"
            type="password"
            autoComplete="new-password"
            required
            minLength={10}
            maxLength={128}
            hint="Pelo menos 10 caracteres. Uma frase fácil de lembrar é uma boa senha."
            value={password}
            error={form.errors.password}
            onChange={(e) => setPassword(e.target.value)}
          />
          <Submit busy={form.busy}>Criar conta</Submit>
        </form>
      )}
      <p>
        Já tem conta? <Link to="/entrar">Entrar</Link>
      </p>
    </section>
  );
}

function EmailOnly({
  title,
  intro,
  path,
  button,
}: {
  title: string;
  intro: string;
  path: "/v1/auth/email-verification/resend" | "/v1/auth/password-reset/request";
  button: string;
}) {
  const form = useForm();
  const [email, setEmail] = useState("");
  const [sent, setSent] = useState<Sent>(null);
  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    const out = await form.run(() => api.POST(path, { body: { email } }));
    if (out.ok) setSent(out.data.data);
  }
  return (
    <section aria-labelledby="titulo-email">
      <h2 id="titulo-email">{title}</h2>
      <p>{intro}</p>
      {form.generalError}
      <SentNotice sent={sent} />
      {!sent && (
        <form onSubmit={onSubmit} noValidate>
          <Field
            label="E-mail"
            type="email"
            autoComplete="email"
            required
            value={email}
            error={form.errors.email}
            onChange={(e) => setEmail(e.target.value)}
          />
          <Submit busy={form.busy}>{button}</Submit>
        </form>
      )}
      <p>
        <Link to="/entrar">Voltar para entrar</Link>
      </p>
    </section>
  );
}

export function ReenviarVerificacao() {
  return (
    <EmailOnly
      title="Reenviar confirmação de e-mail"
      path="/v1/auth/email-verification/resend"
      button="Reenviar link"
      intro="Enviaremos um novo link de confirmação (válido por 24 horas). O link anterior deixa de valer."
    />
  );
}

export function EsqueciSenha() {
  return (
    <EmailOnly
      title="Esqueci minha senha"
      path="/v1/auth/password-reset/request"
      button="Enviar link"
      intro="Enviaremos um link para criar uma senha nova. Ele vale por 30 minutos e funciona uma única vez."
    />
  );
}
