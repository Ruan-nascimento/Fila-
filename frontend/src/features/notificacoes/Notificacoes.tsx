import { useCallback, useEffect, useId, useMemo, useRef, useState, type ReactNode } from "react";
import { Bell, BellOff, BellRing, CheckCheck, ChevronRight, History, Search, Trash2, X } from "lucide-react";
import { createPortal } from "react-dom";
import { useNavigate } from "react-router";
import { useStreamMessages } from "../../stream/context";
import { Button, Choice } from "../../ui/ui";
import { cx } from "../../ui/cx";
import { NotificacoesContext, useNotificacoes, type ItemNotificacao, type Papel } from "./contexto";
import {
  apagarHistorico,
  chaveHistorico,
  gravarHistorico,
  lerHistorico,
  visiveisNoPainel,
  type Historico,
} from "./historico";
import { RegrasCliente, RegrasEquipe, type Notificacao } from "./regras";
import {
  alertar,
  conviteDispensado,
  definirDiscreto,
  definirLigadas,
  definirSom,
  discretoLigado,
  instalarPonteDesktop,
  noProgramaDesktop,
  somLigado,
  dispensarConvite,
  ligadas as estaoLigadas,
  mostrarNoSistema,
  olhando,
  pedirPermissao,
  permissao as lerPermissao,
  prepararSistema,
  type Permissao,
} from "./sistema";

const time = new Intl.DateTimeFormat("pt-BR", { timeStyle: "short" });

/**
 * Notificações de uma página (loja do cliente ou gestão da equipe), a partir do fluxo de
 * atualizações que a página já tem aberto (uma conexão por página). Quem está olhando a página vê
 * um aviso na própria tela; com a página em segundo plano (ou o programa sem foco), a notificação
 * vai para o sistema — se a pessoa permitiu. Tudo entra no histórico deste aparelho; o sino mostra
 * o que chegou depois do último "Limpar".
 */
