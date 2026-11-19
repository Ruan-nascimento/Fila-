// Tela local do Fila Gestão: confere se o painel responde e abre a entrada do dono (/dono).
// Sem resposta, mostra o motivo e "Tentar novamente". A partir daí a janela fica no site.
"use strict";

const { invoke } = window.__TAURI__.core;
const $ = (id) => document.getElementById(id);

async function abrir() {
  $("tentar").disabled = true;
  $("abrindo").hidden = false;
  $("falha").hidden = true;
  let painel = null;
  try {
    painel = await invoke("painel");
    $("origem").textContent = painel.origem;
    await invoke("verificar_painel");
    window.location.replace(painel.entrada);
  } catch (erro) {
    $("motivo").textContent = typeof erro === "string" ? erro : "Erro inesperado ao abrir o painel.";
    $("abrindo").hidden = true;
    $("falha").hidden = false;
    $("tentar").disabled = false;
    $("tentar").focus();
  }
}

$("tentar").addEventListener("click", abrir);
abrir();
