package main

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"
)

// The files a site's manifests are made from, one per file in deploy/base/NAME.
//
//go:embed templates/*.yaml
var templates embed.FS

// Paths relative to the repository root.
const (
	servicesFile = "terraform/envs/prod/services.json"
	baseDir      = "deploy/base"
	overlayFile  = "deploy/overlays/prod/kustomization.yaml"
	componentDir = "deploy/components/network-isolation"
)

// Service is one site behind the shared Gateway, as listed in services.json.
// Terraform reads the same file.
type Service struct {
	Name      string `json:"name"`      // Deployment, Service, service account and image name
	Namespace string `json:"namespace"` // its own namespace
	Site      string `json:"site"`      // first label of the hostname: SITE.<ip>.sslip.io
	Port      int    `json:"port"`      // the container's HTTP port
	Bucket    bool   `json:"bucket"`    // read/write on the runs bucket (its own folder)
}

// HostPlaceholder is the text in the HTTPRoute that deploy/gke/apply.sh
// replaces with the site's hostname: GENSHIN_HOSTNAME for site "genshin".
func (s Service) HostPlaceholder() string {
	return strings.ToUpper(strings.ReplaceAll(s.Site, "-", "_")) + "_HOSTNAME"
}

type servicesDoc struct {
	Services []Service `json:"services"`
}

// dnsLabel is a Kubernetes name that is also a valid DNS label and Google
// Cloud resource name part: lower case letters, digits and dashes, starting
// with a letter, at most 30 characters (Certificate Manager names add a
// prefix, and the hostname adds the IP).
var dnsLabel = regexp.MustCompile(`^[a-z]([-a-z0-9]{0,28}[a-z0-9])?$`)

// reserved names: the shared Gateway's namespace and Kubernetes' own.
var reserved = map[string]bool{"gateway": true, "default": true, "kube-system": true, "kube-public": true, "kube-node-lease": true}

func (s Service) validate() error {
	for _, f := range []struct{ what, v string }{{"name", s.Name}, {"namespace", s.Namespace}, {"site", s.Site}} {
		if !dnsLabel.MatchString(f.v) {
			return fmt.Errorf("%s %q: use lower case letters, digits and dashes, starting with a letter (at most 30)", f.what, f.v)
		}
	}
	if reserved[s.Namespace] {
		return fmt.Errorf("namespace %q is reserved", s.Namespace)
	}
	if s.Port < 1024 || s.Port > 65535 {
		return fmt.Errorf("port %d: use 1024-65535 (the pods run as a non-root user)", s.Port)
	}
	return nil
}

// Repo is the repository the tool works on.
type Repo struct{ Root string }

// findRoot walks up from dir to the folder holding services.json.
func findRoot(dir string) (string, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, servicesFile)); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no %s above the current folder; run inside the repository or pass -root", servicesFile)
		}
		dir = parent
	}
}

func (r Repo) path(p ...string) string { return filepath.Join(append([]string{r.Root}, p...)...) }

func (r Repo) load() ([]Service, error) {
	b, err := os.ReadFile(r.path(servicesFile))
	if err != nil {
		return nil, err
	}
	var doc servicesDoc
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("%s: %w", servicesFile, err)
	}
	return doc.Services, nil
}

func (r Repo) save(svcs []Service) error {
	b, err := json.MarshalIndent(servicesDoc{svcs}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(r.path(servicesFile), append(b, '\n'), 0o644)
}

// conflicts reports a new service clashing with an existing one. Names,
// namespaces and sites must each be unique: one site per namespace keeps
// each site's NetworkPolicies and identity separate.
func conflicts(existing []Service, s Service) error {
	for _, e := range existing {
		switch {
		case e.Name == s.Name:
			return fmt.Errorf("a service named %q already exists", s.Name)
		case e.Namespace == s.Namespace:
			return fmt.Errorf("namespace %q is already used by %s", s.Namespace, e.Name)
		case e.Site == s.Site:
			return fmt.Errorf("site %q is already used by %s", s.Site, e.Name)
		}
	}
	return nil
}

// Add registers a new site in services.json, writes its manifests to
// deploy/base/NAME and adds it to the prod overlay. Nothing is written if
// any check fails.
func (r Repo) Add(s Service) ([]string, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	svcs, err := r.load()
	if err != nil {
		return nil, err
	}
	if err := conflicts(svcs, s); err != nil {
		return nil, err
	}
	dir := r.path(baseDir, s.Name)
	if _, err := os.Stat(dir); err == nil {
		return nil, fmt.Errorf("%s already exists", filepath.Join(baseDir, s.Name))
	}
	files, err := renderBase(s)
	if err != nil {
		return nil, err
	}
	svcs = append(svcs, s)
	overlay, err := renderOverlay(svcs)
	if err != nil {
		return nil, err
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	var written []string
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(dir, f.name), f.body, 0o644); err != nil {
			return written, err
		}
		written = append(written, filepath.Join(baseDir, s.Name, f.name))
	}
	if err := r.save(svcs); err != nil {
		return written, err
	}
	if err := os.WriteFile(r.path(overlayFile), overlay, 0o644); err != nil {
		return written, err
	}
	return append(written, servicesFile, overlayFile), nil
}