export function NotificacoesProvider({
  papel,
  escopo,
  destino,
  icon,
  children,
}: {
  papel: Papel;
  /** De quem e de qual loja é o histórico (ex.: "equipe:<loja>:<conta>"). */
  escopo: string;
  /** Para onde cada notificação leva ao ser tocada (no sino, na tela ou no sistema). */
  destino: (n: Notificacao) => string;
  icon?: string | undefined;
  children: ReactNode;
}) {
  const navigate = useNavigate();
  const destinoRef = useRef(destino);
  useEffect(() => {
    destinoRef.current = destino;
  });
  const regras = useRef<RegrasCliente | RegrasEquipe>(
    papel === "cliente" ? new RegrasCliente() : new RegrasEquipe(),
  );
  // Histórico deste aparelho; trocar de loja/pessoa (escopo) relê o histórico certo.
  const [estado, setEstado] = useState(() => ({ escopo, hist: lerHistorico(escopo) }));
  if (estado.escopo !== escopo) setEstado({ escopo, hist: lerHistorico(escopo) });
  const hist = estado.escopo === escopo ? estado.hist : lerHistorico(escopo);
  const [toasts, setToasts] = useState<ItemNotificacao[]>([]);
  const [perm, setPerm] = useState<Permissao>(() => lerPermissao());
  const [ligadas, setLigadas] = useState(() => estaoLigadas());

  /** Muda o histórico a partir do que está GRAVADO (outra aba pode ter acrescentado algo). */
  const mudar = useCallback(
    (f: (h: Historico) => Historico) => {
      const novo = f(lerHistorico(escopo));
      gravarHistorico(escopo, novo);
      setEstado({ escopo, hist: novo });
    },
    [escopo],
  );

  useEffect(() => prepararSistema(), []);
  // A permissão pode mudar nas configurações do navegador: relê ao voltar para a página.
  useEffect(() => {
    const reler = () => {
      setPerm(lerPermissao());
      setLigadas(estaoLigadas());
    };
    document.addEventListener("visibilitychange", reler);
    return () => document.removeEventListener("visibilitychange", reler);
  }, []);
  // Outra aba (ou o programa) mexeu no histórico: acompanha.
  useEffect(() => {
    const outraAba = (e: StorageEvent) => {
      if (e.key === chaveHistorico(escopo)) setEstado({ escopo, hist: lerHistorico(escopo) });
    };
    window.addEventListener("storage", outraAba);
    return () => window.removeEventListener("storage", outraAba);
  }, [escopo]);

  const fechar = useCallback((id: string) => setToasts((t) => t.filter((x) => x.id !== id)), []);

  const abrir = useCallback(
    (n: ItemNotificacao) => {
      setToasts((t) => t.filter((x) => x.id !== n.id));
      mudar((h) => ({ ...h, itens: h.itens.map((x) => (x.id === n.id ? { ...x, lida: true } : x)) }));
      navigate(n.url);
    },
    [navigate, mudar],
  );

  const mostrar = useCallback(
    (n: Notificacao) => {
      const item: ItemNotificacao = { ...n, at: new Date(), lida: false, url: destinoRef.current(n) };
      // Outra aba já registrou o mesmo acontecimento: não duplica no histórico.
      mudar((h) => (h.itens.some((x) => x.id === item.id) ? h : { ...h, itens: [item, ...h.itens] }));
      if (olhando()) {
        setToasts((t) =>
          [item, ...t.filter((x) => x.id !== n.id && !(n.group && x.group === n.group))].slice(0, 3),
        );
        window.setTimeout(() => fechar(item.id), n.important ? 15_000 : 7_000);
        alertar(Boolean(n.important));
      } else {
        void mostrarNoSistema(n, item.url, icon, () => abrir(item));
      }
    },
    [fechar, icon, abrir, mudar],
  );

  useStreamMessages((m) => {
    for (const n of regras.current.aplicar(m)) mostrar(n);
  });
  // Programa Fila Gestão: a bandeja liga/desliga o som (preferência deste aparelho).
  useEffect(() => instalarPonteDesktop(() => {}), []);

  const value = useMemo(() => {
    const lista = visiveisNoPainel(hist);
    return {
      papel,
      lista,
      historico: hist.itens,
      naoLidas: lista.filter((x) => !x.lida).length,
      marcarLidas: () => {
        if (hist.itens.some((x) => !x.lida))
          mudar((h) => ({ ...h, itens: h.itens.map((x) => ({ ...x, lida: true })) }));
      },
      limpar: () =>
        mudar((h) => ({ itens: h.itens.map((x) => ({ ...x, lida: true })), limpoAte: new Date() })),
      apagarHistorico: () => {
        apagarHistorico(escopo);
        setEstado({ escopo, hist: { itens: [], limpoAte: null } });
      },
      permissao: perm,
      ligadas,
      ativar: async () => {
        const r = await pedirPermissao();
        setPerm(r);
        if (r === "granted") definirLigadas(true);
        setLigadas(estaoLigadas());
      },
      desligar: () => {
        definirLigadas(false);
        setLigadas(false);
      },
      abrir,
    };
  }, [papel, hist, perm, ligadas, abrir, mudar, escopo]);

  return (
    <NotificacoesContext.Provider value={value}>
      {children}
      <Avisos toasts={toasts} onClose={fechar} onOpen={abrir} />
    </NotificacoesContext.Provider>
  );
}

/** Avisos na própria tela (canto inferior), lidos pelo leitor de tela sem roubar o foco. */
function Avisos({
  toasts,
  onClose,
  onOpen,
}: {
  toasts: ItemNotificacao[];
  onClose: (id: string) => void;
  onOpen: (n: ItemNotificacao) => void;
}) {
  return (
    <div
      aria-live="polite"
      aria-relevant="additions"
      className="pointer-events-none fixed inset-x-4 bottom-4 z-50 grid justify-items-end gap-2 pb-safe sm:inset-x-auto sm:right-6 sm:w-96"
    >
      {toasts.map((t) => (
        <div
          key={t.id}
          role={t.important ? "alert" : "status"}
          className={cx(
            "pointer-events-auto flex w-full items-start gap-3 rounded-xl border bg-surface p-4 shadow-lg",
            t.important ? "border-primary ring-2 ring-primary/30" : "border-line",
          )}
        >
          <span className="inline-flex size-9 shrink-0 items-center justify-center rounded-full bg-select text-accent">
            {t.important ? (
              <BellRing aria-hidden="true" className="size-5" />
            ) : (
              <Bell aria-hidden="true" className="size-5" />
            )}
          </span>
          {/* O aviso inteiro leva ao lugar dele (ex.: a fila do barbeiro). */}
          <button
            type="button"
            onClick={() => onOpen(t)}
            className="grid min-w-0 flex-1 cursor-pointer gap-0.5 rounded-lg text-left"
          >
            <span className="font-semibold">{t.title}</span>
            <span className="text-sm text-fg-muted">{t.body}</span>
          </button>
          <button
            type="button"
            aria-label="Fechar aviso"
            onClick={() => onClose(t.id)}
            className="-m-1 inline-flex size-9 shrink-0 items-center justify-center rounded-lg text-fg-muted hover:bg-surface-2"
          >
            <X aria-hidden="true" className="size-4" />
          </button>
        </div>
      ))}
    </div>
  );
}

