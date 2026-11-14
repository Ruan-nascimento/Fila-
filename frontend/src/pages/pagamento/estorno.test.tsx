import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { fakeApi, problem } from "../../test/fake-api";
import type { Order } from "../comanda/textos";
import { Pendencias } from "../gestao/Pendencias";
import { ReceberPagamento } from "./ReceberPagamento";

const EST = "0190a000-0000-7000-8000-0000000000e1";
const ORDER = "0190a000-0000-7000-8000-0000000000f1";
const RECEIPT = "0190a000-0000-7000-8000-0000000000a9";
const REFUND = "0190a000-0000-7000-8000-0000000000b9";
const ISSUE = "0190a000-0000-7000-8000-0000000000c9";
const brl = (c: number) => ({ amount_cents: c, currency: "BRL" });
const base = `/v1/establishments/${EST}/orders/${ORDER}`;
const owner = { estId: EST, estName: "Loja", isOwner: true, professionalId: null };
const pro = { estId: EST, estName: "Loja", isOwner: false, professionalId: "pro1" };

function receipt(extra: object = {}) {
  return {
    id: RECEIPT,
    method: "cash",
    amount: brl(4500),
    status: "conferred",
    classification: "principal",
    pix_charge_id: null,
    registered_at: "2026-11-12T12:05:00Z",
    registered_by: "professional",
    conferred_at: "2026-11-12T12:05:00Z",
    conferred_by_role: "professional",
    cash_tendered: brl(5000),
    change_given: brl(500),
    card_reference: null,
    refunded: brl(0),
    ...extra,
  };
}

function refund(extra: object = {}) {
  return {
    id: REFUND,
    receipt_id: RECEIPT,
    amount: brl(4500),
    status: "under_review",
    reason: "corte refeito",
    requested_at: "2026-11-12T13:00:00Z",
    resolved_at: null,
    evidence: null,
    ...extra,
  };
}

function order(payment: object = {}): Order {
  return {
    id: ORDER,
    establishment_id: EST,
    participation_id: "p1",
    status: "settled",
    version: 9,
    items: [],
    total: brl(4500),
    total_frozen_at: "2026-11-12T12:00:00Z",
    pending_change_request: null,
    change_requests: [],
    closure_reason: null,
    interrupted_at: null,
    review_note: null,
    adjustments: [],
    points_expected: null,
    payment: {
      method_choice: "cash",
      amounts: {
        due: brl(4500),
        received: brl(4500),
        conferred: brl(4500),
        refunded: brl(0),
        outstanding: brl(0),
      },
      verifying: false,
      charges: [],
      receipts: [receipt()],
      refunds: [],
      open_issue_kinds: [],
      ...payment,
    },
    created_at: "2026-11-12T12:00:00Z",
    closed_at: "2026-11-12T12:05:00Z",
  } as Order;
}

describe("Estorno na comanda", () => {
  it("só o dono vê 'Estornar'; o pedido leva só o motivo e pede a senha quando o servidor exige", async () => {
    let calls = 0;
    const { requests } = fakeApi({
      [`POST ${base}/receipts/${RECEIPT}/refunds`]: () => {
        calls++;
        return calls === 1
          ? problem(403, "reauthentication_required", "Confirme sua senha")
          : Response.json(
              { data: order({ refunds: [refund({ status: "under_review" })] }) },
              { status: 202 },
            );
      },
      "POST /v1/auth/reauthenticate": () => new Response(null, { status: 204 }),
    });
    const { unmount } = render(
      <ReceberPagamento ctx={pro} order={order()} onSaved={() => {}} onConflict={async () => {}} />,
    );
    expect(screen.queryByRole("button", { name: /Estornar/ })).toBeNull();
    unmount();
    const saved = vi.fn();
    render(<ReceberPagamento ctx={owner} order={order()} onSaved={saved} onConflict={async () => {}} />);
    fireEvent.click(screen.getByRole("button", { name: /Estornar Dinheiro de R\$\s*45,00/ }));
    fireEvent.change(screen.getByLabelText(/Motivo do estorno/), { target: { value: "corte refeito" } });
    fireEvent.click(screen.getByRole("button", { name: "Pedir estorno" }));
    fireEvent.change(await screen.findByLabelText("Sua senha"), { target: { value: "senha-forte-1" } });
    fireEvent.click(screen.getByRole("button", { name: "Confirmar e continuar" }));
    await waitFor(() => expect(saved).toHaveBeenCalled());
    const posts = requests.filter((r) => r.path.endsWith("/refunds"));
    expect(posts).toHaveLength(2);
    expect(posts[0]?.body).toEqual({ reason: "corte refeito" });
    expect(posts[1]?.headers.get("Idempotency-Key")).toBe(posts[0]?.headers.get("Idempotency-Key"));
  });

  it("estorno em revisão: dono confirma só com evidência; não aparece 'estornado' antes", async () => {
    const bodies: unknown[] = [];
    fakeApi({
      [`POST ${base}/refunds/${REFUND}/confirm`]: (_r, body) => {
        bodies.push(body);
        return Response.json({
          data: order({ refunds: [refund({ status: "confirmed", evidence: "owner_documented" })] }),
        });
      },
    });
    render(
      <ReceberPagamento
        ctx={owner}
        order={order({ refunds: [refund()] })}
        onSaved={() => {}}
        onConflict={async () => {}}
      />,
    );
    expect(screen.getByText(/estorno em revisão do dono/)).toBeInTheDocument();
    expect(screen.queryByText(/estornado R\$/)).toBeNull();
    const confirm = screen.getByRole("button", { name: "Confirmar estorno" });
    expect(confirm).toBeDisabled();
    fireEvent.change(screen.getByLabelText(/Evidência/), { target: { value: "recibo assinado" } });
    fireEvent.change(screen.getByLabelText(/Referência externa/), { target: { value: "REC-1" } });
    fireEvent.click(screen.getByRole("button", { name: "Confirmar estorno" }));
    await waitFor(() =>
      expect(bodies).toEqual([{ evidence_note: "recibo assinado", external_reference: "REC-1" }]),
    );
    // Não há botão de estornar de novo um recebimento que já tem estorno ativo.
    expect(screen.queryByRole("button", { name: /^Estornar/ })).toBeNull();
  });

  it("recebimento com estorno não aparece para conferência; pendência aparece para a equipe", () => {
    fakeApi({});
    render(
      <ReceberPagamento
        ctx={pro}
        order={order({
          receipts: [
            receipt({
              method: "pix",
              status: "registered",
              classification: "excess",
              registered_by: "provider",
              cash_tendered: null,
              change_given: null,
            }),
            receipt({ id: "0190a000-0000-7000-8000-0000000000aa" }),
          ],
          refunds: [refund({ status: "processing" })],
          open_issue_kinds: ["late_receipt"],
        })}
        onSaved={() => {}}
        onConflict={async () => {}}
      />,
    );
    expect(screen.queryByRole("button", { name: /Conferir/ })).toBeNull();
    // Profissional não estorna (só o dono), mesmo havendo recebimento estornável.
    expect(screen.queryByRole("button", { name: /Estornar/ })).toBeNull();
    expect(screen.getByRole("note", { name: "Pendências desta comanda" })).toHaveTextContent(
      /Pix pago depois/,
    );
  });
});

