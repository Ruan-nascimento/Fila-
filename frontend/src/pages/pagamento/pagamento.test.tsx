import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { fakeApi, problem } from "../../test/fake-api";
import type { Order } from "../comanda/textos";
import { PagamentoCliente } from "./PagamentoCliente";
import { ReceberPagamento } from "./ReceberPagamento";

const EST = "0190a000-0000-7000-8000-0000000000e1";
const ORDER = "0190a000-0000-7000-8000-0000000000f1";
const CHARGE = "0190a000-0000-7000-8000-0000000000c9";
const RECEIPT = "0190a000-0000-7000-8000-0000000000r1";
const brl = (c: number) => ({ amount_cents: c, currency: "BRL" });
const BR_CODE = "00020101021226950014br.gov.bcb.pix2573exemplo.teste/15204000053039865802BR6304ABCD";
const base = `/v1/establishments/${EST}/orders/${ORDER}`;

function charge(extra: object = {}) {
  return {
    id: CHARGE,
    order_id: ORDER,
    status: "pending",
    amount: brl(7500),
    br_code: BR_CODE,
    expires_at: "2026-11-12T12:15:00Z",
    created_at: "2026-11-12T12:00:00Z",
    resolved_at: null,
    cancel_requested_at: null,
    ...extra,
  };
}

function order(payment: object = {}, extra: object = {}): Order {
  return {
    id: ORDER,
    establishment_id: EST,
    participation_id: "p1",
    status: "awaiting_payment",
    version: 5,
    items: [],
    total: brl(7500),
    total_frozen_at: "2026-11-12T12:00:00Z",
    pending_change_request: null,
    change_requests: [],
    closure_reason: null,
    interrupted_at: null,
    review_note: null,
    adjustments: [],
    points_expected: null,
    payment: {
      method_choice: null,
      amounts: {
        due: brl(7500),
        received: brl(0),
        conferred: brl(0),
        refunded: brl(0),
        outstanding: brl(7500),
      },
      verifying: false,
      charges: [],
      receipts: [],
      refunds: [],
      open_issue_kinds: [],
      ...payment,
    },
    created_at: "2026-11-12T12:00:00Z",
    closed_at: null,
    ...extra,
  } as Order;
}

