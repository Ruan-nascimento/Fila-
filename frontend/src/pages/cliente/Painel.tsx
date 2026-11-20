import { useCallback, useEffect, useId, useRef, useState, type FormEvent } from "react";
import { Link } from "react-router";
import { ArrowRight, Clock, MapPin, QrCode, Search, Store, UsersRound, X } from "lucide-react";
import { api } from "../../api/client";
import { call, problemMessage } from "../../api/problem";
import type { components } from "../../api/schema.gen";
import { paletteVars } from "../../features/theme/paletas";
import { espera } from "../../tempo";
import { ApiStatus } from "../../features/status/ApiStatus";
import { useSession } from "../../session/context";
import { Badge, ButtonLink, Button, EmptyState, LoadError, Loading, Segmented } from "../../ui/ui";
import { cx } from "../../ui/cx";
import { arredondar, distancia, situacao } from "./formato";

type MyVisit = components["schemas"]["MyVisit"];
type Item = components["schemas"]["DirectoryEstablishment"];
type Ordem = "distance" | "open" | "name";
type ImageRef = components["schemas"]["ImageRef"];

const time = new Intl.DateTimeFormat("pt-BR", { timeStyle: "short" });

/**
 * Painel do cliente (página inicial): as filas em que a pessoa está, em qualquer barbearia, e a
 * busca de barbearias (mais perto primeiro com a localização permitida; senão, abertas agora ou
 * A–Z), com páginas. Quem é da equipe tem o atalho para a gestão.
 */
export function PainelCliente() {
  const { state } = useSession();
  const s = state.status === "ready" ? state.session : null;
  const staff = s?.session?.kind === "staff";
  const temIdentidade = Boolean(s?.account) || (s?.visitors.length ?? 0) > 0;
  const nome = s?.account?.name.split(" ")[0];
  return (
    <div className="grid gap-8">
      <header className="grid gap-1">
        <h1 className="m-0 text-2xl font-bold tracking-tight sm:text-3xl">
          {nome ? `Olá, ${nome}!` : "Encontre sua barbearia"}
        </h1>
        <p className="m-0 text-fg-muted">
          {temIdentidade
            ? "Acompanhe suas filas e encontre outras barbearias."
            : "Veja quem está aberto agora, quanto tempo de espera e entre na fila pelo celular."}
        </p>
      </header>

      {staff && (
        <div className="flex flex-wrap items-center justify-between gap-3 rounded-xl border border-line bg-surface p-4">
          <p className="m-0 text-sm">Você está com o acesso da equipe.</p>
          <ButtonLink to="/gestao" icon={Store}>
            Ir para a gestão
          </ButtonLink>
        </div>
      )}

      {temIdentidade && <MinhasFilas />}
      <Buscar />

      <aside className="grid gap-3 rounded-xl border border-dashed border-line p-4 sm:grid-cols-[auto_1fr] sm:items-center">
        <span className="inline-flex size-11 items-center justify-center rounded-xl bg-select text-accent">
          <QrCode aria-hidden="true" className="size-6" />
        </span>
        <div className="grid gap-1">
          <p className="m-0 font-semibold">Está na barbearia?</p>
          <p className="m-0 text-sm text-fg-muted">
            Aponte a câmera para o QR code do balcão: ele abre a loja direto, sem precisar buscar.
          </p>
        </div>
        {!s?.account && (
          <div className="flex flex-wrap gap-2 sm:col-span-2">
            <ButtonLink to="/entrar" variant="secondary" icon={UsersRound}>
              Entrar na minha conta
            </ButtonLink>
            <ButtonLink to="/entrar?como=equipe" variant="ghost" icon={Store}>
              Acesso da equipe
            </ButtonLink>
          </div>
        )}
      </aside>
      <ApiStatus quietWhenReady />
    </div>
  );
}

/* ---------------------------------------------------------------- Minhas filas */

