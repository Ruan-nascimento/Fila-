import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import { Link, NavLink, Navigate, Route, Routes, useLocation, useParams } from "react-router";
import {
  ArrowLeftRight,
  ChevronRight,
  Ellipsis,
  FileClock,
  Gift,
  ReceiptText,
  ScrollText,
  Scissors,
  Store,
  TriangleAlert,
  UserRound,
  UsersRound,
  X,
} from "lucide-react";
import { api } from "../../api/client";
import { call } from "../../api/problem";
import { useBrandChanged, useStoreBrand } from "../../features/theme/tema";
import { useSession } from "../../session/context";
import { StreamProvider, StreamStatusNotice } from "../../stream/StreamProvider";
import { AvisosGerais } from "../../ui/estados";
import { ConviteNotificacoes, NotificacoesProvider, Sino } from "../../features/notificacoes/Notificacoes";
import { Brand, SkipLink, type StoreMark } from "../../ui/layout";
import { EmptyState, Loading, Notice, PageHeader, type IconType } from "../../ui/ui";
import { cx } from "../../ui/cx";
import { NaoElegivel } from "../dono/Dono";
import { lojasDoDono, modoDono } from "../dono/modo";
import { BotaoDashboard } from "../dono/Dashboard";
import { Auditoria } from "./Auditoria";
import { Comandas } from "./Comandas";
import type { StaffContext } from "./comum";
import { Exclusoes } from "./Exclusoes";
import { Filas } from "./Filas";
import { LojaConfig } from "./LojaConfig";
import { Pendencias } from "./Pendencias";
import { Premios } from "./Premios";
import { Profissionais } from "./Profissionais";
import { Servicos } from "./Servicos";

