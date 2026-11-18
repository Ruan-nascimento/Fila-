import { useEffect, useId, useState, type FormEvent } from "react";
import { api } from "../../api/client";
import { call, fieldErrors, problemMessage } from "../../api/problem";
import type { components } from "../../api/schema.gen";
import { CopyButton, Field, LoadError, Loading, Notice, PageHeader, Submit } from "../../ui/ui";
import { useCommand } from "./comando";
import { CommandError, type StaffContext } from "./comum";

type Establishment = components["schemas"]["Establishment"];

/** Fusos do Brasil sugeridos (o campo aceita qualquer fuso válido; o servidor confere). */
const brTimezones = [
  "America/Sao_Paulo",
  "America/Bahia",
  "America/Fortaleza",
  "America/Recife",
  "America/Maceio",
  "America/Belem",
  "America/Araguaina",
  "America/Manaus",
  "America/Cuiaba",
  "America/Campo_Grande",
  "America/Porto_Velho",
  "America/Boa_Vista",
  "America/Rio_Branco",
  "America/Noronha",
];

/** Configuração da loja (só o dono): nome, endereço público (slug) e fuso horário. */
export function LojaConfig({ ctx }: { ctx: StaffContext }) {
  const [est, setEst] = useState<Establishment | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [attempt, setAttempt] = useState(0);
  const cmd = useCommand();
  const [form, setForm] = useState({ name: "", slug: "", timezone: "" });
  const [saved, setSaved] = useState(false);
  const listId = useId();

  useEffect(() => {
    let active = true;
    void call(() =>
      api.GET("/v1/establishments/{establishmentId}", { params: { path: { establishmentId: ctx.estId } } }),
    ).then((out) => {
      if (!active) return;
      if (out.ok) {
        setEst(out.data.data);
        setLoadError(null);
        setForm({ name: out.data.data.name, slug: out.data.data.slug, timezone: out.data.data.timezone });
      } else setLoadError(problemMessage(out.problem, out.offline));
    });
    return () => {
      active = false;
    };
  }, [ctx.estId, attempt]);

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    if (!est) return;
    setSaved(false);
    const out = await cmd.run(`loja:${est.version}:${JSON.stringify(form)}`, (key) =>
      api.PATCH("/v1/establishments/{establishmentId}", {
        params: { path: { establishmentId: ctx.estId }, header: { "Idempotency-Key": key } },
        body: { expected_version: est.version, ...form },
      }),
    );
    if (out.ok) {
      setEst(out.data.data);
      setSaved(true);
    } else if (out.problem?.code === "version_conflict" && out.problem.current) {
      const cur = out.problem.current as unknown as Establishment;
      setEst(cur);
      setForm({ name: cur.name, slug: cur.slug, timezone: cur.timezone });
    }
  }

  const header = (
    <PageHeader title="Dados da loja" description="Nome, link público e fuso horário da barbearia." />
  );
  if (loadError && !est)
    return (
      <>
        {header}
        <LoadError message={loadError} onRetry={() => setAttempt((n) => n + 1)} />
      </>
    );
  if (!est)
    return (
      <>
        {header}
        <Loading />
      </>
    );
  const errors = fieldErrors(cmd.problem);
  // Prévia a partir da origem e da rota reais da aplicação.
  const origin = globalThis.location?.origin ?? "";
  const link = `${origin}/loja/${est.slug}`;
  const prefix = `${origin.replace(/^https?:\/\//, "")}/loja/`;
  return (
    <div className="grid gap-5">
      {header}
      <form onSubmit={onSubmit} noValidate className="grid max-w-xl gap-4" aria-label="Dados da loja">
        {!cmd.problem?.errors?.length && <CommandError problem={cmd.problem} offline={cmd.offline} />}
        {saved && (
          <Notice kind="ok">
            <p>Dados salvos.</p>
          </Notice>
        )}
        <Field
          label="Nome da loja"
          required
          maxLength={80}
          value={form.name}
          error={errors.name}
          onChange={(e) => setForm({ ...form, name: e.target.value })}
        />
        <Field
          label="Endereço público"
          prefix={prefix}
          required
          autoCapitalize="none"
          spellCheck={false}
          value={form.slug}
          error={errors.slug}
          hint="Letras minúsculas, números e hífen. É o link que os clientes usam para entrar na fila."
          onChange={(e) => setForm({ ...form, slug: e.target.value.toLowerCase() })}
        />
        <Field
          label="Fuso horário"
          required
          maxLength={64}
          list={listId}
          value={form.timezone}
          error={errors.timezone}
          hint="Ex.: America/Sao_Paulo, America/Manaus."
          onChange={(e) => setForm({ ...form, timezone: e.target.value })}
        />
        <datalist id={listId}>
          {brTimezones.map((tz) => (
            <option key={tz} value={tz} />
          ))}
        </datalist>
        <div>
          <Submit busy={cmd.busy !== null}>Salvar</Submit>
        </div>
      </form>
      <section aria-labelledby="titulo-link-loja" className="grid max-w-xl gap-2 border-t border-line pt-5">
        <h2 id="titulo-link-loja" className="m-0 text-base font-semibold">
          Link da loja
        </h2>
        <p className="m-0 text-sm text-fg-muted">
          Use em QR code, redes sociais e no balcão. Mostra o endereço salvo.
        </p>
        <p className="m-0 rounded-lg bg-surface-2 px-3 py-2 font-mono text-sm break-all">
          <a href={link}>{link}</a>
        </p>
        <CopyButton text={link} label="Copiar link" />
      </section>
    </div>
  );
}
