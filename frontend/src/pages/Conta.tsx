import { useState, type FormEvent } from "react";
import { Link, useNavigate, useSearchParams } from "react-router";
import {
  BadgeCheck,
  ChevronRight,
  KeyRound,
  LogOut,
  Monitor,
  Moon,
  Store,
  Sun,
  Trash2,
  Unlink,
} from "lucide-react";
import { api } from "../api/client";
import { call, fieldErrors, problemMessage, type Problem } from "../api/problem";
import type { components } from "../api/schema.gen";
import { forgetProfile, useMyProfile } from "../features/account/perfil";
import { PerfilProfissional } from "../features/account/PerfilProfissional";
import { Reautenticar } from "../features/account/Reautenticar";
import { GoogleButton } from "../features/auth/GoogleButton";
import { usePublicConfig } from "../features/config/publicConfig";
import { ImageUploadField } from "../features/media/ImageUploadField";
import { applyAppearance, type Appearance } from "../features/theme/tema";
import { modoDono } from "./dono/modo";
import { useSession } from "../session/context";
import {
  Avatar,
  Badge,
  Button,
  ButtonLink,
  Card,
  Field,
  LoadError,
  Loading,
  Notice,
  PageHeader,
  PasswordField,
  Section,
  Segmented,
  Submit,
} from "../ui/ui";
import { useInlinePanel } from "../ui/useInlinePanel";
import { useForm } from "../ui/useForm";
import { ExcluirConta, ExclusaoSolicitada } from "./exclusao/Exclusao";
import type { DeletionRequest } from "./exclusao/textos";
import { useCommand } from "./gestao/comando";
import { CommandError } from "./gestao/comum";

type MyProfile = components["schemas"]["MyProfile"];

const dateTime = new Intl.DateTimeFormat("pt-BR", { dateStyle: "short", timeStyle: "short" });

function NaoConectado() {
  return (
    <section aria-labelledby="titulo-conta" className="grid gap-4">
      <PageHeader id="titulo-conta" title="Minha conta" />
      <p className="m-0">Você não está conectado.</p>
      <div className="flex flex-wrap gap-2">
        <ButtonLink to="/entrar">Entrar</ButtonLink>
        <ButtonLink to="/cadastro" variant="secondary">
          Criar conta
        </ButtonLink>
      </div>
    </section>
  );
}

const googleStatus: Record<string, { kind: "ok" | "erro" | "info"; text: string }> = {
  conectado: { kind: "ok", text: "Google conectado. Você pode entrar com ele nesta conta de cliente." },
  ja_vinculado: { kind: "erro", text: "Esta conta Google já está ligada a outra conta. Nada foi alterado." },
  falhou: { kind: "erro", text: "Não foi possível conectar o Google. Tente de novo." },
  cancelado: { kind: "info", text: "Conexão com o Google cancelada. Nada foi alterado." },
  indisponivel: { kind: "erro", text: "O Google não está disponível agora." },
};

