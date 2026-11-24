import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { fold, matches } from "./busca";
import { Quantidade } from "./Quantidade";

describe("Busca do catálogo", () => {
  it("ignora maiúsculas, acentos e espaços extras, em qualquer ordem de termos", () => {
    expect(fold("  Corte   DEGRADÊ ")).toBe("corte degrade");
    expect(matches("Corte degradê", "degrade")).toBe(true);
    expect(matches("Corte e barba", "BARBA corte")).toBe(true);
    expect(matches("Pomada", "shampoo")).toBe(false);
    expect(matches("Qualquer", "   ")).toBe(true);
  });
});

describe("Quantidade", () => {
  it("diminui, aumenta e aceita digitação, sempre dentro do limite e acima do já realizado", () => {
    const change = vi.fn();
    const { rerender } = render(
      <Quantidade label="Quantidade de Corte" value={2} min={2} max={3} onChange={change} />,
    );
    expect(screen.getByRole("button", { name: "Diminuir quantidade de corte" })).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: "Aumentar quantidade de corte" }));
    expect(change).toHaveBeenLastCalledWith(3);
    rerender(<Quantidade label="Quantidade de Corte" value={3} min={2} max={3} onChange={change} />);
    expect(screen.getByRole("button", { name: "Aumentar quantidade de corte" })).toBeDisabled();
    const input = screen.getByLabelText("Quantidade de Corte", { selector: "input" });
    fireEvent.change(input, { target: { value: "99" } });
    fireEvent.blur(input);
    expect(input).toHaveValue("3"); // limitado ao máximo, sem enviar além dele
    fireEvent.change(input, { target: { value: "-1,5" } });
    expect(input).toHaveValue("15"); // só dígitos: nada de negativo nem fração
  });
});
