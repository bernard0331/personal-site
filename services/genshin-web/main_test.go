package main

import (
	"encoding/json"
	"github.com/bernard0331/personal-site/services/genshin-calc/catalog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCatalogOffersOnlyReleasedCharactersAsLumine(t *testing.T) {
	c, err := buildCatalog()
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	c.serve(rec, httptest.NewRequest("GET", "/api/catalog", nil))
	var got catalogResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	keys := map[string]apiCharacter{}
	for _, ch := range got.Characters {
		keys[ch.Key] = ch
	}
	for _, k := range []string{"anastasya", "danica", "aetheranemo"} {
		if _, ok := keys[k]; ok {
			t.Errorf("%s must not be offered", k)
		}
	}
	if a := keys["arlecchino"]; a.Name != "Arlecchino" || a.Icon == "" || a.Element != "pyro" {
		t.Errorf("arlecchino: %+v", a)
	}
	if len(got.Weapons) < 200 || len(got.Sets) < 50 {
		t.Errorf("only %d weapons and %d sets", len(got.Weapons), len(got.Sets))
	}
}

func TestImagesCannotEscapeTheirDirectory(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.png"), []byte("png"), 0o644)
	os.WriteFile(filepath.Join(filepath.Dir(dir), "secret.txt"), []byte("secret"), 0o644)
	srv := http.StripPrefix("/img/", imageServer(dir))
	for path, want := range map[string]int{"/img/a.png": 200, "/img/../secret.txt": 404, "/img/%2e%2e/secret.txt": 404} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/img/a.png", nil)
		req.URL.Path = strings.Replace(path, "%2e%2e", "..", 1)
		srv.ServeHTTP(rec, req)
		if (rec.Code == 200) != (want == 200) || strings.Contains(rec.Body.String(), "secret") {
			t.Errorf("%s: status %d body %q", path, rec.Code, rec.Body.String())
		}
	}
}

func TestImportGOOD(t *testing.T) {
	cat, err := catalog.Load()
	if err != nil {
		t.Fatal(err)
	}
	file := `{"format":"GOOD","version":2,"source":"test",
	  "characters":[{"key":"Arlecchino","level":90,"constellation":1,"ascension":6,"talent":{"auto":9,"skill":9,"burst":9}},
	                {"key":"NotACharacter","level":90,"constellation":0,"ascension":6,"talent":{"auto":1,"skill":1,"burst":1}}],
	  "weapons":[{"key":"CrimsonMoonsSemblance","level":90,"ascension":6,"refinement":1,"location":"Arlecchino","lock":true}],
	  "artifacts":[]}`
	rec := httptest.NewRecorder()
	importGOOD(cat)(rec, httptest.NewRequest("POST", "/api/good", strings.NewReader(file)))
	var got goodAccount
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("%v: %s", err, rec.Body.String())
	}
	if len(got.Characters) != 1 || got.Characters[0].Key != "arlecchino" || got.Characters[0].Cons != 1 {
		t.Errorf("characters: %+v", got.Characters)
	}
	if len(got.Weapons) != 1 || got.Weapons[0].Key != "crimsonmoonssemblance" || got.Dropped != 1 {
		t.Errorf("weapons %+v, dropped %d", got.Weapons, got.Dropped)
	}
	rec = httptest.NewRecorder()
	importGOOD(cat)(rec, httptest.NewRequest("POST", "/api/good", strings.NewReader("not json")))
	if rec.Code != 400 {
		t.Errorf("bad file: status %d", rec.Code)
	}
}
