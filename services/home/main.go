// Command home serves the landing page: a short introduction and links to
// the other sites, with each site's status checked from inside the cluster.
//
// The status check is the reason this is a separate service: it calls
// genshin-web's /healthz over the cluster network (service to service),
// which the mesh secures with mutual TLS and authorizes by identity.
package main

import (
	"context"
	"embed"
	"flag"
	"html/template"
	"log"
	"net/http"
	"time"
)

//go:embed index.html
var files embed.FS

var page = template.Must(template.ParseFS(files, "index.html"))

// site is one link on the page.
type site struct {
	Name, Description, URL string
	health                 string // in-cluster health URL; empty: not checked
	Status                 string // "up", "down", or "" when not checked
}

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "address to listen on")
	genshinURL := flag.String("genshin-url", "", "public URL of the Genshin team optimizer")
	genshinHealth := flag.String("genshin-health", "http://genshin-web.genshin.svc.cluster.local/healthz", "the optimizer's health URL inside the cluster")
	flag.Parse()

	sites := []site{{
		Name:        "Genshin Impact team optimizer",
		Description: "Finds an account's strongest teams with no character or weapon used twice, by simulation.",
		URL:         *genshinURL,
		health:      *genshinHealth,
	}}
	client := &http.Client{Timeout: 2 * time.Second}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		out := checkAll(r.Context(), client, sites)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		if err := page.Execute(w, out); err != nil {
			log.Print(err)
		}
	})
	srv := &http.Server{Addr: *addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	log.Printf("listening on http://%s", *addr)
	log.Fatal(srv.ListenAndServe())
}

// checkAll asks each site's health URL, at the same time.
func checkAll(ctx context.Context, client *http.Client, sites []site) []site {
	out := make([]site, len(sites))
	done := make(chan struct{})
	for i, s := range sites {
		go func() {
			defer func() { done <- struct{}{} }()
			out[i] = s
			if s.health == "" {
				return
			}
			out[i].Status = "down"
			if healthy(ctx, client, s.health) {
				out[i].Status = "up"
			}
		}()
	}
	for range sites {
		<-done
	}
	return out
}

func healthy(ctx context.Context, client *http.Client, url string) bool {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("health %s: %v", url, err)
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
