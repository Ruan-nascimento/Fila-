import { useState, type FormEvent } from "react";
import { useNavigate } from "react-router";
import { UserRoundPlus } from "lucide-react";
import { api } from "../../api/client";
import { call } from "../../api/problem";
import { Button, ButtonLink, Field, Submit } from "../../ui/ui";
import { slugFromInput } from "./acessoLoja";

/**
 * "Entrar sem conta" (visitante) na jornada de cliente. Com a loja conhecida, leva à identificação
 * por apelido DAQUELA loja (sem apagar sessão nenhuma: a página da loja decide o que fazer com
 * uma conta já conectada). Sem loja conhecida, pede o link/código antes — nunca adivinha a loja
 * nem cria visitante sem estabelecimento. Não depende do Google.
 */
export function EntrarSemConta({ slug }: { slug: string | null }) {
  const navigate = useNavigate();
  const [open, setOpen] = useState(false);
  const [value, setValue] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  if (slug)
    return (
      <ButtonLink to={`/loja/${slug}?entrada=visitante`} variant="secondary" icon={UserRoundPlus} block>
        Entrar sem conta
      </ButtonLink>
    );

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    const s = slugFromInput(value);
    if (!s) {
      setError("Cole o link da barbearia (ou o código que aparece depois de /loja/).");
      return;
    }
    setBusy(true);
    const out = await call(() => api.GET("/v1/public/slugs/{slug}", { params: { path: { slug: s } } }));
    setBusy(false);
    if (out.ok) navigate(`/loja/${s}?entrada=visitante`);
    else
      setError(
        out.problem?.status === 404
          ? "Não encontramos essa barbearia. Confira o link ou o QR Code."
          : "Não foi possível conferir agora. Tente de novo.",
      );
  }

  if (!open)
    return (
      <Button variant="secondary" icon={UserRoundPlus} block onClick={() => setOpen(true)}>
        Entrar sem conta
      </Button>
    );
  return (
    <form
      onSubmit={onSubmit}
      noValidate
      aria-label="Entrar sem conta"
      className="grid gap-3 rounded-xl border border-line bg-surface p-4"
    >
      <p className="m-0 text-sm">
        Para entrar sem conta, informe de qual barbearia você quer participar da fila.
      </p>
      <Field
        label="Link ou código da barbearia"
        autoCapitalize="none"
        spellCheck={false}
        hint="Está no QR Code ou no link que a barbearia divulga. Ex.: barbearia-do-ze"
        value={value}
        error={error ?? undefined}
        onChange={(e) => setValue(e.target.value)}
      />
      <div className="flex flex-wrap gap-2">
        <Submit busy={busy}>Continuar</Submit>
        <Button variant="secondary" onClick={() => setOpen(false)}>
          Cancelar
        </Button>
      </div>
    </form>
  );
}
