// Command mockserver serves the in-memory mock API for trying the provider by hand:
//
//	go run ./internal/mockapi/cmd/mockserver -addr 127.0.0.1:18080 -key upm_test_key
//
// then point the provider at it with endpoint = "http://127.0.0.1:18080" and that key.
package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/uplapse/terraform-provider-uplapse/internal/mockapi"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:18080", "listen address")
	key := flag.String("key", "upm_test_key", "API key the mock accepts")
	flag.Parse()
	srv := mockapi.New(*key)
	log.Printf("mock Uplapse API on http://%s", *addr)
	log.Fatal(http.ListenAndServe(*addr, logRequests(srv)))
}

func logRequests(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s", r.Method, r.URL.Path)
		h.ServeHTTP(w, r)
	})
}
