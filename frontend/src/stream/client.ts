import type { components } from "../api/schema.gen";

export type EventType = components["schemas"]["EventType"];
export type AudienceKind = components["schemas"]["AudienceKind"];

/** Mensagem do fluxo (EventEnvelope). `data` é a visão completa do recurso para a audiência. */
export type StreamMessage = Omit<components["schemas"]["EventEnvelope"], "data"> & { data: unknown };

export type StreamStatus = "connecting" | "live" | "reconnecting" | "unsupported";

/** O mínimo de EventSource que o cliente usa (permite um falso nos testes). */
export type EventSourceLike = {
  addEventListener(type: string, listener: (ev: MessageEvent) => void): void;
  close(): void;
  onerror: ((ev: Event) => void) | null;
  onopen: ((ev: Event) => void) | null;
};

const eventTypes: EventType[] = [
  "stream.snapshot",
  "stream.session_ended",
  "queue.public.updated",
  "queue.updated",
  "participation.updated",
  "order.updated",
  "change_request.updated",
  "payment.updated",
  "financial_issue.updated",
  "pendencies.updated",
  "loyalty.updated",
  "reward_reservation.updated",
  "deletion_request.updated",
];

export type StreamOptions = {
  estId: string;
  channels: AudienceKind[];
  onMessage: (m: StreamMessage) => void;
  onStatus: (s: StreamStatus) => void;
  /** A sessão foi encerrada no servidor: o chamador relê a sessão e abre outro fluxo. */
  onSessionEnded: () => void;
  open?: (url: string) => EventSourceLike;
  random?: () => number;
  win?: Pick<Window, "addEventListener" | "removeEventListener" | "setTimeout" | "clearTimeout">;
  doc?: Pick<Document, "addEventListener" | "removeEventListener" | "visibilityState">;
};

/** Espera antes da tentativa n (0, 1, 2…): "jitter total" entre 0 e min(30 s, 1 s × 2^n). */
export function backoffDelay(attempt: number, random: () => number): number {
  return Math.floor(random() * Math.min(30_000, 1000 * 2 ** attempt));
}

/**
 * Uma conexão SSE por aba (eventos-sse.md §1 e §7). Credenciais só por cookie; o cursor (posição de
 * leitura, não é credencial) vai no endereço ao reconectar. Ao voltar para a aba ou recuperar a
 * rede, reconecta na hora; nos outros casos espera um tempo aleatório crescente.
 */
export class StreamClient {
  private es: EventSourceLike | null = null;
  private cursor: string | null = null;
  private attempt = 0;
  private timer: number | null = null;
  private stopped = false;
  private readonly o: Required<Omit<StreamOptions, "open">> & {
    open: ((url: string) => EventSourceLike) | null;
  };

  constructor(opts: StreamOptions) {
    const native = typeof EventSource === "undefined" ? null : (url: string) => new EventSource(url);
    this.o = {
      random: Math.random,
      win: window,
      doc: document,
      ...opts,
      open: opts.open ?? native,
    };
  }

  private readonly onOnline = () => this.reconnectNow();
  private readonly onVisible = () => {
    if (this.o.doc.visibilityState === "visible") this.reconnectNow();
  };

  start(): void {
    if (!this.o.open) {
      this.o.onStatus("unsupported");
      return;
    }
    this.o.win.addEventListener("online", this.onOnline);
    this.o.doc.addEventListener("visibilitychange", this.onVisible);
    this.connect();
  }

  stop(): void {
    this.stopped = true;
    this.clearTimer();
    this.es?.close();
    this.es = null;
    this.o.win.removeEventListener("online", this.onOnline);
    this.o.doc.removeEventListener("visibilitychange", this.onVisible);
  }

  /** Reconecta já (com o cursor): ao voltar para a aba a conexão pode ter morrido em silêncio. */
  reconnectNow(): void {
    if (this.stopped) return;
    this.clearTimer();
    this.es?.close();
    this.es = null;
    this.attempt = 0;
    this.connect();
  }

  private clearTimer() {
    if (this.timer !== null) this.o.win.clearTimeout(this.timer);
    this.timer = null;
  }

  private connect() {
    if (this.stopped || !this.o.open) return;
    this.o.onStatus(this.cursor ? "reconnecting" : "connecting");
    const q = new URLSearchParams({ establishment_id: this.o.estId, channels: this.o.channels.join(",") });
    if (this.cursor) q.set("cursor", this.cursor);
    const es = this.o.open(`/v1/stream?${q.toString()}`);
    this.es = es;
    for (const type of eventTypes) {
      es.addEventListener(type, (ev) => this.handle(es, type, ev));
    }
    es.onopen = () => {
      // Retomada pelo cursor: o servidor aceitou e reenvia o que faltou, em ordem. Sem cursor,
      // os dados só valem depois do snapshot.
      if (this.es === es && this.cursor) {
        this.attempt = 0;
        this.o.onStatus("live");
      }
    };
    es.onerror = () => {
      if (this.es !== es) return;
      // Fecha a reconexão nativa: aplicamos nosso próprio backoff com jitter.
      es.close();
      this.es = null;
      this.o.onStatus("reconnecting");
      const delay = backoffDelay(this.attempt, this.o.random);
      this.attempt += 1;
      this.timer = this.o.win.setTimeout(() => {
        this.timer = null;
        this.connect();
      }, delay);
    };
  }

  private handle(es: EventSourceLike, type: EventType, ev: MessageEvent) {
    if (this.es !== es) return; // mensagem atrasada de uma conexão já trocada
    let msg: StreamMessage;
    try {
      msg = JSON.parse(String(ev.data)) as StreamMessage;
    } catch {
      return;
    }
    if (ev.lastEventId) this.cursor = ev.lastEventId;
    if (type === "stream.session_ended") {
      this.stop();
      this.o.onStatus("reconnecting");
      this.o.onSessionEnded();
      return;
    }
    if (type === "stream.snapshot") {
      this.attempt = 0;
      this.o.onStatus("live");
    }
    this.o.onMessage(msg);
  }
}
