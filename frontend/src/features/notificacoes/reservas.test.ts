import { describe, expect, it } from "vitest";
import type { StreamMessage } from "../../stream/client";
import { RegrasCliente, RegrasEquipe } from "./regras";

function msg(
  data: Record<string, unknown>,
  version: number,
  audience: "visit" | "owner" | "professional",
): StreamMessage {
  return {
    type: "appointment.updated",
    audience,
    resource: { type: "appointment", id: String(data.id), version },
    occurred_at: "2026-11-12T12:00:00Z",
    data,
  } as unknown as StreamMessage;
}

const base = {
  id: "a1",
  nick: "Ana",
  origin: "online",
  local_date: "2026-11-13",
  local_time: "10:00",
  professional: { id: "p1", display_name: "Beto" },
  payment: { mode: "free", booking: null },
  arrival_signal_at: null,
  arrived_at: null,
  cancelled_by: null,
  called_at: null,
};

describe("notificações de reserva", () => {
  it("equipe: nova reserva, pagamento, Cheguei e cancelamento do cliente — uma vez cada, levando à Agenda do dia", () => {
    const r = new RegrasEquipe();
    const nova = r.aplicar(
      msg(
        { ...base, status: "held", payment: { mode: "deposit", booking: { status: "awaiting_payment" } } },
        1,
        "owner",
      ),
    );
    expect(nova.map((n) => n.title)).toEqual(["Nova reserva"]);
    expect(nova[0]?.agendaData).toBe("2026-11-13");
    const paga = r.aplicar(
      msg(
        { ...base, status: "confirmed", payment: { mode: "deposit", booking: { status: "paid" } } },
        2,
        "owner",
      ),
    );
    expect(paga.map((n) => n.title)).toEqual(["Reserva paga"]);
    const chegou = r.aplicar(
      msg({ ...base, status: "confirmed", arrival_signal_at: "2026-11-13T12:55:00Z" }, 3, "owner"),
    );
    expect(chegou[0]?.important).toBe(true);
    // Reconexão: o mesmo evento de novo não notifica.
    expect(
      r.aplicar(msg({ ...base, status: "confirmed", arrival_signal_at: "2026-11-13T12:55:00Z" }, 3, "owner")),
    ).toEqual([]);
    const cancelou = r.aplicar(msg({ ...base, status: "cancelled", cancelled_by: "customer" }, 4, "owner"));
    expect(cancelou.map((n) => n.title)).toEqual(["Reserva cancelada pelo cliente"]);
    // Evento da visão do titular não vira notificação de equipe.
    expect(r.aplicar(msg({ ...base, id: "a2", status: "confirmed" }, 1, "visit"))).toEqual([]);
  });

  it("cliente: chamada do horário é importante e leva a Minhas reservas", () => {
    const r = new RegrasCliente();
    const n = r.aplicar(msg({ ...base, status: "called", called_at: "2026-11-13T12:58:00Z" }, 5, "visit"));
    expect(n).toHaveLength(1);
    expect(n[0]?.important).toBe(true);
    expect(n[0]?.aba).toBe("reservas");
    expect(r.aplicar(msg({ ...base, status: "cancelled", cancelled_by: "customer" }, 6, "visit"))).toEqual(
      [],
    );
  });
});
