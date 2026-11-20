// Package midia cuida das imagens enviadas pelas pessoas: avatar pessoal (privado), foto pública
// do profissional e logo da loja (públicas). ADR-0028.
//
// Estratégia: processar uma vez no envio e servir variantes prontas.
//   - O envio guarda o original num staging privado e cria um registro "processing" (202).
//   - O worker decodifica com limites, aplica a orientação EXIF, recorta, reduz (sem ampliar) e
//     recodifica (JPEG ou PNG). Metadados (inclusive localização) não sobrevivem à recodificação.
//     O original é apagado; o registro vira "ready" só com todas as variantes gravadas.
//   - O módulo dono da referência (identidade: avatar; estabelecimento: logo e foto profissional)
//     "reivindica" o registro na própria transação (Claim), conferindo dono, finalidade e loja.
//   - Trocar ou remover libera o registro (Release); a limpeza apaga os arquivos depois.
//
// Visibilidade vem da finalidade: avatar é sempre privado; foto profissional e logo são públicas.
// Uma imagem privada nunca vira pública trocando o id: a publicação copia as variantes para um
// registro novo (CopyAsPublic).
//
// O banco guarda só metadados e chaves aleatórias; os bytes ficam no armazenamento (Storage).
// Este módulo é dono da tabela media_asset.
package midia
