import { useCallback, useEffect, useSyncExternalStore } from "react";
import { api } from "../../api/client";
import { call, problemMessage } from "../../api/problem";
import type { components } from "../../api/schema.gen";

export type MyProfile = components["schemas"]["MyProfile"];

/**
 * Perfil da própria conta (GET /v1/me) compartilhado entre telas: "Minha conta" e a moldura da
 * gestão (foto no link "Minha conta"). Carregado UMA vez por conta; salvar ou remover a foto
 * atualiza todos os lugares; trocar de pessoa (sair e entrar com outra conta no mesmo computador)
 * descarta o perfil anterior — nenhuma foto de outra pessoa fica na tela.
 */
type Entry = { accountId: string; profile: MyProfile | null; error: string | null; loading: boolean };

let entry: Entry | null = null;
const listeners = new Set<() => void>();

function emit() {
  for (const l of listeners) l();
}

function subscribe(l: () => void) {
  listeners.add(l);
  return () => listeners.delete(l);
}

function snapshot() {
  return entry;
}

function load(accountId: string, force: boolean) {
  if (!force && entry?.accountId === accountId && (entry.profile !== null || entry.loading)) return;
  const keep = entry?.accountId === accountId ? entry.profile : null;
  entry = { accountId, profile: keep, error: null, loading: true };
  emit();
  void call(() => api.GET("/v1/me")).then((out) => {
    if (entry?.accountId !== accountId) return; // trocou de pessoa no meio: resposta antiga ignorada
    entry = out.ok
      ? { accountId, profile: out.data.data, error: null, loading: false }
      : { accountId, profile: keep, error: problemMessage(out.problem, out.offline), loading: false };
    emit();
  });
}

/** Grava o perfil confirmado pelo servidor (ex.: depois de salvar ou remover a foto). */
export function publishProfile(accountId: string, p: MyProfile) {
  entry = { accountId, profile: p, error: null, loading: false };
  emit();
}

/** Esquece o perfil (saída da conta). */
export function forgetProfile() {
  entry = null;
  emit();
}

export function useMyProfile(accountId: string | undefined) {
  const e = useSyncExternalStore(subscribe, snapshot, snapshot);
  useEffect(() => {
    if (accountId) load(accountId, false);
  }, [accountId]);
  const mine = accountId && e?.accountId === accountId ? e : null;
  const setProfile = useCallback(
    (p: MyProfile) => {
      if (accountId) publishProfile(accountId, p);
    },
    [accountId],
  );
  const reload = useCallback(() => {
    if (accountId) load(accountId, true);
  }, [accountId]);
  return { profile: mine?.profile ?? null, setProfile, error: mine?.error ?? null, reload };
}