describe("Painel de pendências do dono", () => {
  it("mostra contadores e pendências; resolver exige nota e não apaga nada", async () => {
    const bodies: unknown[] = [];
    const issue = {
      id: ISSUE,
      order_id: ORDER,
      kind: "late_receipt",
      status: "open",
      amount: brl(7500),
      created_at: "2026-11-12T13:00:00Z",
      resolved_at: null,
      resolution_note: null,
    };
    fakeApi({
      [`GET /v1/establishments/${EST}/pendencies`]: () =>
        Response.json({
          data: {
            orders_awaiting_payment: 2,
            orders_awaiting_confirmation: 1,
            interruptions_to_review: 0,
            charges_unknown: 1,
            open_financial_issues: 1,
            refunds_under_review: 0,
            deletion_requests_processing: 0,
            unavailable_signals: 0,
          },
          cursor: "c1",
        }),
      [`GET /v1/establishments/${EST}/financial-issues`]: () =>
        Response.json({ data: [issue], page: { next_cursor: null, has_more: false } }),
      [`POST /v1/establishments/${EST}/financial-issues/${ISSUE}/resolve`]: (_r, body) => {
        bodies.push(body);
        return Response.json({
          data: {
            ...issue,
            status: "resolved",
            resolved_at: "2026-11-12T14:00:00Z",
            resolution_note: "devolvido em dinheiro",
          },
        });
      },
    });
    render(<Pendencias ctx={owner} />);
    const resumo = await screen.findByRole("list", { name: "Resumo" });
    expect(
      within(resumo)
        .getByText(/Pix verificando com o banco/)
        .closest("li"),
    ).toHaveTextContent("1");
    expect(await screen.findByText(/Pix pago depois de trocar o meio/)).toBeInTheDocument();
    expect(screen.getByText(/valor R\$\s*75,00/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Marcar como resolvida" }));
    expect(bodies).toHaveLength(0);
    fireEvent.change(screen.getByLabelText(/Como foi tratada/), {
      target: { value: "devolvido em dinheiro" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Marcar como resolvida" }));
    expect(await screen.findByText(/Resolvida em .*: devolvido em dinheiro/)).toBeInTheDocument();
    expect(bodies).toEqual([{ resolution_note: "devolvido em dinheiro" }]);
  });

  it("pendência da restauração de backup: sem comanda, com a referência do provedor", async () => {
    fakeApi({
      [`GET /v1/establishments/${EST}/pendencies`]: () =>
        Response.json({
          data: {
            orders_awaiting_payment: 0,
            orders_awaiting_confirmation: 0,
            interruptions_to_review: 0,
            charges_unknown: 0,
            open_financial_issues: 1,
            refunds_under_review: 0,
            deletion_requests_processing: 0,
            unavailable_signals: 0,
          },
          cursor: "c1",
        }),
      [`GET /v1/establishments/${EST}/financial-issues`]: () =>
        Response.json({
          data: [
            {
              id: ISSUE,
              order_id: null,
              provider_reference: "pix_char_abc123",
              kind: "restored_orphan_charge",
              status: "open",
              amount: brl(3000),
              created_at: "2026-11-14T10:00:00Z",
              resolved_at: null,
              resolution_note: null,
            },
          ],
          page: { next_cursor: null, has_more: false },
        }),
    });
    render(<Pendencias ctx={owner} />);
    expect(await screen.findByText(/backup restaurado não conhecia/)).toBeInTheDocument();
    const card = screen.getByText(/sem comanda/);
    expect(card).toHaveTextContent(/referência na AbacatePay pix_char_abc123/);
    expect(card).toHaveTextContent(/valor R\$\s*30,00/);
  });
});
