import { useEffect, useState, type FormEvent } from "react";
import { api } from "../../api/client";
import { call, fieldErrors, problemMessage } from "../../api/problem";
import type { components } from "../../api/schema.gen";
import { Field, Notice, Submit } from "../../ui/ui";
import { useCommand } from "./comando";
import { CommandError, type StaffContext } from "./comum";

type Establishment = components["schemas"]["Establishment"];

/** Configuração da loja (só o dono): nome, endereço público (slug) e fuso horário. */
export function LojaConfig({ ctx }: { ctx: StaffContext }) {
  const [est, setEst] = useState<Establishment | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const cmd = useCommand();
  const [form, setForm] = useState({ name: "", slug: "", timezone: "" });
  const [saved, setSaved] = useState(false);

  useEffect(() => {
    let active = true;
    void call(() =>
      api.GET("/v1/establishments/{establishmentId}", { params: { path: { establishmentId: ctx.estId } } }),
    ).then((out) => {
      if (!active) return;
      if (out.ok) {
        setEst(out.data.data);
        setForm({ name: out.data.data.name, slug: out.data.data.slug, timezone: out.data.data.timezone });
      } else setLoadError(problemMessage(out.problem, out.offline));
    });
    return () => {
      active = false;
    };
  }, [ctx.estId]);

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

  if (loadError) return <Notice kind="erro">{loadError}</Notice>;
  if (!est) return <p role="status">Carregando…</p>;
  const errors = fieldErrors(cmd.problem);
  return (
    <form onSubmit={onSubmit} noValidate className="card" aria-label="Dados da loja">
      {!cmd.problem?.errors?.length && <CommandError problem={cmd.problem} offline={cmd.offline} />}
      {saved && <Notice kind="ok">Dados salvos.</Notice>}
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
        required
        value={form.slug}
        error={errors.slug}
        hint={`Letras minúsculas, números e hífen. Link da loja: ${globalThis.location?.origin ?? ""}/loja/${form.slug}`}
        onChange={(e) => setForm({ ...form, slug: e.target.value.toLowerCase() })}
      />
      <Field
        label="Fuso horário"
        required
        maxLength={64}
        value={form.timezone}
        error={errors.timezone}
        hint="Ex.: America/Sao_Paulo, America/Manaus."
        onChange={(e) => setForm({ ...form, timezone: e.target.value })}
      />
      <Submit busy={cmd.busy !== null}>Salvar</Submit>
    </form>
  );
}
