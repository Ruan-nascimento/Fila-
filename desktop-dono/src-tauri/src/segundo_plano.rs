//! Segundo plano (ADR-0037): com a preferência ligada, fechar a janela (X) só a esconde e o
//! programa continua na bandeja recebendo os avisos da gestão; "Sair do Fila" encerra de verdade.
//!
//! - A preferência é DESTE computador (arquivo na pasta de configuração do programa), começa
//!   DESLIGADA e só muda por um gesto da pessoa no menu da bandeja (nunca em silêncio).
//! - Sem bandeja funcional (a criação falhou), a janela nunca é escondida: fechar encerra, como
//!   antes. Com instância única (lib.rs), abrir o programa de novo também traz a janela de volta.
//! - Os avisos continuam sendo os da própria página (fluxo SSE da sessão de dono): nenhuma
//!   segunda sessão, nenhum fluxo paralelo — então logout, expiração e revogação valem igual.
//! - O som é uma preferência do aparelho guardada pela página (localStorage da origem do painel);
//!   a bandeja grava o valor nela avaliando um script na própria janela (a página não ganha
//!   acesso a comando nenhum do programa).

use std::{
    fs,
    path::PathBuf,
    sync::{
        atomic::{AtomicBool, Ordering},
        Mutex,
    },
};

use serde::{Deserialize, Serialize};
use tauri::{
    menu::{CheckMenuItem, Menu, MenuItem, PredefinedMenuItem},
    tray::{MouseButton, MouseButtonState, TrayIconBuilder, TrayIconEvent},
    AppHandle, Manager, Runtime, WebviewWindow, Wry,
};
use tauri_plugin_dialog::{DialogExt, MessageDialogKind};

/// Texto da primeira ativação (pedido do produto, sem mudar o sentido).
pub const EXPLICACAO: &str =
    "O Fila continuará aberto na bandeja para receber avisos. Para encerrar completamente, use Sair do Fila.";

#[derive(Serialize, Deserialize, Clone, Debug, PartialEq)]
#[serde(default)]
pub struct Preferencias {
    /// Continuar executando em segundo plano ao fechar a janela.
    pub segundo_plano: bool,
    /// Som dos avisos (espelha a preferência da página; padrão ligado).
    pub som: bool,
    /// A explicação da primeira ativação já foi mostrada.
    pub explicado: bool,
}

impl Default for Preferencias {
    fn default() -> Self {
        Preferencias { segundo_plano: false, som: true, explicado: false }
    }
}

/// Estado do segundo plano, guardado no app.
pub struct SegundoPlano {
    prefs: Mutex<Preferencias>,
    arquivo: Option<PathBuf>,
    /// A bandeja foi criada: só então esconder a janela é seguro.
    pub bandeja: AtomicBool,
    /// "Sair do Fila" pedido: o fechamento seguinte não esconde, encerra.
    pub saindo: AtomicBool,
    menu_som: Mutex<Option<CheckMenuItem<Wry>>>,
    menu_fundo: Mutex<Option<CheckMenuItem<Wry>>>,
}

impl SegundoPlano {
    pub fn carregar(arquivo: Option<PathBuf>) -> Self {
        let prefs = arquivo
            .as_ref()
            .and_then(|p| fs::read(p).ok())
            .and_then(|b| serde_json::from_slice(&b).ok())
            .unwrap_or_default();
        SegundoPlano {
            prefs: Mutex::new(prefs),
            arquivo,
            bandeja: AtomicBool::new(false),
            saindo: AtomicBool::new(false),
            menu_som: Mutex::new(None),
            menu_fundo: Mutex::new(None),
        }
    }

    pub fn prefs(&self) -> Preferencias {
        self.prefs.lock().map(|p| p.clone()).unwrap_or_default()
    }

    fn salvar(&self, p: &Preferencias) {
        if let Ok(mut cur) = self.prefs.lock() {
            *cur = p.clone();
        }
        if let Some(arq) = &self.arquivo {
            if let Some(dir) = arq.parent() {
                let _ = fs::create_dir_all(dir);
            }
            let _ = fs::write(arq, serde_json::to_vec_pretty(p).unwrap_or_default());
        }
    }

    /// Esconder no lugar de fechar? Só com a preferência ligada, bandeja funcionando e sem
    /// "Sair do Fila" pedido.
    pub fn esconder_ao_fechar(&self) -> bool {
        self.prefs().segundo_plano && self.bandeja.load(Ordering::SeqCst) && !self.saindo.load(Ordering::SeqCst)
    }
}

/// Script que grava a preferência de som na página (origem do painel) e avisa a ponte da página,
/// se ela existir. Sem dados da página voltando para o programa.
pub fn script_som(on: bool) -> String {
    let v = if on { "on" } else { "off" };
    format!(
        "try{{localStorage.setItem('fila-notificacoes-som','{v}')}}catch(e){{}};\
         try{{window.__filaDesktop&&window.__filaDesktop.definirSom({on})}}catch(e){{}}"
    )
}

/// Mostra e foca a janela principal (bandeja, segunda instância).
pub fn mostrar<R: Runtime>(app: &AppHandle<R>) {
    if let Some(w) = app.get_webview_window("principal") {
        let _ = w.unminimize();
        let _ = w.show();
        let _ = w.set_focus();
    }
}

/// Encerra o processo de verdade: não esconde mais, para o fluxo do Dashboard e sai. Não toca
/// em nada da loja (atendimentos continuam no servidor).
pub fn sair(app: &AppHandle) {
    if let Some(sp) = app.try_state::<SegundoPlano>() {
        sp.saindo.store(true, Ordering::SeqCst);
    }
    app.exit(0);
}

