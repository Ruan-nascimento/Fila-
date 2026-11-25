import { useEffect, useRef, useState, type RefObject } from "react";
import {
  Link,
  Navigate,
  NavLink,
  Route,
  Routes,
  useLocation,
  useParams,
  useSearchParams,
} from "react-router";
import { CalendarClock, CalendarPlus, History, MapPin, Phone, Star, Store, UsersRound } from "lucide-react";
import { useStoreBrand } from "../features/theme/tema";
import { formatPhone } from "../features/auth/acessoLoja";
import { api } from "../api/client";
import { call, problemMessage } from "../api/problem";
import type { components } from "../api/schema.gen";
import { useSession } from "../session/context";
import { StreamProvider, StreamStatusNotice } from "../stream/StreamProvider";
import { NotificacoesProvider, Sino } from "../features/notificacoes/Notificacoes";
import { PublicShell } from "../ui/layout";
import { ButtonLink, EmptyState, LoadError, Loading, Notice } from "../ui/ui";
import { cx } from "../ui/cx";
import { Pontos } from "./fidelidade/Pontos";
import { StoreDataProvider } from "./fila/dados";
import { useStoreData } from "./fila/dadosContexto";
import { Historico } from "./fila/Historico";
import { CodigoUmaVez, ContaNaLoja, Identificacao, VisitanteAtual } from "./fila/Identificacao";
import { MinhaVisita } from "./fila/MinhaVisita";
import { Agendar } from "./agenda/Agendar";
import { MinhasReservas } from "./agenda/MinhasReservas";

type PublicEstablishment = components["schemas"]["PublicEstablishment"];
type VisitorCreated = components["schemas"]["VisitorSessionCreated"];

/** Página da loja aberta pelo link/QR: filas, serviços, entrada na fila e a visita em andamento. */
export function Loja() {
  const { slug = "" } = useParams();
  const [shop, setShop] = useState<PublicEstablishment | null>(null);
  const [error, setError] = useState<{ notFound: boolean; message: string } | null>(null);
  const [attempt, setAttempt] = useState(0);
  const { state } = useSession();
  const location = useLocation();
  // A cor da loja vale só aqui dentro; ao sair ou trocar de loja, os tokens são limpos.
  useStoreBrand(shop?.slug === slug ? shop.palette : null);

  useEffect(() => {
    let active = true;
    void call(() => api.GET("/v1/public/slugs/{slug}", { params: { path: { slug } } })).then((out) => {
      if (!active) return;
      if (out.ok) {
        setShop(out.data.data);
        setError(null);
      } else
        setError({
          notFound: out.problem?.status === 404,
          message: problemMessage(out.problem, out.offline),
        });
    });
    return () => {
      active = false;
    };
  }, [slug, attempt]);

  if (error?.notFound)
    return (
      <PublicShell>
        <div role="alert">
          <EmptyState
            icon={Store}
            title={<span className="text-xl">Loja não encontrada</span>}
            action={<ButtonLink to="/">Voltar ao início</ButtonLink>}
          >
            <p>Confira o link e tente novamente.</p>
          </EmptyState>
        </div>
      </PublicShell>
    );
  if (error)
    return (
      <PublicShell>
        <LoadError message={error.message} onRetry={() => setAttempt((n) => n + 1)} />
      </PublicShell>
    );
  // Endereço antigo (a loja trocou de nome; QR Code já impresso): leva ao endereço atual,
  // mantendo o resto do caminho (histórico, pontos) e a entrada pedida (?entrada=visitante).
  if (shop && shop.slug !== slug) {
    const rest = location.pathname.replace(/^\/loja\/[^/]+/, "");
    return <Navigate to={`/loja/${shop.slug}${rest}${location.search}`} replace />;
  }
  if (!shop)
    return (
      <PublicShell>
        <Loading>Carregando loja…</Loading>
      </PublicShell>
    );
  // Uma conexão de atualizações para a página: filas públicas e, com visitante ou conta, a visita.
  // Ela fica acima das rotas internas: trocar de aba ou de layout não abre outra conexão.
  const account = state.status === "ready" ? state.session.account : null;
  const identified =
    state.status === "ready" &&
    (account !== null || state.session.visitors.some((v) => v.establishment_id === shop.id));
  return (
    <StreamProvider
      estId={shop.id}
      channels={
        account !== null ? ["public", "visit", "account"] : identified ? ["public", "visit"] : ["public"]
      }
    >
      {/* Notificações do cliente: vêm do mesmo fluxo da página (posição, chamada, atendimento…). */}
      <NotificacoesProvider
        papel="cliente"
        escopo={`cliente:${shop.id}:${
          account?.id ??
          (state.status === "ready"
            ? state.session.visitors.find((v) => v.establishment_id === shop.id)?.id
            : null) ??
          "anonimo"
        }`}
        destino={(n) => (n.aba === "pontos" ? `/loja/${shop.slug}/pontos` : `/loja/${shop.slug}`)}
        icon={shop.logo?.variants[0]?.url}
      >
        <PublicShell width="wide" store={shop} actions={identified ? <Sino /> : undefined}>
          <StoreDataProvider shopId={shop.id} ordering={shop.ordering}>
            <LojaConteudo shop={shop} />
          </StoreDataProvider>
        </PublicShell>
      </NotificacoesProvider>
    </StreamProvider>
  );
}

