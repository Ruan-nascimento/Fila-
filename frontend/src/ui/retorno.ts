/** Só caminhos internos (nunca "//outro-site" nem URL absoluta) podem ser destino de volta. */
export function safeReturn(value: string | null): string | null {
  if (!value || !value.startsWith("/") || value.startsWith("//") || value.startsWith("/\\")) return null;
  return value;
}