/** "Minha conta": dados da pessoa autenticada, sem depender de loja selecionada. */
export function Conta() {
  const { state, refresh } = useSession();
  const navigate = useNavigate();
  const [params, setParams] = useSearchParams();
  const [logoutError, setLogoutError] = useState<string | null>(null);
  const [leaving, setLeaving] = useState(false);
  const cfg = usePublicConfig();
  const accountId = state.status === "ready" ? state.session.account?.id : undefined;
  const { profile, setProfile, error, reload } = useMyProfile(accountId);
  const gs = params.get("google");

  if (state.status === "loading") return <Loading />;
  if (state.status === "offline")
    return (
      <Notice kind="erro">
        <p>Sem conexão com o servidor. Verifique a internet e tente de novo.</p>
      </Notice>
    );
  const { account, session, memberships } = state.session;
  if (!account || !session) return <NaoConectado />;

  async function logout() {
    setLogoutError(null);
    setLeaving(true);
    const out = await call(() => api.POST("/v1/auth/logout"));
    if (out.ok) forgetProfile();
    setLeaving(false);
    if (!out.ok && out.problem?.status !== 401) {
      setLogoutError(problemMessage(out.problem, out.offline));
      return;
    }
    await refresh();
    navigate(modoDono() ? "/dono" : "/");
  }

  const lojas = new Map<string, { name: string; roles: string[]; professional: boolean }>();
  for (const m of memberships) {
    const cur = lojas.get(m.establishment_id) ?? {
      name: m.establishment_name,
      roles: [],
      professional: false,
    };
    cur.roles.push(m.role === "owner" ? "dono" : "profissional");
    if (m.professional_id) cur.professional = true;
    lojas.set(m.establishment_id, cur);
  }
  const updated = (p: MyProfile) => {
    setProfile(p);
    void refresh();
  };
  const mediaEnabled = cfg?.media_uploads === true;

  return (
    <section aria-labelledby="titulo-conta" className="grid gap-8">
      <PageHeader
        id="titulo-conta"
        title="Minha conta"
        description="Seus dados pessoais. As configurações da barbearia ficam na gestão."
      />
      {gs && googleStatus[gs] && (
        <Notice kind={googleStatus[gs].kind}>
          <p>{googleStatus[gs].text}</p>
          <p>
            <button
              type="button"
              className="min-h-11 font-semibold underline"
              onClick={() => {
                params.delete("google");
                setParams(params, { replace: true });
              }}
            >
              Fechar aviso
            </button>
          </p>
        </Notice>
      )}
      <Card className="flex flex-wrap items-center gap-4 p-5">
        <Avatar name={account.name} image={profile?.avatar} size={56} priority className="size-14 text-lg" />
        <dl className="m-0 grid min-w-0 flex-1 gap-0.5">
          <dt className="sr-only">Nome</dt>
          <dd className="m-0 text-xl font-bold">{account.name}</dd>
          <dt className="sr-only">E-mail</dt>
          <dd className="m-0 text-fg-muted">{account.email}</dd>
          <dt className="sr-only">Usando como</dt>
          <dd className="m-0 mt-1 flex flex-wrap gap-1">
            <Badge tone="primary">{session.kind === "staff" ? "Equipe da barbearia" : "Cliente"}</Badge>
            {session.auth_method === "google" && <Badge>Entrou com o Google</Badge>}
          </dd>
          <dt className="sr-only">Sessão válida até</dt>
          <dd className="m-0 text-xs text-fg-muted">
            Sessão válida até {dateTime.format(new Date(session.expires_at))}
          </dd>
        </dl>
      </Card>

      {error && !profile && <LoadError message={error} onRetry={reload} />}

      <Section title="Dados pessoais">
        <MeuNome current={account.name} profile={profile} onDone={updated} />
        <div className="grid max-w-xl gap-1 text-sm">
          <span className="font-semibold">E-mail</span>
          <span className="flex flex-wrap items-center gap-2">
            {account.email}
            {profile?.email_verified && (
              <span className="inline-flex items-center gap-1 text-success">
                <BadgeCheck aria-hidden="true" className="size-4" /> verificado
              </span>
            )}
          </span>
          <span className="text-fg-muted">O e-mail não pode ser trocado por aqui.</span>
        </div>
      </Section>

      {profile && (
        <Section
          title="Foto pessoal"
          description="Aparece para você e, durante o atendimento, para a equipe da loja em que você estiver na fila. Nunca na fila pública."
        >
          {mediaEnabled ? (
            <MinhaFoto profile={profile} name={account.name} onDone={updated} />
          ) : (
            <p className="m-0 text-sm text-fg-muted">
              O envio de fotos ainda não está disponível. Usamos suas iniciais.
            </p>
          )}
        </Section>
      )}

      {profile && <Aparencia profile={profile} onDone={updated} />}

      {profile && (
        <MetodosDeAcesso profile={profile} googleEnabled={cfg?.google_login === true} onChanged={reload} />
      )}

      {lojas.size > 0 && (
        <Section title="Acesso de equipe">
          <ul className="m-0 grid list-none divide-y divide-line rounded-xl border border-line bg-surface p-0">
            {[...lojas].map(([id, l]) => (
              <li key={id}>
                {session.kind === "staff" ? (
                  <Link
                    to={`/gestao/${id}/filas`}
                    className="flex min-h-11 items-center gap-3 px-4 py-3 text-fg no-underline hover:bg-surface-2"
                  >
                    <Store aria-hidden="true" className="size-5 text-fg-muted" />
                    <span className="flex-1">
                      <span className="block font-medium">{l.name}</span>
                      <span className="text-sm text-fg-muted">{l.roles.join(" e ")}</span>
                    </span>
                    <ChevronRight aria-hidden="true" className="size-5 text-fg-muted" />
                  </Link>
                ) : (
                  <div className="flex items-center gap-3 px-4 py-3">
                    <Store aria-hidden="true" className="size-5 text-fg-muted" />
                    <span>
                      {l.name}: {l.roles.join(" e ")}
                    </span>
                  </div>
                )}
              </li>
            ))}
          </ul>
          {session.kind !== "staff" && (
            <p className="m-0 text-sm text-fg-muted">
              Para usar o acesso de equipe, saia e entre escolhendo “Equipe da barbearia” (com senha).
            </p>
          )}
          {session.kind === "staff" &&
            profile &&
            [...lojas]
              .filter(([, l]) => l.professional)
              .map(([id]) => (
                <PerfilProfissional
                  key={id}
                  estId={id}
                  mediaEnabled={mediaEnabled}
                  hasAvatar={profile.avatar !== null}
                />
              ))}
        </Section>
      )}

      <div className="grid gap-2">
        {logoutError && <Notice kind="erro">{logoutError}</Notice>}
        <Button
          variant="secondary"
          icon={LogOut}
          className="justify-self-start"
          disabled={leaving}
          onClick={() => void logout()}
        >
          Sair desta conta
        </Button>
      </div>

      <section aria-labelledby="titulo-zona-exclusao" className="grid gap-2 border-t border-line pt-5">
        <h2 id="titulo-zona-exclusao" className="m-0 text-base font-semibold">
          Excluir conta
        </h2>
        <p className="m-0 text-sm text-fg-muted">
          Remove seus dados pessoais e encerra pontos e prêmios. Antes de confirmar, você vê todos os efeitos.
        </p>
        <ButtonLink to="/conta/excluir" variant="danger-ghost" icon={Trash2} className="justify-self-start">
          Excluir conta
        </ButtonLink>
      </section>
    </section>
  );
}

