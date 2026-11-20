import { useEffect, useId, useState, type FormEvent } from "react";
import { Link } from "react-router";
import { Gift, Store, UsersRound } from "lucide-react";
import { api } from "../../api/client";
import { call, fieldErrors, problemMessage } from "../../api/problem";
import type { components } from "../../api/schema.gen";
import { usePublicConfig } from "../../features/config/publicConfig";
import { StoreQrPanel } from "../../features/establishments/StoreQrPanel";
import { ThemePalettePicker } from "../../features/establishments/ThemePalettePicker";
import { ImageUploadField } from "../../features/media/ImageUploadField";
import { paletteOf, paletteVars, type PaletteId } from "../../features/theme/paletas";
import { notifyBrandChanged } from "../../features/theme/tema";
import {
  Button,
  Field,
  LoadError,
  Loading,
  Notice,
  PageHeader,
  Section,
  Submit,
  TextArea,
} from "../../ui/ui";
import { useUnsavedWarning } from "../../ui/useUnsavedWarning";
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

type Form = {
  name: string;
  slug: string;
  timezone: string;
  palette: PaletteId;
  public_description: string;
  public_address: string;
  public_phone: string;
};

function formOf(e: Establishment): Form {
  return {
    name: e.name,
    slug: e.slug,
    timezone: e.timezone,
    palette: paletteOf(e.palette).id,
    public_description: e.public_description ?? "",
    public_address: e.public_address ?? "",
    public_phone: e.public_phone ?? "",
  };
}

/**
 * Endereço do link a partir do nome: minúsculas sem acento, palavras separadas por hífen, de 3 a
 * 50 caracteres (o mesmo formato que o servidor confere). Nome curto demais devolve "".
 */
function slugDoNome(nome: string): string {
  const s = nome
    .normalize("NFD")
    .replace(/[\u0300-\u036f]/g, "")
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 50)
    .replace(/-+$/, "");
  return s.length >= 3 ? s : "";
}

/**
 * Configurações da barbearia (só o dono desta loja): identidade (nome, link, logo e paleta),
 * informações públicas, acesso e divulgação (link e QR Code) e atalhos para a operação. Cada
 * pedido leva a loja do contexto e o servidor confere o vínculo. Trocar o nome troca também o
 * link e o QR Code; o endereço antigo continua abrindo a loja (QR Codes já impressos valem).
 */
