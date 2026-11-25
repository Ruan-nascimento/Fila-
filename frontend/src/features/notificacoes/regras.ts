import type { components } from "../../api/schema.gen";
import type { StreamMessage } from "../../stream/client";

type Participation = components["schemas"]["Participation"];
type Visit = components["schemas"]["Visit"];
type Order = components["schemas"]["Order"];
type ChangeRequest = components["schemas"]["ChangeRequest"];
type PublicQueue = components["schemas"]["PublicQueue"];
type StaffQueue = components["schemas"]["StaffQueue"];
type StaffEntry = StaffQueue["entries"][number];
type LoyaltySummary = components["schemas"]["LoyaltySummary"];
type Snapshot = components["schemas"]["StreamSnapshot"];
type Appointment = components["schemas"]["Appointment"];

/**
 * Uma notificação para a pessoa. `id` é a chave LÓGICA do acontecimento (ex.: "chamada de tal
 * senha às tantas"): o mesmo acontecimento nunca notifica duas vezes, nem quando o evento chega
 * repetido na reconexão, e vira a `tag` da notificação do sistema (outra aba aberta substitui em
 * vez de empilhar).
 */
export type Notificacao = {
  id: string;
  title: string;
  body: string;
  /** Importante: chamada da vez e equivalentes (vibra, toca e fica mais tempo na tela). */
  important?: boolean;
  /** Notificações do mesmo grupo se substituem (ex.: "faltam N na sua frente"). */
  group?: string;
  /** Para onde levar ao tocar: a fila do barbeiro (equipe) ou uma aba da loja (cliente). */
  queueId?: string;
  aba?: "pontos" | "reservas";
  /** Equipe: abrir a Agenda neste dia (AAAA-MM-DD). */
  agendaData?: string;
};

const time = new Intl.DateTimeFormat("pt-BR", { timeStyle: "short" });

function pessoas(n: number): string {
  return n === 1 ? "Falta 1 pessoa na sua frente" : `Faltam ${n} pessoas na sua frente`;
}

/** Guarda o que já foi notificado (chave lógica) e a maior versão vista de cada recurso. */
class Memoria {
  private feitas = new Set<string>();
  private versoes = new Map<string, number>();
  /** true só na primeira vez que a chave aparece. */
  primeira(id: string): boolean {
    if (this.feitas.has(id)) return false;
    this.feitas.add(id);
    return true;
  }
  /** true se a versão é mais nova que a última vista do recurso (e passa a ser a última). */
  nova(chave: string, versao: number): boolean {
    if (versao <= (this.versoes.get(chave) ?? -1)) return false;
    this.versoes.set(chave, versao);
    return true;
  }
  zerarVersoes(): void {
    this.versoes.clear();
  }
}

/**
 * Notificações do CLIENTE (visitante ou conta) na página da loja: tudo o que a barbearia fez na
 * visita dele. O que ele mesmo fez (entrar, desistir, pedir mudança, avisar atraso) não notifica.
 * Usa só eventos que a conexão já recebe (public, visit, account): nenhum dado novo é exposto.
 */
export class RegrasCliente {
  private mem = new Memoria();
  private visita: Participation | null = null;
  private pedidos = new Map<string, ChangeRequest["status"]>();
  private comanda: Order["status"] | null = null;
  private pontos: number | null = null;
  private aFrente: number | null = null;
  private nomes = new Map<string, string>();

  aplicar(m: StreamMessage): Notificacao[] {
    switch (m.type) {
      case "stream.snapshot":
        this.snapshot(m.data as Snapshot);
        return [];
      case "queue.public.updated":
        return this.fila(m.data as PublicQueue);
      case "participation.updated":
        return m.audience === "visit" ? this.participacao(m.data as Participation) : [];
      case "order.updated":
      case "payment.updated":
        return m.audience === "visit" ? this.pagamento(m.data as Order) : [];
      case "change_request.updated":
        return m.audience === "visit" ? this.pedido(m.data as ChangeRequest) : [];
      case "loyalty.updated":
        return this.fidelidade(m.data as LoyaltySummary);
      case "appointment.updated":
        return m.audience === "visit" ? this.reserva(m.data as Appointment, m.resource.version) : [];
      default:
        return [];
    }
  }

