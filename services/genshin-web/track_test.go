package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWithTrack(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})

	rec := httptest.NewRecorder()
	withTrack("", ok).ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if h := rec.Header().Get("X-Track"); h != "" {
		t.Fatalf("X-Track without a track: %q", h)
	}

	rec = httptest.NewRecorder()
	withTrack("canary", ok).ServeHTTP(rec, httptest.NewRequest("GET", "/api/runs/x", nil))
	if h := rec.Header().Get("X-Track"); h != "canary" {
		t.Fatalf("X-Track = %q, want canary", h)
	}
}
