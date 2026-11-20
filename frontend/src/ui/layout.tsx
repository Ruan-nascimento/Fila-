import type { ReactNode } from "react";
import { Link, NavLink } from "react-router";
import { House, KeyRound, LogIn, Scissors, Search, Store, UserPlus, UserRound } from "lucide-react";
import type { components } from "../api/schema.gen";
import { useSession } from "../session/context";
import { AvisosGerais } from "./estados";
import { cx } from "./cx";

/** Identidade de uma loja para o canto superior esquerdo (nome e logo atuais, vindos do servidor). */
export type StoreMark = { name: string; logo?: components["schemas"]["ImageRef"] | null | undefined };

/**
 * Canto superior esquerdo. Dentro de uma loja (página pública e gestão) mostra a logo e o nome
 * DA LOJA; sem logo, um ícone com a cor da loja. Fora de loja, a marca do produto: tesoura + "Fila".
 */
export function Brand({
  to = "/",
  sub,
  store,
}: {
  to?: string;
  sub?: string | undefined;
  store?: StoreMark | null | undefined;
}) {
  const logo = store?.logo?.variants[0];
  return (
    <Link
      to={to}
      className="inline-flex min-h-11 min-w-0 items-center gap-2 rounded-lg pr-1 text-fg no-underline"
    >
      {logo ? (
        <img
          src={logo.url}
          srcSet={store?.logo?.variants.map((v) => `${v.url} ${v.width}w`).join(", ")}
          sizes="32px"
          width={32}
          height={32}
          alt=""
          className="size-8 shrink-0 rounded-lg object-contain"
        />
      ) : (
        <span className="inline-flex size-8 shrink-0 items-center justify-center rounded-lg bg-primary text-on-primary">
          {store ? (
            <Store aria-hidden="true" className="size-[18px]" />
          ) : (
            <Scissors aria-hidden="true" className="size-[18px]" />
          )}
        </span>
      )}
      <span className="grid min-w-0 leading-tight">
        <span className="truncate text-lg font-bold tracking-tight">{store?.name || "Fila"}</span>
        {sub && <span className="truncate text-xs text-fg-muted">{sub}</span>}
      </span>
    </Link>
  );
}

export function SkipLink() {
  return (
    <a
      href="#conteudo"
      className="sr-only z-50 rounded-lg bg-primary px-4 py-3 font-semibold text-on-primary focus:not-sr-only focus:fixed focus:left-3 focus:top-3"
    >
      Pular para o conteúdo
    </a>
  );
}

/** Área de conta no cabeçalho público: "Entrar" ou o nome de quem está conectado. */
function HeaderAccount() {
  const { state } = useSession();
  if (state.status !== "ready") return null;
  const { account, session } = state.session;
  return (
    <nav aria-label="Principal" className="flex items-center gap-1">
      {session?.kind !== "staff" && (
        <NavLink
          to="/"
          end
          className="inline-flex min-h-11 items-center gap-2 rounded-lg px-3 text-sm font-semibold text-fg no-underline hover:bg-surface-2 aria-[current=page]:text-accent"
        >
          <House aria-hidden="true" className="size-5" />
          <span className="max-sm:sr-only">Início</span>
        </NavLink>
      )}
      {session?.kind === "staff" && (
        <NavLink
          to="/gestao"
          className="inline-flex min-h-11 items-center rounded-lg px-3 text-sm font-semibold text-fg no-underline hover:bg-surface-2 aria-[current=page]:text-accent"
        >
          Gestão
        </NavLink>
      )}
      {account ? (
        <NavLink
          to="/conta"
          className="inline-flex min-h-11 items-center gap-2 rounded-lg px-3 text-sm font-semibold text-fg no-underline hover:bg-surface-2 aria-[current=page]:text-accent"
        >
          <UserRound aria-hidden="true" className="size-5" />
          <span className="max-sm:sr-only">Minha conta</span>
        </NavLink>
      ) : (
        <NavLink
          to="/entrar"
          className="inline-flex min-h-11 items-center gap-2 rounded-lg px-3 text-sm font-semibold text-fg no-underline hover:bg-surface-2 aria-[current=page]:text-accent"
        >
          <LogIn aria-hidden="true" className="size-5" />
          Entrar
        </NavLink>
      )}
    </nav>
  );
}

