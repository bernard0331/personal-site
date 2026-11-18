package main

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLimiterOneRunAtATime(t *testing.T) {
	l := newLimiter(3, false)
	done := map[string]bool{}
	finished := func(id string) bool { return done[id] }
	if why := l.allow("1.2.3.4", finished); why != "" {
		t.Fatalf("first run refused: %s", why)
	}
	l.record("1.2.3.4", "a")
	if why := l.allow("1.2.3.4", finished); !strings.Contains(why, "already") {
		t.Errorf("second run while the first is going: got %q", why)
	}
	if why := l.allow("5.6.7.8", finished); why != "" {
		t.Errorf("another visitor refused: %s", why)
	}
	done["a"] = true
	l.record("1.2.3.4", "b")
	done["b"] = true
	l.record("1.2.3.4", "c")
	done["c"] = true
	if why := l.allow("1.2.3.4", finished); !strings.Contains(why, "3 runs in the last hour") {
		t.Errorf("fourth run in an hour: got %q", why)
	}
}

func TestClientIP(t *testing.T) {
	r := httptest.NewRequest("POST", "/api/runs", nil)
	r.RemoteAddr = "10.0.0.1:5555"
	r.Header.Set("X-Forwarded-For", "203.0.113.7, 10.0.0.9")
	if got := newLimiter(1, false).clientIP(r); got != "10.0.0.1" {
		t.Errorf("not behind a proxy: X-Forwarded-For must be ignored, got %s", got)
	}
	if got := newLimiter(1, true).clientIP(r); got != "203.0.113.7" {
		t.Errorf("behind a proxy: got %s, want the first X-Forwarded-For entry", got)
	}
}