export function LojaConfig({ ctx }: { ctx: StaffContext }) {
  const [est, setEst] = useState<Establishment | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [attempt, setAttempt] = useState(0);
  const cmd = useCommand();
  const logoCmd = useCommand();
  const [form, setForm] = useState<Form | null>(null);
  const [saved, setSaved] = useState<string | null>(null);
  const [conflict, setConflict] = useState(false);
  // O link acompanha o nome até o dono editar o link à mão.
  const [slugManual, setSlugManual] = useState(false);
  const listId = useId();
  const cfg = usePublicConfig();

  useEffect(() => {
    let active = true;
    void call(() =>
      api.GET("/v1/establishments/{establishmentId}", { params: { path: { establishmentId: ctx.estId } } }),
    ).then((out) => {
      if (!active) return;
      if (out.ok) {
        setEst(out.data.data);
        setLoadError(null);
        setForm(formOf(out.data.data));
      } else setLoadError(problemMessage(out.problem, out.offline));
    });
    return () => {
      active = false;
    };
  }, [ctx.estId, attempt]);

  const dirty = !!est && !!form && JSON.stringify(form) !== JSON.stringify(formOf(est));
  useUnsavedWarning(dirty);

  function accepted(e: Establishment, msg: string) {
    setEst(e);
    setSaved(msg);
    notifyBrandChanged(ctx.estId);
  }

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    if (!est || !form) return;
    setSaved(null);
    setConflict(false);
    const body = { expected_version: est.version, ...form };
    const out = await cmd.run(`loja:${JSON.stringify(body)}`, (key) =>
      api.PATCH("/v1/establishments/{establishmentId}", {
        params: { path: { establishmentId: ctx.estId }, header: { "Idempotency-Key": key } },
        body,
      }),
    );
    if (out.ok) {
      const linkMudou = out.data.data.slug !== est.slug;
      accepted(
        out.data.data,
        linkMudou
          ? "Configurações salvas. O link e o QR Code agora usam o novo endereço; o link antigo continua levando à loja."
          : "Configurações salvas. A página da loja já mostra a nova identidade.",
      );
      setForm(formOf(out.data.data));
      setSlugManual(false);
    } else if (out.problem?.code === "version_conflict" && out.problem.current) {
      // Outra aba salvou antes: mantém o que foi digitado e mostra o aviso para revisar.
      setEst(out.problem.current as unknown as Establishment);
      setConflict(true);
    }
  }

  async function setLogo(assetId: string): Promise<boolean> {
    if (!est) return false;
    setSaved(null);
    const out = await logoCmd.run(`logo:${assetId}:${est.version}`, (key) =>
      api.PUT("/v1/establishments/{establishmentId}/logo", {
        params: { path: { establishmentId: ctx.estId }, header: { "Idempotency-Key": key } },
        body: { asset_id: assetId, expected_version: est.version },
      }),
    );
    if (out.ok) {
      accepted(out.data.data, "Logo salva.");
      return true;
    }
    if (out.problem?.code === "version_conflict" && out.problem.current) {
      setEst(out.problem.current as unknown as Establishment);
      setConflict(true);
    }
    return false;
  }

  async function removeLogo() {
    if (!est) return;
    setSaved(null);
    const out = await logoCmd.run(`sem-logo:${est.version}`, (key) =>
      api.POST("/v1/establishments/{establishmentId}/logo/remove", {
        params: { path: { establishmentId: ctx.estId }, header: { "Idempotency-Key": key } },
        body: { expected_version: est.version },
      }),
    );
    if (out.ok) accepted(out.data.data, "Logo removida. A loja passa a mostrar o nome.");
    else if (out.problem?.code === "version_conflict" && out.problem.current) {
      setEst(out.problem.current as unknown as Establishment);
      setConflict(true);
    }
  }

  const header = (
    <PageHeader
      title="Configurações da barbearia"
      description={`Identidade, informações públicas e divulgação de ${ctx.estName}. Seus dados pessoais ficam em Minha conta.`}
    />
  );
  if (loadError && !est)
    return (
      <>
        {header}
        <LoadError message={loadError} onRetry={() => setAttempt((n) => n + 1)} />
      </>
    );
  // Trocar de loja invalida tudo da loja anterior (formulário, prévia e QR) até a nova chegar.
  if (!est || !form || est.id !== ctx.estId)
    return (
      <>
        {header}
        <Loading />
      </>
    );
  const errors = fieldErrors(cmd.problem);
  const origin =
    cfg?.public_base_url || new URL(est.public_url || globalThis.location?.href || "http://localhost").origin;
  const set = (patch: Partial<Form>) => {
    setSaved(null);
    setForm({ ...form, ...patch });
  };

  return (
    <div className="grid max-w-4xl gap-8">
      {header}
      {conflict && (
        <Notice kind="info" title="As configurações foram alteradas em outra aba ou aparelho">
          <p>
            O que você digitou foi mantido. Revise e salve de novo, ou recarregue para ver a versão atual.
          </p>
          <p>
            <Button
              variant="secondary"
              onClick={() => {
                setConflict(false);
                setForm(formOf(est));
              }}
            >
              Descartar minhas alterações e ver a atual
            </Button>
          </p>
        </Notice>
      )}
      {saved && (
        <Notice kind="ok">
          <p>{saved}</p>
        </Notice>
      )}

      <form onSubmit={onSubmit} noValidate className="grid gap-8" aria-label="Configurações da barbearia">
        {!cmd.problem?.errors?.length &&
          cmd.problem?.code !== "version_conflict" &&
          cmd.problem?.code !== "slug_taken" && <CommandError problem={cmd.problem} offline={cmd.offline} />}
        <Section title="Identidade">
          <div className="grid gap-6 xl:grid-cols-[minmax(0,1fr)_16rem] xl:items-start">
            <div className="grid max-w-xl gap-4">
              <Field
                label="Nome público"
                required
                maxLength={80}
                value={form.name}
                error={errors.name}
                onChange={(e) => {
                  const name = e.target.value;
                  const auto = slugDoNome(name);
                  set(slugManual || !auto ? { name } : { name, slug: auto });
                }}
              />
              <Field
                label="Endereço do link"
                required
                maxLength={50}
                autoComplete="off"
                spellCheck={false}
                value={form.slug}
                error={errors.slug ?? (cmd.problem?.code === "slug_taken" ? cmd.problem.detail : undefined)}
                hint={`${origin}/loja/${form.slug || "…"} — acompanha o nome automaticamente. Letras minúsculas, números e hífens. O link antigo e QR Codes já impressos continuam levando à loja.`}
                onChange={(e) => {
                  setSlugManual(true);
                  set({ slug: e.target.value.toLowerCase().replace(/\s+/g, "-") });
                }}
              />
              <ThemePalettePicker value={form.palette} onChange={(palette) => set({ palette })} />
            </div>
            <PreviaIdentidade name={form.name} palette={form.palette} logo={est.logo} pending={dirty} />
          </div>
        </Section>

        <Section
          title="Informações públicas"
          description="Aparecem na página da loja para qualquer pessoa. Texto simples, sem formatação."
        >
          <div className="grid max-w-xl gap-4">
            <TextArea
              label="Descrição curta"
              maxLength={280}
              rows={3}
              hint={`${form.public_description.length}/280 caracteres. Deixe vazio para não mostrar.`}
              value={form.public_description}
              error={errors.public_description}
              onChange={(e) => set({ public_description: e.target.value })}
            />
            <Field
              label="Endereço"
              maxLength={200}
              autoComplete="off"
              hint="Opcional. Ex.: Rua das Flores, 120 — Centro"
              value={form.public_address}
              error={errors.public_address}
              onChange={(e) => set({ public_address: e.target.value })}
            />
            <Field
              label="Telefone ou WhatsApp da loja"
              type="tel"
              inputMode="tel"
              autoComplete="off"
              hint="Será publicado na página da loja. Não usamos o seu telefone pessoal."
              value={form.public_phone}
              error={errors.public_phone}
              onChange={(e) => set({ public_phone: e.target.value })}
            />
            <Field
              label="Fuso horário"
              required
              maxLength={64}
              list={listId}
              value={form.timezone}
              error={errors.timezone}
              hint="Ex.: America/Sao_Paulo, America/Manaus."
              onChange={(e) => set({ timezone: e.target.value })}
            />
            <datalist id={listId}>
              {brTimezones.map((tz) => (
                <option key={tz} value={tz} />
              ))}
            </datalist>
          </div>
        </Section>

        <div className="sticky bottom-0 z-10 -mx-1 flex flex-wrap items-center gap-3 border-t border-line bg-bg/95 px-1 py-3 pb-safe">
          <Submit busy={cmd.busy !== null}>Salvar configurações</Submit>
          {dirty && (
            <span role="status" className="text-sm font-semibold text-warning">
              Alterações ainda não salvas
            </span>
          )}
        </div>
      </form>

      <Section
        title="Logo"
        description="Aparece na página da loja, no login vindo da loja e no cartão do QR Code."
      >
        {cfg?.media_uploads ? (
          <div className="max-w-xl">
            <CommandError problem={logoCmd.problem} offline={logoCmd.offline} />
            <ImageUploadField
              purpose="logo"
              establishmentId={ctx.estId}
              current={est.logo}
              currentLabel={`Logo de ${est.name}`}
              onReady={setLogo}
              onRemove={() => void removeLogo()}
              removing={logoCmd.busy !== null}
              saving={logoCmd.busy !== null}
              shape="square"
            />
          </div>
        ) : (
          <p className="m-0 text-sm text-fg-muted">
            O envio de imagens ainda não está disponível. A loja aparece com o nome.
          </p>
        )}
      </Section>

      <Section title="Acesso e divulgação" description="Use em cartazes, no balcão e nas redes sociais.">
        <p className="m-0 text-sm">
          Endereço da loja: <span className="font-mono">/loja/{est.slug}</span>. Ele muda junto com o nome (em
          Identidade); links e QR Codes antigos continuam levando à loja.
        </p>
        <StoreQrPanel origin={origin} slug={est.slug} storeName={est.name} logo={est.logo} />
      </Section>

      <Section title="Operação" description="Regras de fila e fidelidade continuam nas telas próprias.">
        <ul className="m-0 flex list-none flex-wrap gap-2 p-0">
          <li>
            <Link
              to={`/gestao/${ctx.estId}/filas`}
              className="inline-flex min-h-11 items-center gap-2 font-semibold"
            >
              <UsersRound aria-hidden="true" className="size-5" /> Filas
            </Link>
          </li>
          <li>
            <Link
              to={`/gestao/${ctx.estId}/premios`}
              className="inline-flex min-h-11 items-center gap-2 font-semibold"
            >
              <Gift aria-hidden="true" className="size-5" /> Pontos e prêmios
            </Link>
          </li>
        </ul>
      </Section>
    </div>
  );
}