describe("Pagamento do cliente", () => {
  it("escolhe Pix e gera a cobrança enviando só a versão (sem valor); clique duplo usa a mesma chave", async () => {
    const { requests } = fakeApi({
      [`POST ${base}/payment-method`]: () => Response.json({ data: order({ method_choice: "pix" }) }),
      [`POST ${base}/pix-charges`]: () => Response.json({ data: charge() }, { status: 201 }),
    });
    const reload = vi.fn(async () => {});
    const { rerender } = render(<PagamentoCliente shopId={EST} order={order()} onReload={reload} />);
    fireEvent.click(screen.getByLabelText("Pix"));
    await waitFor(() => expect(reload).toHaveBeenCalledTimes(1));
    expect(requests[0]?.body).toEqual({ expected_version: 5, method: "pix", acknowledge_active_pix: false });

    rerender(
      <PagamentoCliente
        shopId={EST}
        order={order({ method_choice: "pix" }, { version: 6 })}
        onReload={reload}
      />,
    );
    const button = screen.getByRole("button", { name: /Gerar Pix de R\$\s*75,00/ });
    fireEvent.click(button);
    fireEvent.click(button);
    await waitFor(() => expect(reload).toHaveBeenCalledTimes(2));
    const pix = requests.filter((r) => r.path.endsWith("/pix-charges"));
    expect(pix.length).toBeGreaterThanOrEqual(1);
    for (const r of pix) {
      expect(r.body).toEqual({ expected_version: 6 });
      expect(r.headers.get("Idempotency-Key")).toBe(pix[0]?.headers.get("Idempotency-Key"));
    }
  });

  it("mostra QR, copia e cola e validade; não existe 'já paguei'", async () => {
    fakeApi({});
    const writeText = vi.fn(async () => {});
    Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true });
    render(
      <PagamentoCliente
        shopId={EST}
        order={order({ method_choice: "pix", charges: [charge()] })}
        onReload={async () => {}}
      />,
    );
    expect(screen.getByRole("img", { name: /QR Code do Pix de R\$\s*75,00/ })).toBeInTheDocument();
    expect(screen.getByLabelText("Pix copia e cola")).toHaveValue(BR_CODE);
    fireEvent.click(screen.getByRole("button", { name: "Copiar código" }));
    await waitFor(() => expect(writeText).toHaveBeenCalledWith(BR_CODE));
    expect(await screen.findByText(/Código copiado/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /já paguei/i })).toBeNull();
    expect(screen.queryByRole("button", { name: /Gerar Pix/ })).toBeNull();
  });

  it("resultado desconhecido: mostra 'verificando' e não deixa gerar outro Pix nem trocar o meio", () => {
    fakeApi({});
    render(
      <PagamentoCliente
        shopId={EST}
        order={order({
          method_choice: "pix",
          verifying: true,
          charges: [charge({ status: "unknown", br_code: null })],
        })}
        onReload={async () => {}}
      />,
    );
    expect(screen.getByText(/Verificando pagamento com o banco/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Gerar Pix/ })).toBeNull();
    expect(screen.getByLabelText("Dinheiro")).toBeDisabled();
  });

  it("Pix indisponível: erro claro, a comanda continua e a nova tentativa usa outra chave", async () => {
    const { requests } = fakeApi({
      [`POST ${base}/pix-charges`]: () =>
        problem(
          503,
          "provider_unavailable",
          "Não foi possível gerar o Pix agora. Tente de novo em instantes.",
        ),
    });
    render(
      <PagamentoCliente shopId={EST} order={order({ method_choice: "pix" })} onReload={async () => {}} />,
    );
    fireEvent.click(screen.getByRole("button", { name: /Gerar Pix/ }));
    expect(await screen.findByText(/Não foi possível gerar o Pix agora/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /Gerar Pix/ }));
    await waitFor(() => expect(requests).toHaveLength(2));
    expect(requests[0]?.headers.get("Idempotency-Key")).not.toBe(requests[1]?.headers.get("Idempotency-Key"));
  });

  it("trocar de Pix em aberto para dinheiro pede ciência antes", async () => {
    const bodies: unknown[] = [];
    fakeApi({
      [`POST ${base}/payment-method`]: (_r, body) => {
        bodies.push(body);
        return (body as { acknowledge_active_pix: boolean }).acknowledge_active_pix
          ? Response.json({ data: order({ method_choice: "cash" }) })
          : problem(409, "pix_charge_active", "Existe um Pix em aberto");
      },
    });
    const reload = vi.fn(async () => {});
    render(
      <PagamentoCliente
        shopId={EST}
        order={order({ method_choice: "pix", charges: [charge()] })}
        onReload={reload}
      />,
    );
    fireEvent.click(screen.getByLabelText("Dinheiro"));
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent(/continua válido até expirar/);
    fireEvent.click(screen.getByRole("button", { name: /Entendi, trocar para dinheiro/ }));
    await waitFor(() => expect(reload).toHaveBeenCalled());
    expect(bodies).toEqual([
      { expected_version: 5, method: "cash", acknowledge_active_pix: false },
      { expected_version: 5, method: "cash", acknowledge_active_pix: true },
    ]);
  });

  it("Pix recebido pelo banco aparece como pago aguardando conferência, nunca como 'não pago'", () => {
    fakeApi({});
    const receipt = {
      id: RECEIPT,
      method: "pix",
      amount: brl(7500),
      status: "registered",
      classification: "principal",
      pix_charge_id: CHARGE,
      registered_at: "2026-11-12T12:05:00Z",
      registered_by: "provider",
      conferred_at: null,
      conferred_by_role: null,
      cash_tendered: null,
      change_given: null,
      card_reference: null,
      refunded: brl(0),
    };
    render(
      <PagamentoCliente
        shopId={EST}
        order={order(
          { method_choice: "pix", charges: [charge({ status: "paid" })], receipts: [receipt] },
          { status: "awaiting_confirmation" },
        )}
        onReload={async () => {}}
      />,
    );
    expect(screen.getByText(/Pagamento recebido pelo banco/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Gerar Pix/ })).toBeNull();
    expect(screen.queryByText(/Como você vai pagar/)).toBeNull();
  });
});

