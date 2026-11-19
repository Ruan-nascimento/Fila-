//! Pedidos nativos à API, só para o painel fixado na compilação e só operações tipadas do
//! Dashboard (ativar, vincular, contexto, consultas analíticas, fluxo de invalidação, sair). Não
//! existe pedido livre: cada comando monta o caminho a partir de enums e valores validados.
//!
//! O token de acesso fica só na memória deste processo (some ao fechar o programa). A chave
//! privada fica no cofre do sistema (chave.rs).

use std::{
    io::{BufRead, BufReader},
    sync::{
        atomic::{AtomicBool, AtomicI64, AtomicU64, Ordering},
        Arc, Mutex,
    },
    time::{Duration, SystemTime, UNIX_EPOCH},
};

use p256::ecdsa::SigningKey;
use serde::{Deserialize, Serialize};
use serde_json::{json, Value};
use tauri::{AppHandle, Emitter, Manager};

use crate::chave;

/// Erro devolvido ao Dashboard (mesmo formato do Problem do servidor, sem detalhes internos).
#[derive(Debug, Serialize, Clone)]
pub struct ErroApi {
    pub status: u16,
    pub code: String,
    pub detail: String,
}

impl ErroApi {
    fn local(code: &str, detail: impl Into<String>) -> Self {
        ErroApi { status: 0, code: code.into(), detail: detail.into() }
    }
}

impl From<String> for ErroApi {
    fn from(s: String) -> Self {
        ErroApi::local("local", s)
    }
}

/// Instalação registrada neste computador (dado não secreto, guardado em arquivo).
#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct Instalacao {
    pub id: String,
    pub label: String,
}

pub struct Estado {
    pub origem: &'static str,
    agente: ureq::Agent,
    /// Diferença (s) entre o relógio do servidor e o deste computador (cabeçalho Date).
    desvio: AtomicI64,
    token: Mutex<Option<String>>,
    /// Ticket entregue pela gestão (interceptado na navegação), ainda não trocado.
    pub ticket: Mutex<Option<(String, String)>>,
    /// Geração do fluxo: incrementar encerra o fluxo anterior.
    fluxo: AtomicU64,
}

impl Estado {
    pub fn new(origem: &'static str) -> Self {
        let agente: ureq::Agent = ureq::Agent::config_builder()
            .timeout_global(Some(Duration::from_secs(20)))
            .http_status_as_error(false)
            .build()
            .into();
        Estado {
            origem,
            agente,
            desvio: AtomicI64::new(0),
            token: Mutex::new(None),
            ticket: Mutex::new(None),
            fluxo: AtomicU64::new(0),
        }
    }

    fn agora(&self) -> i64 {
        let local = SystemTime::now().duration_since(UNIX_EPOCH).map(|d| d.as_secs() as i64).unwrap_or(0);
        local + self.desvio.load(Ordering::Relaxed)
    }

    fn ajustar_relogio(&self, data: Option<&str>) {
        if let Some(t) = data.and_then(|d| httpdate::parse_http_date(d).ok()) {
            let servidor = t.duration_since(UNIX_EPOCH).map(|d| d.as_secs() as i64).unwrap_or(0);
            let local = SystemTime::now().duration_since(UNIX_EPOCH).map(|d| d.as_secs() as i64).unwrap_or(0);
            self.desvio.store(servidor - local, Ordering::Relaxed);
        }
    }

    pub fn tem_token(&self) -> bool {
        self.token.lock().map(|t| t.is_some()).unwrap_or(false)
    }

    /// Limpa o acesso em memória (sessão encerrada, sair, troca de identidade).
    pub fn esquecer_token(&self) {
        if let Ok(mut t) = self.token.lock() {
            *t = None;
        }
        self.fluxo.fetch_add(1, Ordering::SeqCst);
    }

