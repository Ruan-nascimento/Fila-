/**
 * Espera estimada legível: "40 min", "2 h", "2 h 30 min". Acima de uma hora, arredonda para 5
 * minutos (é uma estimativa; "2 h 33 min" passaria uma precisão que ela não tem).
 */
export function espera(minutos: number): string {
  const m = Math.max(0, Math.round(minutos));
  // Espaço não quebrável: "30 min" nunca quebra entre o número e a unidade.
  if (m < 60) return `${m}\u00a0min`;
  const total = Math.round(m / 5) * 5;
  const h = Math.floor(total / 60);
  const resto = total % 60;
  return resto ? `${h}\u00a0h ${resto}\u00a0min` : `${h}\u00a0h`;
}