/**
 * Prévia isolada: a paleta escolhida vale só dentro deste quadro (data-brand + variáveis locais),
 * sem mudar a tela do dono nem a loja publicada até salvar.
 */
function PreviaIdentidade({
  name,
  palette,
  logo,
  pending,
}: {
  name: string;
  palette: PaletteId;
  logo: Establishment["logo"];
  pending: boolean;
}) {
  return (
    <div className="grid min-w-0 gap-2">
      <p className="m-0 text-sm font-semibold">
        Prévia {pending && <span className="font-normal text-warning">(alterações ainda não salvas)</span>}
      </p>
      <div
        data-brand={palette}
        style={paletteVars(palette)}
        className="grid gap-3 rounded-xl border border-line bg-bg p-4"
      >
        <div className="flex items-center gap-2">
          {logo ? (
            <img
              src={logo.variants[0]?.url}
              width={40}
              height={40}
              alt=""
              className="size-10 rounded-lg object-contain"
            />
          ) : (
            <span
              aria-hidden="true"
              className="inline-flex size-10 items-center justify-center rounded-lg bg-select text-accent"
            >
              <Store className="size-5" />
            </span>
          )}
          <span className="min-w-0 truncate font-bold">{name || "Nome da loja"}</span>
        </div>
        <span className="inline-flex min-h-11 items-center justify-center rounded-lg bg-primary px-4 font-semibold text-on-primary">
          Entrar na fila
        </span>
        <span className="text-sm">
          Link de exemplo: <span className="font-semibold text-accent underline">ver serviços</span>
        </span>
        <span className="rounded-lg border border-select-line bg-select px-3 py-2 text-sm text-accent">
          Item selecionado
        </span>
      </div>
    </div>
  );
}
