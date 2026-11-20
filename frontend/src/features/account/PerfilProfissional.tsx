import { useEffect, useState } from "react";
import { Copy } from "lucide-react";
import { api } from "../../api/client";
import { call, problemMessage, type Problem } from "../../api/problem";
import type { components } from "../../api/schema.gen";
import { useCommand } from "../../pages/gestao/comando";
import { CommandError } from "../../pages/gestao/comum";
import { Avatar, Button, LoadError, Loading, Notice } from "../../ui/ui";
import { ImageUploadField } from "../media/ImageUploadField";

type MyProfessional = components["schemas"]["MyProfessionalProfile"];

/**
 * "Meu perfil na barbearia": a foto PÚBLICA do profissional naquela loja, com publicação
 * explícita. O nome profissional é do dono (só leitura aqui). O avatar pessoal continua privado:
 * "Usar minha foto pessoal" publica uma CÓPIA, depois de aviso.
 */
export function PerfilProfissional({
  estId,
  mediaEnabled,
  hasAvatar,
}: {
  estId: string;
  mediaEnabled: boolean;
  hasAvatar: boolean;
}) {
  const [data, setData] = useState<MyProfessional | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [attempt, setAttempt] = useState(0);
  const [saved, setSaved] = useState<string | null>(null);
  const [confirmCopy, setConfirmCopy] = useState(false);
  const cmd = useCommand();

  useEffect(() => {
    let active = true;
    void call(() =>
      api.GET("/v1/establishments/{establishmentId}/my-professional-profile", {
        params: { path: { establishmentId: estId } },
      }),
    ).then((out) => {
      if (!active) return;
      if (out.ok) setData(out.data.data);
      else setLoadError(problemMessage(out.problem, out.offline));
    });
    return () => {
      active = false;
    };
  }, [estId, attempt]);

  function conflict(p: Problem | null) {
    if (p?.code === "version_conflict" && p.current) setData(p.current as unknown as MyProfessional);
  }

  async function publish(body: { asset_id: string } | { from_avatar: true }): Promise<boolean> {
    if (!data) return false;
    setSaved(null);
    const out = await cmd.run(`foto-prof:${data.version}:${JSON.stringify(body)}`, (key) =>
      api.PUT("/v1/establishments/{establishmentId}/my-professional-profile/photo", {
        params: { path: { establishmentId: estId }, header: { "Idempotency-Key": key } },
        body: { ...body, expected_version: data.version },
      }),
    );
    if (out.ok) {
      setData(out.data.data);
      setSaved("Foto publicada. Ela aparece para clientes e visitantes desta loja.");
      return true;
    }
    conflict(out.problem);
    return false;
  }

  async function unpublish() {
    if (!data) return;
    setSaved(null);
    const out = await cmd.run(`despublicar:${data.version}`, (key) =>
      api.POST("/v1/establishments/{establishmentId}/my-professional-profile/photo/remove", {
        params: { path: { establishmentId: estId }, header: { "Idempotency-Key": key } },
        body: { expected_version: data.version },
      }),
    );
    if (out.ok) {
      setData(out.data.data);
      setSaved("Foto retirada da página da loja. Sua foto pessoal não mudou.");
    } else conflict(out.problem);
  }

  if (loadError && !data) return <LoadError message={loadError} onRetry={() => setAttempt((n) => n + 1)} />;
  if (!data) return <Loading />;
  return (
    <section
      aria-labelledby={`perfil-prof-${estId}`}
      className="grid gap-3 rounded-xl border border-line bg-surface p-4"
    >
      <h3 id={`perfil-prof-${estId}`} className="m-0 text-base font-semibold">
        Meu perfil em {data.establishment_name}
      </h3>
      <p className="m-0 text-sm text-fg-muted">
        Nome profissional: <strong className="text-fg">{data.display_name}</strong> (quem altera é o dono da
        loja).
      </p>
      <div
        className="flex items-center gap-3 rounded-lg border border-line bg-surface-2 p-3"
        aria-label="Como aparece no cartão"
      >
        <Avatar name={data.display_name} image={data.photo} size={48} className="size-12" />
        <div className="text-sm">
          <p className="m-0 font-semibold">{data.display_name}</p>
          <p className="m-0 text-fg-muted">Prévia do cartão de profissional</p>
        </div>
      </div>
      <Notice kind="info">
        <p>Esta imagem ficará pública: aparece para quem abrir a página desta loja.</p>
      </Notice>
      <CommandError problem={cmd.problem} offline={cmd.offline} />
      {saved && (
        <Notice kind="ok">
          <p>{saved}</p>
        </Notice>
      )}
      {!mediaEnabled ? (
        <p className="m-0 text-sm text-fg-muted">
          O envio de fotos ainda não está disponível. O cartão mostra suas iniciais.
        </p>
      ) : (
        <>
          <ImageUploadField
            purpose="professional_photo"
            establishmentId={estId}
            current={data.photo}
            currentLabel={`Foto pública de ${data.display_name}`}
            onReady={(assetId) => publish({ asset_id: assetId })}
            onRemove={() => void unpublish()}
            removing={cmd.busy !== null}
          />
          {hasAvatar &&
            (confirmCopy ? (
              <div className="grid gap-2 rounded-lg border border-line p-3 text-sm">
                <p className="m-0">
                  Uma cópia da sua foto pessoal será publicada nesta loja. A foto pessoal continua privada e
                  independente.
                </p>
                <div className="flex flex-wrap gap-2">
                  <Button
                    onClick={() => {
                      setConfirmCopy(false);
                      void publish({ from_avatar: true });
                    }}
                    disabled={cmd.busy !== null}
                  >
                    Publicar cópia
                  </Button>
                  <Button variant="secondary" onClick={() => setConfirmCopy(false)}>
                    Cancelar
                  </Button>
                </div>
              </div>
            ) : (
              <Button
                variant="secondary"
                icon={Copy}
                className="justify-self-start"
                onClick={() => setConfirmCopy(true)}
              >
                Usar minha foto pessoal
              </Button>
            ))}
        </>
      )}
    </section>
  );
}