/**
 * As filas em que a pessoa está. Cada loja tem a própria conexão de tempo real na página dela;
 * aqui, que junta várias lojas, a lista é relida ao voltar para a aba e a cada 30 s.
 */
function MinhasFilas() {
  const [lista, setLista] = useState<MyVisit[] | null>(null);
  const [erro, setErro] = useState<string | null>(null);
  const [lidoEm, setLidoEm] = useState<Date | null>(null);

  const [tentativa, setTentativa] = useState(0);
  const ativo = useRef(true);
  const ler = useCallback(() => {
    void call(() => api.GET("/v1/my-visits")).then((out) => {
      if (!ativo.current) return;
      if (out.ok) {
        setLista(out.data.data);
        setErro(null);
        setLidoEm(new Date());
      } else if (out.problem?.status === 401) setLista([]);
      else setErro(problemMessage(out.problem, out.offline));
    });
  }, []);

  useEffect(() => {
    ativo.current = true;
    ler();
    const visivel = () => document.visibilityState === "visible" && ler();
    const timer = window.setInterval(() => {
      if (document.visibilityState === "visible") ler();
    }, 30_000);
    document.addEventListener("visibilitychange", visivel);
    return () => {
      ativo.current = false;
      window.clearInterval(timer);
      document.removeEventListener("visibilitychange", visivel);
    };
  }, [ler, tentativa]);

  return (
    <section aria-labelledby="titulo-minhas-filas" className="grid gap-3">
      <div className="flex flex-wrap items-baseline justify-between gap-2">
        <h2 id="titulo-minhas-filas" className="m-0 text-lg font-semibold">
          Minhas filas
        </h2>
        {lidoEm && (
          <span className="text-xs text-fg-muted tabular-nums">Atualizado às {time.format(lidoEm)}</span>
        )}
      </div>
      {erro && !lista ? (
        <LoadError message={erro} onRetry={() => setTentativa((n) => n + 1)} />
      ) : !lista ? (
        <Loading>Carregando suas filas…</Loading>
      ) : lista.length === 0 ? (
        <p className="m-0 rounded-xl border border-dashed border-line px-4 py-5 text-center text-sm text-fg-muted">
          Você não está em nenhuma fila agora. Escolha uma barbearia abaixo.
        </p>
      ) : (
        <ul className="m-0 grid list-none gap-3 p-0 sm:grid-cols-2">
          {lista.map((v) => (
            <CartaoVisita key={v.visit.participation.id} v={v} />
          ))}
        </ul>
      )}
    </section>
  );
}

function Logo({
  logo,
  nome,
  palette,
  size = 48,
}: {
  logo: ImageRef | null | undefined;
  nome: string;
  palette: string;
  size?: number;
}) {
  const v = logo?.variants[0];
  if (v)
    return (
      <img
        src={v.url}
        srcSet={logo?.variants.map((x) => `${x.url} ${x.width}w`).join(", ")}
        sizes={`${size}px`}
        width={size}
        height={size}
        alt=""
        decoding="async"
        loading="lazy"
        className="shrink-0 rounded-xl border border-line bg-surface object-contain"
        style={{ width: size, height: size }}
      />
    );
  return (
    <span
      aria-hidden="true"
      data-brand={palette}
      className="inline-flex shrink-0 items-center justify-center rounded-xl bg-primary text-lg font-bold text-on-primary"
      style={{ ...paletteVars(palette), width: size, height: size }}
    >
      {nome.trim().charAt(0).toUpperCase() || <Store className="size-5" />}
    </span>
  );
}

