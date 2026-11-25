import { useEffect, useState, type FormEvent } from "react";
import { MailCheck } from "lucide-react";
import { api } from "../../api/client";
import { call, problemMessage } from "../../api/problem";
import type { components } from "../../api/schema.gen";
import { Button, Field, Notice } from "../../ui/ui";

type Contact = components["schemas"]["VisitorContact"];

/**
 * E-mail do visitante nesta loja (ADR-0039): recebe os avisos da reserva e permite recuperar o
 * acesso em outro aparelho. Só vale depois do código de 6 dígitos enviado ao endereço.
 */
export function ContatoVisitante({ shopId }: { shopId: string }) {
  const [contact, setContact] = useState<Contact | null | undefined>(undefined);
  const [editing, setEditing] = useState(false);
  const [email, setEmail] = useState("");
  const [code, setCode] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const path = { establishmentId: shopId };
  useEffect(() => {
    let active = true;
    void call(() =>
      api.GET("/v1/establishments/{establishmentId}/visitor-contact", { params: { path } }),
    ).then((out) => {
      if (active && out.ok) setContact(out.data.data);
    });
    return () => {
      active = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [shopId]);
  if (contact === undefined) return null;

  async function enviar(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    const out = await call(() =>
      api.POST("/v1/establishments/{establishmentId}/visitor-contact", { params: { path }, body: { email } }),
    );
    setBusy(false);
    if (out.ok) {
      setContact(out.data.data);
      setEditing(false);
      setError(null);
    } else setError(problemMessage(out.problem, out.offline));
  }
  async function confirmar(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    const out = await call(() =>
      api.POST("/v1/establishments/{establishmentId}/visitor-contact/verify", {
        params: { path },
        body: { code },
      }),
    );
    setBusy(false);
    if (out.ok) {
      setContact(out.data.data);
      setCode("");
      setError(null);
    } else setError(problemMessage(out.problem, out.offline));
  }

  if (contact?.verified && !editing)
    return (
      <p className="m-0 flex flex-wrap items-center gap-2 text-sm">
        <MailCheck aria-hidden="true" className="size-4 text-success" /> Avisos da reserva em{" "}
        <strong>{contact.email_masked}</strong>.
        <Button variant="secondary" onClick={() => setEditing(true)}>
          Trocar e-mail
        </Button>
      </p>
    );

  return (
    <section
      aria-label="E-mail para avisos"
      className="grid gap-3 rounded-xl border border-line bg-surface p-4"
    >
      {contact && !contact.verified && !editing ? (
        <form className="grid gap-2" onSubmit={(e) => void confirmar(e)}>
          <p className="m-0 text-sm">
            Enviamos um código para <strong>{contact.email_masked}</strong>. Digite-o para confirmar.
          </p>
          <Field
            label="Código de 6 dígitos"
            inputMode="numeric"
            autoComplete="one-time-code"
            maxLength={6}
            value={code}
            onChange={(e) => setCode(e.target.value.replace(/\D/g, ""))}
          />
          <div className="flex flex-wrap gap-2">
            <Button type="submit" disabled={busy || code.length !== 6}>
              Confirmar e-mail
            </Button>
            <Button variant="secondary" onClick={() => setEditing(true)}>
              Usar outro e-mail
            </Button>
          </div>
        </form>
      ) : (
        <form className="grid gap-2" onSubmit={(e) => void enviar(e)}>
          <p className="m-0 text-sm">
            Sem conta: informe um e-mail para receber a confirmação, o lembrete e os avisos da reserva, e para
            recuperar o acesso em outro aparelho.
          </p>
          <Field
            label="E-mail"
            type="email"
            autoComplete="email"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
          />
          <div>
            <Button type="submit" disabled={busy || !email.includes("@")}>
              Enviar código
            </Button>
          </div>
        </form>
      )}
      {error && <Notice kind="erro">{error}</Notice>}
    </section>
  );
}