type file struct {
	name string
	body []byte
}

func renderBase(s Service) ([]file, error) {
	entries, err := templates.ReadDir("templates")
	if err != nil {
		return nil, err
	}
	var out []file
	for _, e := range entries {
		t, err := template.ParseFS(templates, "templates/"+e.Name())
		if err != nil {
			return nil, err
		}
		var b bytes.Buffer
		if err := t.Execute(&b, s); err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		out = append(out, file{e.Name(), b.Bytes()})
	}
	return out, nil
}

var overlayTemplate = template.Must(template.New("overlay").Parse(`# Production: the shared Gateway and every site, each with its image.
#
# Generated by tools/sitectl from terraform/envs/prod/services.json; edit
# that file (or run "sitectl add"), then "sitectl render". Placeholders in
# capitals (IMAGE_REPOSITORY, IMAGE_TAG, BUCKET_NAME, <SITE>_HOSTNAME) are
# filled in by deploy/gke/apply.sh from Terraform's outputs, because they
# contain the project id, which stays out of the repository. Every image is
# tagged with the commit it was built from, so one IMAGE_TAG serves all.
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
  - ../../base/gateway
{{- range .}}
  - ../../base/{{.Name}}
{{- end}}
images:
{{- range .}}
  - name: {{.Name}}
    newName: IMAGE_REPOSITORY/{{.Name}}
    newTag: IMAGE_TAG
{{- end}}
`))

func renderOverlay(svcs []Service) ([]byte, error) {
	var b bytes.Buffer
	err := overlayTemplate.Execute(&b, svcs)
	return b.Bytes(), err
}

// Render rewrites the prod overlay from services.json.
func (r Repo) Render() error {
	svcs, err := r.load()
	if err != nil {
		return err
	}
	b, err := renderOverlay(svcs)
	if err != nil {
		return err
	}
	return os.WriteFile(r.path(overlayFile), b, 0o644)
}

// Check reports every way the repository disagrees with services.json: a
// site with no manifests, a namespace not open to the Gateway, a site
// without the network isolation component, or an overlay out of date. It
// changes nothing, so CI can run it.
func (r Repo) Check() []error {
	svcs, err := r.load()
	if err != nil {
		return []error{err}
	}
	var problems []error
	seen := []Service{}
	for _, s := range svcs {
		if err := s.validate(); err != nil {
			problems = append(problems, fmt.Errorf("%s: %w", s.Name, err))
		}
		if err := conflicts(seen, s); err != nil {
			problems = append(problems, err)
		}
		seen = append(seen, s)

		kust, err := os.ReadFile(r.path(baseDir, s.Name, "kustomization.yaml"))
		if err != nil {
			problems = append(problems, fmt.Errorf("%s: no manifests (%s/%s/kustomization.yaml)", s.Name, baseDir, s.Name))
			continue
		}
		if !bytes.Contains(kust, []byte("../../components/network-isolation")) {
			problems = append(problems, fmt.Errorf("%s: its kustomization doesn't include %s (default-deny NetworkPolicies)", s.Name, componentDir))
		}
		if !bytes.Contains(kust, []byte("namespace: "+s.Namespace+"\n")) {
			problems = append(problems, fmt.Errorf("%s: its kustomization doesn't set namespace: %s", s.Name, s.Namespace))
		}
		ns, err := os.ReadFile(r.path(baseDir, s.Name, "namespace.yaml"))
		if err != nil || !bytes.Contains(ns, []byte(`personal-site/gateway: "true"`)) {
			problems = append(problems, fmt.Errorf("%s: namespace.yaml must carry the label personal-site/gateway: \"true\"", s.Name))
		}
		route, err := os.ReadFile(r.path(baseDir, s.Name, "httproute.yaml"))
		if err != nil || !bytes.Contains(route, []byte(s.HostPlaceholder())) {
			problems = append(problems, fmt.Errorf("%s: httproute.yaml must use the hostname placeholder %s", s.Name, s.HostPlaceholder()))
		}
	}
	want, err := renderOverlay(svcs)
	if err != nil {
		return append(problems, err)
	}
	have, err := os.ReadFile(r.path(overlayFile))
	if err != nil || !bytes.Equal(have, want) {
		problems = append(problems, errors.New(overlayFile+" is out of date: run sitectl render"))
	}
	return problems
}
