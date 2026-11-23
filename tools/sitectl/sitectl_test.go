package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// testRepo copies the parts of the real repository sitectl reads into a
// temporary folder.
func testRepo(t *testing.T) Repo {
	t.Helper()
	real, err := findRoot(".")
	if err != nil {
		t.Fatal(err)
	}
	dst := t.TempDir()
	for _, p := range []string{servicesFile, overlayFile, "deploy/base/gateway", "deploy/base/genshin-web", "deploy/base/home", componentDir, meshDir} {
		copyPath(t, filepath.Join(real, p), filepath.Join(dst, p))
	}
	return Repo{Root: dst}
}

func copyPath(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		out := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(out, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		os.MkdirAll(filepath.Dir(out), 0o755)
		return os.WriteFile(out, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// The committed repository must already agree with services.json.
func TestRepositoryIsConsistent(t *testing.T) {
	root, err := findRoot(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range (Repo{root}).Check() {
		t.Error(p)
	}
}

func TestAdd(t *testing.T) {
	r := testRepo(t)
	s := Service{Name: "blog", Namespace: "blog", Site: "blog", Port: 8080}
	if _, err := r.Add(s); err != nil {
		t.Fatal(err)
	}
	if problems := r.Check(); len(problems) > 0 {
		t.Fatalf("after add: %v", problems)
	}
	svcs, _ := r.load()
	if len(svcs) != 3 || svcs[2] != s {
		t.Fatalf("services.json: %+v", svcs)
	}
	route, _ := os.ReadFile(r.path(baseDir, "blog", "httproute.yaml"))
	if !strings.Contains(string(route), "BLOG_HOSTNAME") {
		t.Errorf("route has no BLOG_HOSTNAME:\n%s", route)
	}
	dep, _ := os.ReadFile(r.path(baseDir, "blog", "deployment.yaml"))
	if strings.Contains(string(dep), "gcsfuse") {
		t.Error("a service without -bucket mounts the bucket")
	}
	overlay, _ := os.ReadFile(r.path(overlayFile))
	if !strings.Contains(string(overlay), "- ../../base/blog\n") || !strings.Contains(string(overlay), "newName: IMAGE_REPOSITORY/blog\n") {
		t.Errorf("overlay misses blog:\n%s", overlay)
	}

	// With kubectl available, the whole overlay must render, with the
	// policies in the new namespace.
	if _, err := exec.LookPath("kubectl"); err == nil {
		out, err := exec.Command("kubectl", "kustomize", r.path("deploy/overlays/prod")).CombinedOutput()
		if err != nil {
			t.Fatalf("kubectl kustomize: %v\n%s", err, out)
		}
		if !strings.Contains(string(out), "name: default-deny\n  namespace: blog\n") {
			t.Error("rendered overlay has no default-deny policy in namespace blog")
		}
	}
}

func TestAddBucket(t *testing.T) {
	r := testRepo(t)
	if _, err := r.Add(Service{Name: "tfm-api", Namespace: "tfm", Site: "tfm", Port: 9000, Bucket: true}); err != nil {
		t.Fatal(err)
	}
	dep, _ := os.ReadFile(r.path(baseDir, "tfm-api", "deployment.yaml"))
	for _, want := range []string{"gke-gcsfuse/volumes: \"true\"", "only-dir=tfm-api,", "containerPort: 9000"} {
		if !strings.Contains(string(dep), want) {
			t.Errorf("deployment misses %q", want)
		}
	}
}

func TestAddRejects(t *testing.T) {
	cases := map[string]Service{
		"duplicate name":      {Name: "genshin-web", Namespace: "x", Site: "x", Port: 8080},
		"duplicate namespace": {Name: "x", Namespace: "genshin", Site: "x", Port: 8080},
		"duplicate site":      {Name: "x", Namespace: "x", Site: "genshin", Port: 8080},
		"upper case":          {Name: "Home", Namespace: "home", Site: "home", Port: 8080},
		"path in name":        {Name: "../x", Namespace: "x", Site: "x", Port: 8080},
		"reserved namespace":  {Name: "x", Namespace: "gateway", Site: "x", Port: 8080},
		"privileged port":     {Name: "x", Namespace: "x", Site: "x", Port: 80},
	}
	for name, s := range cases {
		t.Run(name, func(t *testing.T) {
			r := testRepo(t)
			before, _ := os.ReadFile(r.path(servicesFile))
			if _, err := r.Add(s); err == nil {
				t.Fatal("accepted")
			}
			after, _ := os.ReadFile(r.path(servicesFile))
			if string(before) != string(after) {
				t.Error("services.json changed after a rejected add")
			}
		})
	}
}

func TestCheckFindsProblems(t *testing.T) {
	r := testRepo(t)
	kust := r.path(baseDir, "genshin-web", "kustomization.yaml")
	b, _ := os.ReadFile(kust)
	os.WriteFile(kust, []byte(strings.ReplaceAll(string(b), "../../components/network-isolation", "")), 0o644)
	os.WriteFile(r.path(overlayFile), []byte("stale\n"), 0o644)
	problems := r.Check()
	if len(problems) != 2 {
		t.Fatalf("want 2 problems, got %v", problems)
	}
}
