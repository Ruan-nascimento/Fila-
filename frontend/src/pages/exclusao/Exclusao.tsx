import { useState, type FormEvent } from "react";
import { Link } from "react-router";
import { CircleCheck, Clock, Search, Trash2 } from "lucide-react";
import { api } from "../../api/client";
import { call, problemMessage } from "../../api/problem";
import { Card, Choice, CopyButton, Field, Notice, PageHeader, PasswordField, Submit } from "../../ui/ui";
import { useCommand } from "../gestao/comando";
import { CommandError } from "../gestao/comum";
import { blockerText, metaText, type DeletionRequest } from "./textos";

const dateTime = new Intl.DateTimeFormat("pt-BR", { dateStyle: "short", timeStyle: "short" });

/** Situação real da solicitação: "em processamento" com o que falta, ou "concluída". */
export function SituacaoExclusao({ request }: { request: DeletionRequest }) {
  if (request.status === "completed") {
    return (
      <Notice kind="ok" title="Exclusão concluída">
        <p>
          {request.completed_at ? `Concluída em ${dateTime.format(new Date(request.completed_at))}. ` : ""}
          Seus dados de perfil foram removidos; comandas e pagamentos ficam guardados sem eles, para
          conferência.
        </p>
      </Notice>
    );
  }
  return (
    <div role="status" className="flex gap-3 rounded-xl border border-select-line/60 bg-select p-4">
      <Clock aria-hidden="true" className="mt-0.5 size-6 shrink-0 text-accent" />
      <div className="grid gap-2 text-sm">
        <p className="m-0 text-base font-semibold text-accent">
          Em processamento desde {dateTime.format(new Date(request.requested_at))}
        </p>
        {request.blockers.length === 0 ? (
          <p className="m-0">Não há pendências: o sistema conclui em instantes.</p>
        ) : (
          <>
            <p className="m-0">Ainda falta resolver (a loja cuida disso; você não precisa fazer nada):</p>
            <ul className="m-0 grid gap-1 pl-5">
              {request.blockers.map((b) => (
                <li key={b}>{blockerText[b]}</li>
              ))}
            </ul>
          </>
        )}
        <p className="m-0 text-fg-muted">{metaText}</p>
      </div>
    </div>
  );
}

/**
 * Excluir a própria conta (R-EXC-01): mostra os efeitos antes, pede a senha de novo e confirma.
 * A resposta traz o protocolo completo UMA vez (também vai por e-mail).
 */
export function ExcluirConta({ onRequested }: { onRequested: (r: DeletionRequest) => void }) {
  const [understood, setUnderstood] = useState(false);
  const [password, setPassword] = useState("");
  const [reauthError, setReauthError] = useState<string | null>(null);
  const [tried, setTried] = useState(false);
  const cmd = useCommand();

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setTried(true);
    if (!understood || password === "") return;
    setReauthError(null);
    const re = await call(() => api.POST("/v1/auth/reauthenticate", { body: { password } }));
    if (!re.ok) {
      setReauthError(problemMessage(re.problem, re.offline));
      return;
    }
    setPassword("");
    const out = await cmd.run("excluir-conta", (key) =>
      api.POST("/v1/me/deletion-request", {
        params: { header: { "Idempotency-Key": key } },
        body: { confirm_effects: true },
      }),
    );
    if (out.ok) onRequested(out.data.data);
  }

  return (
    <form onSubmit={onSubmit} noValidate className="grid gap-5" aria-labelledby="titulo-efeitos">
      <Card className="grid gap-3 p-5">
        <h2 id="titulo-efeitos" className="m-0 text-base font-semibold">
          O que acontece ao excluir
        </h2>
        <ul className="m-0 grid gap-2 pl-5 text-sm">
          <li>Você sai de todas as sessões, inclusive em outros aparelhos, na hora.</li>
          <li>Se estiver aguardando ou chamado em alguma fila, a participação é cancelada, sem cobrança.</li>
          <li>Pontos e prêmios, quando existirem, são encerrados, sem conversão em dinheiro.</li>
          <li>
            Atendimento em andamento e pagamento pendente continuam com a equipe da loja; a exclusão só
            termina depois.
          </li>
          <li>Comandas e pagamentos ficam guardados sem seus dados pessoais, para a conferência da loja.</li>
          <li>
            Se você for a única pessoa dona de uma loja, antes é preciso transferir a administração ou
            encerrar a loja (fale com o suporte).
          </li>
        </ul>
        <p className="m-0 text-sm text-fg-muted">{metaText}</p>
      </Card>
      {reauthError && <Notice kind="erro">{reauthError}</Notice>}
      <CommandError problem={cmd.problem} offline={cmd.offline} />
      <Choice
        type="checkbox"
        checked={understood}
        onChange={(e) => setUnderstood(e.target.checked)}
        label="Li e entendi os efeitos da exclusão"
      />
      <PasswordField
        label="Sua senha (confirmação)"
        autoComplete="current-password"
        required
        value={password}
        onChange={(e) => setPassword(e.target.value)}
      />
      {tried && (!understood || password === "") && (
        <p role="alert" className="m-0 text-sm font-semibold text-danger">
          Marque que entendeu os efeitos e digite sua senha para confirmar.
        </p>
      )}
      <div className="flex flex-wrap gap-2">
        <Submit busy={cmd.busy !== null} variant="danger" icon={Trash2}>
          Excluir minha conta
        </Submit>
        <Link to="/conta" className="inline-flex min-h-11 items-center px-3 font-semibold">
          Voltar
        </Link>
      </div>
    </form>
  );
}

