import { useState, type FormEvent } from "react";
import { Link } from "react-router";
import { api } from "../../api/client";
import { call, problemMessage } from "../../api/problem";
import { Field, Notice, Submit } from "../../ui/ui";
import { useCommand } from "../gestao/comando";
import { CommandError } from "../gestao/comum";
import { blockerText, metaText, type DeletionRequest } from "./textos";

const dateTime = new Intl.DateTimeFormat("pt-BR", { dateStyle: "short", timeStyle: "short" });

/** Situação real da solicitação: "em processamento" com o que falta, ou "concluída". */
export function SituacaoExclusao({ request }: { request: DeletionRequest }) {
  if (request.status === "completed") {
    return (
      <Notice kind="ok">
        Exclusão concluída
        {request.completed_at ? ` em ${dateTime.format(new Date(request.completed_at))}` : ""}. Seus dados de
        perfil foram removidos; comandas e pagamentos ficam guardados sem eles, para conferência.
      </Notice>
    );
  }
  return (
    <div role="status">
      <p>
        <strong>Em processamento</strong> desde {dateTime.format(new Date(request.requested_at))}.
      </p>
      {request.blockers.length === 0 ? (
        <p>Não há pendências: o sistema conclui em instantes.</p>
      ) : (
        <>
          <p>Ainda falta resolver (a loja cuida disso; você não precisa fazer nada):</p>
          <ul>
            {request.blockers.map((b) => (
              <li key={b}>{blockerText[b]}</li>
            ))}
          </ul>
        </>
      )}
      <p className="muted">{metaText}</p>
    </div>
  );
}

/**
 * Excluir a própria conta (R-EXC-01): mostra os efeitos antes, pede a senha de novo e confirma.
 * A resposta traz o protocolo completo UMA vez (também vai por e-mail).
 */
export function ExcluirConta({ onRequested }: { onRequested: (r: DeletionRequest) => void }) {
  const [open, setOpen] = useState(false);
  const [understood, setUnderstood] = useState(false);
  const [password, setPassword] = useState("");
  const [reauthError, setReauthError] = useState<string | null>(null);
  const cmd = useCommand();

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
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

  if (!open) {
    return (
      <section aria-labelledby="titulo-excluir">
        <h3 id="titulo-excluir">Excluir conta</h3>
        <button type="button" className="button button--secondary" onClick={() => setOpen(true)}>
          Quero excluir minha conta…
        </button>
      </section>
    );
  }
  return (
    <form onSubmit={onSubmit} noValidate className="card" aria-labelledby="titulo-excluir">
      <h3 id="titulo-excluir">Excluir conta</h3>
      <p>Antes de confirmar, veja o que acontece:</p>
      <ul>
        <li>Você sai de todas as sessões, inclusive em outros aparelhos, na hora.</li>
        <li>Se estiver aguardando ou chamado em alguma fila, a participação é cancelada, sem cobrança.</li>
        <li>Pontos e prêmios, quando existirem, são encerrados, sem conversão em dinheiro.</li>
        <li>
          Atendimento em andamento e pagamento pendente continuam com a equipe da loja; a exclusão só termina
          depois.
        </li>
        <li>Comandas e pagamentos ficam guardados sem seus dados pessoais, para a conferência da loja.</li>
        <li>
          Se você for a única pessoa dona de uma loja, antes é preciso transferir a administração ou encerrar
          a loja (fale com o suporte).
        </li>
      </ul>
      <p className="muted">{metaText}</p>
      {reauthError && <Notice kind="erro">{reauthError}</Notice>}
      <CommandError problem={cmd.problem} offline={cmd.offline} />
      <label className="choice">
        <input type="checkbox" checked={understood} onChange={(e) => setUnderstood(e.target.checked)} />
        Li e entendi os efeitos da exclusão
      </label>
      <Field
        label="Sua senha (confirmação)"
        type="password"
        autoComplete="current-password"
        required
        value={password}
        onChange={(e) => setPassword(e.target.value)}
      />
      <div className="actions">
        <Submit busy={cmd.busy !== null}>Excluir minha conta</Submit>
        <button type="button" className="button button--secondary" onClick={() => setOpen(false)}>
          Voltar
        </button>
      </div>
      {(!understood || password === "") && (
        <p className="muted">Marque que entendeu os efeitos e digite sua senha para confirmar.</p>
      )}
    </form>
  );
}

/** Mostra o protocolo (uma única vez) e a situação depois do pedido. */
export function ExclusaoSolicitada({ request }: { request: DeletionRequest }) {
  const [copied, setCopied] = useState(false);
  return (
    <section aria-labelledby="titulo-exclusao-pedida">
      <h2 id="titulo-exclusao-pedida">Pedido de exclusão recebido</h2>
      <Notice kind="info">
        <p>
          <strong>Guarde o protocolo.</strong> Ele aparece só agora (também foi enviado ao seu e-mail) e serve
          para consultar a situação.
        </p>
        <p className="code" aria-label="Protocolo">
          {request.protocol}
        </p>
        <p>
          <button
            type="button"
            className="button button--secondary"
            onClick={() =>
              void navigator.clipboard
                .writeText(request.protocol)
                .then(() => setCopied(true))
                .catch(() => setCopied(false))
            }
          >
            Copiar protocolo
          </button>{" "}
          <span role="status">{copied ? "Copiado." : ""}</span>
        </p>
      </Notice>
      <p>Você saiu da conta em todos os aparelhos.</p>
      <SituacaoExclusao request={request} />
      <p>
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
    <section aria-labelledby="titulo-consulta-exclusao">
      <h2 id="titulo-consulta-exclusao">Situação da exclusão de conta</h2>
      <form onSubmit={onSubmit} noValidate className="card">
        {error && <Notice kind="erro">{error}</Notice>}
        <Field
          label="Protocolo"
          required
          autoComplete="off"
          spellCheck={false}
          maxLength={64}
          value={protocol}
          hint="Recebido ao pedir a exclusão (também por e-mail)."
          onChange={(e) => setProtocol(e.target.value)}
        />
        <Submit busy={busy}>Consultar</Submit>
      </form>
      {result && <SituacaoExclusao request={result} />}
    </section>
  );
}