  /** Horário marcado: a barbearia chamou ou cancelou (o que o próprio cliente faz não notifica). */
  private reserva(a: Appointment, versao: number): Notificacao[] {
    if (!this.mem.nova(`a:${a.id}`, versao)) return [];
    const out: Notificacao[] = [];
    if (a.status === "called")
      out.push({
        id: `reserva-chamada:${a.id}:${a.called_at ?? ""}`,
        important: true,
        aba: "reservas",
        title: "Chegou a sua vez",
        body: `${a.professional.display_name} está chamando você para o horário das ${a.local_time}.`,
      });
    if (a.status === "cancelled" && a.cancelled_by === "store")
      out.push({
        id: `reserva-cancelada:${a.id}`,
        aba: "reservas",
        title: "A barbearia cancelou sua reserva",
        body: `Horário das ${a.local_time}. Se houve pagamento, a devolução é feita pelo Pix.`,
      });
    return out.filter((n) => this.mem.primeira(n.id));
  }

  /** Base sem notificar: o estado que a pessoa já está vendo na tela. */
  private snapshot(d: Snapshot) {
    this.mem.zerarVersoes();
    for (const q of d.public_queues ?? []) this.nomes.set(q.queue_id, q.professional.display_name);
    if (d.granted_channels.includes("visit")) {
      const v = d.visit as Visit | null | undefined;
      this.visita = v?.participation ?? null;
      this.comanda = v?.order.status ?? null;
      if (this.visita) this.mem.nova(`p:${this.visita.id}`, this.visita.version);
      this.aFrente = this.visita?.position ? this.visita.position - 1 : null;
    }
    if (d.loyalty) this.pontos = d.loyalty.balance;
  }

  private fila(q: PublicQueue): Notificacao[] {
    this.nomes.set(q.queue_id, q.professional.display_name);
    const v = this.visita;
    if (!v || v.status !== "waiting" || q.queue_id !== v.queue_id) return [];
    const esperando = q.tickets.filter((t) => t.status === "waiting");
    const i = esperando.findIndex((t) => t.ticket_code === v.ticket_code);
    if (i < 0) return [];
    const antes = this.aFrente;
    this.aFrente = i;
    // Só avisa quando a fila ANDA (alguém da frente saiu ou foi chamado), nunca quando aumenta.
    if (antes === null || i >= antes) return [];
    const id = `posicao:${v.id}:${i}`;
    if (!this.mem.primeira(id)) return [];
    if (i === 0)
      return [
        {
          id,
          group: `posicao:${v.id}`,
          title: "Você é o próximo!",
          body: `Senha ${v.ticket_code}. Fique por perto: logo chega a sua vez.`,
        },
      ];
    return [
      {
        id,
        group: `posicao:${v.id}`,
        title: pessoas(i),
        body: `Senha ${v.ticket_code} na fila de ${this.nomes.get(q.queue_id) ?? "seu profissional"}.`,
      },
    ];
  }

  private participacao(p: Participation): Notificacao[] {
    if (!this.mem.nova(`p:${p.id}`, p.version)) return [];
    const antes = this.visita?.id === p.id ? this.visita : null;
    this.visita = p;
    if (p.status !== "waiting") this.aFrente = null;
    else if (p.position) this.aFrente = p.position - 1;
    // Visita que a pessoa acabou de criar (ou que esta aba ainda não conhecia): só a base.
    if (!antes) return [];
    const quem = this.nomes.get(p.queue_id) ?? "o profissional";
    const out: Notificacao[] = [];
    if (antes.queue_id !== p.queue_id && p.status === "waiting") {
      out.push({
        id: `transferencia:${p.id}:${p.queue_id}`,
        title: "Você foi transferido de fila",
        body: `Agora você está na fila de ${quem}, com a senha ${p.ticket_code}.`,
      });
    }
    if (antes.status === p.status) return this.unicos(out);
    switch (p.status) {
      case "called":
        out.push({
          id: `chamada:${p.id}:${p.called_at ?? p.version}`,
          important: true,
          title: "Chegou a sua vez!",
          body: `Senha ${p.ticket_code}: vá até ${quem}${
            p.tolerance_ends_at ? ` até ${time.format(new Date(p.tolerance_ends_at))}` : ""
          }.`,
        });
        break;
      case "waiting":
        if (antes.status === "called")
          out.push({
            id: `devolvido:${p.id}:${p.version}`,
            title: "A barbearia chamou o próximo",
            body: `Você continua na fila${p.position ? ` em ${p.position}º lugar` : ""}, com a senha ${p.ticket_code}.`,
          });
        break;
      case "in_service":
        out.push({
          id: `inicio:${p.id}`,
          title: "Seu atendimento começou",
          body: `Com ${quem}. Bom corte!`,
        });
        break;
      case "completed":
        out.push({
          id: `fim:${p.id}`,
          title: "Seu atendimento terminou",
          body: "Confira a comanda e o pagamento na página da loja.",
        });
        break;
      case "interrupted":
        out.push({
          id: `interrompido:${p.id}`,
          title: "Seu atendimento foi interrompido",
          body: "A loja vai revisar o que foi feito. Só o que foi feito por inteiro é cobrado.",
        });
        break;
      case "no_show":
        out.push({
          id: `ausente:${p.id}`,
          title: "Você foi marcado como ausente",
          body: "O prazo para se apresentar terminou. Se ainda quiser, entre na fila de novo.",
        });
        break;
      case "cancelled":
        // Sem motivo = a própria pessoa desistiu (não notifica quem fez a ação).
        if (p.end_reason)
          out.push({
            id: `cancelada:${p.id}`,
            title: "A barbearia cancelou sua senha",
            body: `Motivo: ${p.end_reason}`,
          });
        break;
    }
    return this.unicos(out);
  }