describe("Recebimento pela equipe", () => {
  const ctx = { estId: EST, estName: "Loja", isOwner: false, professionalId: "pro1" };

  it("dinheiro: calcula o troco e envia o valor devido e o entregue em centavos", async () => {
    const bodies: unknown[] = [];
    fakeApi({
      [`POST ${base}/receipts`]: (_r, body) => {
        bodies.push(body);
        return Response.json({ data: order({}, { status: "settled" }) }, { status: 201 });
      },
    });
    const saved = vi.fn();
    render(<ReceberPagamento ctx={ctx} order={order()} onSaved={saved} onConflict={async () => {}} />);
    fireEvent.change(screen.getByLabelText(/Valor entregue/), { target: { value: "80" } });
    expect(screen.getByText(/Troco:/)).toHaveTextContent(/R\$\s*5,00/);
    fireEvent.change(screen.getByLabelText(/Valor entregue/), { target: { value: "70" } });
    expect(screen.getByText(/precisa ser pelo menos R\$\s*75,00/)).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText(/Valor entregue/), { target: { value: "80,10" } });
    fireEvent.click(screen.getByRole("button", { name: /Registrar R\$\s*75,00 recebido e conferido/ }));
    await waitFor(() => expect(saved).toHaveBeenCalled());
    expect(bodies).toEqual([
      { expected_version: 5, method: "cash", amount_cents: 7500, cash_tendered_cents: 8010 },
    ]);
  });

  it("cartão: recusa número de cartão na referência sem enviar nada", () => {
    const { requests } = fakeApi({});
    render(<ReceberPagamento ctx={ctx} order={order()} onSaved={() => {}} onConflict={async () => {}} />);
    fireEvent.click(screen.getByLabelText("Cartão (maquininha)"));
    fireEvent.change(screen.getByLabelText(/Referência da maquininha/), {
      target: { value: "4111 1111 1111 1111" },
    });
    fireEvent.click(screen.getByRole("button", { name: /Registrar/ }));
    expect(screen.getByText(/nunca o número do cartão/)).toBeInTheDocument();
    expect(requests).toHaveLength(0);
  });

  it("Pix em aberto: não recebe na loja sem trocar o meio; Pix verificando: espera", () => {
    fakeApi({});
    const { rerender } = render(
      <ReceberPagamento
        ctx={ctx}
        order={order({ method_choice: "pix", charges: [charge()] })}
        onSaved={() => {}}
        onConflict={async () => {}}
      />,
    );
    expect(screen.queryByRole("form", { name: "Receber na loja" })).toBeNull();
    expect(screen.getByRole("button", { name: "Trocar para dinheiro" })).toBeInTheDocument();
    rerender(
      <ReceberPagamento
        ctx={ctx}
        order={order({ verifying: true, charges: [charge({ status: "unknown", br_code: null })] })}
        onSaved={() => {}}
        onConflict={async () => {}}
      />,
    );
    expect(screen.getByText(/Verificando um Pix com o banco/)).toBeInTheDocument();
    expect(screen.queryByRole("form", { name: "Receber na loja" })).toBeNull();
  });

  it("confere o Pix recebido (registro separado)", async () => {
    const { requests } = fakeApi({
      [`POST ${base}/receipts/${RECEIPT}/confer`]: () =>
        Response.json({ data: order({}, { status: "settled" }) }),
    });
    const receipt = {
      id: RECEIPT,
      method: "pix",
      amount: brl(7500),
      status: "registered",
      classification: "principal",
      pix_charge_id: CHARGE,
      registered_at: "2026-11-12T12:05:00Z",
      registered_by: "provider",
      conferred_at: null,
      conferred_by_role: null,
      cash_tendered: null,
      change_given: null,
      card_reference: null,
      refunded: brl(0),
    };
    const saved = vi.fn();
    render(
      <ReceberPagamento
        ctx={ctx}
        order={order(
          { receipts: [receipt], charges: [charge({ status: "paid" })] },
          { status: "awaiting_confirmation" },
        )}
        onSaved={saved}
        onConflict={async () => {}}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: /Conferir recebimento Pix de R\$\s*75,00/ }));
    await waitFor(() => expect(saved).toHaveBeenCalled());
    expect(requests[0]?.body).toEqual({});
    expect(requests[0]?.headers.get("Idempotency-Key")).toBeTruthy();
  });
});