/** Som e conteúdo discreto: preferências DESTE aparelho (nunca da loja nem de outros clientes). */
function PreferenciasAparelho() {
  const [som, setSom] = useState(somLigado);
  const [discreto, setDiscreto] = useState(discretoLigado);
  return (
    <div className="grid gap-2" role="group" aria-label="Preferências deste aparelho">
      {noProgramaDesktop() ? (
        <p className="m-0 text-sm text-fg-muted">
          Som: {som ? "ligado" : "desligado"}. No programa, ligue ou desligue pelo ícone do Fila na bandeja.
        </p>
      ) : (
        <Choice
          type="checkbox"
          checked={som}
          onChange={(e) => {
            definirSom(e.target.checked);
            setSom(e.target.checked);
          }}
          label="Som nos avisos"
          description="Só neste aparelho."
        />
      )}
      <Choice
        type="checkbox"
        checked={discreto}
        onChange={(e) => {
          definirDiscreto(e.target.checked);
          setDiscreto(e.target.checked);
        }}
        label="Conteúdo discreto nas notificações"
        description="A notificação do sistema não mostra nomes, serviços nem valores (bom para a tela bloqueada)."
      />
    </div>
  );
}

/** Explica a situação da permissão e oferece ligar/desligar neste aparelho. */
function PreferenciaSistema() {
  const ctx = useNotificacoes();
  if (!ctx) return null;
  const { permissao, ligadas, ativar, desligar } = ctx;
  if (permissao === "unsupported")
    return (
      <p className="m-0 text-sm text-fg-muted">
        Este navegador não mostra notificações do sistema. Os avisos aparecem aqui enquanto a página estiver
        aberta.
      </p>
    );
  if (permissao === "denied")
    return (
      <p className="m-0 text-sm text-fg-muted">
        As notificações estão bloqueadas para este site. Para receber, libere em “Notificações” nas
        configurações do navegador (ícone ao lado do endereço) e volte para esta página.
      </p>
    );
  if (ligadas)
    return (
      <div className="grid gap-2">
        <p className="m-0 text-sm text-fg-muted">
          Notificações ligadas neste aparelho. Elas chegam enquanto esta página (ou o programa) estiver
          aberta, mesmo em segundo plano.
        </p>
        <Button variant="secondary" icon={BellOff} onClick={desligar}>
          Desligar neste aparelho
        </Button>
        <PreferenciasAparelho />
      </div>
    );
  return (
    <div className="grid gap-2">
      <p className="m-0 text-sm text-fg-muted">Receba os avisos mesmo com a página em segundo plano.</p>
      <Button icon={BellRing} onClick={() => void ativar()}>
        Ativar notificações
      </Button>
    </div>
  );
}

