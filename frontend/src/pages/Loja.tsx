import { useEffect, useState, type FormEvent } from "react";
import { useParams } from "react-router";
import { api } from "../api/client";
import { call, problemMessage } from "../api/problem";
import type { components } from "../api/schema.gen";
import { useSession } from "../session/context";
import { Field, Notice, Submit } from "../ui/ui";
import { useForm } from "../ui/useForm";
import { formatCents } from "../money";
import { StreamProvider, StreamStatusNotice } from "../stream/StreamProvider";
import { usePublicQueues } from "./fila/filasPublicas";
import { Historico } from "./fila/Historico";
import { MinhaVisita } from "./fila/MinhaVisita";

type PublicEstablishment = components["schemas"]["PublicEstablishment"];
type VisitorCreated = components["schemas"]["VisitorSessionCreated"];

const dateTime = new Intl.DateTimeFormat("pt-BR", { dateStyle: "short", timeStyle: "short" });

/** Página da loja aberta pelo link/QR: identifica o visitante (nick) ou recupera a visita pelo código. */
export function Loja() {
  const { slug = "" } = useParams();
  const [shop, setShop] = useState<PublicEstablishment | null>(null);
  const [error, setError] = useState<string | null>(null);
  const { state } = useSession();

  useEffect(() => {
    let active = true;
    void call(() => api.GET("/v1/public/slugs/{slug}", { params: { path: { slug } } })).then((out) => {
      if (!active) return;
      if (out.ok) setShop(out.data.data);
      else
        setError(
          out.problem?.status === 404
            ? "Loja não encontrada. Confira o link."
            : problemMessage(out.problem, out.offline),
        );
    });
    return () => {
      active = false;
    };
  }, [slug]);

  if (error) return <Notice kind="erro">{error}</Notice>;
  if (!shop) return <p role="status">Carregando loja…</p>;
  // Uma conexão de atualizações para a página: filas públicas e, com visitante ou conta, a visita.
  const identified =
    state.status === "ready" &&
    (state.session.account !== null || state.session.visitors.some((v) => v.establishment_id === shop.id));
  return (
    <StreamProvider estId={shop.id} channels={identified ? ["public", "visit"] : ["public"]}>
      <section aria-labelledby="titulo-loja">
        <h2 id="titulo-loja">{shop.name}</h2>
        <StreamStatusNotice />
        <Visitante shop={shop} />
        <CatalogoPublico shopId={shop.id} />
      </section>
    </StreamProvider>
  );
}

function Visitante({ shop }: { shop: PublicEstablishment }) {
  const { state, refresh } = useSession();
  const [created, setCreated] = useState<VisitorCreated | null>(null);
  const [endError, setEndError] = useState<string | null>(null);

  if (state.status !== "ready") return <p role="status">Carregando…</p>;
  const current = state.session.visitors.find((v) => v.establishment_id === shop.id);
  const account = state.session.account;

  async function end() {
    setEndError(null);
    const out = await call(() => api.POST("/v1/visitor-sessions/current/end"));
    if (!out.ok && out.problem?.status !== 401) {
      setEndError(problemMessage(out.problem, out.offline));
      return;
    }
    setCreated(null);
    await refresh();
  }

  return (
    <>
      {created && <CodigoUmaVez created={created} />}
      {account && !current && (
        <p className="card">
          Você está usando a conta de <strong>{account.name}</strong> nesta loja.
        </p>
      )}
      {(current || account) && <MinhaVisita shopId={shop.id} />}
      {account && <Historico shopId={shop.id} />}
      {current ? (
        <div className="card">
          <p>
            Você está identificado neste aparelho como <strong>{current.nick}</strong>.
          </p>
          <p className="muted">
            Acesso válido até {dateTime.format(new Date(current.expires_at))} (renova a cada uso).
          </p>
          {endError && <Notice kind="erro">{endError}</Notice>}
          <button type="button" className="button button--secondary" onClick={() => void end()}>
            Sair deste aparelho
          </button>
          <p className="muted">
            Sair não cancela sua visita. Para voltar depois, use o código de recuperação.
          </p>
        </div>
      ) : account ? null : (
        <>
          <NovoVisitante
            shopId={shop.id}
            onCreated={async (c) => {
              setCreated(c);
              await refresh();
            }}
          />
          <Recuperar
            shopId={shop.id}
            onRecovered={async (c) => {
              setCreated(c);
              await refresh();
            }}
          />
        </>
      )}
    </>
  );
}

function NovoVisitante({
  shopId,
  onCreated,
}: {
  shopId: string;
  onCreated: (c: VisitorCreated) => Promise<void>;
}) {
  const form = useForm();
  const [nick, setNick] = useState("");
  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    const out = await form.run(() =>
      api.POST("/v1/establishments/{establishmentId}/visitor-sessions", {
        params: { path: { establishmentId: shopId } },
        body: { nick },
      }),
    );
    if (out.ok) await onCreated(out.data.data);
  }
  return (
    <form onSubmit={onSubmit} noValidate aria-labelledby="titulo-nick" className="card">
      <h3 id="titulo-nick">Entrar sem conta</h3>
      {form.generalError}
      <Field
        label="Como quer ser chamado"
        required
        maxLength={30}
        autoComplete="nickname"
        value={nick}
        hint="Apelido de até 30 caracteres. Ele não é senha: outras pessoas podem usar o mesmo apelido."
        error={form.errors.nick}
        onChange={(e) => setNick(e.target.value)}
      />
      <Submit busy={form.busy}>Continuar</Submit>
    </form>
  );
}

