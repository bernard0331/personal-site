// Command genshin-calc is the simulation and solver side of the Genshin team
// optimizer. For now it has one subcommand:
//
//	genshin-calc inspect FILE    read a GOOD file and say what it contains
package main

import (
	"fmt"
	"os"

	"github.com/bernard0331/personal-site/services/genshin-calc/catalog"
	"github.com/bernard0331/personal-site/services/genshin-calc/good"
)

func main() {
	if len(os.Args) != 3 || os.Args[1] != "inspect" {
		fmt.Fprintln(os.Stderr, "usage: genshin-calc inspect FILE")
		os.Exit(2)
	}
	if err := inspect(os.Args[2]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func inspect(path string) error {
	cat, err := catalog.Load()
	if err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	acc, rep, err := good.Parse(f, cat)
	if err != nil {
		return err
	}
	fmt.Printf("Source: %s (checked against %s %s)\n", acc.Source, cat.Engine.Name, cat.Engine.Release)
	fmt.Printf("Characters: %d\nWeapons:    %d\nArtifacts:  %d\n", len(acc.Characters), len(acc.Weapons), len(acc.Artifacts))
	if rep.Dropped > 0 {
		fmt.Printf("Dropped %d entries:\n", rep.Dropped)
		for _, p := range rep.Problems {
			fmt.Println("  -", p)
		}
	}
	return nil
}