  private pagamento(o: Order): Notificacao[] {
    if (!this.mem.nova(`o:${o.id}`, o.version)) return [];
    const antes = this.comanda;
    this.comanda = o.status;
    if (antes === null || antes === o.status || o.status !== "settled") return [];
    return this.unicos([
      { id: `pago:${o.id}`, title: "Pagamento confirmado", body: "Obrigado! Sua comanda está quitada." },
    ]);
  }

  private pedido(cr: ChangeRequest): Notificacao[] {
    const antes = this.pedidos.get(cr.id);
    this.pedidos.set(cr.id, cr.status);
    if (antes === cr.status || !cr.decided_by_role) return [];
    const nomes = cr.add.map((a) => a.name).join(", ");
    if (cr.status === "approved")
      return this.unicos([
        {
          id: `pedido:${cr.id}:approved`,
          title: "O barbeiro aceitou seu pedido de mudança",
          body: nomes ? `Adicionado: ${nomes}. A comanda já foi atualizada.` : "A comanda já foi atualizada.",
        },
      ]);
    if (cr.status === "rejected")
      return this.unicos([
        {
          id: `pedido:${cr.id}:rejected`,
          title: "O barbeiro recusou seu pedido de mudança",
          body: cr.decision_reason ? `Motivo: ${cr.decision_reason}` : "A comanda continua como estava.",
        },
      ]);
    return [];
  }

  private fidelidade(l: LoyaltySummary): Notificacao[] {
    const antes = this.pontos;
    this.pontos = l.balance;
    if (antes === null || l.balance <= antes) return [];
    const ganho = l.balance - antes;
    return this.unicos([
      {
        id: `pontos:${l.balance}:${ganho}`,
        aba: "pontos",
        title: ganho === 1 ? "Você ganhou 1 ponto" : `Você ganhou ${ganho} pontos`,
        body: `Seu saldo agora é de ${l.balance} ${l.balance === 1 ? "ponto" : "pontos"}.`,
      },
    ]);
  }

  private unicos(list: Notificacao[]): Notificacao[] {
    return list.filter((n) => this.mem.primeira(n.id));
  }
}

type Conhecida = Pick<
  StaffEntry,
  "nick" | "ticket_code" | "status" | "late_notice_at" | "has_pending_change_request"
> & {
  queue_id: string;
};

/**
 * Notificações da EQUIPE (profissional da fila e dono recebem as mesmas): o que os CLIENTES
 * fizeram — entrar, sair, avisar atraso, pedir mudança na comanda. O que a própria equipe faz
 * (chamar, iniciar, concluir) não notifica. Profissional recebe só a própria fila; o dono, todas
 * (o servidor já filtra pela audiência).
 */
export class RegrasEquipe {
  private mem = new Memoria();
  private pessoas = new Map<string, Conhecida>();
  private nomes = new Map<string, string>();
  /** Com mais de uma fila (dono), o texto diz de qual profissional é. */
  private filas = new Set<string>();

  aplicar(m: StreamMessage): Notificacao[] {
    switch (m.type) {
      case "stream.snapshot":
        this.snapshot(m.data as Snapshot);
        return [];
      case "queue.updated":
        return this.fila(m.data as StaffQueue, m.resource.version);
      case "participation.updated":
        return m.audience === "visit" ? [] : this.participacao(m.data as Participation);
      case "appointment.updated":
        return m.audience === "visit" ? [] : this.reserva(m.data as Appointment, m.resource.version);
      default:
        return [];
    }
  }

  private reservas = new Map<string, { status: Appointment["status"]; sinal: string | null }>();