function CartaoVisita({ v }: { v: MyVisit }) {
  const p = v.visit.participation;
  const s = situacao(v.visit);
  return (
    <li>
      <Link
        to={`/loja/${v.establishment.slug}`}
        className={cx(
          "grid gap-3 rounded-xl border bg-surface p-4 text-fg no-underline transition-colors hover:border-primary/60",
          s.destaque ? "border-primary ring-2 ring-primary/30" : "border-line",
        )}
      >
        <div className="flex items-center gap-3">
          <Logo
            logo={v.establishment.logo}
            nome={v.establishment.name}
            palette={v.establishment.palette}
            size={44}
          />
          <div className="grid min-w-0 flex-1">
            <span className="truncate font-semibold">{v.establishment.name}</span>
            <span className="truncate text-sm text-fg-muted">com {v.professional_name}</span>
          </div>
          <ArrowRight aria-hidden="true" className="size-5 shrink-0 text-fg-muted" />
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <Badge tone={s.tom === "neutral" ? "neutral" : s.tom}>{s.texto}</Badge>
          <span className="text-sm text-fg-muted">
            Senha <strong className="text-fg">{p.ticket_code}</strong>
          </span>
          {p.status === "waiting" && p.estimated_wait && (
            <span className="inline-flex items-center gap-1 text-sm text-fg-muted">
              <Clock aria-hidden="true" className="size-4" />~{espera(p.estimated_wait.minutes)}
            </span>
          )}
          {p.status === "called" && p.tolerance_ends_at && (
            <span className="text-sm font-semibold">
              Apresente-se até {time.format(new Date(p.tolerance_ends_at))}
            </span>
          )}
        </div>
      </Link>
    </li>
  );
}

/* ---------------------------------------------------------------- Encontrar barbearias */

type Posicao = { lat: number; lng: number };
type Gps = "idle" | "asking" | "ok" | "denied" | "unavailable";

/** Já permitiu antes? Então usa a localização sem perguntar de novo. */
async function gpsJaPermitido(): Promise<boolean> {
  try {
    const st = await navigator.permissions?.query({ name: "geolocation" as PermissionName });
    return st?.state === "granted";
  } catch {
    return false;
  }
}

function lerPosicao(): Promise<Posicao> {
  return new Promise((ok, falha) => {
    if (!("geolocation" in navigator)) {
      falha(new Error("sem gps"));
      return;
    }
    navigator.geolocation.getCurrentPosition(
      (p) => ok({ lat: arredondar(p.coords.latitude), lng: arredondar(p.coords.longitude) }),
      (e) => falha(e),
      { enableHighAccuracy: false, timeout: 10_000, maximumAge: 5 * 60_000 },
    );
  });
}

type Resultado = { chave: string; itens: Item[] | null; cursor: string | null; erro: string | null };

type Busca = { ok: true; itens: Item[]; cursor: string | null } | { ok: false; erro: string };

async function buscarBarbearias(
  termo: string,
  sort: Ordem,
  pos: Posicao | null,
  cursor: string | null,
): Promise<Busca> {
  const query: { q?: string; sort: Ordem; lat?: number; lng?: number; limit: number; cursor?: string } = {
    sort,
    limit: 12,
  };
  if (termo) query.q = termo;
  if (pos) {
    query.lat = pos.lat;
    query.lng = pos.lng;
  }
  if (cursor) query.cursor = cursor;
  const out = await call(() => api.GET("/v1/public/establishments", { params: { query } }));
  if (!out.ok) return { ok: false, erro: problemMessage(out.problem, out.offline) };
  return {
    ok: true,
    itens: out.data.data,
    cursor: out.data.page.has_more ? (out.data.page.next_cursor ?? null) : null,
  };
}

