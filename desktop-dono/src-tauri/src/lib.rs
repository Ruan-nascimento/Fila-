//! Fila Gestão: uma janela que abre a gestão do site (entrada /dono) na origem fixada na
//! compilação (build.rs). A interface, o login, os cookies, o CSRF e o tempo real (SSE) são os
//! do próprio site: a janela é um navegador restrito a essa origem. Nenhuma regra de acesso fica
//! aqui; quem autoriza é o servidor.
//!
//! Dashboard (ADR-0027): página LOCAL (dashboard.html) com os gráficos. Os dados vêm de pedidos
//! nativos tipados (api.rs) com prova de posse da chave da instalação (chave.rs), presos à MESMA
//! sessão de dono da gestão por um ticket de uso único que a gestão entrega ao navegar para
//! /dono/painel-analitico#t=… (interceptado aqui, nunca carregado).

mod api;
mod chave;
mod segundo_plano;

use std::{sync::Arc, sync::OnceLock, time::Duration};

use serde::Serialize;
use serde_json::Value;
use tauri::{
    ipc::CapabilityBuilder, webview::PageLoadEvent, AppHandle, Manager, State, Url, WebviewUrl, WebviewWindowBuilder,
    WindowEvent,
};
use tauri_plugin_opener::OpenerExt;

use api::{Consulta, ErroApi, Estado, Instalacao};

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

/// Endereço de uma tela local (o Windows usa http://tauri.localhost).
fn tela(pagina: &str) -> Url {
    if let Some(dev) = LOCAL_DEV.get() {
        return dev.join(pagina).expect("endereço do servidor de desenvolvimento");
    }
    let base = if cfg!(windows) { "http://tauri.localhost" } else { "tauri://localhost" };
    Url::parse(&format!("{base}/{pagina}")).expect("endereço fixo")
}

fn tela_local() -> Url {
    tela("index.html")
}

/// Rota do frontend que entrega o ticket do Dashboard (ver frontend/src/pages/dono/Dashboard.tsx).
const ROTA_DASHBOARD: &str = "/dono/painel-analitico";

/// Lê `t` (ticket) e `est` (loja) do fragmento do atalho do Dashboard.
fn ticket_do_atalho(url: &Url) -> Option<(String, String)> {
    if url.origin() != origem().origin() || url.path() != ROTA_DASHBOARD {
        return None;
    }
    let frag = url.fragment()?;
    let mut t = None;
    let mut est = None;
    for (k, v) in url::form_urlencoded::parse(frag.as_bytes()) {
        match k.as_ref() {
            "t" if v.len() >= 20 && v.len() <= 64 => t = Some(v.into_owned()),
            "est" if uuid::Uuid::parse_str(&v).is_ok() => est = Some(v.into_owned()),
            _ => {}
        }
    }
    Some((t?, est.unwrap_or_default()))
}

/// A janela só navega pela origem configurada e pelas telas locais.
fn permitido(url: &Url) -> bool {
    local(url) || url.origin() == origem().origin()
}

