import { useEffect, useState, type FormEvent, type Ref } from "react";
import { Link } from "react-router";
import { KeyRound, LogOut, ShieldCheck } from "lucide-react";
import { api } from "../../api/client";
import { call, problemMessage } from "../../api/problem";
import type { components } from "../../api/schema.gen";
import { Button, ButtonLink, Card, ConfirmPanel, CopyButton, Field, Notice, Submit } from "../../ui/ui";
import { useInlinePanel } from "../../ui/useInlinePanel";
import { useForm } from "../../ui/useForm";

type VisitorCreated = components["schemas"]["VisitorSessionCreated"];
type VisitorView = components["schemas"]["VisitorView"];

const dateTime = new Intl.DateTimeFormat("pt-BR", { dateStyle: "short", timeStyle: "short" });

/**
 * Identificação sem conta: o apelido cria a sessão de visitante (primeira operação); entrar na
 * fila é outra (feita depois, com a escolha mantida). "Já tenho um código" recupera a visita feita
 * em outro aparelho.
 */
export function Identificacao({
  shopId,
  shopName,
  returnTo,
  needed,
  nickRef,
  onIdentified,
}: {
  shopId: string;
  shopName: string;
  returnTo: string;
  needed: boolean;
  nickRef: Ref<HTMLInputElement>;
  onIdentified: (c: VisitorCreated) => Promise<void>;
}) {
  const [recovering, setRecovering] = useState(false);
  return (
    <Card className="grid gap-5 p-5">
      {recovering ? (
        <Recuperar shopId={shopId} onRecovered={onIdentified} onBack={() => setRecovering(false)} />
      ) : (
        <NovoVisitante
          shopId={shopId}
          shopName={shopName}
          needed={needed}
          nickRef={nickRef}
          onCreated={onIdentified}
          onRecover={() => setRecovering(true)}
        />
      )}
      <div className="grid gap-2 border-t border-line pt-4">
        <p className="m-0 text-sm text-fg-muted">Tem conta? Histórico e pontos ficam guardados nela.</p>
        <ButtonLink to={`/entrar?voltar=${encodeURIComponent(returnTo)}`} variant="secondary">
          Entrar com minha conta
        </ButtonLink>
      </div>
    </Card>
  );
}

function NovoVisitante({
  shopId,
  shopName,
  needed,
  nickRef,
  onCreated,
  onRecover,
}: {
  shopId: string;
  shopName: string;
  needed: boolean;
  nickRef: Ref<HTMLInputElement>;
  onCreated: (c: VisitorCreated) => Promise<void>;
  onRecover: () => void;
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
    <form onSubmit={onSubmit} noValidate aria-labelledby="titulo-nick" className="grid gap-4">
      <div className="grid gap-1">
        <h2 id="titulo-nick" className="m-0 text-lg font-semibold">
          Como quer ser chamado?
        </h2>
        <p className="m-0 text-sm text-fg-muted">
          {needed
            ? "Para entrar na fila, informe um apelido. Sua escolha de profissional e serviços fica guardada."
            : `Informe um apelido para entrar na fila de ${shopName}.`}
        </p>
      </div>
      {form.generalError}
      <Field
        ref={nickRef}
        label="Apelido"
        required
        maxLength={30}
        autoComplete="nickname"
        enterKeyHint="go"
        placeholder="Ex.: Pedro"
        value={nick}
        hint="Até 30 caracteres. Não é senha: outras pessoas podem usar o mesmo apelido."
        error={form.errors.nick}
        onChange={(e) => setNick(e.target.value)}
      />
      <Submit busy={form.busy} block>
        Continuar
      </Submit>
      <Button variant="ghost" icon={KeyRound} onClick={onRecover}>
        Já tenho um código
      </Button>
    </form>
  );
}

function Recuperar({
  shopId,
  onRecovered,
  onBack,
}: {
  shopId: string;
  onRecovered: (c: VisitorCreated) => Promise<void>;
  onBack: () => void;
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
    <form onSubmit={onSubmit} noValidate aria-labelledby="titulo-recuperar" className="grid gap-4">
      <div className="grid gap-1">
        <h2 id="titulo-recuperar" className="m-0 text-lg font-semibold">
          Já tenho um código
        </h2>
        <p className="m-0 text-sm text-fg-muted">
          Use o código de recuperação recebido quando entrou na loja em outro aparelho.
        </p>
      </div>
      {form.generalError}
      <Field
        label="Código de recuperação"
        required
        autoComplete="off"
        autoCapitalize="characters"
        spellCheck={false}
        maxLength={32}
        autoFocus
        className="font-mono tracking-wider uppercase"
        placeholder="XXXX-XXXX-XXXX-XXXX"
        value={code}
        hint="Ao usar, você recebe um código novo e o aparelho antigo perde o acesso."
        error={form.errors.recovery_code}
        onChange={(e) => setCode(e.target.value)}
      />
      <Submit busy={form.busy} block>
        Recuperar
      </Submit>
      <Button variant="ghost" onClick={onBack}>
        Voltar para o apelido
      </Button>
    </form>
  );
}

