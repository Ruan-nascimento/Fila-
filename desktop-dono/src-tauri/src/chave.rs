//! Chave da instalação (P-256) e prova de posse DPoP (RFC 9449).
//!
//! - A chave privada é criada aqui, no componente nativo, e fica no cofre do sistema operacional
//!   (Gerenciador de Credenciais do Windows; Secret Service no Linux). O JavaScript nunca a vê.
//! - A prova é um JWS compacto ES256 (RFC 7515/7518) com `typ: dpop+jwt` e a chave pública no
//!   cabeçalho `jwk`. A assinatura é a ECDSA P-256/SHA-256 da RustCrypto (crate p256): nenhum
//!   algoritmo próprio; aqui só se monta o envelope padronizado.
//! - Não há função genérica de assinatura exposta: só `prova(método, url, token)`, chamada pelos
//!   pedidos tipados de `api.rs`.

use base64::{engine::general_purpose::URL_SAFE_NO_PAD as B64, Engine};
use p256::ecdsa::{signature::Signer, Signature, SigningKey};
use rand_core::{OsRng, RngCore};
use serde_json::{json, Value};
use sha2::{Digest, Sha256};

const SERVICO: &str = "br.fila.gestao";

fn entrada(origem: &str) -> Result<keyring::Entry, String> {
    keyring::Entry::new(SERVICO, &format!("instalacao:{origem}")).map_err(erro_cofre)
}

fn erro_cofre(e: keyring::Error) -> String {
    format!(
        "O cofre de senhas do sistema não está disponível ({e}). No Linux, o Fila Gestão usa o \
         chaveiro da sessão (GNOME Keyring/KWallet): confira se ele está ativo."
    )
}

/// Lê a chave guardada (None = este computador ainda não foi ativado).
pub fn carregar(origem: &str) -> Result<Option<SigningKey>, String> {
    match entrada(origem)?.get_password() {
        Ok(b64) => {
            let bytes = B64.decode(b64.trim()).map_err(|_| "chave local corrompida".to_string())?;
            SigningKey::from_slice(&bytes).map(Some).map_err(|_| "chave local corrompida".into())
        }
        Err(keyring::Error::NoEntry) => Ok(None),
        Err(e) => Err(erro_cofre(e)),
    }
}

/// Cria um par novo e guarda a privada no cofre (substitui a anterior, se houver).
pub fn criar(origem: &str) -> Result<SigningKey, String> {
    let chave = SigningKey::random(&mut OsRng);
    entrada(origem)?.set_password(&B64.encode(chave.to_bytes())).map_err(erro_cofre)?;
    Ok(chave)
}

/// Apaga a chave local (computador revogado: será preciso ativar de novo com código novo).
pub fn apagar(origem: &str) -> Result<(), String> {
    match entrada(origem)?.delete_credential() {
        Ok(()) | Err(keyring::Error::NoEntry) => Ok(()),
        Err(e) => Err(erro_cofre(e)),
    }
}

/// Chave pública no formato JWK (RFC 7517/7518).
pub fn jwk(chave: &SigningKey) -> Value {
    let ponto = chave.verifying_key().to_encoded_point(false);
    json!({
        "kty": "EC",
        "crv": "P-256",
        "x": B64.encode(ponto.x().expect("ponto não comprimido")),
        "y": B64.encode(ponto.y().expect("ponto não comprimido")),
    })
}

/// Monta a prova DPoP para um pedido. `iat` é o instante já corrigido pelo relógio do servidor.
pub fn prova(chave: &SigningKey, metodo: &str, htu: &str, iat: i64, token: Option<&str>) -> String {
    let mut jti = [0u8; 16];
    OsRng.fill_bytes(&mut jti);
    let cabecalho = json!({ "typ": "dpop+jwt", "alg": "ES256", "jwk": jwk(chave) });
    let mut corpo = json!({
        "jti": jti.iter().map(|b| format!("{b:02x}")).collect::<String>(),
        "htm": metodo,
        "htu": htu,
        "iat": iat,
    });
    if let Some(t) = token {
        corpo["ath"] = Value::String(B64.encode(Sha256::digest(t.as_bytes())));
    }
    let entrada = format!("{}.{}", B64.encode(cabecalho.to_string()), B64.encode(corpo.to_string()));
    let assinatura: Signature = chave.sign(entrada.as_bytes());
    format!("{entrada}.{}", B64.encode(assinatura.to_bytes()))
}

#[cfg(test)]
mod testes {
    use super::*;
    use p256::ecdsa::{signature::Verifier, VerifyingKey};

    #[test]
    fn prova_es256_verificavel_com_a_chave_publica_do_cabecalho() {
        let chave = SigningKey::random(&mut OsRng);
        let p = prova(&chave, "GET", "https://painel.exemplo/v1/desktop/context", 1_800_000_000, Some("tok"));
        let partes: Vec<&str> = p.split('.').collect();
        assert_eq!(partes.len(), 3);
        let cab: Value = serde_json::from_slice(&B64.decode(partes[0]).unwrap()).unwrap();
        assert_eq!(cab["typ"], "dpop+jwt");
        assert_eq!(cab["alg"], "ES256");
        let corpo: Value = serde_json::from_slice(&B64.decode(partes[1]).unwrap()).unwrap();
        assert_eq!(corpo["htm"], "GET");
        assert_eq!(corpo["ath"], B64.encode(Sha256::digest(b"tok")));
        // Reconstrói a chave pública só a partir do cabeçalho, como o servidor faz.
        let x = B64.decode(cab["jwk"]["x"].as_str().unwrap()).unwrap();
        let y = B64.decode(cab["jwk"]["y"].as_str().unwrap()).unwrap();
        let mut sec1 = vec![4u8];
        sec1.extend(x);
        sec1.extend(y);
        let pubk = VerifyingKey::from_sec1_bytes(&sec1).unwrap();
        let sig = Signature::from_slice(&B64.decode(partes[2]).unwrap()).unwrap();
        pubk.verify(format!("{}.{}", partes[0], partes[1]).as_bytes(), &sig).unwrap();
        // jti nunca se repete.
        let outra = prova(&chave, "GET", "https://painel.exemplo/v1/desktop/context", 1_800_000_000, None);
        assert_ne!(partes[1], outra.split('.').nth(1).unwrap());
    }
}