/** Sino do cabeçalho: quantidade de avisos novos e a lista dos últimos, com a preferência. */
export function Sino({
  className,
  abrirPara = "esquerda",
}: {
  className?: string;
  /**
   * Para que lado o painel abre a partir do sino. No cabeçalho (sino à direita), para a esquerda;
   * no menu lateral (sino à esquerda da tela), para a direita, por cima do conteúdo.
   */
  abrirPara?: "esquerda" | "direita";
}) {
  const ctx = useNotificacoes();
  const [aberto, setAberto] = useState(false);
  const [historico, setHistorico] = useState(false);
  const panelId = useId();
  const ref = useRef<HTMLDivElement>(null);
  const marcar = ctx?.marcarLidas;
  useEffect(() => {
    if (!aberto) return;
    marcar?.();
    const fora = (e: PointerEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setAberto(false);
    };
    const esc = (e: KeyboardEvent) => e.key === "Escape" && setAberto(false);
    document.addEventListener("pointerdown", fora);
    document.addEventListener("keydown", esc);
    return () => {
      document.removeEventListener("pointerdown", fora);
      document.removeEventListener("keydown", esc);
    };
  }, [aberto, marcar, ctx?.lista.length]);
  if (!ctx) return null;
  const n = ctx.naoLidas;
  return (
    <div ref={ref} className={cx("relative", className)}>
      <button
        type="button"
        aria-expanded={aberto}
        aria-controls={aberto ? panelId : undefined}
        aria-label={n > 0 ? `Notificações: ${n} ${n === 1 ? "nova" : "novas"}` : "Notificações"}
        onClick={() => setAberto((a) => !a)}
        className="relative inline-flex size-11 items-center justify-center rounded-lg text-fg hover:bg-surface-2"
      >
        <Bell aria-hidden="true" className="size-5" />
        {n > 0 && (
          <span
            aria-hidden="true"
            className="absolute top-1.5 right-1.5 inline-flex min-w-5 items-center justify-center rounded-full bg-primary px-1 text-xs font-bold text-on-primary tabular-nums"
          >
            {n > 9 ? "9+" : n}
          </span>
        )}
      </button>
      {aberto && (
        <div
          id={panelId}
          role="region"
          aria-label="Notificações"
          className={cx(
            "absolute top-full z-50 mt-2 grid w-[min(22rem,calc(100vw-2rem))] gap-3 rounded-xl border border-line bg-surface p-4 shadow-lg",
            abrirPara === "direita" ? "left-0" : "right-0",
          )}
        >
          <div className="flex items-center justify-between gap-2">
            <p className="m-0 font-semibold">Notificações</p>
            {ctx.lista.length > 0 && (
              <button
                type="button"
                onClick={ctx.limpar}
                className="inline-flex min-h-11 items-center gap-1 rounded-lg px-2 text-sm font-semibold text-accent hover:bg-select"
              >
                <CheckCheck aria-hidden="true" className="size-4" />
                Limpar
              </button>
            )}
          </div>
          {ctx.lista.length === 0 ? (
            <p className="m-0 text-sm text-fg-muted">
              {ctx.papel === "cliente"
                ? "Nada por enquanto. Avisamos aqui quando a fila andar e quando chegar a sua vez."
                : "Nada por enquanto. Avisamos aqui quando alguém entrar, sair, avisar atraso ou pedir mudança."}
            </p>
          ) : (
            <ol className="m-0 grid max-h-80 list-none gap-2 overflow-y-auto p-0">
              {ctx.lista.map((x) => (
                <li key={x.id}>
                  {/* Cada aviso leva ao lugar dele (ex.: a fila do barbeiro). */}
                  <button
                    type="button"
                    onClick={() => {
                      setAberto(false);
                      ctx.abrir(x);
                    }}
                    className="flex w-full items-center gap-2 rounded-lg bg-surface-2 px-3 py-2 text-left hover:bg-select"
                  >
                    <span className="grid min-w-0 flex-1 gap-0.5">
                      <span className="flex items-baseline justify-between gap-2 text-sm font-semibold">
                        <span>{x.title}</span>
                        <time
                          className="shrink-0 text-xs font-normal text-fg-muted tabular-nums"
                          dateTime={x.at.toISOString()}
                        >
                          {time.format(x.at)}
                        </time>
                      </span>
                      <span className="text-sm text-fg-muted">{x.body}</span>
                    </span>
                    <ChevronRight aria-hidden="true" className="size-4 shrink-0 text-fg-muted" />
                  </button>
                </li>
              ))}
            </ol>
          )}
          {ctx.historico.length > 0 && (
            <button
              type="button"
              onClick={() => {
                setAberto(false);
                setHistorico(true);
              }}
              className="inline-flex min-h-11 items-center justify-center gap-2 rounded-lg border border-line px-3 text-sm font-semibold hover:bg-surface-2"
            >
              <History aria-hidden="true" className="size-4" />
              Ver histórico ({ctx.historico.length})
            </button>
          )}
          <div className="border-t border-line pt-3">
            <PreferenciaSistema />
          </div>
        </div>
      )}
      {historico && <HistoricoNotificacoes onClose={() => setHistorico(false)} />}
    </div>
  );
}