function LojaConteudo({ shop }: { shop: PublicEstablishment }) {
  const { state } = useSession();
  const base = `/loja/${shop.slug}`;
  const account = state.status === "ready" ? state.session.account : null;
  const identified =
    state.status === "ready" &&
    (account !== null || state.session.visitors.some((v) => v.establishment_id === shop.id));
  // Forma de atendimento (ADR-0038): a ação principal da página se adapta.
  const mode = shop.ordering?.service_mode ?? "queue";
  const tabs = [
    ...(mode !== "schedule" ? [{ to: base, label: "Fila", icon: UsersRound, end: true }] : []),
    ...(mode !== "queue"
      ? [
          {
            to: mode === "schedule" ? base : `${base}/agendar`,
            label: "Agendar horário",
            icon: CalendarPlus,
            end: true,
          },
        ]
      : []),
    ...(identified && mode !== "queue"
      ? [{ to: `${base}/reservas`, label: "Minhas reservas", icon: CalendarClock, end: false }]
      : []),
    ...(account
      ? [
          { to: `${base}/historico`, label: "Histórico", icon: History, end: false },
          { to: `${base}/pontos`, label: "Pontos", icon: Star, end: false },
        ]
      : []),
  ];
  return (
    <div className="grid gap-5">
      <header className="flex flex-wrap items-start gap-4">
        {shop.logo && (
          <img
            src={shop.logo.variants[0]?.url}
            srcSet={shop.logo.variants.map((v) => `${v.url} ${v.width}w`).join(", ")}
            sizes="64px"
            width={64}
            height={64}
            alt={`Logo de ${shop.name}`}
            decoding="async"
            className="size-16 shrink-0 rounded-xl border border-line bg-surface object-contain"
          />
        )}
        <div className="grid min-w-0 flex-1 gap-1">
          <h1 className="m-0 text-2xl font-bold tracking-tight sm:text-[1.75rem]">{shop.name}</h1>
          {shop.description && <p className="m-0 text-fg-muted">{shop.description}</p>}
          <ResumoFilas />
          {(shop.address || shop.phone) && (
            <ul className="m-0 flex list-none flex-wrap gap-x-4 gap-y-1 p-0 text-sm text-fg-muted">
              {shop.address && (
                <li className="inline-flex items-center gap-1">
                  <MapPin aria-hidden="true" className="size-4" />
                  {shop.address}
                </li>
              )}
              {shop.phone && (
                <li className="inline-flex items-center gap-1">
                  <Phone aria-hidden="true" className="size-4" />
                  <a href={`tel:${shop.phone}`}>{formatPhone(shop.phone)}</a>
                </li>
              )}
            </ul>
          )}
        </div>
      </header>
      {mode === "hybrid" && (
        <div className="flex flex-wrap gap-2">
          <ButtonLink to={base} variant="secondary">
            <UsersRound aria-hidden="true" className="size-5" /> Entrar na fila
          </ButtonLink>
          <ButtonLink to={`${base}/agendar`}>
            <CalendarPlus aria-hidden="true" className="size-5" /> Agendar horário
          </ButtonLink>
        </div>
      )}
      {tabs.length > 1 && (
        <nav aria-label="Nesta loja" className="-mx-1 overflow-x-auto">
          <ul className="m-0 flex w-max list-none gap-1 border-b border-line p-0 px-1">
            {tabs.map((t) => (
              <li key={t.to}>
                <NavLink
                  to={t.to}
                  end={t.end}
                  className={({ isActive }) =>
                    cx(
                      "-mb-px inline-flex min-h-11 items-center gap-2 border-b-2 px-3 text-sm font-semibold no-underline",
                      isActive
                        ? "border-primary text-accent"
                        : "border-transparent text-fg-muted hover:text-fg",
                    )
                  }
                >
                  <t.icon aria-hidden="true" className="size-4" />
                  {t.label}
                </NavLink>
              </li>
            ))}
          </ul>
        </nav>
      )}
      <StreamStatusNotice />
      <Routes>
        <Route index element={mode === "schedule" ? <Agendar shop={shop} /> : <FilaDaLoja shop={shop} />} />
        <Route path="visita" element={<FilaDaLoja shop={shop} />} />
        <Route
          path="agendar"
          element={mode === "queue" ? <Navigate to={base} replace /> : <Agendar shop={shop} />}
        />
        <Route
          path="reservas"
          element={
            identified ? <MinhasReservas shopId={shop.id} slug={shop.slug} /> : <Navigate to={base} replace />
          }
        />
        <Route
          path="historico"
          element={account ? <Historico shopId={shop.id} /> : <Navigate to={base} replace />}
        />
        <Route
          path="pontos"
          element={account ? <Pontos shopId={shop.id} /> : <Navigate to={base} replace />}
        />
        <Route path="*" element={<Navigate to={base} replace />} />
      </Routes>
    </div>
  );
}

