import { useEffect, useId, useRef, useState, type ChangeEvent, type DragEvent } from "react";
import { Crosshair, ImagePlus, Trash2 } from "lucide-react";
import { Button, Notice, type ImageRef } from "../../ui/ui";
import { cx } from "../../ui/cx";
import {
  centered,
  checkFile,
  limits,
  readAsDataURL,
  uploadImage,
  type Crop,
  type MediaPurpose,
} from "./envio";

type Phase =
  | { kind: "idle" }
  | { kind: "selected"; file: File; url: string; w: number; h: number }
  | { kind: "uploading"; file: File; url: string; w: number; h: number; processing: boolean }
  | { kind: "error"; message: string };

const PREVIEW = 160;

/**
 * Escolher, enquadrar e enviar uma imagem. Estados: escolhendo → prévia (NÃO salva) → enviando →
 * processando → pronta (onReady com o id) ou erro. Enviar não é salvar: quem usa o campo associa o
 * id com o comando da tela e só então mostra "salvo" (confirmação do servidor).
 *
 * Avatar/foto: recorte quadrado com controles de posição e zoom (teclado nativo dos controles
 * deslizantes) e "Centralizar". Logo: enquadrada inteira (sem cortar o nome da marca).
 */
export function ImageUploadField({
  purpose,
  establishmentId,
  current,
  currentLabel,
  onReady,
  onRemove,
  removing = false,
  saving = false,
  shape = "circle",
}: {
  purpose: MediaPurpose;
  establishmentId?: string;
  current: ImageRef | null;
  currentLabel: string;
  /** Imagem processada: associe com o comando da tela. Devolve true se salvou. */
  onReady: (assetId: string) => Promise<boolean>;
  onRemove?: (() => void) | undefined;
  removing?: boolean;
  saving?: boolean;
  shape?: "circle" | "square";
}) {
  const id = useId();
  const input = useRef<HTMLInputElement>(null);
  const [phase, setPhase] = useState<Phase>({ kind: "idle" });
  const [crop, setCrop] = useState<Crop>({ x: 0, y: 0, size: 1 });
  const [drag, setDrag] = useState(false);
  const abort = useRef<AbortController | null>(null);
  const isLogo = purpose === "logo";

  useEffect(() => () => abort.current?.abort(), []);

  async function pick(file: File | undefined) {
    if (!file) return;
    const problem = checkFile(file);
    if (problem) {
      setPhase({ kind: "error", message: problem });
      return;
    }
    let url: string;
    try {
      url = await readAsDataURL(file);
    } catch {
      setPhase({ kind: "error", message: "Não foi possível abrir este arquivo." });
      return;
    }
    const img = new Image();
    img.onload = () => {
      const w = img.naturalWidth;
      const h = img.naturalHeight;
      setPhase({ kind: "selected", file, url, w, h });
      setCrop(centered(w, h));
    };
    img.onerror = () =>
      setPhase({ kind: "error", message: "Não foi possível ler esta imagem. Tente outra foto." });
    img.src = url;
  }

  function onChange(e: ChangeEvent<HTMLInputElement>) {
    void pick(e.target.files?.[0]);
    e.target.value = "";
  }

  function onDrop(e: DragEvent) {
    e.preventDefault();
    setDrag(false);
    void pick(e.dataTransfer.files[0]);
  }

  async function send() {
    if (phase.kind !== "selected") return;
    const sel = phase;
    setPhase({ ...sel, kind: "uploading", processing: false });
    const ctrl = new AbortController();
    abort.current = ctrl;
    const out = await uploadImage(sel.file, purpose, {
      ...(establishmentId ? { establishmentId } : {}),
      crop: isLogo ? null : crop,
      signal: ctrl.signal,
      onProcessing: () => setPhase({ ...sel, kind: "uploading", processing: true }),
    });
    if (ctrl.signal.aborted) return;
    if (!out.ok) {
      setPhase({ kind: "error", message: out.message });
      return;
    }
    const saved = await onReady(out.assetId);
    // Salvo: a prévia some (a imagem atual passa a ser a do servidor). Erro: mantém a escolha.
    setPhase(saved ? { kind: "idle" } : sel);
  }

  function cancel() {
    abort.current?.abort();
    setPhase({ kind: "idle" });
  }

  const sel = phase.kind === "selected" || phase.kind === "uploading" ? phase : null;
  const busy = phase.kind === "uploading" || saving;
  const round = shape === "circle" ? "rounded-full" : "rounded-xl";
  const maxPos = sel ? limits(sel.w, sel.h, crop.size) : { x: 0, y: 0 };

  return (
    <div className="grid gap-3">
      <div className="flex flex-wrap items-start gap-4">
        <div className="grid justify-items-center gap-1">
          {sel ? (
            <CropPreview sel={sel} crop={crop} logo={isLogo} round={round} />
          ) : current ? (
            <img
              src={current.variants[current.variants.length - 1]?.url}
              width={PREVIEW / 2}
              height={PREVIEW / 2}
              alt={currentLabel}
              decoding="async"
              className={cx(
                "size-20 border border-line bg-surface-2",
                isLogo ? "object-contain" : "object-cover",
                round,
              )}
            />
          ) : (
            <span
              className={cx(
                "inline-flex size-20 items-center justify-center border border-dashed border-line-strong text-fg-muted",
                round,
              )}
            >
              <ImagePlus aria-hidden="true" className="size-7" />
              <span className="sr-only">Sem imagem</span>
            </span>
          )}
          {sel && (
            <span className="rounded bg-warning-soft px-2 py-0.5 text-xs font-semibold text-warning">
              Prévia — ainda não salva
            </span>
          )}
        </div>
        <div
          onDragOver={(e) => {
            e.preventDefault();
            setDrag(true);
          }}
          onDragLeave={() => setDrag(false)}
          onDrop={onDrop}
          className={cx(
            "grid min-w-0 flex-1 gap-2 rounded-xl border border-dashed p-3 text-sm",
            drag ? "border-primary bg-select" : "border-line",
          )}
        >
          <label htmlFor={id} className="font-semibold">
            {isLogo ? "Escolher logo" : "Escolher foto"}
          </label>
          <input
            ref={input}
            id={id}
            type="file"
            accept="image/jpeg,image/png,image/webp"
            disabled={busy}
            onChange={onChange}
            className="w-full min-w-0 max-w-full text-sm file:mr-3 file:min-h-11 file:rounded-lg file:border-0 file:bg-surface-2 file:px-3 file:font-semibold file:text-fg"
          />
          <p className="m-0 text-fg-muted">
            JPEG, PNG ou WebP, até 5 MB. Você também pode arrastar a imagem para cá.
          </p>
        </div>
      </div>

      {sel && !isLogo && (
        <fieldset className="grid max-w-sm gap-2 rounded-xl border border-line p-3" disabled={busy}>
          <legend className="px-1 text-sm font-semibold">Enquadramento</legend>
          <Slider
            label="Zoom"
            min={0.3}
            max={1}
            value={1.3 - crop.size}
            onChange={(v) => setCrop(clamp(sel, { ...crop, size: 1.3 - v }))}
          />
          <Slider
            label="Horizontal"
            min={0}
            max={maxPos.x}
            value={crop.x}
            onChange={(v) => setCrop({ ...crop, x: v })}
          />
          <Slider
            label="Vertical"
            min={0}
            max={maxPos.y}
            value={crop.y}
            onChange={(v) => setCrop({ ...crop, y: v })}
          />
          <Button
            variant="secondary"
            icon={Crosshair}
            className="justify-self-start"
            onClick={() => setCrop(centered(sel.w, sel.h))}
          >
            Centralizar
          </Button>
        </fieldset>
      )}

      <div aria-live="polite" className="text-sm">
        {phase.kind === "uploading" && (
          <p className="m-0">{phase.processing ? "Foto em processamento…" : "Enviando…"}</p>
        )}
        {phase.kind === "error" && (
          <Notice kind="erro">
            <p>{phase.message}</p>
          </Notice>
        )}
      </div>

      <div className="flex flex-wrap gap-2">
        {sel && (
          <>
            <Button onClick={() => void send()} disabled={busy}>
              {isLogo ? "Salvar logo" : "Salvar foto"}
            </Button>
            <Button variant="secondary" onClick={cancel}>
              Cancelar
            </Button>
          </>
        )}
        {!sel && current && onRemove && (
          <Button variant="danger-ghost" icon={Trash2} onClick={onRemove} disabled={removing || busy}>
            {isLogo ? "Remover logo" : "Remover foto"}
          </Button>
        )}
      </div>
    </div>
  );
}

