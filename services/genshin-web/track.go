package main

import (
	"net/http"
	"regexp"
)

// trackCookie is the cookie that keeps a visitor on one version of the site
// during a canary release (deploy/gke/canary.sh). The Gateway's route sends
// requests carrying track=canary to the canary and track=stable to the
// stable version, and splits only visitors without the cookie by weight.
//
// Stickiness matters here: each version holds its run queue in memory and
// keeps its runs in its own bucket folder, so a run started on one version
// can only be followed on that version.
const trackCookie = "track"

var trackName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}$`)

// withTrack sets the track cookie on every response, naming this version.
// An empty track (Cloud Run, local runs) sets nothing.
func withTrack(track string, next http.Handler) http.Handler {
	if track == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie(trackCookie); err != nil || c.Value != track {
			http.SetCookie(w, &http.Cookie{
				Name:     trackCookie,
				Value:    track,
				Path:     "/",
				MaxAge:   2 * 24 * 60 * 60, // as long as an unfinished run may be resumed
				HttpOnly: true,
				Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
				SameSite: http.SameSiteLaxMode,
			})
		}
		next.ServeHTTP(w, r)
	})
}