const dia = new Intl.DateTimeFormat("pt-BR", { weekday: "long", day: "numeric", month: "long" });

function rotuloDoDia(d: Date): string {
  const hoje = new Date();
  const ontem = new Date(hoje);
  ontem.setDate(hoje.getDate() - 1);
  if (d.toDateString() === hoje.toDateString()) return "Hoje";
  if (d.toDateString() === ontem.toDateString()) return "Ontem";
  const t = dia.format(d);
  return t.charAt(0).toUpperCase() + t.slice(1);
}

/**
 * Histórico completo deste aparelho (até 30 dias), inclusive o que foi limpo do sino. Agrupado por
 * dia, com busca; tocar num aviso leva ao lugar dele. Apagar exige confirmação.
 */
function HistoricoNotificacoes({ onClose }: { onClose: () => void }) {
  const ctx = useNotificacoes();
  const ref = useRef<HTMLDivElement>(null);
  const [busca, setBusca] = useState("");
  const [confirmar, setConfirmar] = useState(false);
  const tituloId = useId();
  useEffect(() => {
    const antes = document.activeElement as HTMLElement | null;
    ref.current?.querySelector<HTMLElement>("input, button")?.focus();
    return () => antes?.focus?.();
  }, []);
  if (!ctx) return null;
  const termo = busca.trim().toLocaleLowerCase("pt-BR");
  const itens = termo
    ? ctx.historico.filter((x) => `${x.title} ${x.body}`.toLocaleLowerCase("pt-BR").includes(termo))
    : ctx.historico;
  const grupos: { rotulo: string; itens: ItemNotificacao[] }[] = [];
  for (const x of itens) {
    const r = rotuloDoDia(x.at);
    const g = grupos.at(-1);
    if (g && g.rotulo === r) g.itens.push(x);
    else grupos.push({ rotulo: r, itens: [x] });
  }
  function onKeyDown(e: React.KeyboardEvent) {
    if (e.key === "Escape") {
      e.preventDefault();
      onClose();
      return;
    }
    if (e.key !== "Tab") return;
    const focaveis = Array.from(ref.current?.querySelectorAll<HTMLElement>("input, button") ?? []);
    const primeiro = focaveis[0];
    const ultimo = focaveis.at(-1);
    if (e.shiftKey && document.activeElement === primeiro) {
      e.preventDefault();
      ultimo?.focus();
    } else if (!e.shiftKey && document.activeElement === ultimo) {
      e.preventDefault();
      primeiro?.focus();
    }
  }
  return createPortal(
    <div className="fixed inset-0 z-[60] flex items-end justify-center sm:items-center sm:p-6">
      <div aria-hidden="true" className="absolute inset-0 bg-black/50" onClick={onClose} />
      <div
        ref={ref}
        role="dialog"
        aria-modal="true"
        aria-labelledby={tituloId}
        onKeyDown={onKeyDown}
        className="pb-safe relative grid max-h-[90dvh] w-full max-w-2xl grid-rows-[auto_auto_minmax(0,1fr)_auto] gap-3 rounded-t-2xl border border-line bg-surface p-4 shadow-2xl sm:rounded-2xl sm:p-6"
      >
        <div className="flex items-center justify-between gap-2">
          <div className="grid">
            <h2 id={tituloId} className="m-0 text-lg font-semibold">
              Histórico de notificações
            </h2>
            <p className="m-0 text-sm text-fg-muted">
              Neste aparelho, últimos 30 dias — inclusive o que você limpou do sino.
            </p>
          </div>
          <button
            type="button"
            onClick={onClose}
            aria-label="Fechar histórico"
            className="inline-flex size-11 shrink-0 items-center justify-center rounded-lg text-fg-muted hover:bg-surface-2"
          >
            <X aria-hidden="true" className="size-5" />
          </button>
        </div>
        <label className="relative block">
          <span className="sr-only">Buscar no histórico</span>
          <Search
            aria-hidden="true"
            className="pointer-events-none absolute top-1/2 left-3 size-5 -translate-y-1/2 text-fg-muted"
          />
          <input
            type="search"
            value={busca}
            onChange={(e) => setBusca(e.target.value)}
            placeholder="Buscar por nome, senha ou assunto"
            className="min-h-11 w-full rounded-lg border border-line bg-surface py-2 pr-3 pl-10 text-base"
          />
        </label>
        <div className="min-h-0 overflow-y-auto">
          {grupos.length === 0 ? (
            <p className="m-0 py-6 text-center text-sm text-fg-muted">
              {termo ? `Nada com “${busca.trim()}”.` : "Nenhuma notificação guardada neste aparelho."}
            </p>
          ) : (
            grupos.map((g) => (
              <section key={g.rotulo} className="mb-4 grid gap-2" aria-label={g.rotulo}>
                <h3 className="m-0 text-xs font-semibold tracking-wide text-fg-muted uppercase">
                  {g.rotulo}
                </h3>
                <ol className="m-0 grid list-none gap-2 p-0">
                  {g.itens.map((x) => (
                    <li key={x.id}>
                      <button
                        type="button"
                        onClick={() => {
                          onClose();
                          ctx.abrir(x);
                        }}
                        className="flex w-full items-center gap-3 rounded-lg bg-surface-2 px-3 py-2 text-left hover:bg-select"
                      >
                        <time
                          className="w-12 shrink-0 text-sm text-fg-muted tabular-nums"
                          dateTime={x.at.toISOString()}
                        >
                          {time.format(x.at)}
                        </time>
                        <span className="grid min-w-0 flex-1 gap-0.5">
                          <span className="text-sm font-semibold">{x.title}</span>
                          <span className="text-sm text-fg-muted">{x.body}</span>
                        </span>
                        <ChevronRight aria-hidden="true" className="size-4 shrink-0 text-fg-muted" />
                      </button>
                    </li>
                  ))}
                </ol>
              </section>
            ))
          )}
        </div>
        <div className="flex flex-wrap items-center justify-between gap-2 border-t border-line pt-3">
          {confirmar ? (
            <>
              <p className="m-0 text-sm font-semibold">Apagar todo o histórico deste aparelho?</p>
              <div className="flex gap-2">
                <Button variant="secondary" onClick={() => setConfirmar(false)}>
                  Cancelar
                </Button>
                <Button
                  variant="danger"
                  onClick={() => {
                    ctx.apagarHistorico();
                    setConfirmar(false);
                  }}
                >
                  Apagar
                </Button>
              </div>
            </>
          ) : (
            <>
              <span className="text-sm text-fg-muted">
                {ctx.historico.length} {ctx.historico.length === 1 ? "notificação" : "notificações"}
              </span>
              {ctx.historico.length > 0 && (
                <Button variant="danger-ghost" icon={Trash2} onClick={() => setConfirmar(true)}>
                  Apagar histórico
                </Button>
              )}
            </>
          )}
        </div>
      </div>
    </div>,
    document.body,
  );
}

/**
 * Convite para ligar as notificações do sistema (só quando o navegador ainda não perguntou e a
 * pessoa não dispensou neste aparelho). O pedido de permissão sai do toque no botão.
 */
export function ConviteNotificacoes({ texto }: { texto: string }) {
  const ctx = useNotificacoes();
  const [fechado, setFechado] = useState(() => conviteDispensado());
  if (!ctx || fechado || ctx.permissao !== "default") return null;
  return (
    <div className="flex flex-wrap items-center gap-3 rounded-xl border border-select-line bg-select px-4 py-3">
      <BellRing aria-hidden="true" className="size-5 shrink-0 text-accent" />
      <p className="m-0 min-w-0 flex-1 text-sm">{texto}</p>
      <div className="flex flex-wrap gap-2">
        <Button onClick={() => void ctx.ativar()}>Ativar notificações</Button>
        <Button
          variant="ghost"
          onClick={() => {
            dispensarConvite();
            setFechado(true);
          }}
        >
          Agora não
        </Button>
      </div>
    </div>
  );
}
