package main

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bernard0331/personal-site/services/genshin-calc/catalog"
)

func TestSnapshotValidation(t *testing.T) {
	cat, _ := catalog.Load()
	ok := func() Snapshot {
		return Snapshot{Characters: map[string]int{"arlecchino": 1, "bennett": 6},
			Weapons: map[string][]int{"favoniuslance": {5, 1}}, Energy: 2, Pins: []string{"arlecchino"}}
	}
	if err := (&Snapshot{}).validate(cat); err == nil {
		t.Error("an empty account must be refused")
	}
	s := ok()
	if err := s.validate(cat); err != nil {
		t.Fatalf("valid snapshot refused: %v", err)
	}
	bad := map[string]func(*Snapshot){
		"unknown character":   func(s *Snapshot) { s.Characters["notacharacter"] = 0 },
		"unreleased":          func(s *Snapshot) { s.Characters["danica"] = 0 },
		"aether":              func(s *Snapshot) { s.Characters["aetheranemo"] = 0 },
		"constellation 7":     func(s *Snapshot) { s.Characters["bennett"] = 7 },
		"refinement 6":        func(s *Snapshot) { s.Weapons["favoniuslance"] = []int{6} },
		"unknown weapon":      func(s *Snapshot) { s.Weapons["notaweapon"] = []int{1} },
		"energy 3":            func(s *Snapshot) { s.Energy = 3 },
		"pin not owned":       func(s *Snapshot) { s.Pins = []string{"zibai"} },
		"pinned and excluded": func(s *Snapshot) { s.Excluded = []string{"arlecchino"} },
		"unknown mode":        func(s *Snapshot) { s.Mode = "overnight" },
		"top 1% quick":        func(s *Snapshot) { s.Artifacts = "top1" },
		"unknown artifacts":   func(s *Snapshot) { s.Mode, s.Artifacts = "light", "mine" },
		"settings not thorough": func(s *Snapshot) {
			s.Mode, s.Refine = "standard", &RefineOptions{Top: 10, Iterations: 1000}
		},
		"too many teams": func(s *Snapshot) {
			s.Mode, s.Refine = "thorough", &RefineOptions{Top: 5000, Iterations: 1000}
		},
		"odd iterations": func(s *Snapshot) {
			s.Mode, s.Refine = "thorough", &RefineOptions{Top: 10, Iterations: 999999}
		},
	}
	good := map[string]func(*Snapshot){
		"light":           func(s *Snapshot) { s.Mode = "light" },
		"standard top 1%": func(s *Snapshot) { s.Mode, s.Artifacts = "standard", "top1" },
		"thorough settings": func(s *Snapshot) {
			s.Mode, s.Refine = "thorough", &RefineOptions{Top: 50, PerElement: 5, Iterations: 500, SkipWaiting: true}
		},
	}
	for name, f := range good {
		s := ok()
		f(&s)
		if err := s.validate(cat); err != nil {
			t.Errorf("%s: refused: %v", name, err)
		}
	}
	for name, f := range bad {
		s := ok()
		f(&s)
		if err := s.validate(cat); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestCreateAndGetRun(t *testing.T) {
	cat, _ := catalog.Load()
	rs := newRuns(cat, t.TempDir(), 1)
	post := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		rs.create(rec, httptest.NewRequest("POST", "/api/runs", strings.NewReader(body)))
		return rec
	}
	rec := post(`{"characters":{"arlecchino":1},"weapons":{},"energy":2}`)
	if rec.Code != 202 {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	var created struct{ ID string }
	json.Unmarshal(rec.Body.Bytes(), &created)
	if !runID.MatchString(created.ID) {
		t.Fatalf("id %q", created.ID)
	}
	if rec := post(`{"characters":{"arlecchino":1},"energy":2}`); rec.Code != 503 {
		t.Errorf("a full queue must turn runs away, got %d", rec.Code)
	}
	if rec := post(`not json`); rec.Code != 400 {
		t.Errorf("bad body: %d", rec.Code)
	}
	get := func(id string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/api/runs/"+id, nil)
		req.SetPathValue("id", id)
		rs.get(rec, req)
		return rec
	}
	if rec := get(created.ID); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"state":"queued"`) {
		t.Errorf("get: %d %s", rec.Code, rec.Body.String())
	}
	for _, id := range []string{"../../etc/passwd", "abc", strings.Repeat("a", 26)} {
		if rec := get(id); rec.Code != 404 {
			t.Errorf("%q: %d", id, rec.Code)
		}
	}
}
