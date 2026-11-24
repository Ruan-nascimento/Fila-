// Ponte com o componente nativo (Tauri). Só comandos tipados: não existe pedido livre, assinatura
// genérica nem abertura de URL arbitrária. Os dados ficam em memória (nada em localStorage).

export type ErroNativo = { status: number; code: string; detail: string };

type Tauri = {
  core: { invoke<T>(cmd: string, args?: Record<string, unknown>): Promise<T> };
  event: { listen<T>(evt: string, cb: (e: { payload: T }) => void): Promise<() => void> };
};

function tauri(): Tauri | null {
  return ((globalThis as { __TAURI__?: Tauri }).__TAURI__ ?? null) as Tauri | null;
}

export function noPrograma(): boolean {
  return tauri() !== null;
}

export async function invocar<T>(cmd: string, args?: Record<string, unknown>): Promise<T> {
  const t = tauri();
  if (!t) throw { status: 0, code: "sem_programa", detail: "Abra o Dashboard pelo programa Fila Gestão." } as ErroNativo;
  try {
    return await t.core.invoke<T>(cmd, args);
  } catch (e) {
    if (typeof e === "object" && e && "code" in e) throw e as ErroNativo;
    throw { status: 0, code: "local", detail: String(e) } as ErroNativo;
  }
}

export function ouvir<T>(evt: string, cb: (payload: T) => void): () => void {
  const t = tauri();
  if (!t) return () => {};
  let solto = false;
  let soltar: (() => void) | null = null;
  void t.event.listen<T>(evt, (e) => cb(e.payload)).then((u) => {
    if (solto) u();
    else soltar = u;
  });
  return () => {
    solto = true;
    soltar?.();
  };
}

export type Instalacao = { id: string; label: string };
export type EstadoDashboard = {
  instalacao: Instalacao | null;
  vinculado: boolean;
  ticket_pendente: boolean;
  loja_pedida: string | null;
  painel: string;
};
export type Loja = { id: string; name: string; timezone: string };
export type Contexto = {
  account: { id: string; name: string };
  installation: { id: string; label: string };
  access_expires_at: string;
  establishments: Loja[];
};

export type Filtros = {
  from: string;
  to: string;
  compare: boolean;
  professional_id?: string | null;
  service_id?: string | null;
  method?: "pix" | "cash" | "card" | null;
};

export type Consulta =
  | { tipo: "resumo"; loja: string; filtros: Filtros }
  | { tipo: "serie"; loja: string; metrica: Metrica; granularidade?: Gran | null; filtros: Filtros }
  | { tipo: "detalhamento"; loja: string; dimensao: Dimensao; filtros: Filtros }
  | { tipo: "registros"; loja: string; registro: Registro; filtros: Filtros; recorte: Recorte };

export type Metrica = "cash" | "ticket" | "movement" | "wait" | "duration" | "loyalty";
export type Gran = "hour" | "day" | "week" | "month";
export type Dimensao =
  | "payment_methods" | "outcomes" | "professionals" | "services" | "products" | "peak_hours"
  | "wait_bands" | "pending" | "rewards" | "recurrence";
export type Registro =
  | "receipts" | "refunds" | "orders" | "participations" | "service_items" | "financial_issues" | "loyalty_entries";
export type Recorte = Partial<{
  bucket_start: string; bucket_end: string; cursor: string; ref: string; state: string;
  kind: string; status: string; dow: number; hour: number; band: string;
}>;

export type Valor = { value: number | null; status: "ok" | "empty" | "not_collected" | "not_applicable"; sample?: number };
export type Meta = {
  establishment_id: string;
  establishment_name: string;
  timezone: string;
  period: { from: string; to: string; start: string; end: string; effective_end: string };
  compare_period: { from: string; to: string; start: string; end: string; effective_end: string } | null;
  granularity?: Gran;
  generated_at: string;
  snapshot_at?: string;
  collection_started_at: string;
  ignored_filters: string[];
};
export type Resposta<T> = { data: T; meta: Meta };

export type Destino = { tela: "filas" } | { tela: "pendencias" } | { tela: "comanda"; id: string };

export const nativo = {
  estado: () => invocar<EstadoDashboard>("estado_dashboard"),
  ativar: (codigo: string) => invocar<Instalacao>("ativar_instalacao", { codigo }),
  vincular: () => invocar<Contexto>("vincular_sessao"),
  contexto: () => invocar<Contexto>("contexto_dashboard"),
  consultar: <T>(consulta: Consulta) => invocar<Resposta<T>>("consultar", { consulta }),
  iniciarFluxo: (loja: string) => invocar<void>("iniciar_fluxo", { loja }),
  pararFluxo: () => invocar<void>("parar_fluxo"),
  abrirGestao: (loja: string, destino: Destino) => invocar<void>("abrir_gestao", { loja, destino }),
  sair: () => invocar<void>("sair_dashboard"),
  esquecer: () => invocar<void>("esquecer_instalacao"),
};

/** O caminho de gestão devolvido pela API vira um destino tipado (ou nada, se não houver tela). */
export function destinoDe(path: string | undefined, loja: string): Destino | null {
  if (!path) return null;
  const m = path.match(/^\/gestao\/([0-9a-f-]{36})\/(comandas\/([0-9a-f-]{36})|pendencias|filas)$/);
  if (!m || m[1] !== loja) return null;
  if (m[3]) return { tela: "comanda", id: m[3] };
  return m[2] === "pendencias" ? { tela: "pendencias" } : { tela: "filas" };
}