/** Área separada da exclusão: efeitos, confirmação (senha ou código), e no fim o protocolo. */
export function ExcluirContaPagina() {
  const { state, refresh } = useSession();
  const [deletion, setDeletion] = useState<DeletionRequest | null>(null);
  const accountId = state.status === "ready" ? state.session.account?.id : undefined;
  const { profile } = useMyProfile(accountId);
  if (deletion) return <ExclusaoSolicitada request={deletion} />;
  if (state.status === "loading") return <Loading />;
  if (state.status !== "ready" || !state.session.account) return <NaoConectado />;
  return (
    <section aria-labelledby="titulo-excluir-pagina" className="grid gap-5">
      <PageHeader
        id="titulo-excluir-pagina"
        title="Excluir conta"
        back={{ to: "/conta", label: "Minha conta" }}
        description="Você pode excluir sua conta do Fila. A ação é permanente."
      />
      <ExcluirConta
        hasPassword={profile?.has_password ?? true}
        onRequested={(d) => {
          setDeletion(d);
          void refresh();
        }}
      />
    </section>
  );
}

function conflictOf(p: Problem | null): MyProfile | null {
  return p?.code === "version_conflict" && p.current ? (p.current as unknown as MyProfile) : null;
}

/** Nome exibido: muda para as próximas visitas (as já feitas guardam o nome da época). */
function MeuNome({
  current,
  profile,
  onDone,
}: {
  current: string;
  profile: MyProfile | null;
  onDone: (p: MyProfile) => void;
}) {
  const [name, setName] = useState(current);
  const [done, setDone] = useState(false);
  const [conflict, setConflict] = useState(false);
  const cmd = useCommand();
  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setDone(false);
    setConflict(false);
    if (name.trim() === "" || name.trim() === current) return;
    const body = profile ? { name: name.trim(), expected_version: profile.version } : { name: name.trim() };
    const out = await cmd.run(`nome:${JSON.stringify(body)}`, (key) =>
      api.PATCH("/v1/me", { params: { header: { "Idempotency-Key": key } }, body }),
    );
    if (out.ok) {
      setDone(true);
      onDone(out.data.data);
    } else {
      const cur = conflictOf(out.problem);
      if (cur) {
        // Outra aba mudou o perfil: mantém o que foi digitado e pede para revisar.
        setConflict(true);
        onDone(cur);
      }
    }
  }
  const errors = fieldErrors(cmd.problem);
  return (
    <form onSubmit={onSubmit} noValidate aria-label="Meu nome" className="grid max-w-xl gap-3">
      {!cmd.problem?.errors?.length && !conflict && (
        <CommandError problem={cmd.problem} offline={cmd.offline} />
      )}
      {conflict && (
        <Notice kind="info">
          <p>Seu perfil foi alterado em outra aba ou aparelho. Revise o nome e salve de novo.</p>
        </Notice>
      )}
      {done && (
        <Notice kind="ok">
          <p>Nome atualizado.</p>
        </Notice>
      )}
      <Field
        label="Nome exibido"
        autoComplete="name"
        required
        maxLength={80}
        hint="Aparece para a equipe nas próximas visitas."
        value={name}
        error={errors.name}
        onChange={(e) => setName(e.target.value)}
      />
      <div>
        <Submit busy={cmd.busy !== null}>Salvar nome</Submit>
      </div>
    </form>
  );
}

