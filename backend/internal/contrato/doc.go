// Package contrato contém os tipos Go gerados a partir de docs/contrato/openapi.yaml (ADR-0008).
//
// Não edite types.gen.go à mão. Depois de mudar o OpenAPI, rode na raiz do repositório:
//
//	go -C backend generate ./internal/contrato
//
// O CI falha se o arquivo gerado estiver desatualizado.
package contrato

//go:generate go tool oapi-codegen -config oapi-codegen.yaml ../../../docs/contrato/openapi.yaml
