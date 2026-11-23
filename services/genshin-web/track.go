package main

import (
	"net/http"
	"regexp"
)

// trackHeader names the version of the site that answered, during a canary
// release (deploy/gke/canary.sh). A run lives only on the version that
// started it (each version holds its run queue in memory and keeps its runs
// in its own bucket folder), so the page remembers each run's version and
// sends it back in the same header when it asks about that run; the
// Gateway's route sends X-Track: canary to the canary and X-Track: stable to
// stable, and splits everything else by weight.
//
// A header, not a cookie: the route can then use exact header matches.
// Matching a cookie needs a regular expression, and Google's load balancer
// allows only one per URL map (found on the live cluster, 2026-11-23).
const trackHeader = "X-Track"

var trackName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}$`)

// withTrack sets X-Track on every response, naming this version. An empty
// track (Cloud Run, local runs) sets nothing.
func withTrack(track string, next http.Handler) http.Handler {
	if track == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(trackHeader, track)
		next.ServeHTTP(w, r)
	})
}