function MinhaFoto({
  profile,
  name,
  onDone,
}: {
  profile: MyProfile;
  name: string;
  onDone: (p: MyProfile) => void;
}) {
  const cmd = useCommand();
  const [msg, setMsg] = useState<string | null>(null);
  async function attach(assetId: string): Promise<boolean> {
    setMsg(null);
    const out = await cmd.run(`avatar:${assetId}:${profile.version}`, (key) =>
      api.PUT("/v1/me/avatar", {
        params: { header: { "Idempotency-Key": key } },
        body: { asset_id: assetId, expected_version: profile.version },
      }),
    );
    if (out.ok) {
      onDone(out.data.data);
      setMsg("Foto salva.");
      return true;
    }
    const cur = conflictOf(out.problem);
    if (cur) onDone(cur);
    return false;
  }
  async function remove() {
    setMsg(null);
    const out = await cmd.run(`remover-avatar:${profile.version}`, (key) =>
      api.POST("/v1/me/avatar/remove", {
        params: { header: { "Idempotency-Key": key } },
        body: { expected_version: profile.version },
      }),
    );
    if (out.ok) {
      onDone(out.data.data);
      setMsg("Foto removida. Cópias que alguém já tenha baixado não podem ser recolhidas.");
    } else {
      const cur = conflictOf(out.problem);
      if (cur) onDone(cur);
    }
  }
  return (
    <div className="grid max-w-xl gap-3">
      {cmd.problem?.code === "version_conflict" ? (
        <Notice kind="info">
          <p>Seu perfil mudou em outra aba. Confira a foto atual e tente de novo.</p>
        </Notice>
      ) : (
        <CommandError problem={cmd.problem} offline={cmd.offline} />
      )}
      {msg && (
        <Notice kind="ok">
          <p>{msg}</p>
        </Notice>
      )}
      <ImageUploadField
        purpose="avatar"
        current={profile.avatar}
        currentLabel={`Foto de ${name}`}
        onReady={attach}
        onRemove={() => void remove()}
        removing={cmd.busy !== null}
        saving={cmd.busy !== null}
      />
    </div>
  );
}

function Aparencia({ profile, onDone }: { profile: MyProfile; onDone: (p: MyProfile) => void }) {
  const cmd = useCommand();
  const [pending, setPending] = useState<Appearance | null>(null);
  const value = pending ?? profile.appearance;
  async function choose(a: Appearance) {
    setPending(a);
    const out = await cmd.run(`aparencia:${a}:${profile.version}`, (key) =>
      api.PATCH("/v1/me", {
        params: { header: { "Idempotency-Key": key } },
        body: { appearance: a, expected_version: profile.version },
      }),
    );
    setPending(null);
    if (out.ok) {
      applyAppearance(out.data.data.appearance);
      onDone(out.data.data);
    } else {
      const cur = conflictOf(out.problem);
      if (cur) onDone(cur);
    }
  }
  return (
    <Section
      title="Aparência"
      description="Vale para você em qualquer loja. A cor principal de cada loja é escolhida pela barbearia."
    >
      <div className="max-w-md">
        <Segmented
          legend="Tema"
          name="aparencia"
          value={value}
          onChange={(a) => void choose(a)}
          options={[
            { value: "system", label: "Do sistema", icon: Monitor },
            { value: "light", label: "Claro", icon: Sun },
            { value: "dark", label: "Escuro", icon: Moon },
          ]}
        />
      </div>
      <CommandError problem={cmd.problem} offline={cmd.offline} />
    </Section>
  );
}