function Recuperar({
  shopId,
  onRecovered,
}: {
  shopId: string;
  onRecovered: (c: VisitorCreated) => Promise<void>;
}) {
  const form = useForm();
  const [code, setCode] = useState("");
  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    // O código vai no corpo do pedido, nunca no endereço (R-VIS-06).
    const out = await form.run(() =>
      api.POST("/v1/establishments/{establishmentId}/visitor-sessions/recover", {
        params: { path: { establishmentId: shopId } },
        body: { recovery_code: code },
      }),
    );
    if (out.ok) {
      setCode("");
      await onRecovered(out.data.data);
    }
  }
  return (
    <form onSubmit={onSubmit} noValidate aria-labelledby="titulo-recuperar" className="card">
      <h3 id="titulo-recuperar">Já estava na loja em outro aparelho?</h3>
      {form.generalError}
      <Field
        label="Código de recuperação"
        required
        autoComplete="off"
        spellCheck={false}
        maxLength={32}
        value={code}
        hint="Formato XXXX-XXXX-XXXX-XXXX. Ao usar, você recebe um código novo e o aparelho antigo perde o acesso."
        error={form.errors.recovery_code}
        onChange={(e) => setCode(e.target.value)}
      />
      <Submit busy={form.busy}>Recuperar</Submit>
    </form>
  );
}

/** Mostra o código uma única vez (não fica guardado no navegador nem no servidor em claro). */
function CodigoUmaVez({ created }: { created: VisitorCreated }) {
  const [copied, setCopied] = useState(false);
  async function copy() {
    try {
      await navigator.clipboard.writeText(created.recovery_code);
      setCopied(true);
    } catch {
      setCopied(false);
    }
  }
  return (
    <Notice kind="info">
      <p>
        <strong>Guarde seu código de recuperação.</strong> Ele aparece só agora e serve para recuperar a
        visita em outro aparelho até {dateTime.format(new Date(created.recovery_code_valid_until))}.
      </p>
      <p className="code" aria-label="Código de recuperação">
        {created.recovery_code}
      </p>
      <p>
        <button type="button" className="button button--secondary" onClick={() => void copy()}>
          Copiar código
        </button>{" "}
        <span role="status">{copied ? "Copiado." : ""}</span>
      </p>
      <p className="muted">
        Não compartilhe nem tire foto em local público. Sem o código, a equipe resolve no balcão.
      </p>
    </Notice>
  );
}

type PublicService = components["schemas"]["PublicService"];
type PublicQueue = components["schemas"]["PublicQueue"];

const queueStatus: Record<PublicQueue["status"], string> = {
  open: "● Aberta",
  paused: "Ⅱ Pausada",
  closed: "■ Fechada",
};

/** Serviços e filas públicos da loja: só nome, preço, duração, profissional e situação da fila. */
function CatalogoPublico({ shopId }: { shopId: string }) {
  const [services, setServices] = useState<PublicService[] | null>(null);
  const queues = usePublicQueues(shopId) ?? [];
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    let active = true;
    const path = { establishmentId: shopId };
    // O catálogo de serviços não vem pelo fluxo SSE: é lido ao abrir a tela (eventos-sse.md §4).
    void call(() =>
      api.GET("/v1/public/establishments/{establishmentId}/services", { params: { path } }),
    ).then((s) => {
      if (!active) return;
      if (s.ok) setServices(s.data.data);
      else setError(problemMessage(s.problem, s.offline));
    });
    return () => {
      active = false;
    };
  }, [shopId]);

  if (error) return <Notice kind="erro">{error}</Notice>;
  if (!services) return <p role="status">Carregando serviços…</p>;
  const names = new Map(queues.map((q) => [q.professional.id, q.professional.display_name]));
  return (
    <>
      <h3>Profissionais</h3>
      {queues.length === 0 ? (
        <p>Nenhum profissional disponível no momento.</p>
      ) : (
        <ul className="cards">
          {queues.map((q) => (
            <li key={q.queue_id} className="card">
              <strong>{q.professional.display_name}</strong>
              <span>{queueStatus[q.status]}</span>
              <span className="muted">
                {q.waiting_count} aguardando · espera estimada de {q.estimated_wait.minutes} min (estimativa)
              </span>
            </li>
          ))}
        </ul>
      )}
      <h3>Serviços</h3>
      {services.length === 0 ? (
        <p>Nenhum serviço disponível no momento.</p>
      ) : (
        <ul className="cards">
          {services.map((s) => (
            <li key={s.id} className="card">
              <strong>{s.name}</strong>
              <span>
                {formatCents(s.price.amount_cents)} · cerca de {s.duration_minutes} min
              </span>
              <span className="muted">
                Com:{" "}
                {s.professional_ids
                  .map((id) => names.get(id))
                  .filter(Boolean)
                  .join(", ") || "nenhum profissional no momento"}
              </span>
            </li>
          ))}
        </ul>
      )}
    </>
  );
}
