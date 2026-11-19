//! Fila Gestão: uma janela que abre a gestão do site (entrada /dono) na origem fixada na
//! compilação (build.rs). A interface, o login, os cookies, o CSRF e o tempo real (SSE) são os
//! do próprio site: a janela é um navegador restrito a essa origem. Nenhuma regra de acesso fica
//! aqui; quem autoriza é o servidor.

use std::{sync::OnceLock, time::Duration};

use serde::Serialize;
use tauri::{webview::PageLoadEvent, Url, WebviewUrl, WebviewWindowBuilder};
use tauri_plugin_opener::OpenerExt;

/// Origem do painel (ex.: https://fila.suabarbearia.com.br), validada no build.rs.
const PAINEL_URL: &str = env!("FILA_PAINEL_URL");
/// producao | desenvolvimento | teste-local
const PAINEL_MODO: &str = env!("FILA_PAINEL_MODO");
/// Rota do frontend que oferece só as lojas em que a conta é dona.
const ENTRADA: &str = "/dono";

fn origem() -> Url {
    Url::parse(PAINEL_URL).expect("origem validada no build.rs")
}

/// Em `tauri dev` a tela local vem do servidor de desenvolvimento da CLI (ex.:
/// http://127.0.0.1:1430). Só é preenchido em builds de debug.
static LOCAL_DEV: OnceLock<Url> = OnceLock::new();

/// Telas locais do programa (a tela de carregamento e de erro, em carregador/).
fn local(url: &Url) -> bool {
    if let Some(dev) = LOCAL_DEV.get() {
        if url.origin() == dev.origin() {
            return true;
        }
    }
    match url.scheme() {
        "tauri" => url.host_str() == Some("localhost"),
        // Windows serve os arquivos locais em http(s)://tauri.localhost
        "http" | "https" => url.host_str() == Some("tauri.localhost"),
        _ => false,
    }
}

/// Endereço da tela local de carregamento (o Windows usa http://tauri.localhost).
fn tela_local() -> Url {
    if let Some(dev) = LOCAL_DEV.get() {
        return dev.join("/index.html").expect("endereço do servidor de desenvolvimento");
    }
    let base = if cfg!(windows) { "http://tauri.localhost" } else { "tauri://localhost" };
    Url::parse(&format!("{base}/index.html")).expect("endereço fixo")
}

/// A janela só navega pela origem configurada e pelas telas locais.
fn permitido(url: &Url) -> bool {
    local(url) || url.origin() == origem().origin()
}

#[derive(Serialize)]
struct Painel {
    origem: &'static str,
    entrada: String,
    modo: &'static str,
}

#[tauri::command]
fn painel() -> Painel {
    Painel { origem: PAINEL_URL, entrada: format!("{PAINEL_URL}{ENTRADA}"), modo: PAINEL_MODO }
}

/// Confere se o painel responde antes de abri-lo; o erro vira a mensagem da tela local.
#[tauri::command]
async fn verificar_painel() -> Result<(), String> {
    tauri::async_runtime::spawn_blocking(|| {
        let agente: ureq::Agent = ureq::Agent::config_builder()
            .timeout_global(Some(Duration::from_secs(10)))
            .http_status_as_error(false)
            .build()
            .into();
        match agente.get(format!("{PAINEL_URL}/healthz/live")).call() {
            Ok(r) if r.status().is_success() => Ok(()),
            Ok(r) => Err(format!(
                "O servidor do painel respondeu com erro ({}). Ele pode estar em manutenção.",
                r.status().as_u16()
            )),
            Err(ureq::Error::Timeout(_)) => {
                Err("O painel demorou demais para responder. Confira a internet.".into())
            }
            Err(e) => Err(format!("Não foi possível conectar ao painel. Confira a internet. ({e})")),
        }
    })
    .await
    .map_err(|e| e.to_string())?
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        .plugin(tauri_plugin_opener::init())
        .invoke_handler(tauri::generate_handler![painel, verificar_painel])
        .setup(|app| {
            let handle = app.handle().clone();
            if cfg!(debug_assertions) {
                if let Some(dev) = app.config().build.dev_url.clone() {
                    let _ = LOCAL_DEV.set(dev);
                }
            }
            let titulo = match PAINEL_MODO {
                "producao" => "Fila Gestão".to_string(),
                modo => format!("Fila Gestão ({modo}: {PAINEL_URL})"),
            };
            WebviewWindowBuilder::new(app, "principal", WebviewUrl::App("index.html".into()))
                .title(titulo)
                .inner_size(1280.0, 820.0)
                .min_inner_size(800.0, 600.0)
                .center()
                .on_navigation(move |url| {
                    if permitido(url) {
                        return true;
                    }
                    // Links para fora (ex.: site de pagamento, WhatsApp) abrem no navegador padrão.
                    if matches!(url.scheme(), "https" | "http" | "mailto" | "tel") {
                        let _ = handle.opener().open_url(url.as_str(), None::<&str>);
                    }
                    false
                })
                .on_page_load(|webview, payload| {
                    if payload.event() == PageLoadEvent::Finished && !permitido(payload.url()) {
                        // Garantia extra: nunca fica em página fora da origem configurada (inclui a
                        // página de erro do próprio WebView): volta à tela local, que confere o painel
                        // e oferece "Tentar novamente".
                        let _ = webview.navigate(tela_local());
                    }
                })
                .build()?;
            Ok(())
        })
        .run(tauri::generate_context!())
        .expect("erro ao iniciar o Fila Gestão");
}

#[cfg(test)]
mod testes {
    use super::*;

    #[test]
    fn so_a_origem_do_painel_e_as_telas_locais() {
        let o = origem();
        assert!(permitido(&o.join("/dono").unwrap()));
        assert!(permitido(&o.join("/gestao/x/filas?mostrar=1").unwrap()));
        assert!(permitido(&Url::parse("tauri://localhost/index.html").unwrap()));
        assert!(permitido(&Url::parse("http://tauri.localhost/index.html").unwrap()));
        assert!(!permitido(&Url::parse("https://outro-site.com/dono").unwrap()));
        assert!(!permitido(&Url::parse("tauri://outro/index.html").unwrap()));
        assert!(!permitido(&Url::parse("file:///etc/passwd").unwrap()));
        // mesma máquina, outra porta: outra origem
        let mut porta = o.clone();
        porta.set_port(Some(9)).unwrap();
        assert!(!permitido(&porta));
    }

}