    /// Pedido com prova DPoP. `caminho` sempre começa com /v1/ e é montado por este módulo.
    fn pedir(&self, chave: &SigningKey, metodo: &str, caminho: &str, corpo: Option<Value>) -> Result<Value, ErroApi> {
        debug_assert!(caminho.starts_with("/v1/"));
        let url = format!("{}{}", self.origem, caminho);
        let htu = format!("{}{}", self.origem, caminho.split('?').next().unwrap_or(caminho));
        let token = self.token.lock().map_err(|_| ErroApi::local("local", "estado interno"))?.clone();
        let prova = chave::prova(chave, metodo, &htu, self.agora(), token.as_deref());
        let resposta = match metodo {
            "GET" => {
                let mut r = self.agente.get(&url).header("DPoP", &prova);
                if let Some(t) = &token {
                    r = r.header("Authorization", &format!("DPoP {t}"));
                }
                r.call()
            }
            _ => {
                let mut r = self
                    .agente
                    .post(&url)
                    .header("DPoP", &prova)
                    .header("Origin", self.origem)
                    .header("Content-Type", "application/json");
                if let Some(t) = &token {
                    r = r.header("Authorization", &format!("DPoP {t}"));
                }
                r.send(corpo.unwrap_or(json!({})).to_string())
            }
        };
        let mut resposta = resposta.map_err(|e| ErroApi::local("offline", format!("Sem conexão com o painel. ({e})")))?;
        self.ajustar_relogio(resposta.headers().get("date").and_then(|v| v.to_str().ok()));
        let status = resposta.status().as_u16();
        let texto = resposta.body_mut().read_to_string().unwrap_or_default();
        if status == 204 {
            return Ok(Value::Null);
        }
        let valor: Value = serde_json::from_str(&texto).unwrap_or(Value::Null);
        if (200..300).contains(&status) {
            return Ok(valor);
        }
        let code = valor["code"].as_str().unwrap_or("erro").to_string();
        if status == 401 {
            self.esquecer_token(); // a sessão ou o vínculo acabaram: nada fica em memória
        }
        Err(ErroApi {
            status,
            code,
            detail: valor["detail"].as_str().unwrap_or("O painel recusou o pedido.").to_string(),
        })
    }

    /// Ativa este computador com o código do operador: cria a chave e a registra no servidor.
    pub fn ativar(&self, codigo: &str) -> Result<Value, ErroApi> {
        let codigo: String = codigo.chars().filter(|c| c.is_ascii_alphanumeric() || *c == '-').take(24).collect();
        if codigo.len() < 16 {
            return Err(ErroApi::local("validation_failed", "Digite o código completo (XXXX-XXXX-XXXX-XXXX)."));
        }
        let chave = chave::criar(self.origem)?;
        match self.pedir(&chave, "POST", "/v1/desktop/installations/activate", Some(json!({ "code": codigo }))) {
            Ok(v) => Ok(v["data"].clone()),
            Err(e) => {
                let _ = chave::apagar(self.origem); // chave sem registro não serve para nada
                Err(e)
            }
        }
    }

    /// Troca o ticket entregue pela gestão por um acesso preso à chave (mesma sessão de dono).
    pub fn vincular(&self, chave: &SigningKey) -> Result<Value, ErroApi> {
        let pendente = self.ticket.lock().map_err(|_| ErroApi::local("local", "estado interno"))?.take();
        let Some((ticket, _loja)) = pendente else {
            return Err(ErroApi::local("desktop_link_invalid", "Abra o Dashboard pela gestão (menu Dashboard)."));
        };
        self.esquecer_token();
        let v = self.pedir(chave, "POST", "/v1/desktop/bindings", Some(json!({ "ticket": ticket })))?;
        let token = v["data"]["access_token"].as_str().unwrap_or_default().to_string();
        if let Ok(mut t) = self.token.lock() {
            *t = Some(token);
        }
        self.contexto(chave)
    }

    pub fn contexto(&self, chave: &SigningKey) -> Result<Value, ErroApi> {
        Ok(self.pedir(chave, "GET", "/v1/desktop/context", None)?["data"].clone())
    }

    pub fn consultar(&self, chave: &SigningKey, c: &Consulta) -> Result<Value, ErroApi> {
        let caminho = c.caminho()?;
        self.pedir(chave, "GET", &caminho, None)
    }

