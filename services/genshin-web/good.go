package main

import (
	"encoding/json"
	"net/http"

	"github.com/bernard0331/personal-site/services/genshin-calc/catalog"
	"github.com/bernard0331/personal-site/services/genshin-calc/good"
)

// goodAccount is an imported account as the page uses it: simulator keys,
// checked ranges, and the problems found in the file.
type goodAccount struct {
	Characters []goodCharacter `json:"characters"`
	Weapons    []goodWeapon    `json:"weapons"`
	Artifacts  []goodArtifact  `json:"artifacts"`
	Problems   []string        `json:"problems,omitempty"`
	Dropped    int             `json:"dropped"`
}

type goodCharacter struct {
	Key   string `json:"key"`
	Cons  int    `json:"cons"`
	Level int    `json:"level"`
}

type goodWeapon struct {
	Key        string `json:"key"`
	Refinement int    `json:"refinement"`
}

type goodArtifact struct {
	Set    string   `json:"set"`
	Slot   string   `json:"slot"`
	Rarity int      `json:"rarity"`
	Level  int      `json:"level"`
	Main   string   `json:"main"`
	Subs   [][2]any `json:"subs"` // [key, value] in GOOD units (percent as 5.8)
}

// importGOOD reads an uploaded GOOD file with the genshin-calc parser, which
// treats it as untrusted: unknown keys and out-of-range values are dropped
// and reported, and the size is capped.
func importGOOD(cat *catalog.Catalog) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, good.MaxBytes+1)
		acc, rep, err := good.Parse(r.Body, cat)
		if err != nil {
			httpError(w, http.StatusBadRequest, err.Error())
			return
		}
		out := goodAccount{Problems: rep.Problems, Dropped: rep.Dropped}
		for _, c := range acc.Characters {
			out.Characters = append(out.Characters, goodCharacter{c.Key, c.Constellation, c.Level})
		}
		for _, wp := range acc.Weapons {
			out.Weapons = append(out.Weapons, goodWeapon{wp.Key, wp.Refinement})
		}
		for _, a := range acc.Artifacts {
			ga := goodArtifact{Set: a.Set, Slot: a.Slot, Rarity: a.Rarity, Level: a.Level, Main: a.MainStat}
			for _, s := range a.Substats {
				ga.Subs = append(ga.Subs, [2]any{s.Key, s.Value})
			}
			out.Artifacts = append(out.Artifacts, ga)
		}
		writeJSON(w, out)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

// httpError replies with a JSON error the page can show as is.
func httpError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
