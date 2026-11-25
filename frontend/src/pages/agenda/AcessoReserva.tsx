import { useEffect, useRef, useState, type FormEvent } from "react";
import { Navigate } from "react-router";
import { api } from "../../api/client";
import { call, problemMessage } from "../../api/problem";
import { useSession } from "../../session/context";
import { takeFragmentToken } from "../../ui/fragment";
import { Button, Field, Loading, Notice, Section } from "../../ui/ui";

/**
 * Recuperar a reserva pelo e-mail (ADR-0039). Sem token: pede o link (a resposta é sempre a mesma,
 * para não revelar se o e-mail existe). Com o link (#token=…): troca por um POST — abrir o link não
 * muda nada — e leva a "Minhas reservas".
 */
export function AcessoReserva({ shopId, slug }: { shopId: string; slug: string }) {
  const [token] = useState(() => takeFragmentToken());
  const { refresh } = useSession();
  const [state, setState] = useState<"idle" | "busy" | "ok" | "sent">(token ? "busy" : "idle");
  const [error, setError] = useState<string | null>(null);
  const [email, setEmail] = useState("");
  const started = useRef(false);
  useEffect(() => {
    if (!token || started.current) return;
    started.current = true;
    void call(() =>
      api.POST("/v1/public/establishments/{establishmentId}/appointment-access/exchange", {
        params: { path: { establishmentId: shopId } },
        body: { token },
      }),
    ).then(async (out) => {
      if (out.ok) {
        await refresh();
        setState("ok");
      } else {
        setError(problemMessage(out.problem, out.offline));
        setState("idle");
      }
    });
  }, [token, shopId, refresh]);
  if (state === "ok") return <Navigate to={`/loja/${slug}/reservas`} replace />;
  if (state === "busy" && token) return <Loading>Abrindo sua reserva…</Loading>;

  async function pedir(e: FormEvent) {
    e.preventDefault();
    setState("busy");
    const out = await call(() =>
      api.POST("/v1/public/establishments/{establishmentId}/appointment-access", {
        params: { path: { establishmentId: shopId } },
        body: { email },
      }),
    );
    if (out.ok) {
      setState("sent");
      setError(null);
    } else {
      setState("idle");
      setError(problemMessage(out.problem, out.offline));
    }
  }
  return (
    <Section
      title="Recuperar minha reserva"
      description="Para quem reservou sem conta e confirmou um e-mail nesta barbearia."
    >
      {error && <Notice kind="erro">{error}</Notice>}
      {state === "sent" ? (
        <Notice kind="ok" title="Pedido recebido">
          <p>
            Se este e-mail estiver confirmado nesta barbearia, o link chega em alguns minutos (vale 30
            minutos, uma vez).
          </p>
        </Notice>
      ) : (
        <form className="grid max-w-md gap-2" onSubmit={(e) => void pedir(e)}>
          <Field
            label="E-mail"
            type="email"
            autoComplete="email"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
          />
          <div>
            <Button type="submit" disabled={state === "busy" || !email.includes("@")}>
              Receber link por e-mail
            </Button>
          </div>
        </form>
      )}
    </Section>
  );
}
