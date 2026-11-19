import type { Problem } from "../api/problem";
import type { components } from "../api/schema.gen";

export type OwnerSessionConflict = components["schemas"]["OwnerSessionConflict"];

/** Lê o desafio do 409 `owner_session_active` (Problem.current). */
export function conflitoDe(problem: Problem | null): OwnerSessionConflict | null {
  if (problem?.code !== "owner_session_active") return null;
  const c = problem.current as Partial<OwnerSessionConflict> | undefined;
  return c?.challenge && c.other_session ? (c as OwnerSessionConflict) : null;
}