/** Mostra o protocolo (uma única vez) e a situação depois do pedido. */
export function ExclusaoSolicitada({ request }: { request: DeletionRequest }) {
  return (
    <section aria-labelledby="titulo-exclusao-pedida" className="grid gap-5">
      <PageHeader id="titulo-exclusao-pedida" title="Pedido de exclusão recebido" />
      <Card className="grid gap-3 p-5">
        <p className="m-0 flex items-center gap-2 font-semibold">
          <CircleCheck aria-hidden="true" className="size-5 text-success" />
          Guarde o protocolo.
        </p>
        <p className="m-0 text-sm text-fg-muted">
          Ele aparece só agora (também foi enviado ao seu e-mail) e serve para consultar a situação.
        </p>
        <p
          id="protocolo"
          className="m-0 rounded-lg bg-surface-2 px-3 py-3 text-center font-mono text-lg font-bold tracking-wider break-all"
        >
          {request.protocol}
        </p>
        <CopyButton text={request.protocol} label="Copiar protocolo" />
      </Card>
      <p className="m-0">Você saiu da conta em todos os aparelhos.</p>
      <SituacaoExclusao request={request} />
      <p className="m-0">
        <Link to="/exclusao">Consultar a situação depois</Link>
      </p>
    </section>
  );
}

/** Página pública: consulta pelo protocolo (vai no corpo do pedido, nunca no endereço). */
export function ConsultarExclusao() {
  const [protocol, setProtocol] = useState("");
  const [result, setResult] = useState<DeletionRequest | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    const out = await call(() => api.POST("/v1/deletion-requests/status", { body: { protocol } }));
    setBusy(false);
    if (out.ok) setResult(out.data.data);
    else {
      setResult(null);
      setError(
        out.problem?.status === 404
          ? "Protocolo não encontrado. Confira e tente de novo."
          : problemMessage(out.problem, out.offline),
      );
    }
  }
  return (
    <section aria-labelledby="titulo-consulta-exclusao" className="grid gap-5">
      <PageHeader
        id="titulo-consulta-exclusao"
        title="Situação da exclusão"
        description="Acompanhe o pedido de exclusão de conta pelo protocolo."
      />
      <form
        onSubmit={onSubmit}
        noValidate
        className="grid gap-3 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-end"
      >
        {error && (
          <div className="sm:col-span-2">
            <Notice kind="erro">
              <p>{error}</p>
            </Notice>
          </div>
        )}
        <Field
          label="Protocolo"
          required
          autoComplete="off"
          spellCheck={false}
          maxLength={64}
          className="font-mono"
          value={protocol}
          hint="Recebido ao pedir a exclusão (também por e-mail)."
          onChange={(e) => setProtocol(e.target.value)}
        />
        <div className="sm:mb-7">
          <Submit busy={busy} icon={Search} block>
            Consultar
          </Submit>
        </div>
      </form>
      {result && <SituacaoExclusao request={result} />}
    </section>
  );
}
