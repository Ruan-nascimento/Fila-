import { useCallback, useRef, useState } from "react";
import { Link } from "react-router";
import { CalendarCheck, ChevronLeft, UserRound } from "lucide-react";
import { api } from "../../api/client";
import type { components } from "../../api/schema.gen";
import { dataPorExtenso, itensTexto, type Appointment } from "../../features/agenda/horario";
import { PagamentoReserva } from "../../features/agenda/PagamentoReserva";
import { useStreamMessages } from "../../stream/context";
import { SeletorHorario, type Availability, type Slot } from "../../features/agenda/SeletorHorario";
import { formatCents } from "../../money";
import { useSession } from "../../session/context";
import { espera } from "../../tempo";
import { Button, Card, Loading, LoadError, Notice, Section } from "../../ui/ui";
import { cx } from "../../ui/cx";
import { useStoreData } from "../fila/dadosContexto";
import { CodigoUmaVez, Identificacao } from "../fila/Identificacao";
import { SeletorItens } from "../fila/SeletorItens";
import { totais } from "../fila/pedidoTotais";
import { useCommand } from "../gestao/comando";
import { CommandError } from "../gestao/comum";

type PublicEstablishment = components["schemas"]["PublicEstablishment"];
type VisitorCreated = components["schemas"]["VisitorSessionCreated"];
type Passo = "profissional" | "servicos" | "horario" | "revisao" | "feito";

const passos: { id: Exclude<Passo, "feito">; label: string }[] = [
  { id: "profissional", label: "Profissional" },
  { id: "servicos", label: "Serviços" },
  { id: "horario", label: "Dia e horário" },
  { id: "revisao", label: "Revisão" },
];

/**
 * Agendar horário (ADR-0038): profissional → serviços e quantidades → dia/horário → revisão →
 * confirmação. Voltar não zera o que continua válido. Horário disputado na confirmação volta para
 * a escolha de horário com os serviços mantidos. Reserva gratuita: nada a pagar agora.
 */
