// Sem janela de console no Windows (em produção).
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

fn main() {
    fila_gestao_lib::run()
}
