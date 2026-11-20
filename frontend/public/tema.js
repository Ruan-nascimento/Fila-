// Aplica a aparência escolhida neste aparelho ANTES do primeiro desenho (sem piscar o tema).
// Arquivo externo (a CSP de produção não permite script embutido). Só lê uma preferência local.
try {
  var a = localStorage.getItem("fila-aparencia");
  if (a === "light" || a === "dark") document.documentElement.setAttribute("data-theme", a);
} catch (e) {
  // armazenamento bloqueado: segue o sistema
}
