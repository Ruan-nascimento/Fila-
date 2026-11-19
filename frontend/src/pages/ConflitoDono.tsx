import { useEffect, useRef, useState } from "react";
import { MonitorSmartphone, X } from "lucide-react";
import { api } from "../api/client";
import { call, problemMessage } from "../api/problem";
import type { OwnerSessionConflict } from "./conflito";
import { useSession } from "../session/context";
import { Button, Notice } from "../ui/ui";

function quando(iso: string): string {
  const d = new Date(iso);
  return d.toLocaleString("pt-BR", { day: "2-digit", month: "2-digit", hour: "2-digit", minute: "2-digit" });
}

/**
 * Sessão única do dono (ADR-0027): a outra sessão continua até a pessoa confirmar aqui. Só mostra
 * o contexto que o servidor conhece com segurança (programa autorizado ou "outro aparelho ou
 * navegador" e o último uso aproximado). O desafio fica só em memória, nunca na URL.
 */
export function ConflitoDono({
  conflito,
  onDone,
  onCancel,
}: {
  conflito: OwnerSessionConflict;
  /** Acesso transferido para este aparelho. */
  onDone: () => void;
  /** Cancelado (ou a confirmação não vale mais): volta ao formulário. */
  onCancel: (aviso?: string) => void;
}) {
  const { refresh } = useSession();
  const [busy, setBusy] = useState(false);
  const [erro, setErro] = useState<string | null>(null);
  const [incerto, setIncerto] = useState(false);
  const [vencido, setVencido] = useState(() => Date.parse(conflito.expires_at) <= Date.now());
  const titulo = useRef<HTMLHeadingElement>(null);

  useEffect(() => titulo.current?.focus(), []);
  useEffect(() => {
    const ms = Date.parse(conflito.expires_at) - Date.now();
    if (ms <= 0) return;
    const t = window.setTimeout(() => setVencido(true), ms);
    return () => window.clearTimeout(t);
  }, [conflito.expires_at]);

  const outro =
    conflito.other_session.channel === "desktop_authorized"
      ? `no programa Fila Gestão${conflito.other_session.installation_name ? ` (“${conflito.other_session.installation_name}”)` : ""}`
      : "em outro aparelho ou navegador";

  async function continuar() {
    setBusy(true);
    setErro(null);
    const out = await call(() =>
      api.POST("/v1/auth/owner-session/takeover", { body: { challenge: conflito.challenge } }),
    );
    if (out.ok) {
      await refresh();
      setBusy(false);
      onDone();
      return;
    }
    if (out.offline) {
      // A resposta se perdeu: antes de repetir, confere se a troca já aconteceu. Repetir com o
      // MESMO desafio nunca revoga de novo: o servidor só reentrega o acesso a esta tentativa.
      setIncerto(true);
      setBusy(false);
      return;
    }
    setBusy(false);
    if (out.problem?.code === "owner_session_challenge_invalid") {
      onCancel(problemMessage(out.problem));
      return;
    }
    setErro(problemMessage(out.problem));
  }

  async function cancelar() {
    setBusy(true);
    await call(() => api.POST("/v1/auth/owner-session/cancel", { body: { challenge: conflito.challenge } }));
    setBusy(false);
    onCancel();
  }

  return (
    <section
      role="alertdialog"
      aria-modal="false"
      aria-labelledby="titulo-conflito"
      aria-describedby="texto-conflito"
      className="grid gap-4 rounded-xl border border-line bg-surface p-4"
    >
      <h2
        id="titulo-conflito"
        ref={titulo}
        tabIndex={-1}
        className="m-0 flex items-center gap-2 text-lg font-semibold"
      >
        <MonitorSmartphone aria-hidden="true" className="size-5 text-accent" />
        Acesso de dono aberto em outro lugar
      </h2>
      <div id="texto-conflito" className="grid gap-1 text-fg">
        <p className="m-0">Esta conta já está com o acesso de dono aberto {outro}.</p>
        <p className="m-0 text-sm text-fg-muted">
          Último uso por volta de {quando(conflito.other_session.last_used_at)}. Só uma sessão de dono pode
          ficar ativa por vez. Se você continuar aqui, a outra é encerrada na hora (mesmo que aquele aparelho
          esteja desligado ou perdido).
        </p>
      </div>
      {erro && <Notice kind="erro">{erro}</Notice>}
      {vencido && !incerto && (
        <Notice kind="aviso">A confirmação venceu. Cancele e entre de novo para ver a situação atual.</Notice>
      )}
      {incerto && (
        <Notice kind="aviso" title="Não sabemos se a troca foi concluída.">
          <p>A conexão caiu antes da resposta. Verifique de novo: nada será encerrado duas vezes.</p>
        </Notice>
      )}
      <div className="flex flex-wrap gap-3">
        <Button disabled={busy || (vencido && !incerto)} onClick={() => void continuar()}>
          {busy
            ? "Confirmando…"
            : incerto
              ? "Verificar de novo"
              : "Continuar neste aparelho e encerrar a outra sessão"}
        </Button>
        <Button variant="secondary" icon={X} disabled={busy} onClick={() => void cancelar()}>
          Cancelar e manter a outra sessão
        </Button>
      </div>
    </section>
  );
}