    pub fn sair(&self, chave: &SigningKey) {
        let _ = self.pedir(chave, "POST", "/v1/desktop/bindings/current/end", None);
        self.esquecer_token();
    }

    /// Abre o fluxo SSE de invalidação numa thread; eventos vão para a janela como
    /// "dashboard://fluxo". Uma geração nova (troca de loja, sair) encerra a anterior.
    pub fn iniciar_fluxo(self: &Arc<Self>, app: AppHandle, chave: SigningKey, loja: uuid::Uuid) {
        let geracao = self.fluxo.fetch_add(1, Ordering::SeqCst) + 1;
        let estado = Arc::clone(self);
        let parar = Arc::new(AtomicBool::new(false));
        std::thread::spawn(move || {
            let mut espera = 1u64;
            while estado.fluxo.load(Ordering::SeqCst) == geracao && !parar.load(Ordering::SeqCst) {
                let caminho = format!("/v1/desktop/analytics/establishments/{loja}/stream");
                let token = estado.token.lock().ok().and_then(|t| t.clone());
                let Some(token) = token else { break };
                let htu = format!("{}{}", estado.origem, caminho);
                let prova = chave::prova(&chave, "GET", &htu, estado.agora(), Some(&token));
                let agente: ureq::Agent = ureq::Agent::config_builder()
                    .timeout_global(None)
                    .timeout_recv_body(Some(Duration::from_secs(45))) // heartbeat a cada 15 s
                    .http_status_as_error(false)
                    .build()
                    .into();
                let r = agente
                    .get(&format!("{}{}", estado.origem, caminho))
                    .header("DPoP", &prova)
                    .header("Authorization", &format!("DPoP {token}"))
                    .header("Accept", "text/event-stream")
                    .call();
                match r {
                    Ok(resp) if resp.status().as_u16() == 200 => {
                        espera = 1;
                        let _ = app.emit_to("principal", "dashboard://fluxo", json!({ "event": "conectado", "loja": loja }));
                        let leitor = BufReader::new(resp.into_body().into_reader());
                        let mut evento = String::new();
                        for linha in leitor.lines() {
                            if estado.fluxo.load(Ordering::SeqCst) != geracao {
                                return;
                            }
                            let Ok(linha) = linha else { break };
                            if let Some(e) = linha.strip_prefix("event: ") {
                                evento = e.to_string();
                            } else if let Some(d) = linha.strip_prefix("data: ") {
                                let dados: Value = serde_json::from_str(d).unwrap_or(Value::Null);
                                let _ = app.emit_to(
                                    "principal",
                                    "dashboard://fluxo",
                                    json!({ "event": evento, "data": dados, "loja": loja }),
                                );
                                if evento == "session_ended" {
                                    estado.esquecer_token();
                                    return;
                                }
                            }
                        }
                    }
                    Ok(resp) if resp.status().as_u16() == 401 => {
                        estado.esquecer_token();
                        let _ = app.emit_to(
                            "principal",
                            "dashboard://fluxo",
                            json!({ "event": "session_ended", "data": { "reason": "session_revoked" }, "loja": loja }),
                        );
                        return;
                    }
                    _ => {}
                }
                if estado.fluxo.load(Ordering::SeqCst) != geracao {
                    return;
                }
                let _ = app.emit_to("principal", "dashboard://fluxo", json!({ "event": "reconectando", "loja": loja }));
                std::thread::sleep(Duration::from_secs(espera));
                espera = (espera * 2).min(30);
            }
        });
    }

    pub fn parar_fluxo(&self) {
        self.fluxo.fetch_add(1, Ordering::SeqCst);
    }
}

// ---------------------------------------------------------------------------------------------
// Consultas tipadas: só estes caminhos existem.

#[derive(Debug, Deserialize, Clone, Copy)]
#[serde(rename_all = "snake_case")]
pub enum Metrica {
    Cash,
    Ticket,
    Movement,
    Wait,
    Duration,
    Loyalty,
}

#[derive(Debug, Deserialize, Clone, Copy)]
#[serde(rename_all = "snake_case")]
pub enum Dimensao {
    PaymentMethods,
    Outcomes,
    Professionals,
    Services,
    PeakHours,
    WaitBands,
    Pending,
    Rewards,
    Recurrence,
}

