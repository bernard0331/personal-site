package coarse

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func team(dps float64, element string, members ...string) Result {
	return Result{Key: TeamKey(members) + element, Team: members, DPS: dps,
		ElementDPS: map[string]float64{element: dps}}
}

func names(rs []Result) []string {
	var out []string
	for _, r := range rs {
		out = append(out, TeamKey(r.Team))
	}
	return out
}

func TestSelectKeepsBestRotationPerTeam(t *testing.T) {
	results := []Result{
		team(100, "pyro", "a", "b", "c", "d"),
		team(300, "pyro", "d", "c", "b", "a"), // same team, better rotation
		{Key: "failed", Team: []string{"e", "f", "g", "h"}, DPS: 999, Error: "boom"},
	}
	got := Select(results, Picks{Top: 10})
	if len(got) != 1 || got[0].DPS != 300 {
		t.Errorf("want only the 300 DPS rotation, got %+v", got)
	}
}

func TestSelectUnion(t *testing.T) {
	results := []Result{
		team(500, "pyro", "a", "b", "c", "d"),
		team(400, "pyro", "a", "b", "c", "e"),
		team(300, "hydro", "a", "b", "c", "f"),
		team(200, "pyro", "a", "b", "c", "g"),
		team(100, "pyro", "a", "b", "h", "i"),
	}
	// Top 1 takes abcd; the best hydro team takes abcf; per character 1 adds
	// abce (best for e), abcg (best for g) and abhi (best for h and i).
	got := names(Select(results, Picks{Top: 1, PerElement: 1}))
	if want := []string{"a,b,c,d", "a,b,c,f"}; !reflect.DeepEqual(got, want) {
		t.Errorf("top + per element: got %v, want %v", got, want)
	}
	got = names(Select(results, Picks{PerCharacter: 1}))
	if len(got) != 5 {
		t.Errorf("every team holds some character's best: got %v", got)
	}
	got = names(Select(results, Picks{}))
	if len(got) != 0 {
		t.Errorf("no picks should choose nothing: %v", got)
	}
}

func TestMainElement(t *testing.T) {
	r := Result{ElementDPS: map[string]float64{"pyro": 10, "cryo": 30, "physical": 5}}
	if MainElement(r) != "cryo" {
		t.Errorf("got %s", MainElement(r))
	}
}

func TestReadResultsLastLineWins(t *testing.T) {
	path := filepath.Join(t.TempDir(), "r.jsonl")
	text := `{"key":"a","dps":1,"error":"boom"}
{"key":"b","dps":2}
{"key":"a","dps":3}
{"key":"c","dps":4,"te`
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	rs, err := ReadResults(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 2 || rs[0].Key != "a" || rs[0].DPS != 3 || rs[0].Error != "" || rs[1].DPS != 2 {
		t.Errorf("got %+v", rs)
	}
}

func TestOptimiserFingerprintDependsOnMode(t *testing.T) {
	b := &Binary{fingerprint: "abc"}
	kqm, top := &Optimiser{Bin: b}, &Optimiser{Bin: b, Options: SubstatModes["top1"]}
	if kqm.Fingerprint() == top.Fingerprint() || kqm.Fingerprint() == b.Fingerprint() {
		t.Errorf("fingerprints must differ: %q %q %q", b.Fingerprint(), kqm.Fingerprint(), top.Fingerprint())
	}
}

func TestSummariseKeepsStatsWhenAsked(t *testing.T) {
	raw := []byte(`{"config_file": "x char lvl=90/90;\nx add stats hp=4780 atk=311;\nx add stats cr=0.0331*10;\noptions iteration=10;",
	  "statistics": {"dps": {"mean": 1}}}`)
	j := job("a", "cfg", "x", "y", "z", "w")
	r, _ := Summarise(j, raw)
	if r.Stats != nil {
		t.Errorf("stats kept without KeepStats: %q", r.Stats)
	}
	j.KeepStats = true
	r, _ = Summarise(j, raw)
	if want := []string{"x add stats hp=4780 atk=311;", "x add stats cr=0.0331*10;"}; !reflect.DeepEqual(r.Stats, want) {
		t.Errorf("got %q", r.Stats)
	}
}