/** Lista as lojas em que a conta tem vínculo de equipe. */
export function GestaoInicio() {
  const { state } = useSession();
  // No programa Fila Gestão (modo dono), a escolha de loja é a do dono: só lojas com vínculo de dono.
  if (modoDono()) return <Navigate to="/dono" replace />;
  if (state.status !== "ready") return <Loading />;
  const { session, memberships } = state.session;
  if (!session) return <Navigate to="/entrar?como=equipe" replace />;
  if (session.kind !== "staff") {
    return (
      <Notice kind="info">
        <p>Para gerenciar a loja, saia e entre escolhendo “Equipe da barbearia”.</p>
      </Notice>
    );
  }
  const lojas = new Map<string, { name: string; roles: string[] }>();
  for (const m of memberships) {
    const cur = lojas.get(m.establishment_id) ?? { name: m.establishment_name, roles: [] };
    cur.roles.push(m.role === "owner" ? "Dono" : "Profissional");
    lojas.set(m.establishment_id, cur);
  }
  // Com uma loja só, vai direto para ela (link direto e Voltar continuam funcionando).
  if (lojas.size === 1) {
    const [id] = [...lojas.keys()];
    return <Navigate to={`/gestao/${id}/filas`} replace />;
  }
  return (
    <section aria-labelledby="titulo-gestao" className="grid gap-4">
      <PageHeader id="titulo-gestao" title="Gestão" description="Escolha a loja que vai operar." />
      {lojas.size === 0 ? (
        <EmptyState icon={Store} title="Esta conta não tem acesso de equipe ativo">
          <p>Peça ao dono da loja um convite.</p>
        </EmptyState>
      ) : (
        <ul className="m-0 grid list-none divide-y divide-line rounded-xl border border-line bg-surface p-0">
          {[...lojas].map(([id, l]) => (
            <li key={id}>
              <Link
                to={`/gestao/${id}/filas`}
                className="flex min-h-11 items-center gap-3 px-4 py-3 text-fg no-underline hover:bg-surface-2"
              >
                <Store aria-hidden="true" className="size-5 text-fg-muted" />
                <span className="flex-1">
                  <span className="block font-semibold">{l.name}</span>
                  <span className="text-sm text-fg-muted">{l.roles.join(" e ")}</span>
                </span>
                <ChevronRight aria-hidden="true" className="size-5 text-fg-muted" />
              </Link>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

type Dest = { path: string; label: string; icon: IconType; owner?: boolean };
type Group = { title: string; items: Dest[] };

/** Destinos agrupados por atividade. Só escondem o que não se aplica; quem decide é o servidor. */
const groups: Group[] = [
  {
    title: "Operação",
    items: [
      { path: "filas", label: "Filas", icon: UsersRound },
      { path: "comandas", label: "Comandas", icon: ReceiptText },
      { path: "premios", label: "Prêmios", icon: Gift },
    ],
  },
  {
    title: "Cadastros",
    items: [
      { path: "servicos", label: "Catálogo", icon: Scissors },
      { path: "profissionais", label: "Profissionais", icon: UserRound, owner: true },
      { path: "loja", label: "Configurações da loja", icon: Store, owner: true },
    ],
  },
  {
    title: "Administração",
    items: [
      { path: "pendencias", label: "Pendências", icon: TriangleAlert, owner: true },
      { path: "exclusoes", label: "Exclusões", icon: FileClock, owner: true },
      { path: "auditoria", label: "Auditoria", icon: ScrollText, owner: true },
    ],
  },
];

function visibleGroups(isOwner: boolean): Group[] {
  return groups
    .map((g) => ({ ...g, items: g.items.filter((d) => isOwner || !d.owner) }))
    .filter((g) => g.items.length > 0);
}

/** Área da equipe de uma loja. */
export function GestaoLoja() {
  const { estId = "" } = useParams();
  const { state } = useSession();
  if (state.status !== "ready") return <Loading />;
  const { session, memberships, account } = state.session;
  const dono = modoDono();
  if (!session || session.kind !== "staff") return <Navigate to={dono ? "/dono" : "/gestao"} replace />;
  const mine = memberships.filter((m) => m.establishment_id === estId);
  if (mine.length === 0)
    return (
      <div className="mx-auto max-w-xl p-6">
        <Notice kind="erro" title="Você não tem acesso a esta loja.">
          <p>
            <Link to="/gestao">Ver as lojas desta conta</Link>
          </p>
        </Notice>
      </div>
    );
  const ctx: StaffContext = {
    estId,
    estName: mine[0]?.establishment_name ?? "",
    isOwner: mine.some((m) => m.role === "owner"),
    professionalId: mine.find((m) => m.professional_id)?.professional_id ?? null,
  };
  // Modo dono: endereço direto ou troca de loja para uma loja sem vínculo de dono é recusado aqui
  // também (o servidor já recusa o que é só do dono; isto impede operar como profissional).
  if (dono && !ctx.isOwner)
    return (
      <div className="mx-auto max-w-xl p-6">
        <NaoElegivel titulo="Nesta janela só aparecem lojas em que você é dono." verLojas>
          <p>Nesta loja a conta é profissional. Para atender, use o navegador, em Gestão.</p>
        </NaoElegivel>
      </div>
    );
  const stores = dono
    ? lojasDoDono(state.session).length
    : new Set(memberships.map((m) => m.establishment_id)).size;
  // Endereços completos: dentro de uma rota "/gestao/:estId/*", um caminho relativo é somado ao
  // endereço atual (react-router 7), e "filas" a partir de /filas/x viraria /filas/x/filas…
  const base = `/gestao/${estId}`;
  const owner = (el: ReactNode) => (ctx.isOwner ? el : <Navigate to={`${base}/filas`} replace />);
  return (
    // Uma conexão de atualizações para toda a área da equipe desta loja. O layout muda só por CSS
    // (a mesma árvore em qualquer largura): trocar de tamanho não abre outra conexão.
    <StreamProvider estId={estId} channels={ctx.isOwner ? ["owner"] : ["professional"]}>
      <StaffShell
        ctx={ctx}
        base={base}
        userName={account?.name ?? ""}
        accountId={account?.id ?? ""}
        multipleStores={stores > 1}
      >
        <Routes>
          <Route path="filas/:queueId?" element={<Filas ctx={ctx} />} />
          <Route path="comandas/:orderId?" element={<Comandas ctx={ctx} />} />
          <Route path="servicos" element={<Servicos ctx={ctx} />} />
          <Route path="premios" element={<Premios ctx={ctx} />} />
          <Route path="pendencias" element={owner(<Pendencias ctx={ctx} />)} />
          <Route path="profissionais" element={owner(<Profissionais ctx={ctx} />)} />
          <Route path="loja" element={owner(<LojaConfig ctx={ctx} />)} />
          <Route path="exclusoes" element={owner(<Exclusoes ctx={ctx} />)} />
          <Route path="auditoria" element={owner(<Auditoria ctx={ctx} />)} />
          <Route path="*" element={<Navigate to={`${base}/filas`} replace />} />
        </Routes>
      </StaffShell>
    </StreamProvider>
  );
}

function navItemClass({ isActive }: { isActive: boolean }) {
  return cx(
    "flex min-h-11 items-center gap-3 rounded-lg px-3 text-sm font-medium no-underline",
    isActive ? "bg-select font-semibold text-accent" : "text-fg hover:bg-surface-2",
  );
}

function StaffShell({
  ctx,
  base,
  userName,
  accountId,
  multipleStores,
  children,
}: {
  ctx: StaffContext;
  base: string;
  userName: string;
  accountId: string;
  multipleStores: boolean;
  children: ReactNode;
}) {
  const gs = visibleGroups(ctx.isOwner);
  const role = ctx.isOwner ? "Dono" : "Profissional";
  // Nome e logo ATUAIS da loja (relidos quando o dono salva): o canto superior esquerdo muda na hora.
  const marca = useMarcaDaLoja(ctx.estId);
  const store = { name: marca?.name ?? ctx.estName, logo: marca?.logo };
  return (
    // Barbeiro e dono recebem as mesmas notificações (cada um das filas que pode ver).
    <NotificacoesProvider
      papel="equipe"
      escopo={`equipe:${ctx.estId}:${accountId}`}
      destino={(n) => (n.queueId ? `${base}/filas/${n.queueId}` : `${base}/filas`)}
      icon={marca?.logo?.variants[0]?.url}
    >
      <div className="min-h-dvh bg-bg lg:grid lg:grid-cols-[16rem_minmax(0,1fr)]">
        <SkipLink />
        {/* Computador: navegação lateral agrupada por atividade */}
        <aside className="sticky top-0 z-30 hidden h-dvh flex-col border-r border-line bg-surface lg:flex">
          <div className="flex items-center justify-between gap-1 px-4 pt-4 pb-2">
            <Brand to={`${base}/filas`} store={store} sub="Gestão" />
            <Sino abrirPara="direita" />
          </div>
          <nav aria-label="Gestão da loja" className="flex-1 overflow-y-auto px-3 py-2">
            {ctx.isOwner && (
              <div className="mb-3">
                <BotaoDashboard estId={ctx.estId} />
              </div>
            )}
            {gs.map((g) => (
              <div key={g.title} className="mb-4">
                <p className="m-0 mb-1 px-3 text-xs font-semibold tracking-wide text-fg-muted uppercase">
                  {g.title}
                </p>
                <ul className="m-0 grid list-none gap-0.5 p-0">
                  {g.items.map((d) => (
                    <li key={d.path}>
                      <NavLink to={`${base}/${d.path}`} className={navItemClass}>
                        <d.icon aria-hidden="true" className="size-5" />
                        {d.label}
                      </NavLink>
                    </li>
                  ))}
                </ul>
              </div>
            ))}
          </nav>
          <div className="grid gap-1 border-t border-line p-3">
            {multipleStores && (
              <Link
                to="/gestao"
                className="flex min-h-11 items-center gap-3 rounded-lg px-3 text-sm text-fg no-underline hover:bg-surface-2"
              >
                <ArrowLeftRight aria-hidden="true" className="size-5" />
                Trocar de loja
              </Link>
            )}
            <Link
              to="/conta"
              className="flex min-h-11 items-center gap-3 rounded-lg px-3 text-sm text-fg no-underline hover:bg-surface-2"
            >
              <UserRound aria-hidden="true" className="size-5" />
              <span className="grid min-w-0 leading-tight">
                <span className="font-medium">Minha conta</span>
                <span className="truncate text-xs text-fg-muted">
                  {userName ? `${userName} · ${role}` : role}
                </span>
              </span>
            </Link>
          </div>
        </aside>

        <div className="flex min-h-dvh min-w-0 flex-col">
          {/* Celular e tablet: barra superior com a loja em operação */}
          <header className="sticky top-0 z-30 border-b border-line bg-surface/95 backdrop-blur lg:hidden">
            <div className="flex items-center justify-between gap-3 px-4 py-1.5">
              <Brand to={`${base}/filas`} store={store} sub="Gestão" />
              <Sino className="ml-auto" />
              {ctx.isOwner && <BotaoDashboard estId={ctx.estId} compact />}
              <Link
                to="/conta"
                aria-label={`Minha conta (${role})`}
                className="inline-flex size-11 items-center justify-center rounded-lg text-fg hover:bg-surface-2"
              >
                <UserRound aria-hidden="true" className="size-5" />
              </Link>
            </div>
          </header>
          <main
            id="conteudo"
            tabIndex={-1}
            className="mx-auto grid w-full max-w-6xl flex-1 content-start gap-4 px-4 pt-5 pb-28 outline-none sm:px-6 lg:px-8 lg:pt-8 lg:pb-12"
          >
            <AvisosGerais />
            <StreamStatusNotice />
            <ConviteNotificacoes texto="Quer ser avisado quando alguém entrar na fila, sair, avisar que vai atrasar ou pedir mudança na comanda? Ative as notificações neste aparelho." />
            {children}
          </main>
          <BottomNav base={base} groups={gs} multipleStores={multipleStores} />
        </div>
      </div>
    </NotificacoesProvider>
  );
}

/** Navegação compacta (celular e tablet): Filas, Comandas e Mais (os demais destinos do perfil). */
function BottomNav({
  base,
  groups: gs,
  multipleStores,
}: {
  base: string;
  groups: Group[];
  multipleStores: boolean;
}) {
  const [open, setOpen] = useState(false);
  const location = useLocation();
  const triggerRef = useRef<HTMLButtonElement>(null);
  const primary = ["filas", "comandas"];
  const extra = gs
    .map((g) => ({ ...g, items: g.items.filter((d) => !primary.includes(d.path)) }))
    .filter((g) => g.items.length > 0);
  const inExtra = extra.some((g) => g.items.some((d) => location.pathname.startsWith(`${base}/${d.path}`)));

  // Fecha ao navegar (inclusive pelo Voltar do navegador).
  const [path, setPath] = useState(location.pathname);
  if (path !== location.pathname) {
    setPath(location.pathname);
    setOpen(false);
  }

  const tab =
    "flex min-h-14 flex-1 flex-col items-center justify-center gap-0.5 text-xs font-medium no-underline";
  return (
    <>
      <nav
        aria-label="Gestão da loja"
        className="pb-safe fixed inset-x-0 bottom-0 z-30 border-t border-line bg-surface lg:hidden"
      >
        <ul className="m-0 mx-auto flex max-w-xl list-none p-0">
          {[
            { path: "filas", label: "Filas", icon: UsersRound },
            { path: "comandas", label: "Comandas", icon: ReceiptText },
          ].map((d) => (
            <li key={d.path} className="flex flex-1">
              <NavLink
                to={`${base}/${d.path}`}
                className={({ isActive }) =>
                  cx(
                    tab,
                    "border-t-2",
                    isActive ? "border-primary text-accent" : "border-transparent text-fg-muted",
                  )
                }
              >
                <d.icon aria-hidden="true" className="size-6" />
                {d.label}
              </NavLink>
            </li>
          ))}
          <li className="flex flex-1">
            <button
              ref={triggerRef}
              type="button"
              aria-haspopup="dialog"
              aria-expanded={open}
              onClick={() => setOpen(true)}
              className={cx(
                tab,
                "border-t-2",
                inExtra ? "border-primary text-accent" : "border-transparent text-fg-muted",
              )}
            >
              <Ellipsis aria-hidden="true" className="size-6" />
              Mais
            </button>
          </li>
        </ul>
      </nav>
      {open && (
        <MaisSheet
          base={base}
          groups={extra}
          multipleStores={multipleStores}
          onClose={() => {
            setOpen(false);
            triggerRef.current?.focus();
          }}
        />
      )}
    </>
  );
}

/** Folha com os demais destinos: foco entra nela, Tab fica dentro, Escape fecha e o foco volta. */
function MaisSheet({
  base,
  groups: gs,
  multipleStores,
  onClose,
}: {
  base: string;
  groups: Group[];
  multipleStores: boolean;
  onClose: () => void;
}) {
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    ref.current?.querySelector<HTMLElement>("a, button")?.focus();
  }, []);
  function onKeyDown(e: React.KeyboardEvent) {
    if (e.key === "Escape") {
      e.preventDefault();
      onClose();
      return;
    }
    if (e.key !== "Tab") return;
    const items = Array.from(ref.current?.querySelectorAll<HTMLElement>("a, button") ?? []);
    const first = items[0];
    const last = items.at(-1);
    if (e.shiftKey && document.activeElement === first) {
      e.preventDefault();
      last?.focus();
    } else if (!e.shiftKey && document.activeElement === last) {
      e.preventDefault();
      first?.focus();
    }
  }
  return (
    <div className="fixed inset-0 z-40 lg:hidden">
      <div aria-hidden="true" className="absolute inset-0 bg-black/40" onClick={onClose} />
      <div
        ref={ref}
        role="dialog"
        aria-modal="true"
        aria-labelledby="titulo-mais"
        onKeyDown={onKeyDown}
        className="pb-safe absolute inset-x-0 bottom-0 max-h-[85dvh] overflow-y-auto rounded-t-2xl border-t border-line bg-surface p-4 shadow-2xl"
      >
        <div className="mb-2 flex items-center justify-between">
          <h2 id="titulo-mais" className="m-0 text-base font-semibold">
            Mais
          </h2>
          <button
            type="button"
            onClick={onClose}
            aria-label="Fechar"
            className="inline-flex size-11 items-center justify-center rounded-lg text-fg-muted hover:bg-surface-2"
          >
            <X aria-hidden="true" className="size-5" />
          </button>
        </div>
        {gs.map((g) => (
          <div key={g.title} className="mb-3">
            <p className="m-0 mb-1 px-3 text-xs font-semibold tracking-wide text-fg-muted uppercase">
              {g.title}
            </p>
            <ul className="m-0 grid list-none gap-0.5 p-0">
              {g.items.map((d) => (
                <li key={d.path}>
                  <NavLink to={`${base}/${d.path}`} className={navItemClass} onClick={onClose}>
                    <d.icon aria-hidden="true" className="size-5" />
                    {d.label}
                  </NavLink>
                </li>
              ))}
            </ul>
          </div>
        ))}
        <div className="grid gap-0.5 border-t border-line pt-3">
          {multipleStores && (
            <Link to="/gestao" className={navItemClass({ isActive: false })} onClick={onClose}>
              <ArrowLeftRight aria-hidden="true" className="size-5" />
              Trocar de loja
            </Link>
          )}
          <Link to="/conta" className={navItemClass({ isActive: false })} onClick={onClose}>
            <UserRound aria-hidden="true" className="size-5" />
            Minha conta
          </Link>
        </div>
      </div>
    </div>
  );
}

type Marca = { estId: string; palette: string; name: string; logo: StoreMark["logo"] };

/**
 * Identidade da loja na gestão (paleta, nome e logo); relê depois que o dono salva. Também
 * atualiza a sessão, para que a lista de lojas e os demais lugares mostrem o nome novo.
 */
function useMarcaDaLoja(estId: string): Marca | null {
  const { refresh } = useSession();
  const [got, setGot] = useState<Marca | null>(null);
  const [version, setVersion] = useState(0);
  const reload = useCallback(() => {
    setVersion((n) => n + 1);
    void refresh();
  }, [refresh]);
  useBrandChanged(estId, reload);
  useEffect(() => {
    let active = true;
    void call(() =>
      api.GET("/v1/establishments/{establishmentId}", { params: { path: { establishmentId: estId } } }),
    ).then((out) => {
      if (active && out.ok) {
        const d = out.data.data;
        setGot({ estId, palette: d.palette, name: d.name, logo: d.logo });
      }
    });
    return () => {
      active = false;
    };
  }, [estId, version]);
  const mine = got?.estId === estId ? got : null;
  useStoreBrand(mine?.palette);
  return mine;
}