#[derive(Debug, Deserialize, Clone, Copy)]
#[serde(rename_all = "snake_case")]
pub enum TipoRegistro {
    Receipts,
    Refunds,
    Orders,
    Participations,
    ServiceItems,
    FinancialIssues,
    LoyaltyEntries,
}

#[derive(Debug, Deserialize, Clone, Copy)]
#[serde(rename_all = "snake_case")]
pub enum Granularidade {
    Hour,
    Day,
    Week,
    Month,
}

#[derive(Debug, Deserialize, Clone, Copy)]
#[serde(rename_all = "snake_case")]
pub enum Meio {
    Pix,
    Cash,
    Card,
}

#[derive(Debug, Deserialize, Clone, Default)]
pub struct Filtros {
    pub from: String,
    pub to: String,
    #[serde(default)]
    pub compare: bool,
    pub professional_id: Option<uuid::Uuid>,
    pub service_id: Option<uuid::Uuid>,
    pub method: Option<Meio>,
}

/// Recorte de um detalhamento (bucket clicado, célula do mapa, faixa, situação…).
#[derive(Debug, Deserialize, Clone, Default)]
pub struct Recorte {
    pub bucket_start: Option<String>,
    pub bucket_end: Option<String>,
    pub cursor: Option<String>,
    pub r#ref: Option<String>,
    pub state: Option<String>,
    pub kind: Option<String>,
    pub status: Option<String>,
    pub dow: Option<u8>,
    pub hour: Option<u8>,
    pub band: Option<String>,
}

#[derive(Debug, Deserialize, Clone)]
#[serde(tag = "tipo", rename_all = "snake_case")]
pub enum Consulta {
    Resumo { loja: uuid::Uuid, filtros: Filtros },
    Serie { loja: uuid::Uuid, metrica: Metrica, granularidade: Option<Granularidade>, filtros: Filtros },
    Detalhamento { loja: uuid::Uuid, dimensao: Dimensao, filtros: Filtros },
    Registros { loja: uuid::Uuid, registro: TipoRegistro, filtros: Filtros, recorte: Recorte },
}

fn nome<T: std::fmt::Debug>(v: T) -> String {
    // PaymentMethods → payment_methods
    let s = format!("{v:?}");
    let mut out = String::new();
    for (i, c) in s.chars().enumerate() {
        if c.is_ascii_uppercase() {
            if i > 0 {
                out.push('_');
            }
            out.push(c.to_ascii_lowercase());
        } else {
            out.push(c);
        }
    }
    out
}

fn data_valida(s: &str) -> bool {
    let b = s.as_bytes();
    b.len() == 10 && b[4] == b'-' && b[7] == b'-' && s.chars().enumerate().all(|(i, c)| i == 4 || i == 7 || c.is_ascii_digit())
}

fn seguro(s: &str, max: usize) -> Result<String, ErroApi> {
    if s.len() > max || !s.chars().all(|c| c.is_ascii_alphanumeric() || "-_:.+=".contains(c)) {
        return Err(ErroApi::local("validation_failed", "valor de filtro inválido"));
    }
    Ok(s.to_string())
}

