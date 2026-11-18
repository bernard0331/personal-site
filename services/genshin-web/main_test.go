package main

import (
	"encoding/json"
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
