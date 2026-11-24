// faultserver answers every request with an error, except its health check.
//
// It exists to test alerting on GKE: deployed as the canary of a site
// (deploy/gke/fault.yaml), it passes the load balancer's health check and so
// takes its share of traffic, then fails it, which spends the site's error
// budget the way a broken release would. (A canary with no ready pods doesn't
// work for this: the load balancer simply sends its share to stable.)
package main

import (
	"flag"
	"log"
	"net/http"
)

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	code := flag.Int("code", http.StatusServiceUnavailable, "status for every request except /healthz")
	flag.Parse()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "injected fault", *code)
	})
	log.Printf("faultserver on %s, answering %d", *addr, *code)
	log.Fatal(http.ListenAndServe(*addr, mux))
}
