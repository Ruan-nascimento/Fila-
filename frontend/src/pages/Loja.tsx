import { useEffect, useRef, useState } from "react";
import { Navigate, NavLink, Route, Routes, useParams } from "react-router";
import { History, Star, Store, UsersRound } from "lucide-react";
import { api } from "../api/client";
import { call, problemMessage } from "../api/problem";
import type { components } from "../api/schema.gen";
import { useSession } from "../session/context";
import { StreamProvider, StreamStatusNotice } from "../stream/StreamProvider";
import { PublicShell } from "../ui/layout";
import { ButtonLink, EmptyState, LoadError, Loading } from "../ui/ui";
import { cx } from "../ui/cx";
import { Pontos } from "./fidelidade/Pontos";
import { StoreDataProvider } from "./fila/dados";
import { useStoreData } from "./fila/dadosContexto";
import { Historico } from "./fila/Historico";
import { CodigoUmaVez, ContaNaLoja, Identificacao, VisitanteAtual } from "./fila/Identificacao";
import { MinhaVisita } from "./fila/MinhaVisita";

type PublicEstablishment = components["schemas"]["PublicEstablishment"];
type VisitorCreated = components["schemas"]["VisitorSessionCreated"];

/** Página da loja aberta pelo link/QR: filas, serviços, entrada na fila e a visita em andamento. */
export function Loja() {
  const { slug = "" } = useParams();
  const [shop, setShop] = useState<PublicEstablishment | null>(null);
  const [error, setError] = useState<{ notFound: boolean; message: string } | null>(null);
  const [attempt, setAttempt] = useState(0);
  const { state } = useSession();

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
    <PublicShell width="wide" context={shop.name}>
      <StreamProvider
        estId={shop.id}
        channels={
          account !== null ? ["public", "visit", "account"] : identified ? ["public", "visit"] : ["public"]
        }
      >
        <StoreDataProvider shopId={shop.id}>
          <LojaConteudo shop={shop} />
        </StoreDataProvider>
      </StreamProvider>
    </PublicShell>
  );
}

function LojaConteudo({ shop }: { shop: PublicEstablishment }) {
  const { state } = useSession();
  const base = `/loja/${shop.slug}`;
  const account = state.status === "ready" ? state.session.account : null;
  return (
    <div className="grid gap-5">
      <header className="grid gap-1">
        <h1 className="m-0 text-2xl font-bold tracking-tight sm:text-[1.75rem]">{shop.name}</h1>
        <ResumoFilas />
      </header>
      {account && (
        <nav aria-label="Nesta loja" className="-mx-1 overflow-x-auto">
          <ul className="m-0 flex w-max list-none gap-1 border-b border-line p-0 px-1">
            {[
              { to: base, label: "Fila", icon: UsersRound, end: true },
              { to: `${base}/historico`, label: "Histórico", icon: History, end: false },
              { to: `${base}/pontos`, label: "Pontos", icon: Star, end: false },
            ].map((t) => (
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
        <Route index element={<FilaDaLoja shop={shop} />} />
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

function FilaDaLoja({ shop }: { shop: PublicEstablishment }) {
  const { state, refresh } = useSession();
  const { selection } = useStoreData();
  const [created, setCreated] = useState<VisitorCreated | null>(null);
  const [needIdentity, setNeedIdentity] = useState(false);
  const nickRef = useRef<HTMLInputElement>(null);

  if (state.status === "loading") return <Loading />;
  const visitor =
    state.status === "ready" ? state.session.visitors.find((v) => v.establishment_id === shop.id) : undefined;
  const account = state.status === "ready" ? state.session.account : null;
  const identity = account ? "account" : visitor ? "visitor" : "none";

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
