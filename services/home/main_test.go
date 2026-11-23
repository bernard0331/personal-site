package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCheckAll(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer up.Close()
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer down.Close()

	got := checkAll(context.Background(), &http.Client{Timeout: time.Second}, []site{
		{Name: "a", health: up.URL},
		{Name: "b", health: down.URL},
		{Name: "c", health: "http://127.0.0.1:1/"},
		{Name: "d"},
	})
	want := []string{"up", "down", "down", ""}
	for i, s := range got {
		if s.Status != want[i] {
			t.Errorf("%s: status %q, want %q", s.Name, s.Status, want[i])
		}
	}
}
