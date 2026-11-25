import { describe, expect, it } from "vitest";
import {
  dataNoFuso,
  diaDaSemana,
  horaNoFuso,
  horaParaMinutos,
  minutosParaHora,
  rotuloDia,
  somarDias,
} from "./horario";

// C-AG-12 — datas e horas no fuso da loja (frontend).
describe("horário da agenda", () => {
  it("converte minutos e horas, inclusive o fim do dia (24:00)", () => {
    expect(minutosParaHora(540)).toBe("09:00");
    expect(minutosParaHora(1440)).toBe("24:00");
    expect(horaParaMinutos("09:30")).toBe(570);
    expect(horaParaMinutos("24:00")).toBe(1440);
    expect(horaParaMinutos("24:30")).toBeNull();
    expect(horaParaMinutos("9h")).toBeNull();
  });

  it("usa o fuso da loja, não o do celular", () => {
    // 01:30 UTC de quinta = 22:30 de quarta em São Paulo.
    const t = new Date("2026-11-26T01:30:00Z");
    expect(dataNoFuso(t, "America/Sao_Paulo")).toBe("2026-11-25");
    expect(horaNoFuso(t, "America/Sao_Paulo")).toBe("22:30");
    expect(dataNoFuso(t, "Asia/Tokyo")).toBe("2026-11-26");
  });

  it("vira o mês e mostra dias relativos", () => {
    expect(somarDias("2026-11-30", 1)).toBe("2026-12-01");
    expect(diaDaSemana("2026-11-25")).toBe(3);
    expect(rotuloDia("2026-11-25", "2026-11-25")).toBe("Hoje");
    expect(rotuloDia("2026-11-26", "2026-11-25")).toBe("Amanhã");
    expect(rotuloDia("2026-11-27", "2026-11-25")).toBe("sex, 27/11");
  });
});