/** "2 filas abertas · 3 aguardando": resumo a partir do resultado próprio das filas. */
function ResumoFilas() {
  const { queues, queuesError } = useStoreData();
  if (queues === null)
    return (
      <p className="m-0 text-sm text-fg-muted">
        {queuesError ? "Situação das filas indisponível" : "Carregando filas…"}
      </p>
    );
  const open = queues.filter((q) => q.status === "open");
  const waiting = open.reduce((n, q) => n + q.waiting_count, 0);
  return (
    <p className="m-0 flex items-center gap-2 text-sm text-fg-muted">
      <span
        aria-hidden="true"
        className={cx("size-2 rounded-full", open.length > 0 ? "bg-success" : "bg-fg-muted")}
      />
      {open.length === 0
        ? "Nenhuma fila aberta no momento"
        : `${open.length === 1 ? "1 fila aberta" : `${open.length} filas abertas`} · ${waiting} ${waiting === 1 ? "pessoa aguardando" : "pessoas aguardando"}`}
    </p>
  );
}

function useFocusNick(when: boolean, ref: RefObject<HTMLInputElement | null>) {
  useEffect(() => {
    if (when) ref.current?.focus();
  }, [when, ref]);
}

function FilaDaLoja({ shop }: { shop: PublicEstablishment }) {
  const { state, refresh } = useSession();
  const { selection } = useStoreData();
  const [created, setCreated] = useState<VisitorCreated | null>(null);
  const [params, setParams] = useSearchParams();
  // QR "entrar sem conta": abre a identificação por apelido, mas nunca cria visita sozinho.
  const guestEntry = params.get("entrada") === "visitante";
  const [needIdentity, setNeedIdentity] = useState(guestEntry);
  const nickRef = useRef<HTMLInputElement>(null);

  const visitor =
    state.status === "ready" ? state.session.visitors.find((v) => v.establishment_id === shop.id) : undefined;
  const account = state.status === "ready" ? state.session.account : null;
  const identity = account ? "account" : visitor ? "visitor" : "none";
  // Entrada sem conta pelo QR: leva o foco ao apelido (só quando ainda não há identidade).
  useFocusNick(state.status === "ready" && guestEntry && identity === "none", nickRef);
  if (state.status === "loading") return <Loading />;

  async function identified(c: VisitorCreated) {
    setCreated(c);
    setNeedIdentity(false);
    await refresh();
  }

  return (
    <div className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_22rem] lg:items-start lg:gap-8">
      <div className="grid min-w-0 gap-6">
        <MinhaVisita
          key={identity}
          shopId={shop.id}
          identified={identity !== "none"}
          onNeedIdentity={() => {
            setNeedIdentity(true);
            const el = nickRef.current;
            el?.focus();
            el?.scrollIntoView?.({ block: "center", behavior: "smooth" });
          }}
        />
      </div>
      <aside aria-label="Identificação" className="grid gap-4 lg:sticky lg:top-20">
        {guestEntry && identity === "account" && account && (
          <Notice kind="info" title="Você está conectado com sua conta">
            <p>
              O link é de entrada sem conta, mas nada foi trocado: você continua como {account.name}. Para
              entrar como visitante, saia da conta antes em <Link to="/conta">Minha conta</Link>.
            </p>
            <p>
              <button
                type="button"
                className="min-h-11 font-semibold underline"
                onClick={() => {
                  params.delete("entrada");
                  setParams(params, { replace: true });
                }}
              >
                Continuar com minha conta
              </button>
            </p>
          </Notice>
        )}
        {guestEntry && identity === "visitor" && visitor && (
          <Notice kind="info">
            <p>Você já está nesta loja sem conta, como “{visitor.nick}”. Sua visita continua aqui.</p>
          </Notice>
        )}
        {created && (
          <CodigoUmaVez
            created={created}
            selectionKept={selection.queueId !== "" && selection.serviceIds.length > 0}
            onDismiss={() => setCreated(null)}
          />
        )}
        {identity === "none" && (
          <Identificacao
            shopId={shop.id}
            shopName={shop.name}
            returnTo={`/loja/${shop.slug}`}
            needed={needIdentity}
            nickRef={nickRef}
            onIdentified={identified}
          />
        )}
        {identity === "visitor" && visitor && (
          <VisitanteAtual
            visitor={visitor}
            onEnded={async () => {
              setCreated(null);
              await refresh();
            }}
          />
        )}
        {identity === "account" && account && <ContaNaLoja name={account.name} base={`/loja/${shop.slug}`} />}
      </aside>
    </div>
  );
}