  /** Reservas: o que o CLIENTE fez — reservou online, pagou, cancelou, avisou que chegou. */
  private reserva(a: Appointment, versao: number): Notificacao[] {
    if (!this.mem.nova(`a:${a.id}`, versao)) return [];
    const antes = this.reservas.get(a.id);
    this.reservas.set(a.id, { status: a.status, sinal: a.arrival_signal_at ?? null });
    const quem = a.nick ?? "Cliente";
    const quando = `${a.local_date.slice(8, 10)}/${a.local_date.slice(5, 7)} às ${a.local_time}`;
    const out: Notificacao[] = [];
    const base = { agendaData: a.local_date };
    if (versao === 1 && a.origin === "online" && (a.status === "confirmed" || a.status === "held"))
      out.push({
        ...base,
        id: `reservou:${a.id}`,
        title: "Nova reserva",
        body: `${quem} marcou ${quando} com ${a.professional.display_name}.`,
      });
    if (
      a.status === "confirmed" &&
      a.payment.booking?.status === "paid" &&
      (!antes || antes.status === "held")
    )
      out.push({
        ...base,
        id: `pagou:${a.id}`,
        title: "Reserva paga",
        body: `${quem} pagou pelo Pix o horário de ${quando}.`,
      });
    if (a.status === "cancelled" && a.cancelled_by === "customer" && antes?.status !== "cancelled")
      out.push({
        ...base,
        id: `cancelou:${a.id}`,
        title: "Reserva cancelada pelo cliente",
        body: `${quem} cancelou ${quando}. O horário ficou livre.`,
      });
    if (a.arrival_signal_at && !a.arrived_at && (!antes || !antes.sinal))
      out.push({
        ...base,
        id: `chegou:${a.id}:${a.arrival_signal_at}`,
        important: true,
        title: `${quem} avisou que chegou`,
        body: `Horário das ${a.local_time} com ${a.professional.display_name}. Confirme a chegada na Agenda.`,
      });
    return out.filter((n) => this.mem.primeira(n.id));
  }

  private snapshot(d: Snapshot) {
    this.mem.zerarVersoes();
    this.pessoas.clear();
    for (const q of d.staff_queues ?? []) this.lembrar(q);
  }

  private lembrar(q: StaffQueue) {
    this.filas.add(q.queue_id);
    this.nomes.set(q.queue_id, q.professional.display_name);
    for (const e of q.entries)
      this.pessoas.set(e.participation_id, {
        nick: e.nick,
        ticket_code: e.ticket_code,
        status: e.status,
        late_notice_at: e.late_notice_at ?? null,
        has_pending_change_request: e.has_pending_change_request ?? false,
        queue_id: q.queue_id,
      });
  }

  private onde(queueId: string): string {
    const nome = this.nomes.get(queueId);
    return this.filas.size > 1 && nome ? ` na fila de ${nome}` : "";
  }

  private fila(q: StaffQueue, versao: number): Notificacao[] {
    if (!this.mem.nova(`q:${q.queue_id}`, versao)) return [];
    const out: Notificacao[] = [];
    for (const e of q.entries) {
      const antes = this.pessoas.get(e.participation_id);
      const quem = `${e.nick} (${e.ticket_code})`;
      if (!antes) {
        if (e.status === "waiting")
          out.push({
            id: `entrou:${e.participation_id}:${q.queue_id}`,
            queueId: q.queue_id,
            title: "Nova pessoa na fila",
            body: `${quem} entrou${this.onde(q.queue_id) || " na fila"}.`,
          });
        continue;
      }
      if (!antes.late_notice_at && e.late_notice_at)
        out.push({
          id: `atraso:${e.participation_id}:${e.late_notice_at}`,
          queueId: q.queue_id,
          important: true,
          title: `${e.nick} avisou que vai atrasar`,
          body: `Senha ${e.ticket_code}${this.onde(q.queue_id)}. Você pode descer a pessoa e chamar o próximo.`,
        });
      if (!antes.has_pending_change_request && e.has_pending_change_request)
        out.push({
          id: `pedido:${e.participation_id}:${q.version}:${versao}`,
          queueId: q.queue_id,
          title: `${e.nick} pediu mudança na comanda`,
          body: `Senha ${e.ticket_code}${this.onde(q.queue_id)}. Abra a comanda para aceitar ou recusar.`,
        });
    }
    this.lembrar(q);
    return out.filter((n) => this.mem.primeira(n.id));
  }

  private participacao(p: Participation): Notificacao[] {
    if (!this.mem.nova(`p:${p.id}`, p.version)) return [];
    const antes = this.pessoas.get(p.id);
    if (antes) this.pessoas.set(p.id, { ...antes, status: p.status });
    // Desistência do próprio cliente: cancelada sem motivo (cancelamento da loja exige motivo).
    if (p.status !== "cancelled" || p.end_reason || (antes && antes.status === "cancelled")) return [];
    const n: Notificacao = {
      id: `saiu:${p.id}`,
      queueId: p.queue_id,
      title: "Alguém saiu da fila",
      body: `${p.nick} (${p.ticket_code}) desistiu${this.onde(p.queue_id)}.`,
    };
    return this.mem.primeira(n.id) ? [n] : [];
  }
}
