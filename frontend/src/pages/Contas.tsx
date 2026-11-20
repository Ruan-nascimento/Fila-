import { useState, type FormEvent } from "react";
import { Link, useSearchParams } from "react-router";
import { MailCheck } from "lucide-react";
import { api } from "../api/client";
import { Field, Notice, PageHeader, PasswordField, Submit } from "../ui/ui";
import { useForm } from "../ui/useForm";
import { safeReturn } from "../ui/retorno";
import { GoogleButton } from "../features/auth/GoogleButton";
import { EntrarSemConta } from "../features/auth/EntrarSemConta";
import { slugFromContext, usePublicShop } from "../features/auth/acessoLoja";
import { usePublicConfig } from "../features/config/publicConfig";
import { useStoreBrand } from "../features/theme/tema";

type Sent = { message: string } | null;

/** Resposta genérica: o servidor não revela se o e-mail existe (R-CONTA-02). */
function SentNotice({ sent }: { sent: Sent }) {
  if (!sent) return null;
  return (
    <Notice kind="ok">
      <p>{sent.message}</p>
      {import.meta.env.DEV && (
        <p className="text-sm text-fg-muted">
          Em desenvolvimento, os e-mails ficam na pasta backend/.correio (veja o README).
        </p>
      )}
    </Notice>
  );
}

export function Cadastro() {
  const form = useForm();
  const [params] = useSearchParams();
  const voltar = safeReturn(params.get("voltar"));
  const slug = slugFromContext(params.get("loja"), voltar);
  const { shop } = usePublicShop(slug);
  useStoreBrand(shop?.palette);
  const cfg = usePublicConfig();
  // Volta para a loja sempre pelo endereço ATUAL (o link pode ter vindo de um QR com o nome antigo).
  const lojaVolta = voltar?.startsWith("/loja/")
    ? shop
      ? voltar.replace(/^\/loja\/[^/?#]+/, `/loja/${shop.slug}`)
      : voltar
    : shop
      ? `/loja/${shop.slug}`
      : undefined;
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

  const entrar = lojaVolta ? `/entrar?${new URLSearchParams({ voltar: lojaVolta }).toString()}` : "/entrar";
  return (
    <section aria-labelledby="titulo-cadastro" className="grid gap-5">
      <PageHeader
        id="titulo-cadastro"
        title="Criar conta de cliente"
        description={
          shop
            ? `Para ${shop.name}: com conta você acompanha seu histórico e, quando a barbearia ativar, acumula pontos.`
            : "Com conta você acompanha seu histórico e, quando a barbearia ativar, acumula pontos."
        }
      />
      {form.generalError}
      {sent && (
        <div className="grid gap-3">
          <MailCheck aria-hidden="true" className="size-8 text-accent" />
          <SentNotice sent={sent} />
          <p className="m-0 text-sm text-fg-muted">
            Abra o link do e-mail para confirmar a conta. Não chegou?{" "}
            <Link to="/reenviar-verificacao">Reenviar confirmação</Link>
          </p>
        </div>
      )}
      {!sent && (
        <form onSubmit={onSubmit} noValidate className="grid gap-4">
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
            inputMode="email"
            required
            value={email}
            error={form.errors.email}
            onChange={(e) => setEmail(e.target.value)}
          />
          <PasswordField
            label="Senha"
            autoComplete="new-password"
            required
            minLength={10}
            maxLength={128}
            hint="Pelo menos 10 caracteres. Uma frase fácil de lembrar é uma boa senha."
            value={password}
            error={form.errors.password}
            onChange={(e) => setPassword(e.target.value)}
          />
          <Submit busy={form.busy} block>
            Criar conta
          </Submit>
        </form>
      )}
      {!sent && (
        <div className="grid gap-3">
          {cfg?.google_login && (
            <>
              <p className="m-0 text-center text-sm text-fg-muted">ou</p>
              <GoogleButton intent={{ kind: "login", establishmentSlug: shop?.slug, returnTo: lojaVolta }} />
            </>
          )}
          <EntrarSemConta slug={shop?.slug ?? null} />
        </div>
      )}
      <p className="m-0 text-center text-sm">
        Já tem conta?{" "}
        <Link to={entrar} className="font-semibold">
          Entrar
        </Link>
      </p>
      <p className="m-0 text-center text-xs text-fg-muted">
        Este cadastro cria uma conta de cliente. A equipe da barbearia entra pelo convite do dono.
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
    <section aria-labelledby="titulo-email" className="grid gap-5">
      <PageHeader id="titulo-email" title={title} description={intro} />
      {form.generalError}
      <SentNotice sent={sent} />
      {!sent && (
        <form onSubmit={onSubmit} noValidate className="grid gap-4">
          <Field
            label="E-mail"
            type="email"
            autoComplete="email"
            inputMode="email"
            required
            value={email}
            error={form.errors.email}
            onChange={(e) => setEmail(e.target.value)}
          />
          <Submit busy={form.busy} block>
            {button}
          </Submit>
        </form>
      )}
      <p className="m-0 text-center text-sm">
        <Link to="/entrar" className="font-semibold">
          Voltar para entrar
        </Link>
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