impl Consulta {
    fn caminho(&self) -> Result<String, ErroApi> {
        let (loja, resto, filtros) = match self {
            Consulta::Resumo { loja, filtros } => (loja, "/summary".to_string(), filtros),
            Consulta::Serie { loja, metrica, filtros, .. } => (loja, format!("/series/{}", nome(metrica)), filtros),
            Consulta::Detalhamento { loja, dimensao, filtros } => (loja, format!("/breakdowns/{}", nome(dimensao)), filtros),
            Consulta::Registros { loja, registro, filtros, .. } => (loja, format!("/records/{}", nome(registro)), filtros),
        };
        if !data_valida(&filtros.from) || !data_valida(&filtros.to) {
            return Err(ErroApi::local("validation_failed", "período inválido"));
        }
        let mut q: Vec<(String, String)> = vec![("from".into(), filtros.from.clone()), ("to".into(), filtros.to.clone())];
        if filtros.compare && !matches!(self, Consulta::Registros { .. }) {
            q.push(("compare".into(), "previous".into()));
        }
        if let Some(p) = filtros.professional_id {
            q.push(("professional_id".into(), p.to_string()));
        }
        if let Some(s) = filtros.service_id {
            q.push(("service_id".into(), s.to_string()));
        }
        if let Some(m) = filtros.method {
            q.push(("method".into(), nome(m)));
        }
        if let Consulta::Serie { granularidade: Some(g), .. } = self {
            q.push(("granularity".into(), nome(g)));
        }
        if let Consulta::Registros { recorte: r, .. } = self {
            for (k, v) in [
                ("bucket_start", &r.bucket_start),
                ("bucket_end", &r.bucket_end),
                ("cursor", &r.cursor),
                ("ref", &r.r#ref),
                ("state", &r.state),
                ("kind", &r.kind),
                ("status", &r.status),
                ("band", &r.band),
            ] {
                if let Some(v) = v {
                    q.push((k.into(), seguro(v, 300)?));
                }
            }
            if let Some(d) = r.dow {
                q.push(("dow".into(), d.min(7).to_string()));
            }
            if let Some(h) = r.hour {
                q.push(("hour".into(), h.min(23).to_string()));
            }
        }
        let consulta: Vec<String> = q.iter().map(|(k, v)| format!("{k}={}", codificar(v))).collect();
        Ok(format!("/v1/desktop/analytics/establishments/{loja}{resto}?{}", consulta.join("&")))
    }
}

fn codificar(v: &str) -> String {
    v.bytes()
        .map(|b| match b {
            b'A'..=b'Z' | b'a'..=b'z' | b'0'..=b'9' | b'-' | b'_' | b'.' | b'~' => (b as char).to_string(),
            _ => format!("%{b:02X}"),
        })
        .collect()
}

/// Arquivo com o id e o nome da instalação (não secreto).
pub fn arquivo_instalacao(app: &AppHandle) -> Option<std::path::PathBuf> {
    app.path().app_data_dir().ok().map(|d| d.join("instalacao.json"))
}

#[cfg(test)]
mod testes {
    use super::*;

    #[test]
    fn caminhos_tipados() {
        let loja = uuid::Uuid::nil();
        let f = Filtros { from: "2026-11-01".into(), to: "2026-11-18".into(), compare: true, method: Some(Meio::Cash), ..Default::default() };
        let c = Consulta::Detalhamento { loja, dimensao: Dimensao::PaymentMethods, filtros: f.clone() };
        assert_eq!(
            c.caminho().unwrap(),
            "/v1/desktop/analytics/establishments/00000000-0000-0000-0000-000000000000/breakdowns/payment_methods?from=2026-11-01&to=2026-11-18&compare=previous&method=cash"
        );
        let r = Consulta::Registros {
            loja,
            registro: TipoRegistro::ServiceItems,
            filtros: f.clone(),
            recorte: Recorte { bucket_start: Some("2026-11-12T03:00:00Z".into()), ..Default::default() },
        };
        assert!(r.caminho().unwrap().contains("/records/service_items?") && r.caminho().unwrap().contains("bucket_start=2026-11-12T03%3A00%3A00Z"));
        let ruim = Consulta::Resumo { loja, filtros: Filtros { from: "../../x".into(), to: "2026-11-18".into(), ..Default::default() } };
        assert!(ruim.caminho().is_err());
        let injecao = Consulta::Registros {
            loja,
            registro: TipoRegistro::Receipts,
            filtros: f,
            recorte: Recorte { kind: Some("a&b=/v1/auth".into()), ..Default::default() },
        };
        assert!(injecao.caminho().is_err());
        // Enums só aceitam os valores do contrato.
        assert!(serde_json::from_str::<Consulta>(r#"{"tipo":"serie","loja":"00000000-0000-0000-0000-000000000000","metrica":"x","filtros":{"from":"2026-11-01","to":"2026-11-02"}}"#).is_err());
    }
}
