//! Fixa no programa, na compilação, a origem do painel que a janela pode abrir.
//!
//! Ordem: variável FILA_PAINEL_URL; senão o arquivo ../painel.url (primeira linha sem "#");
//! senão, só em desenvolvimento (tauri dev), o Vite local http://127.0.0.1:5173.
//! Produção (tauri build) exige HTTPS. FILA_PAINEL_TESTE_LOCAL=1 permite, só para testar o
//! instalador nesta máquina, http em 127.0.0.1/localhost; esse pacote nunca deve ser distribuído.

use std::{env, fs, path::Path};
use url::Url;

const DEV_PADRAO: &str = "http://127.0.0.1:5173";

fn main() {
    println!("cargo:rerun-if-env-changed=FILA_PAINEL_URL");
    println!("cargo:rerun-if-env-changed=FILA_PAINEL_TESTE_LOCAL");
    println!("cargo:rerun-if-changed=../painel.url");

    let release = env::var("PROFILE").as_deref() == Ok("release");
    let teste_local = env::var("FILA_PAINEL_TESTE_LOCAL").as_deref() == Ok("1");

    let bruto = env::var("FILA_PAINEL_URL")
        .ok()
        .filter(|v| !v.trim().is_empty())
        .or_else(|| ler_arquivo(Path::new("../painel.url")))
        .or_else(|| (!release).then(|| DEV_PADRAO.to_string()));
    let Some(bruto) = bruto else {
        falhar("defina a URL do painel: FILA_PAINEL_URL=https://seu-dominio ou uma linha em desktop-dono/painel.url");
    };

    let url = Url::parse(bruto.trim()).unwrap_or_else(|e| falhar(&format!("URL do painel inválida ({e}): {bruto}")));
    if !url.username().is_empty() || url.password().is_some() {
        falhar("a URL do painel não pode ter usuário ou senha");
    }
    if url.path() != "/" || url.query().is_some() || url.fragment().is_some() {
        falhar("use só a origem do painel, sem caminho (ex.: https://fila.suabarbearia.com.br)");
    }
    let host = url.host_str().unwrap_or_default().to_ascii_lowercase();
    let loopback = matches!(host.as_str(), "127.0.0.1" | "localhost" | "[::1]");
    let modo = match url.scheme() {
        "https" if release && (host.ends_with("exemplo.com.br") || host.ends_with("example.com")) => {
            falhar("troque o endereço de exemplo pela URL real do painel")
        }
        "https" if release => "producao",
        "https" => "desenvolvimento",
        "http" if loopback && !release => "desenvolvimento",
        "http" if loopback && teste_local => "teste-local",
        "http" if release => falhar("produção só abre HTTPS: use https://… (FILA_PAINEL_TESTE_LOCAL=1 só para teste em 127.0.0.1)"),
        "http" => falhar("http só é aceito para 127.0.0.1 ou localhost"),
        outro => falhar(&format!("esquema não aceito: {outro}")),
    };

    let origem = url.origin().ascii_serialization();
    println!("cargo:rustc-env=FILA_PAINEL_URL={origem}");
    println!("cargo:rustc-env=FILA_PAINEL_MODO={modo}");
    println!("cargo:warning=Fila Gestão: painel {origem} ({modo})");

    // Os comandos do programa só ficam disponíveis para a tela local (ver capabilities/).
    let manifesto = tauri_build::AppManifest::new().commands(&[
        "painel",
        "verificar_painel",
        "estado_dashboard",
        "ativar_instalacao",
        "vincular_sessao",
        "contexto_dashboard",
        "consultar",
        "iniciar_fluxo",
        "parar_fluxo",
        "abrir_gestao",
        "sair_dashboard",
        "esquecer_instalacao",
    ]);
    tauri_build::try_build(tauri_build::Attributes::new().app_manifest(manifesto))
        .expect("falha no tauri-build");
}

fn ler_arquivo(p: &Path) -> Option<String> {
    let texto = fs::read_to_string(p).ok()?;
    texto
        .lines()
        .map(str::trim)
        .find(|l| !l.is_empty() && !l.starts_with('#'))
        .map(str::to_string)
}

fn falhar(msg: &str) -> ! {
    eprintln!("\nFila Gestão: {msg}\n");
    panic!("{msg}");
}