/** Mostra o código uma única vez (não fica guardado no navegador nem no servidor em claro). */
export function CodigoUmaVez({
  created,
  selectionKept,
  onDismiss,
}: {
  created: VisitorCreated;
  selectionKept: boolean;
  onDismiss: () => void;
}) {
  // O formulário do apelido some ao identificar: o foco vai para o código, que é o próximo passo.
  useEffect(() => {
    document.getElementById("titulo-codigo")?.focus();
  }, []);
  return (
    <Card className="grid gap-3 border-primary/50 p-5" role="region" aria-labelledby="titulo-codigo">
      <div className="flex items-start gap-3">
        <ShieldCheck aria-hidden="true" className="mt-0.5 size-6 shrink-0 text-accent" />
        <div className="grid gap-1">
          <h2 id="titulo-codigo" tabIndex={-1} className="m-0 text-base font-semibold">
            Guarde seu código de recuperação
          </h2>
          <p className="m-0 text-sm text-fg-muted">
            Ele aparece só agora e serve para retomar o acesso em outro aparelho até{" "}
            {dateTime.format(new Date(created.recovery_code_valid_until))}. Não é a senha de atendimento.
          </p>
        </div>
      </div>
      <p className="m-0 rounded-lg bg-surface-2 px-3 py-3 text-center font-mono text-xl font-bold tracking-widest break-all sm:text-2xl">
        {created.recovery_code}
      </p>
      <CopyButton text={created.recovery_code} label="Copiar código" />
      <p className="m-0 text-sm text-fg-muted">
        Não compartilhe nem fotografe em local público. Sem o código, a equipe resolve no balcão.
      </p>
      {selectionKept && (
        <Notice kind="info" live={false}>
          <p>Sua escolha foi mantida. Confira e toque em “Entrar na fila”.</p>
        </Notice>
      )}
      <Button variant="ghost" className="justify-self-start" onClick={onDismiss}>
        Já guardei o código
      </Button>
    </Card>
  );
}

/** Visitante identificado neste aparelho, com a saída explicada. */
export function VisitanteAtual({ visitor, onEnded }: { visitor: VisitorView; onEnded: () => Promise<void> }) {
  const panel = useInlinePanel();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  async function end() {
    setError(null);
    setBusy(true);
    const out = await call(() => api.POST("/v1/visitor-sessions/current/end"));
    setBusy(false);
    if (!out.ok && out.problem?.status !== 401) {
      setError(problemMessage(out.problem, out.offline));
      return;
    }
    await onEnded();
  }
  return (
    <Card className="grid gap-3 p-5">
      <p className="m-0">
        Neste aparelho você está como <strong>{visitor.nick}</strong>.
      </p>
      <p className="m-0 text-sm text-fg-muted">
        Acesso válido até {dateTime.format(new Date(visitor.expires_at))} (renova a cada uso).
      </p>
      {error && <Notice kind="erro">{error}</Notice>}
      {panel.open ? (
        <ConfirmPanel
          panelId={panel.panelId}
          onKeyDown={panel.onKeyDown}
          title="Sair deste aparelho?"
          confirmLabel="Sair deste aparelho"
          cancelLabel="Ficar"
          busy={busy}
          onConfirm={() => void end()}
          onCancel={panel.hide}
        >
          <p>
            Sua visita continua na fila. Este aparelho deixa de mostrá-la; para voltar aqui ou em outro
            aparelho, use o código de recuperação.
          </p>
        </ConfirmPanel>
      ) : (
        <Button id={panel.triggerId} variant="secondary" icon={LogOut} onClick={() => panel.show("aberto")}>
          Sair deste aparelho
        </Button>
      )}
    </Card>
  );
}

export function ContaNaLoja({ name, base }: { name: string; base: string }) {
  return (
    <Card className="grid gap-2 p-5">
      <p className="m-0">
        Usando a conta de <strong>{name}</strong>.
      </p>
      <p className="m-0 text-sm text-fg-muted">
        Veja o <Link to={`${base}/historico`}>histórico</Link> e os <Link to={`${base}/pontos`}>pontos</Link>{" "}
        nesta loja.
      </p>
    </Card>
  );
}
