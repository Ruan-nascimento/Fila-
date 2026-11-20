import { useState, type FormEvent } from "react";
import { Link, useNavigate, useSearchParams } from "react-router";
import { Store, UserRound } from "lucide-react";
import { api } from "../api/client";
import { call, fieldErrors, problemMessage, type Problem } from "../api/problem";
import { useSession } from "../session/context";
import { safeReturn } from "../ui/retorno";
import { Field, Notice, PageHeader, PasswordField, Segmented, Submit } from "../ui/ui";
import { ConflitoDono } from "./ConflitoDono";
import { GoogleButton } from "../features/auth/GoogleButton";
import { EntrarSemConta } from "../features/auth/EntrarSemConta";
import { slugFromContext, usePublicShop } from "../features/auth/acessoLoja";
import { usePublicConfig } from "../features/config/publicConfig";
import { useStoreBrand } from "../features/theme/tema";
import { conflitoDe, type OwnerSessionConflict } from "./conflito";

/**
 * Entrar na conta. Com `equipe`, o contexto fica fixo em “Equipe da barbearia” e a pessoa volta
 * para `destino` (entrada do dono no programa Fila Gestão); a regra de acesso continua no servidor.
 */
export function Entrar({
  equipe,
  destino,
  titulo = "Entrar",
  descricao = "Acesse sua conta para acompanhar a fila ou atender clientes.",
}: { equipe?: boolean; destino?: string; titulo?: string; descricao?: string } = {}) {
  const { refresh } = useSession();
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const voltar = destino ?? safeReturn(params.get("voltar"));
  // Loja de onde a pessoa veio (QR "entrar com conta" ou botão da loja): nome, logo e cor dela.
  const slug = equipe ? null : slugFromContext(params.get("loja"), voltar);
  const { shop, missing } = usePublicShop(slug);
  useStoreBrand(shop?.palette);
  const cfg = usePublicConfig();
  const googleStatus = params.get("google");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [context, setContext] = useState<"customer" | "staff">(
    equipe || params.get("como") === "equipe" ? "staff" : "customer",
  );
  const [busy, setBusy] = useState(false);
  const [problem, setProblem] = useState<Problem | null>(null);
  const [offline, setOffline] = useState(false);
  const [conflito, setConflito] = useState<OwnerSessionConflict | null>(null);
  const [aviso, setAviso] = useState<string | null>(null);

  function seguir() {
    // Volta para a loja de onde veio (contexto mantido); equipe vai para a gestão.
    navigate(voltar ?? (shop ? `/loja/${shop.slug}` : context === "staff" ? "/gestao" : "/conta"));
  }
  // Volta para a loja sempre pelo endereço ATUAL (o link pode ter vindo de um QR com o nome antigo).
  const lojaVolta = voltar?.startsWith("/loja/")
    ? shop
      ? voltar.replace(/^\/loja\/[^/?#]+/, `/loja/${shop.slug}`)
      : voltar
    : shop
      ? `/loja/${shop.slug}`
      : undefined;

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setAviso(null);
    const out = await call(() => api.POST("/v1/auth/login", { body: { email, password, context } }));
    setBusy(false);
    if (out.ok) {
      setPassword("");
      await refresh();
      seguir();
      return;
    }
    const c = conflitoDe(out.problem);
    if (c) {
      // Credenciais certas, mas o acesso de dono está aberto em outro lugar: pergunta antes.
      setPassword("");
      setProblem(null);
      setConflito(c);
      return;
    }
    setProblem(out.problem);
    setOffline(out.offline);
  }

  const errors = fieldErrors(problem);
  if (conflito)
    return (
      <section aria-labelledby="titulo-entrar" className="grid gap-5">
        <PageHeader id="titulo-entrar" title={titulo} description={descricao} />
        <ConflitoDono
          conflito={conflito}
          onDone={() => {
            setConflito(null);
            seguir();
          }}
          onCancel={(msg) => {
            setConflito(null);
            setAviso(msg ?? "A outra sessão continua ativa. Nada foi alterado.");
          }}
        />
      </section>
    );
  return (
    <section aria-labelledby="titulo-entrar" className="grid gap-5">
      {shop && (
        <div className="flex items-center gap-3">
          {shop.logo ? (
            <img
              src={shop.logo.variants[0]?.url}
              srcSet={shop.logo.variants.map((v) => `${v.url} ${v.width}w`).join(", ")}
              sizes="48px"
              width={48}
              height={48}
              alt=""
              className="size-12 rounded-lg object-contain"
            />
          ) : (
            <span
              aria-hidden="true"
              className="inline-flex size-12 items-center justify-center rounded-lg bg-select text-accent"
            >
              <Store className="size-6" />
            </span>
          )}
          <p className="m-0 font-semibold">{shop.name}</p>
        </div>
      )}
      {missing && (
        <Notice kind="erro">
          <p>
            Não encontramos essa barbearia. Confira o link ou o QR Code. Você ainda pode entrar na sua conta.
          </p>
        </Notice>
      )}
      <PageHeader id="titulo-entrar" title={titulo} description={descricao} />
      {googleStatus && <GoogleAviso status={googleStatus} />}
      {aviso && <Notice kind="info">{aviso}</Notice>}
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
        {!equipe && (
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
        )}
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
      {!equipe && context === "customer" && (
        <div className="grid gap-3">
          {cfg?.google_login && (
            <>
              <p className="m-0 text-center text-sm text-fg-muted">ou</p>
              <GoogleButton
                intent={{ kind: "login", establishmentSlug: shop?.slug, returnTo: lojaVolta ?? undefined }}
              />
            </>
          )}
          <EntrarSemConta slug={shop?.slug ?? null} />
        </div>
      )}
      {context === "staff" && !equipe && lojaVolta && (
        <p className="m-0 text-center text-sm">
          É cliente? <Link to={lojaVolta}>Abrir a página da loja</Link>
        </p>
      )}
      <p className="m-0 flex flex-wrap justify-center gap-x-4 gap-y-1 text-sm">
        <Link to="/esqueci-senha" className="inline-flex min-h-11 items-center font-semibold">
          Esqueci minha senha
        </Link>
        {!equipe && (
          <Link
            to={
              lojaVolta
                ? `/cadastro?${new URLSearchParams({ voltar: lojaVolta, ...(shop ? { loja: shop.slug } : {}) }).toString()}`
                : "/cadastro"
            }
            className="inline-flex min-h-11 items-center font-semibold"
          >
            Criar conta de cliente
          </Link>
        )}
      </p>
      {lojaVolta && (
        <p className="m-0 text-center text-sm text-fg-muted">
          Depois de entrar, você volta para a loja. <Link to={lojaVolta}>Voltar sem entrar</Link>
        </p>
      )}
    </section>
  );
}

const googleText: Record<string, { kind: "info" | "erro"; text: string }> = {
  cancelado: { kind: "info", text: "Você cancelou no Google. Pode tentar de novo ou entrar de outro jeito." },
  falhou: { kind: "erro", text: "Não foi possível entrar com o Google. Tente de novo." },
  indisponivel: {
    kind: "erro",
    text: "Entrar com o Google não está disponível agora. Use e-mail e senha ou entre sem conta.",
  },
  conta_existente: {
    kind: "info",
    text: "Não foi possível criar a conta com este Google. Se você já tem conta com este e-mail, entre com a senha e conecte o Google em Minha conta.",
  },
  verificar_email: {
    kind: "info",
    text: "Quase lá: enviamos um link para confirmar seu e-mail. Depois de confirmar, entre com o Google de novo.",
  },
};

/** Resultado discreto do retorno do Google (nenhum detalhe técnico). */
function GoogleAviso({ status }: { status: string }) {
  const t = googleText[status];
  if (!t) return null;
  return (
    <Notice kind={t.kind}>
      <p>{t.text}</p>
    </Notice>
  );
}