function Buscar() {
  const [texto, setTexto] = useState("");
  const [termo, setTermo] = useState("");
  const [ordem, setOrdem] = useState<Ordem>("open");
  const [pos, setPos] = useState<Posicao | null>(null);
  const [gps, setGps] = useState<Gps>("idle");
  // Resultado da busca atual (a chave diz de qual busca ele é: busca nova = "carregando").
  const [res, setRes] = useState<Resultado | null>(null);
  const [mais, setMais] = useState<{ carregando: boolean; erro: string | null }>({
    carregando: false,
    erro: null,
  });
  const [tentativa, setTentativa] = useState(0);
  const buscaId = useId();

  const pedirGps = useCallback(async () => {
    setGps("asking");
    try {
      setPos(await lerPosicao());
      setGps("ok");
      setOrdem("distance");
    } catch (e) {
      const negado = (e as GeolocationPositionError | undefined)?.code === 1;
      setGps(negado ? "denied" : "unavailable");
      setOrdem((o) => (o === "distance" ? "open" : o));
    }
  }, []);

  // Se a pessoa já permitiu antes, ordena por distância logo de início.
  useEffect(() => {
    void gpsJaPermitido().then((sim) => sim && void pedirGps());
  }, [pedirGps]);

  // Busca com pausa: espera a pessoa parar de digitar.
  useEffect(() => {
    const t = window.setTimeout(() => setTermo(texto.trim()), 300);
    return () => window.clearTimeout(t);
  }, [texto]);

  const sort: Ordem = ordem === "distance" && !pos ? "open" : ordem;
  const coords = sort === "distance" ? pos : null;
  const chave = JSON.stringify([termo, sort, coords, tentativa]);

  useEffect(() => {
    let ativo = true;
    void buscarBarbearias(termo, sort, coords, null).then((out) => {
      if (!ativo) return; // resposta de uma busca antiga
      setRes(
        out.ok
          ? { chave, itens: out.itens, cursor: out.cursor, erro: null }
          : { chave, itens: null, cursor: null, erro: out.erro },
      );
    });
    return () => {
      ativo = false;
    };
    // chave resume termo, ordem, posição e tentativa
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [chave]);

  const carregando = res?.chave !== chave;
  const itens = res?.itens ?? null;
  const erro = res?.erro ?? null;
  const cursor = carregando ? null : (res?.cursor ?? null);

  async function verMais() {
    if (!res || !cursor) return;
    setMais({ carregando: true, erro: null });
    const out = await buscarBarbearias(termo, sort, coords, cursor);
    if (!out.ok) {
      setMais({ carregando: false, erro: out.erro });
      return;
    }
    setMais({ carregando: false, erro: null });
    setRes((r) =>
      r && r.chave === chave ? { ...r, itens: [...(r.itens ?? []), ...out.itens], cursor: out.cursor } : r,
    );
  }

  function onSubmit(e: FormEvent) {
    e.preventDefault();
    setTermo(texto.trim());
  }

  const opcoes: { value: Ordem; label: string; icon?: typeof MapPin }[] = [
    { value: "distance", label: "Mais perto" },
    { value: "open", label: "Abertas agora" },
    { value: "name", label: "A–Z" },
  ];

  return (
    <section aria-labelledby="titulo-buscar" className="grid gap-4">
      <h2 id="titulo-buscar" className="m-0 text-lg font-semibold">
        Barbearias
      </h2>
      <form
        role="search"
        onSubmit={onSubmit}
        className="grid gap-3 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-end"
      >
        <div className="grid gap-1.5">
          <label htmlFor={buscaId} className="text-sm font-semibold">
            Buscar por nome ou endereço
          </label>
          <div className="relative">
            <Search
              aria-hidden="true"
              className="pointer-events-none absolute top-1/2 left-3 size-5 -translate-y-1/2 text-fg-muted"
            />
            <input
              id={buscaId}
              type="search"
              enterKeyHint="search"
              autoComplete="off"
              maxLength={80}
              value={texto}
              onChange={(e) => setTexto(e.target.value)}
              placeholder="Ex.: Barbearia do Zé, Rua das Flores"
              className="min-h-11 w-full rounded-lg border border-line bg-surface py-2 pr-11 pl-10 text-base"
            />
            {texto && (
              <button
                type="button"
                aria-label="Limpar busca"
                onClick={() => setTexto("")}
                className="absolute top-1/2 right-1 inline-flex size-9 -translate-y-1/2 items-center justify-center rounded-lg text-fg-muted hover:bg-surface-2"
              >
                <X aria-hidden="true" className="size-4" />
              </button>
            )}
          </div>
        </div>
        <div className="sm:w-96">
          <Segmented
            legend="Ordenar"
            name="ordem-barbearias"
            value={ordem}
            options={opcoes}
            onChange={(o) => {
              // "Mais perto" pede a localização só neste toque (e só se ainda não temos).
              if (o === "distance" && !pos) void pedirGps();
              else setOrdem(o);
            }}
          />
        </div>
      </form>

      <p role="status" className="m-0 text-sm text-fg-muted">
        {gps === "asking"
          ? "Pedindo sua localização…"
          : gps === "denied"
            ? "Sem acesso à localização: mostrando as abertas agora primeiro. Para ver as mais perto, libere a localização nas configurações do navegador."
            : gps === "unavailable"
              ? "Não foi possível obter sua localização agora. Mostrando as abertas agora primeiro."
              : ordem === "distance" && pos
                ? "Mais perto de você primeiro (distância em linha reta). Sua localização não é guardada."
                : ordem === "open"
                  ? "Abertas agora primeiro, depois por nome."
                  : "Em ordem alfabética."}
      </p>

      {erro && !itens ? (
        <LoadError message={erro} onRetry={() => setTentativa((n) => n + 1)} />
      ) : !itens ? (
        <Loading>Procurando barbearias…</Loading>
      ) : itens.length === 0 ? (
        <EmptyState icon={Search} title="Nenhuma barbearia encontrada">
          <p>
            {termo
              ? `Nada com “${termo}”. Confira a escrita ou tente outro nome.`
              : "Ainda não há barbearias por aqui."}
          </p>
        </EmptyState>
      ) : (
        <>
          <ul
            aria-busy={carregando}
            className={cx("m-0 grid list-none gap-3 p-0 sm:grid-cols-2", carregando && "opacity-70")}
          >
            {itens.map((it) => (
              <CartaoBarbearia key={it.id} it={it} />
            ))}
          </ul>
          {mais.erro && <LoadError message={mais.erro} onRetry={() => void verMais()} />}
          {cursor && (
            <Button
              variant="secondary"
              disabled={mais.carregando}
              onClick={() => void verMais()}
              className="justify-self-center"
            >
              {mais.carregando ? "Carregando…" : "Ver mais barbearias"}
            </Button>
          )}
        </>
      )}
    </section>
  );
}

function CartaoBarbearia({ it }: { it: Item }) {
  const aberta = it.open_queues > 0;
  return (
    <li>
      <Link
        to={`/loja/${it.slug}`}
        className="flex h-full items-start gap-3 rounded-xl border border-line bg-surface p-4 text-fg no-underline transition-colors hover:border-primary/60"
      >
        <Logo logo={it.logo} nome={it.name} palette={it.palette} />
        <div className="grid min-w-0 flex-1 gap-1">
          <span className="font-semibold">{it.name}</span>
          {it.public_address && (
            <span className="inline-flex items-start gap-1 text-sm text-fg-muted">
              <MapPin aria-hidden="true" className="mt-0.5 size-4 shrink-0" />
              <span className="min-w-0">{it.public_address}</span>
            </span>
          )}
          <span className="flex flex-wrap items-center gap-x-3 gap-y-1 text-sm">
            <Badge tone={aberta ? "success" : "neutral"} dot>
              {aberta ? "Aberta agora" : "Fechada agora"}
            </Badge>
            {aberta && (
              <span className="text-fg-muted">
                {it.waiting_count === 0
                  ? "Sem fila"
                  : `${it.waiting_count === 1 ? "1 pessoa" : `${it.waiting_count} pessoas`} na fila · ${
                      !it.shortest_wait_minutes
                        ? "tem profissional livre"
                        : `menor espera ~${espera(it.shortest_wait_minutes)}`
                    }`}
              </span>
            )}
            {it.distance_meters !== null && (
              <span className="font-semibold tabular-nums">{distancia(it.distance_meters)}</span>
            )}
          </span>
        </div>
      </Link>
    </li>
  );
}