/** Senha e Google. Conta só com Google não vê "trocar senha" nem é obrigada a informar senha. */
function MetodosDeAcesso({
  profile,
  googleEnabled,
  onChanged,
}: {
  profile: MyProfile;
  googleEnabled: boolean;
  onChanged: () => void;
}) {
  const [reauthFor, setReauthFor] = useState<"link" | "unlink" | null>(null);
  const [unlinkError, setUnlinkError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const { refresh } = useSession();

  async function startLink() {
    const out = await call(() => api.POST("/v1/auth/google/start", { body: { intent: "link" } }));
    if (out.ok) window.location.assign(out.data.data.authorization_url);
    else if (out.problem?.code === "reauthentication_required") setReauthFor("link");
    else setUnlinkError(problemMessage(out.problem, out.offline));
  }

  async function unlink() {
    setBusy(true);
    setUnlinkError(null);
    const out = await call(() => api.POST("/v1/auth/google/unlink"));
    setBusy(false);
    if (out.ok) {
      onChanged();
      await refresh();
    } else if (out.problem?.code === "reauthentication_required") setReauthFor("unlink");
    else setUnlinkError(problemMessage(out.problem, out.offline));
  }

  return (
    <Section title="Como você entra">
      <ul className="m-0 grid max-w-xl list-none gap-3 p-0">
        <li className="grid gap-2 rounded-xl border border-line bg-surface p-4">
          <span className="flex items-center gap-2 font-semibold">
            <KeyRound aria-hidden="true" className="size-5" /> Senha
          </span>
          {profile.has_password ? (
            <TrocarSenha />
          ) : (
            <p className="m-0 text-sm text-fg-muted">Esta conta não tem senha: você entra com o Google.</p>
          )}
        </li>
        <li className="grid gap-2 rounded-xl border border-line bg-surface p-4">
          <span className="font-semibold">Google</span>
          {profile.google.linked ? (
            <>
              <p className="m-0 text-sm">
                Conectado{profile.google.email ? ` como ${profile.google.email}` : ""}. Serve só para entrar
                como cliente.
              </p>
              {profile.has_password ? (
                <Button
                  variant="secondary"
                  icon={Unlink}
                  className="justify-self-start"
                  disabled={busy}
                  onClick={() => void unlink()}
                >
                  Desconectar Google
                </Button>
              ) : (
                <p className="m-0 text-sm text-fg-muted">
                  É o único jeito de entrar nesta conta, por isso não pode ser desconectado.
                </p>
              )}
            </>
          ) : googleEnabled ? (
            <>
              <p className="m-0 text-sm text-fg-muted">
                Conecte para entrar como cliente com o Google. Pedimos sua senha antes, por segurança.
              </p>
              <div className="max-w-xs">
                <GoogleButton
                  intent={{ kind: "link" }}
                  label="Conectar com o Google"
                  onProblem={(p) => {
                    if (p?.code === "reauthentication_required") {
                      setReauthFor("link");
                      return true;
                    }
                    return false;
                  }}
                />
              </div>
            </>
          ) : (
            <p className="m-0 text-sm text-fg-muted">Entrar com o Google não está disponível.</p>
          )}
          {unlinkError && (
            <Notice kind="erro">
              <p>{unlinkError}</p>
            </Notice>
          )}
          {reauthFor && (
            <Reautenticar
              hasPassword={profile.has_password}
              onCancel={() => setReauthFor(null)}
              onDone={() => {
                const what = reauthFor;
                setReauthFor(null);
                if (what === "link") void startLink();
                else void unlink();
              }}
            />
          )}
        </li>
      </ul>
    </Section>
  );
}

function TrocarSenha() {
  const form = useForm();
  const panel = useInlinePanel();
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [done, setDone] = useState(false);
  const { refresh } = useSession();

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setDone(false);
    const out = await form.run(() =>
      api.POST("/v1/auth/password/change", { body: { current_password: current, new_password: next } }),
    );
    if (out.ok) {
      setCurrent("");
      setNext("");
      setDone(true);
      panel.hide();
      await refresh();
    }
  }

  return (
    <div className="grid gap-3">
      {done && (
        <Notice kind="ok">
          <p>Senha alterada.</p>
        </Notice>
      )}
      {!panel.open ? (
        <Button
          id={panel.triggerId}
          variant="secondary"
          icon={KeyRound}
          className="justify-self-start"
          onClick={() => {
            setDone(false);
            panel.show("aberto");
          }}
        >
          Trocar senha
        </Button>
      ) : (
        <form
          id={panel.panelId}
          onKeyDown={panel.onKeyDown}
          onSubmit={onSubmit}
          noValidate
          aria-label="Trocar senha"
          className="grid gap-4 rounded-xl border border-line bg-surface-2 p-4"
        >
          <p className="m-0 text-sm text-fg-muted">
            As outras sessões abertas desta conta serão encerradas; esta continua.
          </p>
          {form.generalError}
          <PasswordField
            label="Senha atual"
            autoComplete="current-password"
            required
            value={current}
            error={form.errors.current_password}
            onChange={(e) => setCurrent(e.target.value)}
          />
          <PasswordField
            label="Senha nova"
            autoComplete="new-password"
            required
            minLength={10}
            maxLength={128}
            hint="Pelo menos 10 caracteres."
            value={next}
            error={form.errors.new_password}
            onChange={(e) => setNext(e.target.value)}
          />
          <div className="flex flex-wrap gap-2">
            <Submit busy={form.busy}>Trocar senha</Submit>
            <Button variant="secondary" onClick={panel.hide}>
              Cancelar
            </Button>
          </div>
        </form>
      )}
    </div>
  );
}