/**
 * Moldura das telas públicas e do cliente: cabeçalho com a marca (e a loja, quando houver),
 * conteúdo numa coluna de leitura confortável. `wide` libera mais largura (loja, comanda).
 */
export function PublicShell({
  children,
  context,
  store,
  actions,
  width = "normal",
}: {
  children: ReactNode;
  context?: ReactNode;
  /** Loja aberta: o canto superior esquerdo mostra a logo e o nome dela, com link para a loja. */
  store?: (StoreMark & { slug: string }) | null | undefined;
  /** Botões extras no cabeçalho, antes da conta (ex.: sino de notificações). */
  actions?: ReactNode;
  width?: "narrow" | "normal" | "wide";
}) {
  return (
    <div className="min-h-dvh bg-bg">
      <SkipLink />
      <header className="sticky top-0 z-30 border-b border-line bg-surface/95 backdrop-blur supports-[backdrop-filter]:bg-surface/85">
        <div className="mx-auto flex max-w-6xl items-center justify-between gap-3 px-4 py-1.5 sm:px-6">
          <div className="flex min-w-0 items-center gap-3">
            {store ? <Brand to={`/loja/${store.slug}`} store={store} /> : <Brand />}
            {context && (
              <>
                <span aria-hidden="true" className="h-6 w-px shrink-0 bg-line" />
                <div className="min-w-0 truncate text-sm font-semibold">{context}</div>
              </>
            )}
          </div>
          <div className="flex items-center gap-1">
            {actions}
            <HeaderAccount />
          </div>
        </div>
      </header>
      <main
        id="conteudo"
        tabIndex={-1}
        className={cx(
          "mx-auto grid gap-4 px-4 pt-5 pb-16 outline-none sm:px-6 sm:pt-8",
          width === "narrow" && "max-w-xl",
          width === "normal" && "max-w-3xl",
          width === "wide" && "max-w-6xl",
        )}
      >
        <AvisosGerais />
        {children}
      </main>
    </div>
  );
}

const authLinks = [
  { to: "/entrar", label: "Entrar", icon: LogIn },
  { to: "/cadastro", label: "Criar conta", icon: UserPlus },
  { to: "/esqueci-senha", label: "Esqueci minha senha", icon: KeyRound },
  { to: "/exclusao", label: "Consultar exclusão", icon: Search },
];

/**
 * Autenticação e links de e-mail: formulário com largura de leitura (nunca de ponta a ponta) e,
 * do tablet em diante, os destinos de acesso ao lado.
 */
export function AuthShell({ children }: { children: ReactNode }) {
  return (
    <PublicShell width="wide">
      <div className="grid gap-8 md:grid-cols-[13rem_minmax(0,28rem)] md:justify-center lg:gap-16">
        <nav aria-label="Acesso" className="hidden md:block">
          <ul className="m-0 grid list-none gap-1 p-0">
            {authLinks.map((l) => (
              <li key={l.to}>
                <NavLink
                  to={l.to}
                  className="flex min-h-11 items-center gap-3 rounded-lg px-3 text-sm font-medium text-fg no-underline hover:bg-surface-2 aria-[current=page]:bg-select aria-[current=page]:text-accent"
                >
                  <l.icon aria-hidden="true" className="size-5" />
                  {l.label}
                </NavLink>
              </li>
            ))}
          </ul>
        </nav>
        <div className="grid min-w-0 content-start gap-4">{children}</div>
      </div>
    </PublicShell>
  );
}