/// Cria a bandeja. Falhou (sem área de notificação, sem biblioteca): segue sem segundo plano.
pub fn criar_bandeja(app: &AppHandle) -> tauri::Result<()> {
    let sp = app.state::<SegundoPlano>();
    let prefs = sp.prefs();
    let abrir = MenuItem::with_id(app, "abrir", "Abrir Fila", true, None::<&str>)?;
    let som = CheckMenuItem::with_id(app, "som", "Som dos avisos", true, prefs.som, None::<&str>)?;
    let fundo = CheckMenuItem::with_id(
        app,
        "segundo_plano",
        "Continuar executando em segundo plano ao fechar a janela",
        true,
        prefs.segundo_plano,
        None::<&str>,
    )?;
    let sair_item = MenuItem::with_id(app, "sair", "Sair do Fila (encerrar o programa)", true, None::<&str>)?;
    let menu = Menu::with_items(
        app,
        &[&abrir, &PredefinedMenuItem::separator(app)?, &som, &fundo, &PredefinedMenuItem::separator(app)?, &sair_item],
    )?;
    let mut builder = TrayIconBuilder::with_id("fila")
        .tooltip("Fila Gestão")
        .menu(&menu)
        .show_menu_on_left_click(false)
        .on_menu_event(|app, ev| match ev.id().as_ref() {
            "abrir" => mostrar(app),
            "som" => alternar_som(app),
            "segundo_plano" => alternar_segundo_plano(app),
            "sair" => sair(app),
            _ => {}
        })
        .on_tray_icon_event(|tray, ev| {
            if let TrayIconEvent::Click { button: MouseButton::Left, button_state: MouseButtonState::Up, .. } = ev {
                mostrar(tray.app_handle());
            }
        });
    if let Some(icon) = app.default_window_icon() {
        builder = builder.icon(icon.clone());
    }
    builder.build(app)?;
    if let Ok(mut m) = sp.menu_som.lock() {
        *m = Some(som);
    }
    if let Ok(mut m) = sp.menu_fundo.lock() {
        *m = Some(fundo);
    }
    sp.bandeja.store(true, Ordering::SeqCst);
    Ok(())
}

fn alternar_som(app: &AppHandle) {
    let sp = app.state::<SegundoPlano>();
    let mut p = sp.prefs();
    p.som = !p.som;
    sp.salvar(&p);
    if let Ok(m) = sp.menu_som.lock() {
        if let Some(item) = m.as_ref() {
            let _ = item.set_checked(p.som);
        }
    }
    if let Some(w) = app.get_webview_window("principal") {
        aplicar_som(&w, p.som);
    }
}

/// Grava a preferência de som na página da gestão aberta agora (se for a origem do painel).
pub fn aplicar_som(w: &WebviewWindow, on: bool) {
    if let Ok(url) = w.url() {
        if crate::na_origem(&url) {
            let _ = w.eval(script_som(on));
        }
    }
}

fn alternar_segundo_plano(app: &AppHandle) {
    let sp = app.state::<SegundoPlano>();
    let mut p = sp.prefs();
    p.segundo_plano = !p.segundo_plano;
    let explicar = p.segundo_plano && !p.explicado;
    if explicar {
        p.explicado = true;
    }
    sp.salvar(&p);
    if let Ok(m) = sp.menu_fundo.lock() {
        if let Some(item) = m.as_ref() {
            let _ = item.set_checked(p.segundo_plano);
        }
    }
    if explicar {
        // Não bloqueia o laço de eventos: a mensagem fecha sozinha com OK.
        app.dialog().message(EXPLICACAO).title("Fila Gestão em segundo plano").kind(MessageDialogKind::Info).show(|_| {});
    }
}

#[cfg(test)]
mod testes {
    use super::*;

    #[test]
    fn padrao_desligado_e_som_ligado() {
        let p = Preferencias::default();
        assert!(!p.segundo_plano && p.som && !p.explicado);
        // Arquivo antigo/parcial: campos que faltam ficam no padrão.
        let lido: Preferencias = serde_json::from_str(r#"{"som":false}"#).unwrap();
        assert_eq!(lido, Preferencias { segundo_plano: false, som: false, explicado: false });
    }

    #[test]
    fn preferencia_persiste_e_so_esconde_com_bandeja() {
        let dir = std::env::temp_dir().join(format!("fila-prefs-{}", std::process::id()));
        let arq = dir.join("segundo-plano.json");
        let sp = SegundoPlano::carregar(Some(arq.clone()));
        assert!(!sp.esconder_ao_fechar(), "padrão: fechar encerra");
        sp.salvar(&Preferencias { segundo_plano: true, ..Preferencias::default() });
        assert!(!sp.esconder_ao_fechar(), "sem bandeja nunca esconde");
        sp.bandeja.store(true, Ordering::SeqCst);
        assert!(sp.esconder_ao_fechar());
        sp.saindo.store(true, Ordering::SeqCst);
        assert!(!sp.esconder_ao_fechar(), "Sair do Fila encerra de verdade");
        let de_novo = SegundoPlano::carregar(Some(arq));
        assert!(de_novo.prefs().segundo_plano, "preferência lida do arquivo");
        let _ = fs::remove_dir_all(dir);
    }

    #[test]
    fn script_de_som_so_grava_a_preferencia() {
        let s = script_som(false);
        assert!(s.contains("'fila-notificacoes-som','off'") && s.contains("definirSom(false)"));
        assert!(!s.contains("fetch") && !s.contains("__TAURI__"));
    }
}
