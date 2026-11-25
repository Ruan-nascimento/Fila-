// Command simulador serve o simulador de Pix (pagamentotest.Fake) para o ambiente LOCAL de testes
// de navegador (E2E_PIX_CONTROL). Nunca fala com a AbacatePay; só aceita a chave de teste dada.
//
//	go run ./internal/pagamento/pagamentotest/simulador -addr 127.0.0.1:18090 -key abc_dev_simulador
//
// A API usa FILA_ABACATEPAY_BASE_URL=http://127.0.0.1:18090/v2. Controle (sem chave, só local):
// POST /control/set?externalId=<id da tentativa>&status=PAID|EXPIRED|CANCELLED.
package main

import (
	"flag"
	"log"
	"net/http"
	"strings"
	"time"

	"fila/internal/pagamento/pagamentotest"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:18090", "endereço local")
	key := flag.String("key", "abc_dev_simulador", "chave de teste esperada no Bearer")
	flag.Parse()
	if !strings.HasPrefix(*key, "abc_dev_") {
		log.Fatal("o simulador só aceita chave de teste (abc_dev_)")
	}
	if !strings.HasPrefix(*addr, "127.0.0.1:") && !strings.HasPrefix(*addr, "localhost:") {
		log.Fatal("o simulador só escuta no próprio computador")
	}
	fake := pagamentotest.New(*key, time.Now)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /control/set", func(w http.ResponseWriter, r *http.Request) {
		c := fake.ByExternalID(r.URL.Query().Get("externalId"))
		if c == nil || !fake.Set(c.ID, r.URL.Query().Get("status"), 0) {
			http.Error(w, "cobrança não encontrada", http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.Handle("/", fake)
	log.Printf("simulador de Pix em http://%s (chave de teste)", *addr)
	log.Fatal(http.ListenAndServe(*addr, mux))
}