function Slider({
  label,
  min,
  max,
  value,
  onChange,
}: {
  label: string;
  min: number;
  max: number;
  value: number;
  onChange: (v: number) => void;
}) {
  const id = useId();
  return (
    <div className="grid gap-1">
      <label htmlFor={id} className="text-sm">
        {label}
      </label>
      <input
        id={id}
        type="range"
        min={min}
        max={Math.max(min, max)}
        step={0.01}
        value={Math.min(Math.max(value, min), Math.max(min, max))}
        disabled={max <= min}
        onChange={(e) => onChange(Number(e.target.value))}
        className="min-h-11 accent-[var(--primary)]"
      />
    </div>
  );
}

function clamp(sel: { w: number; h: number }, c: Crop): Crop {
  const size = Math.min(1, Math.max(0.3, c.size));
  const m = limits(sel.w, sel.h, size);
  return { size, x: Math.min(c.x, m.x), y: Math.min(c.y, m.y) };
}

function CropPreview({
  sel,
  crop,
  logo,
  round,
}: {
  sel: { url: string; w: number; h: number };
  crop: Crop;
  logo: boolean;
  round: string;
}) {
  if (logo)
    return (
      <img
        src={sel.url}
        width={PREVIEW}
        height={PREVIEW}
        alt="Prévia da logo (ainda não salva)"
        className={cx("size-40 border border-line bg-surface-2 object-contain", round)}
      />
    );
  const side = crop.size * Math.min(sel.w, sel.h);
  const scale = PREVIEW / side;
  return (
    <span
      className={cx("relative inline-block size-40 overflow-hidden border border-line bg-surface-2", round)}
    >
      <img
        src={sel.url}
        alt="Prévia da foto (ainda não salva)"
        className="absolute max-w-none"
        style={{
          width: sel.w * scale,
          height: sel.h * scale,
          left: -crop.x * sel.w * scale,
          top: -crop.y * sel.h * scale,
        }}
      />
    </span>
  );
}
