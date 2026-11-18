// Command genshin-web is the team optimizer's website: it serves the pages and
// a small JSON API, and runs optimizations in the background with the
// genshin-calc packages.
//
// Routes:
//
//	GET /               the single-page app (web/index.html)
//	GET /static/...     the app's scripts and styles (embedded in the binary)
//	GET /img/<name>.png character, weapon and artifact icons, from -img
//	GET /api/catalog    every released character, weapon and artifact set
//	POST /api/good      read an uploaded GOOD file (untrusted) into an account
//	GET /healthz        "ok", for the cluster's health checks
package main

import (
	"embed"
	"flag"
	"io/fs"
	"log"
	"net/http"
	"time"

	"github.com/bernard0331/personal-site/services/genshin-calc/catalog"
)

// web holds the front end. It is embedded so the binary is the whole site,
// apart from the images, which are HoYoverse's and live outside the repo.
//
//go:embed web
var web embed.FS

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "address to listen on")
	imgDir := flag.String("img", "local/web-img", "directory of icons (tools/fetch_images.py fills it)")
	flag.Parse()

	cat, err := catalog.Load()
	if err != nil {
		log.Fatal(err)
	}
	catalogAPI, err := buildCatalog()
	if err != nil {
		log.Fatal(err)
	}

	static, _ := fs.Sub(web, "web")
	mux := http.NewServeMux()
	mux.Handle("GET /static/", noCache(http.StripPrefix("/static/", http.FileServerFS(static))))
	mux.Handle("GET /img/", http.StripPrefix("/img/", imageServer(*imgDir)))
	mux.HandleFunc("GET /api/catalog", catalogAPI.serve)
	mux.HandleFunc("POST /api/good", importGOOD(cat))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFileFS(w, r, static, "index.html")
	})

	srv := &http.Server{
		Addr:              *addr,
		Handler:           logRequests(mux),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("listening on http://%s", *addr)
	log.Fatal(srv.ListenAndServe())
}

// imageServer serves PNG icons from dir. http.Dir refuses paths that climb
// out of the directory, and images may be cached for a day.
func imageServer(dir string) http.Handler {
	files := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=86400")
		files.ServeHTTP(w, r)
	})
}

// noCache makes browsers check with the server before reusing a file, so a
// new version of the page's scripts is never hidden behind an old copy.
func noCache(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		next.ServeHTTP(w, r)
	})
}

// logRequests writes one line per request: method, path, time taken.
func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start).Round(time.Millisecond))
	})
}
