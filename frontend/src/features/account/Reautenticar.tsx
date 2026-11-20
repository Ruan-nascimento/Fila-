import { useState, type FormEvent } from "react";
import { api } from "../../api/client";
import { call, problemMessage } from "../../api/problem";
import { Button, Field, Notice, PasswordField, Submit } from "../../ui/ui";

/**
 * Confirma a identidade antes de uma ação sensível (vale 10 minutos no servidor). Conta com senha
 * digita a senha; conta só com Google recebe um código de 8 dígitos no e-mail — escolher uma conta
 * no Google não é tratado como prova de presença recente.
 */
export function Reautenticar({
  hasPassword,
  onDone,
  onCancel,
}: {
  hasPassword: boolean;
  onDone: () => void;
  onCancel?: () => void;
}) {
  const [value, setValue] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [sent, setSent] = useState<string | null>(null);

  async function sendCode() {
    setError(null);
    const out = await call(() => api.POST("/v1/auth/reauthenticate/email-code"));
    if (out.ok) setSent("Código enviado. Confira seu e-mail (vale 10 minutos).");
    else setError(problemMessage(out.problem, out.offline));
  }

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    e.stopPropagation();
    if (value.trim() === "" || busy) return;
    setBusy(true);
    setError(null);
    const out = await call(() =>
      api.POST("/v1/auth/reauthenticate", {
        body: hasPassword ? { password: value } : { email_code: value.trim() },
      }),
    );
    setBusy(false);
    if (out.ok) {
      setValue("");
      onDone();
    } else setError(problemMessage(out.problem, out.offline));
  }

  return (
    <form
      onSubmit={onSubmit}
      noValidate
      aria-labelledby="titulo-reautenticar"
      className="grid gap-3 rounded-xl border border-select-line bg-select p-4"
    >
      <h3 id="titulo-reautenticar" className="m-0 text-base font-semibold">
        Confirme que é você
      </h3>
      <p className="m-0 text-sm text-fg-muted">
        {hasPassword
          ? "Por segurança, digite sua senha de novo (vale por 10 minutos)."
          : "Sua conta entra com o Google. Enviamos um código de 8 dígitos para o seu e-mail."}
      </p>
      {error && (
        <Notice kind="erro">
          <p>{error}</p>
        </Notice>
      )}
      {hasPassword ? (
        <PasswordField
          label="Sua senha"
          autoComplete="current-password"
          required
          autoFocus
          value={value}
          onChange={(e) => setValue(e.target.value)}
        />
      ) : (
        <>
          <div>
            <Button variant="secondary" onClick={() => void sendCode()}>
              Enviar código por e-mail
            </Button>
          </div>
          {sent && (
            <p role="status" className="m-0 text-sm">
              {sent}
            </p>
          )}
          <Field
            label="Código do e-mail"
            inputMode="numeric"
            autoComplete="one-time-code"
            maxLength={8}
            required
            value={value}
            onChange={(e) => setValue(e.target.value.replace(/\D/g, ""))}
          />
        </>
      )}
      <div className="flex flex-wrap gap-2">
        <Submit busy={busy}>Confirmar</Submit>
        {onCancel && (
          <Button variant="secondary" onClick={onCancel}>
            Cancelar
          </Button>
        )}
      </div>
    </form>
  );
}