export function Agendar({ shop }: { shop: PublicEstablishment }) {
  const { queues, services, products, categories, ordering, servicesError, retryServices } = useStoreData();
  const { state, refresh } = useSession();
  const [passo, setPasso] = useState<Passo>("profissional");
  const [prof, setProf] = useState<string>("");
  const [svc, setSvc] = useState<Record<string, number>>({});
  const [prod, setProd] = useState<Record<string, number>>({});
  const [slot, setSlot] = useState<Slot | null>(null);
  const [info, setInfo] = useState<Availability | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [done, setDone] = useState<Appointment | null>(null);
  const [created, setCreated] = useState<VisitorCreated | null>(null);
  const nickRef = useRef<HTMLInputElement>(null);
  const cmd = useCommand();
  // Reserva retida aguardando o Pix: a confirmação chega pelo evento da reserva (nunca pela tela).
  const recarregar = useCallback(() => {
    if (!done) return;
    void api
      .GET("/v1/establishments/{establishmentId}/appointments/{appointmentId}", {
        params: { path: { establishmentId: shop.id, appointmentId: done.id } },
      })
      .then((r) => {
        if (r.data) setDone(r.data.data);
      });
  }, [done, shop.id]);
  useStreamMessages((m) => {
    if (m.type === "appointment.updated") recarregar();
  });
  if (servicesError && !services) return <LoadError message={servicesError} onRetry={retryServices} />;
  if (!queues || !services) return <Loading>Carregando…</Loading>;
  const account = state.status === "ready" ? state.session.account : null;
  const visitor =
    state.status === "ready" ? state.session.visitors.find((v) => v.establishment_id === shop.id) : undefined;
  const identified = account !== null || visitor !== undefined;
  const profs = queues.map((q) => q.professional);
  const profName = profs.find((p) => p.id === prof)?.display_name ?? "";
  const doProf = services.filter((s) => s.professional_ids.includes(prof));
  const t = totais(doProf, products, svc, prod);
  const items = [
    ...Object.entries(svc).filter(([, n]) => n > 0),
    ...Object.entries(prod).filter(([, n]) => n > 0),
  ].map(([id, n]) => `${id}:${n}`);
  const temServico = Object.values(svc).some((n) => n > 0);

  async function confirmar() {
    if (!slot) return;
    const body = {
      professional_id: prof,
      starts_at: slot.starts_at,
      items: [
        ...Object.entries(svc)
          .filter(([, n]) => n > 0)
          .map(([id, n]) => ({ service_id: id, quantity: n })),
        ...Object.entries(prod)
          .filter(([, n]) => n > 0)
          .map(([id, n]) => ({ product_id: id, quantity: n })),
      ],
    };
    const out = await cmd.run(`agendar:${JSON.stringify(body)}`, (key) =>
      api.POST("/v1/establishments/{establishmentId}/appointments", {
        params: { path: { establishmentId: shop.id }, header: { "Idempotency-Key": key } },
        body,
      }),
    );
    if (out.ok) {
      setDone(out.data.data);
      setPasso("feito");
    } else if (out.problem?.code === "slot_unavailable") {
      // Disputa: outra pessoa ganhou. Volta para os horários, com os serviços mantidos.
      setSlot(null);
      setNotice("Esse horário acabou de ser ocupado. Escolha outro; seus serviços continuam escolhidos.");
      setPasso("horario");
    }
  }

  if (passo === "feito" && done && (done.status === "held" || done.status === "expired"))
    return (
      <Card className="grid gap-4 p-5" role="status">
        <p className="m-0 text-lg font-bold">
          {done.status === "held" ? "Falta pagar para garantir o horário" : "O prazo para pagar acabou"}
        </p>
        <p className="m-0">
          {dataPorExtenso(done.local_date)}, às {done.local_time}, com {done.professional.display_name}.
        </p>
        <PagamentoReserva a={done} onChanged={recarregar} />
        <p className="m-0 text-xs text-fg-muted">{done.terms}</p>
        <Link to={`/loja/${shop.slug}/reservas`} className="inline-flex min-h-11 items-center font-semibold">
          Ver minhas reservas
        </Link>
      </Card>
    );

  if (passo === "feito" && done)
    return (
      <Card className="grid gap-4 p-5" role="status">
        <p className="m-0 flex items-center gap-2 text-lg font-bold">
          <CalendarCheck aria-hidden="true" className="size-6 text-success" /> Horário confirmado
        </p>
        <p className="m-0">
          {dataPorExtenso(done.local_date)}, às {done.local_time}, com {done.professional.display_name}.
        </p>
        <p className="m-0 text-sm text-fg-muted">
          {itensTexto(done)} ·{" "}
          {done.payment.booking
            ? `${formatCents(done.payment.due_at_store.amount_cents)} na barbearia`
            : `${formatCents(done.price.amount_cents)} na barbearia · nada a pagar agora`}
          .
        </p>
        <PagamentoReserva a={done} onChanged={recarregar} />
        <p className="m-0 text-sm">
          Chegue até {done.tolerance_minutes} min depois do horário. Se não puder ir, cancele para liberar o
          horário.
        </p>
        <div className="flex flex-wrap gap-2">
          <Link
            to={`/loja/${shop.slug}/reservas`}
            className="inline-flex min-h-11 items-center font-semibold"
          >
            Ver minhas reservas
          </Link>
        </div>
      </Card>
    );

  const idx = passos.findIndex((p) => p.id === passo);
  return (
    <div className="grid gap-5 lg:grid-cols-[minmax(0,1fr)_22rem] lg:items-start lg:gap-8">
      <div className="grid min-w-0 gap-5">
        <ol aria-label="Etapas" className="m-0 flex list-none flex-wrap gap-2 p-0 text-sm">
          {passos.map((p, i) => (
            <li
              key={p.id}
              aria-current={p.id === passo ? "step" : undefined}
              className={cx(
                "rounded-full border px-3 py-1",
                p.id === passo
                  ? "border-primary bg-select font-semibold text-accent"
                  : i < idx
                    ? "border-line text-fg"
                    : "border-line text-fg-muted",
              )}
            >
              {i + 1}. {p.label}
            </li>
          ))}
        </ol>
        {idx > 0 && (
          <div>
            <Button variant="secondary" onClick={() => setPasso(passos[idx - 1]?.id ?? "profissional")}>
              <ChevronLeft aria-hidden="true" className="size-4" /> Voltar
            </Button>
          </div>
        )}

        {passo === "profissional" && (
          <Section title="Com quem?" description="Escolha o profissional.">
            {profs.length === 0 ? (
              <p className="m-0 text-fg-muted">Nenhum profissional disponível.</p>
            ) : (
              <div className="grid gap-2 sm:grid-cols-2">
                {profs.map((p) => (
                  <Button
                    key={p.id}
                    variant={p.id === prof ? "primary" : "secondary"}
                    aria-pressed={p.id === prof}
                    onClick={() => {
                      if (p.id !== prof) {
                        // Mantém só os serviços que o novo profissional faz.
                        setSvc(
                          Object.fromEntries(
                            Object.entries(svc).filter(([id]) =>
                              services.find((s) => s.id === id)?.professional_ids.includes(p.id),
                            ),
                          ),
                        );
                        setSlot(null);
                      }
                      setProf(p.id);
                      setPasso("servicos");
                    }}
                  >
                    <UserRound aria-hidden="true" className="size-5" /> {p.display_name}
                  </Button>
                ))}
              </div>
            )}
          </Section>
        )}

        {passo === "servicos" && (
          <Section
            title={`Serviços com ${profName}`}
            description="Escolha os serviços e as quantidades (ex.: 3 cortes para a família)."
          >
            <SeletorItens
              title="Serviços"
              services={doProf}
              products={ordering.products_enabled ? products : []}
              categories={categories}
              services_chosen={svc}
              products_chosen={prod}
              onService={(id, n) => {
                setSlot(null);
                setSvc({ ...svc, [id]: n });
              }}
              onProduct={(id, n) => {
                setSlot(null);
                setProd({ ...prod, [id]: n });
              }}
              max={ordering.max_service_quantity}
            />
            <div>
              <Button disabled={!temServico} onClick={() => setPasso("horario")}>
                Escolher dia e horário
              </Button>
            </div>
          </Section>
        )}

        {passo === "horario" && (
          <Section
            title="Dia e horário"
            description={`Com ${profName}. Duração estimada: ${espera(t.minutes)}.`}
          >
            <SeletorHorario
              estId={shop.id}
              professionalId={prof}
              items={items}
              value={slot?.starts_at ?? null}
              initialDate={slot ? undefined : info?.date}
              lastDateFor={(a) => (account ? a.last_date : a.visitor_last_date)}
              onPick={(s, a) => {
                setNotice(null);
                setSlot(s);
                setInfo(a);
                setPasso("revisao");
              }}
              notice={notice}
            />
            {!account && (
              <p className="m-0 text-sm text-fg-muted">
                Sem conta, dá para marcar até 7 dias à frente. Com conta, até a data mais distante que a
                barbearia oferece.
              </p>
            )}
          </Section>
        )}

        {passo === "revisao" && slot && info && (
          <Section title="Revise antes de confirmar">
            <dl className="m-0 grid gap-2 rounded-xl border border-line bg-surface p-4 text-sm sm:grid-cols-[10rem_1fr]">
              <dt className="font-semibold">Quando</dt>
              <dd className="m-0">
                {dataPorExtenso(info.date)}, às {slot.local_time}
              </dd>
              <dt className="font-semibold">Profissional</dt>
              <dd className="m-0">{profName}</dd>
              <dt className="font-semibold">Serviços</dt>
              <dd className="m-0">
                {t.lines.map((l) => (l.qty > 1 ? `${l.qty}× ${l.name}` : l.name)).join(", ")}
              </dd>
              <dt className="font-semibold">Duração</dt>
              <dd className="m-0">
                {espera(info.service_minutes)}
                {info.buffer_minutes > 0 ? ` (+ ${info.buffer_minutes} min de preparação)` : ""}
              </dd>
              <dt className="font-semibold">Valor previsto</dt>
              {info.payment.mode === "free" ? (
                <dd className="m-0">
                  {formatCents(info.price.amount_cents)} na barbearia.{" "}
                  <strong>Reserva gratuita: nada a pagar agora.</strong>
                </dd>
              ) : (
                <dd className="m-0 grid gap-1">
                  <span>
                    <strong>Agora, pelo Pix: {formatCents(info.payment.due_now.amount_cents)}</strong> (
                    {info.payment.mode === "deposit" ? "sinal, descontado do atendimento" : "taxa de reserva"}
                    )
                  </span>
                  <span>Na barbearia: {formatCents(info.payment.due_at_store.amount_cents)}</span>
                  <span>Total previsto: {formatCents(info.payment.estimated_total.amount_cents)}</span>
                </dd>
              )}
            </dl>
            {info.payment.mode !== "free" && (
              <Notice kind="info" title="Antes de pagar">
                <p>{info.payment.terms}</p>
              </Notice>
            )}
            <p className="m-0 text-sm text-fg-muted">
              Chegue no horário; há uma tolerância depois dele. Se não puder ir, cancele em “Minhas reservas”
              para liberar o horário. O valor final é o dos serviços feitos.
            </p>
            <CommandError
              problem={cmd.problem?.code === "slot_unavailable" ? null : cmd.problem}
              offline={cmd.offline}
            />
            {identified ? (
              <div>
                <Button
                  disabled={cmd.busy !== null}
                  aria-busy={cmd.busy !== null}
                  onClick={() => void confirmar()}
                >
                  {cmd.busy
                    ? "Confirmando…"
                    : info.payment.mode === "free"
                      ? `Confirmar ${slot.local_time}`
                      : `Reservar ${slot.local_time} e pagar ${formatCents(info.payment.due_now.amount_cents)}`}
                </Button>
              </div>
            ) : (
              <Notice kind="info" title="Para confirmar, identifique-se">
                <p>
                  Entre com sua conta ou continue sem conta (apelido + código de recuperação). Suas escolhas
                  continuam aqui.
                </p>
              </Notice>
            )}
          </Section>
        )}
      </div>
      <aside aria-label="Identificação" className="grid gap-4 lg:sticky lg:top-20">
        {created && <CodigoUmaVez created={created} selectionKept onDismiss={() => setCreated(null)} />}
        {!identified && (
          <Identificacao
            shopId={shop.id}
            shopName={shop.name}
            returnTo={`/loja/${shop.slug}/agendar`}
            needed={passo === "revisao"}
            nickRef={nickRef}
            motivo="reserva"
            onIdentified={async (c) => {
              setCreated(c);
              await refresh();
            }}
          />
        )}
        {!identified && (
          <Link to={`/loja/${shop.slug}/acesso`} className="text-sm font-semibold">
            Já reservou sem conta e confirmou um e-mail? Recuperar pelo e-mail
          </Link>
        )}
      </aside>
    </div>
  );
}
