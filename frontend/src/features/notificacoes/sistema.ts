import type { Notificacao } from "./regras";

/**
 * Notificações do sistema (navegador ou programa Fila Gestão). Nada é enviado ao servidor: a
 * permissão é do navegador e a preferência fica neste aparelho. No programa desktop, o plugin de
 * notificações do Tauri troca `window.Notification` pela notificação nativa do sistema.
 */

export type Permissao = "granted" | "denied" | "default" | "unsupported";

const PREF = "fila-notificacoes";
const DISPENSADO = "fila-notificacoes-dispensado";
const SW = "/sw-notificacoes.js";

function storage(): Storage | null {
  try {
    return window.localStorage;
  } catch {
    return null; // bloqueado (janela privada): vale só nesta página
  }
}

export function permissao(): Permissao {
  if (typeof window === "undefined" || !("Notification" in window)) return "unsupported";
  return window.Notification.permission as Permissao;
}

/** A pessoa quer notificações do sistema neste aparelho (padrão: sim, se já permitiu). */
export function ligadas(): boolean {
  return permissao() === "granted" && storage()?.getItem(PREF) !== "off";
}

export function definirLigadas(on: boolean): void {
  try {
    storage()?.setItem(PREF, on ? "on" : "off");
  } catch {
    // sem armazenamento: só nesta página
  }
}

/** "Agora não" no convite: não insiste neste aparelho (a pessoa ainda pode ligar pelo sino). */
export function conviteDispensado(): boolean {
  return storage()?.getItem(DISPENSADO) === "1";
}

export function dispensarConvite(): void {
  try {
    storage()?.setItem(DISPENSADO, "1");
  } catch {
    // sem armazenamento: só nesta página
  }
}

/** Pede a permissão. Precisa vir de um toque/clique da pessoa (regra dos navegadores). */
export async function pedirPermissao(): Promise<Permissao> {
  if (permissao() === "unsupported") return "unsupported";
  try {
    const r = await window.Notification.requestPermission();
    if (r === "granted") {
      definirLigadas(true);
      void registrarSW();
    }
    return r as Permissao;
  } catch {
    return permissao();
  }
}

/**
 * Service worker mínimo, só para notificações: o Chrome do Android não aceita `new Notification`
 * e exige mostrar pela registration. Ele não intercepta pedidos nem guarda cache.
 */
async function registrarSW(): Promise<ServiceWorkerRegistration | null> {
  if (typeof navigator === "undefined" || !("serviceWorker" in navigator)) return null;
  try {
    return (
      (await navigator.serviceWorker.getRegistration(SW)) ?? (await navigator.serviceWorker.register(SW))
    );
  } catch {
    return null;
  }
}

/** Já com permissão: deixa o service worker pronto (Android) logo ao abrir a página. */
export function prepararSistema(): void {
  if (permissao() === "granted") void registrarSW();
}

/** Mostra no sistema. `url`: para onde levar a pessoa ao tocar na notificação. */
export async function mostrarNoSistema(
  n: Notificacao,
  url: string,
  icon?: string,
  onClick?: () => void,
): Promise<void> {
  if (!ligadas()) return;
  const opts: NotificationOptions & { renotify?: boolean } = {
    body: n.body,
    tag: n.group ?? n.id,
    data: { url },
    ...(icon ? { icon } : {}),
    ...(n.important ? { requireInteraction: true, renotify: true } : {}),
  };
  try {
    const x = new window.Notification(n.title, opts);
    x.onclick = () => {
      window.focus();
      x.close();
      onClick?.();
    };
    return;
  } catch {
    // Android: construtor proibido; usa o service worker.
  }
  const reg = await registrarSW();
  await reg?.showNotification(n.title, opts).catch(() => undefined);
}

/** A pessoa está olhando para esta página agora? (Senão, a notificação vai para o sistema.) */
export function olhando(): boolean {
  return document.visibilityState === "visible" && document.hasFocus();
}

let audio: AudioContext | null = null;

/** Som curto e vibração para o que é importante (vez do cliente, aviso de atraso). */
export function alertar(important: boolean): void {
  try {
    navigator.vibrate?.(important ? [200, 100, 200] : 120);
  } catch {
    // sem vibração
  }
  try {
    const Ctx =
      window.AudioContext ??
      (window as unknown as { webkitAudioContext?: typeof AudioContext }).webkitAudioContext;
    if (!Ctx) return;
    audio ??= new Ctx();
    const now = audio.currentTime;
    const tons = important ? [880, 1175] : [880];
    tons.forEach((f, i) => {
      const o = audio!.createOscillator();
      const g = audio!.createGain();
      o.frequency.value = f;
      g.gain.setValueAtTime(0.0001, now + i * 0.18);
      g.gain.exponentialRampToValueAtTime(0.15, now + i * 0.18 + 0.02);
      g.gain.exponentialRampToValueAtTime(0.0001, now + i * 0.18 + 0.16);
      o.connect(g).connect(audio!.destination);
      o.start(now + i * 0.18);
      o.stop(now + i * 0.18 + 0.17);
    });
  } catch {
    // o navegador bloqueia som antes do primeiro toque: segue só com o aviso visual
  }
}