/// A página é a gestão remota (origem do painel).
pub(crate) fn na_origem(url: &Url) -> bool {
    url.origin() == origem().origin()
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

// ---------------------------------------------------------------------------------------------
// Comandos do Dashboard (só as telas locais podem chamá-los: capabilities/carregador.json).

#[derive(Serialize)]
struct EstadoDashboard {
    instalacao: Option<Instalacao>,
    vinculado: bool,
    ticket_pendente: bool,
    loja_pedida: Option<String>,
    painel: &'static str,
}

fn ler_instalacao(app: &AppHandle) -> Option<Instalacao> {
    let p = api::arquivo_instalacao(app)?;
    serde_json::from_slice(&std::fs::read(p).ok()?).ok()
}

fn chave_atual(estado: &Estado) -> Result<p256::ecdsa::SigningKey, ErroApi> {
    chave::carregar(estado.origem)?
        .ok_or_else(|| ErroApi { status: 0, code: "desktop_not_activated".into(), detail: "Este computador ainda não foi ativado.".into() })
}

#[tauri::command]
fn estado_dashboard(app: AppHandle, estado: State<'_, Arc<Estado>>) -> EstadoDashboard {
    let ativo = matches!(chave::carregar(estado.origem), Ok(Some(_)));
    let ticket = estado.ticket.lock().ok().and_then(|t| t.clone());
    EstadoDashboard {
        instalacao: if ativo { ler_instalacao(&app) } else { None },
        vinculado: estado.tem_token(),
        ticket_pendente: ticket.is_some(),
        loja_pedida: ticket.map(|(_, est)| est).filter(|e| !e.is_empty()),
        painel: PAINEL_URL,
    }
}

#[tauri::command]
async fn ativar_instalacao(app: AppHandle, estado: State<'_, Arc<Estado>>, codigo: String) -> Result<Instalacao, ErroApi> {
    let e = Arc::clone(&estado);
    let v = tauri::async_runtime::spawn_blocking(move || e.ativar(&codigo)).await.map_err(|e| ErroApi::from(e.to_string()))??;
    let inst = Instalacao { id: v["id"].as_str().unwrap_or_default().into(), label: v["label"].as_str().unwrap_or_default().into() };
    if let Some(p) = api::arquivo_instalacao(&app) {
        let _ = std::fs::create_dir_all(p.parent().expect("pasta de dados"));
        let _ = std::fs::write(p, serde_json::to_vec(&inst).unwrap_or_default());
    }
    Ok(inst)
}

#[tauri::command]
async fn vincular_sessao(estado: State<'_, Arc<Estado>>) -> Result<Value, ErroApi> {
    let e = Arc::clone(&estado);
    tauri::async_runtime::spawn_blocking(move || {
        let k = chave_atual(&e)?;
        e.vincular(&k)
    })
    .await
    .map_err(|e| ErroApi::from(e.to_string()))?
}

#[tauri::command]
async fn contexto_dashboard(estado: State<'_, Arc<Estado>>) -> Result<Value, ErroApi> {
    let e = Arc::clone(&estado);
    tauri::async_runtime::spawn_blocking(move || {
        let k = chave_atual(&e)?;
        e.contexto(&k)
    })
    .await
    .map_err(|e| ErroApi::from(e.to_string()))?
}

#[tauri::command]
async fn consultar(estado: State<'_, Arc<Estado>>, consulta: Consulta) -> Result<Value, ErroApi> {
    let e = Arc::clone(&estado);
    tauri::async_runtime::spawn_blocking(move || {
        let k = chave_atual(&e)?;
        e.consultar(&k, &consulta)
    })
    .await
    .map_err(|e| ErroApi::from(e.to_string()))?
}

#[tauri::command]
fn iniciar_fluxo(app: AppHandle, estado: State<'_, Arc<Estado>>, loja: uuid::Uuid) -> Result<(), ErroApi> {
    let k = chave_atual(&estado)?;
    estado.inner().iniciar_fluxo(app, k, loja);
    Ok(())
}

#[tauri::command]
fn parar_fluxo(estado: State<'_, Arc<Estado>>) {
    estado.parar_fluxo();
}

/// Destinos da gestão que o Dashboard pode abrir (só rotas existentes e autorizadas pela gestão).
#[derive(serde::Deserialize)]
#[serde(tag = "tela", rename_all = "snake_case")]
enum Destino {
    Filas,
    Pendencias,
    Comanda { id: uuid::Uuid },
}

#[tauri::command]
async fn abrir_gestao(app: AppHandle, estado: State<'_, Arc<Estado>>, loja: uuid::Uuid, destino: Destino) -> Result<(), ErroApi> {
    // O Dashboard sai da tela: o fluxo para; o acesso continua até expirar (mesma sessão).
    estado.parar_fluxo();
    let caminho = match destino {
        Destino::Filas => format!("/gestao/{loja}/filas"),
        Destino::Pendencias => format!("/gestao/{loja}/pendencias"),
        Destino::Comanda { id } => format!("/gestao/{loja}/comandas/{id}"),
    };
    let url = origem().join(&caminho).map_err(|e| ErroApi::from(e.to_string()))?;
    if let Some(w) = app.get_webview_window("principal") {
        w.navigate(url).map_err(|e| ErroApi::from(e.to_string()))?;
    }
    Ok(())
}

/// Sair do Dashboard: encerra só o acesso local (a sessão de dono da gestão continua).
#[tauri::command]
async fn sair_dashboard(app: AppHandle, estado: State<'_, Arc<Estado>>) -> Result<(), ErroApi> {
    let e = Arc::clone(&estado);
    tauri::async_runtime::spawn_blocking(move || {
        if let Ok(k) = chave_atual(&e) {
            e.sair(&k);
        }
    })
    .await
    .map_err(|e| ErroApi::from(e.to_string()))?;
    if let Some(w) = app.get_webview_window("principal") {
        let _ = w.navigate(origem().join(ENTRADA).expect("rota fixa"));
    }
    Ok(())
}

/// Computador revogado: apaga a chave local para permitir uma nova ativação.
#[tauri::command]
fn esquecer_instalacao(app: AppHandle, estado: State<'_, Arc<Estado>>) -> Result<(), ErroApi> {
    estado.esquecer_token();
    chave::apagar(estado.origem)?;
    if let Some(p) = api::arquivo_instalacao(&app) {
        let _ = std::fs::remove_file(p);
    }
    Ok(())
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        // Primeiro plugin: abrir o programa de novo só traz a janela existente (e a reabre se
        // estava escondida na bandeja). Duas instâncias gerariam avisos duplicados.
        .plugin(tauri_plugin_single_instance::init(|app, _args, _cwd| segundo_plano::mostrar(app)))
        .plugin(tauri_plugin_dialog::init())
        .plugin(tauri_plugin_opener::init())
        // Notificações da gestão (entrou na fila, saiu, avisou atraso, pediu mudança). O plugin
        // troca `window.Notification` da página pela notificação nativa do sistema.
        .plugin(tauri_plugin_notification::init())
        .manage(Arc::new(Estado::new(PAINEL_URL)))
        .invoke_handler(tauri::generate_handler![
            painel,
            verificar_painel,
            estado_dashboard,
            ativar_instalacao,
            vincular_sessao,
            contexto_dashboard,
            consultar,
            iniciar_fluxo,
            parar_fluxo,
            abrir_gestao,
            sair_dashboard,
            esquecer_instalacao
        ])
        .on_window_event(|window, event| {
            // X com o segundo plano ligado: esconde (a página e o fluxo de avisos continuam).
            if let WindowEvent::CloseRequested { api, .. } = event {
                if let Some(sp) = window.app_handle().try_state::<segundo_plano::SegundoPlano>() {
                    if sp.esconder_ao_fechar() {
                        api.prevent_close();
                        let _ = window.hide();
                    }
                }
            }
        })
        .setup(|app| {
            let arquivo = app.path().app_config_dir().ok().map(|d| d.join("segundo-plano.json"));
            app.manage(segundo_plano::SegundoPlano::carregar(arquivo));
            if let Err(e) = segundo_plano::criar_bandeja(app.handle()) {
                // Sem bandeja: fechar encerra (nunca esconder sem um jeito confiável de reabrir).
                eprintln!("bandeja indisponível ({e}); segundo plano desligado nesta sessão");
            }
            // Só em build de depuração: aciona "fechar a janela" e "Sair do Fila" por tempo, pelo
            // MESMO caminho do clique (CloseRequested e segundo_plano::sair), para o teste
            // automatizado de segundo plano (desktop-dono/testes/segundo-plano.sh). Não existe no
            // pacote de release.
            #[cfg(debug_assertions)]
            {
                let h = app.handle().clone();
                std::thread::spawn(move || {
                    let secs = |v: &str| std::env::var(v).ok().and_then(|s| s.parse::<u64>().ok());
                    if let Some(n) = secs("FILA_TESTE_FECHAR_APOS") {
                        std::thread::sleep(Duration::from_secs(n));
                        if let Some(w) = h.get_webview_window("principal") {
                            let _ = w.close();
                        }
                    }
                    if let Some(n) = secs("FILA_TESTE_SAIR_APOS") {
                        std::thread::sleep(Duration::from_secs(n));
                        segundo_plano::sair(&h);
                    }
                });
            }
            // A gestão é uma página REMOTA (origem do painel). Ela não tem acesso a nenhum comando
            // do programa (capability "carregador" é só local); a única exceção são as três
            // permissões de notificação, e só para a origem do painel nesta janela.
            app.add_capability(
                CapabilityBuilder::new("painel-notificacoes")
                    .remote(format!("{}/*", origem().origin().ascii_serialization()))
                    .local(false)
                    .window("principal")
                    .permission("notification:allow-is-permission-granted")
                    .permission("notification:allow-request-permission")
                    .permission("notification:allow-notify"),
            )?;
            let handle = app.handle().clone();
            let handle_atalho = app.handle().clone();
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
                // Só uma dica de APRESENTAÇÃO para a gestão mostrar o atalho "Dashboard". Não
                // concede nada: os dados exigem a prova de posse da chave, que a página não tem.
                .initialization_script("window.__GESTAO_DESKTOP__ = true;")
                .on_navigation(move |url| {
                    // Atalho do Dashboard: guarda o ticket na memória nativa, cancela a navegação
                    // e abre a página local. O ticket nunca chega a ser carregado como página.
                    if let Some(pendente) = ticket_do_atalho(url) {
                        let estado = handle_atalho.state::<Arc<Estado>>();
                        if let Ok(mut t) = estado.ticket.lock() {
                            *t = Some(pendente);
                        }
                        let h = handle_atalho.clone();
                        tauri::async_runtime::spawn(async move {
                            if let Some(w) = h.get_webview_window("principal") {
                                let _ = w.navigate(tela("dashboard.html"));
                            }
                        });
                        return false;
                    }
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
                    // Gestão carregada: grava nela a preferência de som deste computador.
                    if payload.event() == PageLoadEvent::Finished && na_origem(payload.url()) {
                        if let Some(sp) = webview.app_handle().try_state::<segundo_plano::SegundoPlano>() {
                            let _ = webview.eval(segundo_plano::script_som(sp.prefs().som));
                        }
                    }
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

    #[test]
    fn atalho_do_dashboard_so_na_origem_e_rota_certas() {
        let o = origem();
        let est = "0190a000-0000-7000-8000-0000000000a1";
        let t = "a".repeat(43);
        let ok = o.join(&format!("/dono/painel-analitico#t={t}&est={est}")).unwrap();
        assert_eq!(ticket_do_atalho(&ok), Some((t.clone(), est.to_string())));
        assert!(ticket_do_atalho(&o.join(&format!("/outra#t={t}")).unwrap()).is_none());
        assert!(ticket_do_atalho(&Url::parse(&format!("https://outro.site/dono/painel-analitico#t={t}")).unwrap()).is_none());
        assert!(ticket_do_atalho(&o.join("/dono/painel-analitico#t=curto").unwrap()).is_none());
        // loja inválida é ignorada (o Dashboard escolhe pela lista do servidor)
        let sem_loja = o.join(&format!("/dono/painel-analitico#t={t}&est=../x")).unwrap();
        assert_eq!(ticket_do_atalho(&sem_loja), Some((t, String::new())));
    }
}
