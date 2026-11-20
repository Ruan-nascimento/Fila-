import { useMemo, useState } from "react";
import { Download, Printer } from "lucide-react";
import { accessUrl, type AccessTarget } from "../auth/acessoLoja";
import { QUIET_ZONE, qrMatrix, qrPath, qrPngDataUrl, qrSvg } from "./qr";
import { Button, Choice, ChoiceGroup, CopyButton, type ImageRef } from "../../ui/ui";

const targets: { value: AccessTarget; label: string; description: string; file: string }[] = [
  {
    value: "loja",
    label: "Abrir a barbearia",
    description: "Mostra a loja, os profissionais e as opções de acesso.",
    file: "loja",
  },
  {
    value: "visitante",
    label: "Entrar sem conta",
    description: "Abre direto a identificação por apelido (não entra na fila sozinho).",
    file: "sem-conta",
  },
  {
    value: "conta",
    label: "Entrar com conta",
    description: "Abre o login da loja, com cadastro e entrada sem conta.",
    file: "com-conta",
  },
];

function download(href: string, filename: string) {
  const a = document.createElement("a");
  a.href = href;
  a.download = filename;
  document.body.appendChild(a);
  a.click();
  a.remove();
}

/**
 * QR Code e link de acesso da loja. O QR identifica a loja e um destino de navegação: não
 * autentica, não cria visita e não coloca ninguém na fila. A URL vem da origem pública canônica
 * (configuração do servidor) e do slug salvo — nunca do endereço do desktop ou da API. Sem senha,
 * token, convite ou dado de cliente no link. Logo e nome ficam no cartão, fora do código.
 */
export function StoreQrPanel({
  origin,
  slug,
  storeName,
  logo,
}: {
  origin: string;
  slug: string;
  storeName: string;
  logo: ImageRef | null;
}) {
  const [target, setTarget] = useState<AccessTarget>("loja");
  const url = useMemo(() => accessUrl(origin, slug, target), [origin, slug, target]);
  const matrix = useMemo(() => qrMatrix(url), [url]);
  const t = targets.find((x) => x.value === target) ?? targets[0];
  const filename = `qr-${slug}-${t?.file ?? "loja"}`; // slug já é [a-z0-9-]
  const viewBox = matrix.size + QUIET_ZONE * 2;

  return (
    <div className="grid gap-4 xl:grid-cols-[minmax(0,1fr)_16rem] xl:items-start">
      <div className="grid min-w-0 gap-4">
        <ChoiceGroup legend="Destino do QR Code">
          {targets.map((x) => (
            <Choice
              key={x.value}
              type="radio"
              name="destino-qr"
              value={x.value}
              checked={target === x.value}
              onChange={() => setTarget(x.value)}
              label={x.label}
              description={x.description}
            />
          ))}
        </ChoiceGroup>
        <div className="grid gap-2">
          <p className="m-0 text-sm font-semibold">Link</p>
          <p
            className="m-0 rounded-lg bg-surface-2 px-3 py-2 font-mono text-sm break-all"
            data-testid="link-qr"
          >
            {url}
          </p>
          <div className="flex flex-wrap gap-2">
            <CopyButton text={url} label="Copiar link" />
            <Button
              variant="secondary"
              icon={Download}
              onClick={() => download(qrPngDataUrl(matrix), `${filename}.png`)}
            >
              Baixar PNG
            </Button>
            <Button
              variant="secondary"
              icon={Download}
              onClick={() =>
                download(
                  `data:image/svg+xml;charset=utf-8,${encodeURIComponent(qrSvg(matrix))}`,
                  `${filename}.svg`,
                )
              }
            >
              Baixar SVG
            </Button>
            <Button variant="secondary" icon={Printer} onClick={() => window.print()}>
              Imprimir
            </Button>
          </div>
        </div>
      </div>
      {/* Cartão impresso: só ele aparece na impressão (styles.css, .qr-impressao). */}
      <figure className="qr-impressao m-0 grid justify-items-center gap-2 rounded-xl border border-line bg-white p-4 text-center text-black">
        {logo && (
          <img
            src={logo.variants[0]?.url}
            srcSet={logo.variants.map((v) => `${v.url} ${v.width}w`).join(", ")}
            sizes="64px"
            width={64}
            height={64}
            alt=""
            className="size-16 object-contain"
          />
        )}
        <p className="m-0 text-lg font-bold">{storeName}</p>
        <svg
          className="h-auto w-full max-w-60"
          viewBox={`0 0 ${viewBox} ${viewBox}`}
          role="img"
          aria-label={`QR Code: ${t?.label ?? ""} em ${storeName}`}
          shapeRendering="crispEdges"
        >
          <rect width={viewBox} height={viewBox} fill="#fff" />
          <path d={qrPath(matrix)} fill="#000" />
        </svg>
        <figcaption className="grid gap-1 text-sm">
          <span className="font-semibold">{t?.label}</span>
          <span className="font-mono text-xs break-all">{url}</span>
        </figcaption>
      </figure>
    </div>
  );
}
