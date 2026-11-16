// Command genshin-calc is the simulation and solver side of the Genshin team
// optimizer. It is being built up one subcommand at a time:
//
//	genshin-calc inspect ACCOUNT             read a GOOD file and say what it contains
//	genshin-calc config ROTATION ACCOUNT     print the simulator config for a player's version of a team
//	genshin-calc check-rotations CACHE       parse every rotation in a cached database file and count failures
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/bernard0331/personal-site/services/genshin-calc/catalog"
	"github.com/bernard0331/personal-site/services/genshin-calc/good"
	"github.com/bernard0331/personal-site/services/genshin-calc/rotation"
)

func main() {
	var err error
	switch {
	case len(os.Args) == 3 && os.Args[1] == "inspect":
		err = inspect(os.Args[2])
	case len(os.Args) == 4 && os.Args[1] == "config":
		err = config(os.Args[2], os.Args[3])
	case len(os.Args) == 3 && os.Args[1] == "check-rotations":
		err = checkRotations(os.Args[2])
	default:
		fmt.Fprintln(os.Stderr, "usage: genshin-calc inspect ACCOUNT | config ROTATION ACCOUNT | check-rotations CACHE")
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func readAccount(path string, cat *catalog.Catalog) (*good.Account, *good.Report, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	return good.Parse(f, cat)
}

func inspect(path string) error {
	cat, err := catalog.Load()
	if err != nil {
		return err
	}
	acc, rep, err := readAccount(path, cat)
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

// config prints the simulator config for the player's version of one team.
//
// Weapons are a stand-in until weapon allocation is written: each member gets
// the rotation's example weapon if the player owns it, at their best
// refinement, and otherwise their highest-rarity weapon of the right type.
func config(rotationPath, accountPath string) error {
	cat, err := catalog.Load()
	if err != nil {
		return err
	}
	text, err := os.ReadFile(rotationPath)
	if err != nil {
		return err
	}
	rot, err := rotation.Parse(string(text), cat)
	if err != nil {
		return err
	}
	acc, _, err := readAccount(accountPath, cat)
	if err != nil {
		return err
	}
	owned := map[string]good.Character{}
	for _, c := range acc.Characters {
		owned[c.Key] = c
	}
	loadout := map[string]rotation.Slot{}
	for _, m := range rot.Members {
		c, ok := owned[m.Key]
		if !ok {
			return fmt.Errorf("the account does not have %s", m.Key)
		}
		ch, _ := cat.Character(m.Key)
		weapon, refinement := "", 0
		bestRarity := 0
		for _, w := range acc.Weapons {
			entry, _ := cat.Weapon(w.Key)
			if entry.WeaponType != ch.WeaponType {
				continue
			}
			rarity := entry.Rarity
			if w.Key == m.Weapon {
				rarity = 10 // the rotation's own weapon wins
			}
			if rarity > bestRarity || (rarity == bestRarity && w.Key == weapon && w.Refinement > refinement) {
				weapon, refinement, bestRarity = w.Key, w.Refinement, rarity
			}
		}
		if weapon == "" {
			return fmt.Errorf("the account has no %s for %s", ch.WeaponType, m.Key)
		}
		loadout[m.Key] = rotation.Slot{Constellation: c.Constellation, Level: c.Level, Weapon: weapon, Refinement: refinement}
	}
	cfg, err := rot.Config(loadout)
	if err != nil {
		return err
	}
	fmt.Println(cfg)
	return nil
}

// checkRotations parses every entry of a cached rotation database (one JSON
// object per line, with "id" and "config" fields) and reports why any fail.
func checkRotations(path string) error {
	cat, err := catalog.Load()
	if err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1<<20), 16<<20)
	total, ok := 0, 0
	reasons := map[string]int{}
	for scanner.Scan() {
		var entry struct {
			ID     string `json:"id"`
			Config string `json:"config"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			return err
		}
		total++
		if _, err := rotation.Parse(entry.Config, cat); err != nil {
			reasons[err.Error()]++
		} else {
			ok++
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	fmt.Printf("%d of %d rotations parse\n", ok, total)
	type kv struct {
		reason string
		n      int
	}
	var list []kv
	for r, n := range reasons {
		list = append(list, kv{r, n})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].n > list[j].n })
	for i, e := range list {
		if i == 15 {
			fmt.Printf("  ... and %d more reasons\n", len(list)-15)
			break
		}
		fmt.Printf("  %4d  %s\n", e.n, e.reason)
	}
	return nil
}
