// Command sitectl adds a site to the shared Gateway in one step, so a new
// service is exposed, routed and isolated the same way as the existing ones
// without hand-writing Kubernetes or Terraform.
//
//	sitectl add -name NAME [-namespace NS] [-site SITE] [-port 8080] [-bucket]
//	sitectl list
//	sitectl render
//	sitectl check
//
// "add" records the site in terraform/envs/prod/services.json, the one list
// of sites, and writes its manifests to deploy/base/NAME: a namespace open
// to the Gateway, a Kubernetes service account, a Deployment, a Service, the
// load balancer's health check, an HTTPRoute, and the default-deny
// NetworkPolicies. From the same file Terraform then creates the site's
// hostname, TLS certificate and certificate map entry, opens its port to the
// load balancer, and (with -bucket) grants its service account the runs
// bucket through Workload Identity.
//
// "check" changes nothing and fails if the repository disagrees with
// services.json, so it can run in CI.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	cmd, args := os.Args[1], os.Args[2:]
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	root := fs.String("root", "", "repository root (default: found from the current folder)")
	var s Service
	if cmd == "add" {
		fs.StringVar(&s.Name, "name", "", "service name: Deployment, Service, service account and image (required)")
		fs.StringVar(&s.Namespace, "namespace", "", "namespace (default: the name)")
		fs.StringVar(&s.Site, "site", "", "hostname's first label, SITE.<ip>.sslip.io (default: the name)")
		fs.IntVar(&s.Port, "port", 8080, "the container's HTTP port")
		fs.BoolVar(&s.Bucket, "bucket", false, "give the service read/write on its own folder of the runs bucket")
	}
	fs.Parse(args)

	r := Repo{Root: *root}
	if r.Root == "" {
		wd, err := os.Getwd()
		if err == nil {
			r.Root, err = findRoot(wd)
		}
		if err != nil {
			fail(err)
		}
	}

	switch cmd {
	case "add":
		if s.Name == "" {
			fail(fmt.Errorf("add needs -name"))
		}
		if s.Namespace == "" {
			s.Namespace = s.Name
		}
		if s.Site == "" {
			s.Site = s.Name
		}
		written, err := r.Add(s)
		for _, w := range written {
			fmt.Println("wrote", w)
		}
		if err != nil {
			fail(err)
		}
		fmt.Printf(`
Next:
  1. Build and push the image %s, tagged with the commit, to the registry.
  2. terraform -chdir=terraform/envs/prod apply   (hostname, certificate, port%s)
  3. deploy/gke/apply.sh <any of the images, same tag>
`, s.Name, map[bool]string{true: ", bucket access", false: ""}[s.Bucket])
	case "list":
		svcs, err := r.load()
		if err != nil {
			fail(err)
		}
		fmt.Printf("%-16s %-16s %-16s %-6s %s\n", "NAME", "NAMESPACE", "SITE", "PORT", "BUCKET")
		for _, s := range svcs {
			fmt.Printf("%-16s %-16s %-16s %-6d %v\n", s.Name, s.Namespace, s.Site, s.Port, s.Bucket)
		}
	case "render":
		if err := r.Render(); err != nil {
			fail(err)
		}
		fmt.Println("wrote", overlayFile)
	case "check":
		problems := r.Check()
		for _, p := range problems {
			fmt.Fprintln(os.Stderr, "sitectl:", p)
		}
		if len(problems) > 0 {
			os.Exit(1)
		}
		fmt.Println("ok")
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, strings.TrimSpace(`
usage:
  sitectl add -name NAME [-namespace NS] [-site SITE] [-port 8080] [-bucket]
  sitectl list
  sitectl render
  sitectl check`))
	os.Exit(2)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "sitectl:", err)
	os.Exit(1)
}
