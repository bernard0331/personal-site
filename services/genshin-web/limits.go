package main

import (
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// limiter holds each visitor, told apart by IP address, to one run at a time
// and a number of runs per hour. The site is open to anyone with the link, so
// without it one visitor could fill the queue for everyone. The queue's own
// size still bounds the work all visitors together can ask for.
type limiter struct {
	perHour    int
	trustProxy bool // read the client's address from X-Forwarded-For (behind Cloud Run's front end)

	mu     sync.Mutex
	active map[string]string      // ip -> its latest run id
	recent map[string][]time.Time // ip -> when its runs in the last hour started
}

func newLimiter(perHour int, trustProxy bool) *limiter {
	return &limiter{perHour: perHour, trustProxy: trustProxy, active: map[string]string{}, recent: map[string][]time.Time{}}
}

// clientIP is the visitor's address. Behind a proxy that appends the address
// it saw (Cloud Run does), the first X-Forwarded-For entry is the client's; it
// is only trusted when the server is told it runs behind such a proxy, since
// anyone can send the header.
func (l *limiter) clientIP(r *http.Request) string {
	if l.trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			first, _, _ := strings.Cut(xff, ",")
			return strings.TrimSpace(first)
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// allow reports why ip may not start a run now, or "" if it may. finished says
// whether a run is over (done or failed).
func (l *limiter) allow(ip string, finished func(id string) bool) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if id, ok := l.active[ip]; ok && !finished(id) {
		return "you already have a run going; wait for it to finish"
	}
	l.prune(ip)
	if l.perHour > 0 && len(l.recent[ip]) >= l.perHour {
		wait := time.Until(l.recent[ip][0].Add(time.Hour)).Round(time.Minute)
		return fmt.Sprintf("that's %d runs in the last hour, the most allowed; try again in %s", l.perHour, max(wait, time.Minute))
	}
	return ""
}

// record notes that ip started run id.
func (l *limiter) record(ip, id string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.active[ip] = id
	l.recent[ip] = append(l.recent[ip], time.Now())
}

// prune drops starts older than an hour (with the lock held).
func (l *limiter) prune(ip string) {
	cut := time.Now().Add(-time.Hour)
	ts := l.recent[ip]
	for len(ts) > 0 && ts[0].Before(cut) {
		ts = ts[1:]
	}
	if len(ts) == 0 {
		delete(l.recent, ip)
		return
	}
	l.recent[ip] = ts
}
