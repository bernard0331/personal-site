package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWithTrack(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})

	// No track: no cookie.
	rec := httptest.NewRecorder()
	withTrack("", ok).ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if c := rec.Result().Cookies(); len(c) != 0 {
		t.Fatalf("cookie without a track: %v", c)
	}

	// A new visitor behind the HTTPS load balancer gets a secure cookie.
	rec = httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	withTrack("canary", ok).ServeHTTP(rec, req)
	cs := rec.Result().Cookies()
	if len(cs) != 1 || cs[0].Name != "track" || cs[0].Value != "canary" || !cs[0].Secure || !cs[0].HttpOnly {
		t.Fatalf("cookie: %+v", cs)
	}

	// A visitor already on this track: nothing to set.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: "track", Value: "canary"})
	withTrack("canary", ok).ServeHTTP(rec, req)
	if c := rec.Result().Cookies(); len(c) != 0 {
		t.Fatalf("cookie set again: %v", c)
	}

	// After a rollback the canary's visitors reach stable and are moved over.
	rec = httptest.NewRecorder()
	withTrack("stable", ok).ServeHTTP(rec, req)
	if cs := rec.Result().Cookies(); len(cs) != 1 || cs[0].Value != "stable" {
		t.Fatalf("not moved to stable: %v", cs)
	}
}
