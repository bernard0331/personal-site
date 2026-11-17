// Command genshin-calc is the simulation and solver side of the Genshin team
// optimizer. It is being built up one subcommand at a time:
//
//	genshin-calc inspect ACCOUNT             read a GOOD file and say what it contains
//	genshin-calc config [-four-stars] [-three-stars] ROTATION ACCOUNT
//	                                         print the simulator config for a player's version of a team;
//	                                         -four-stars assumes R1 of every 4-star weapon the file leaves out,
//	                                         -three-stars assumes every 3-star weapon at R5 in unlimited copies
//	genshin-calc check-rotations CACHE       parse every rotation in a cached database file and count failures
//	genshin-calc coarse [options] CACHE ACCOUNT
//	                                         simulate every rotation the account can field and cache the results
//	                                         (see coarse_cmd.go; -h lists the options)
//	genshin-calc full [options] CACHE ACCOUNT
//	                                         simulate the quick pass's most promising teams again, with
//	                                         optimised substats and more iterations (see full_cmd.go)
package main

import (
	"bufio"
	"encoding/json"
	"flag"
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
	case len(os.Args) >= 4 && os.Args[1] == "config":
		fs := flag.NewFlagSet("config", flag.ExitOnError)
		var opt weaponAssumptions
		fs.BoolVar(&opt.fourStars, "four-stars", false, "assume R1 of every 4-star weapon the file leaves out")
		fs.BoolVar(&opt.threeStars, "three-stars", false, "assume every 3-star weapon at R5, unlimited copies")
		fs.Parse(os.Args[2:])
		if fs.NArg() != 2 {
			fmt.Fprintln(os.Stderr, "usage: genshin-calc config [-four-stars] [-three-stars] ROTATION ACCOUNT")
			os.Exit(2)
		}
		err = config(fs.Arg(0), fs.Arg(1), opt)
	case len(os.Args) == 3 && os.Args[1] == "check-rotations":
		err = checkRotations(os.Args[2])
	case len(os.Args) >= 2 && os.Args[1] == "coarse":
		err = coarseCmd(os.Args[2:])
	case len(os.Args) >= 2 && os.Args[1] == "full":
		err = fullCmd(os.Args[2:])
	default:
		fmt.Fprintln(os.Stderr, "usage: genshin-calc inspect ACCOUNT | config [-four-stars] [-three-stars] ROTATION ACCOUNT | check-rotations CACHE | coarse [options] CACHE ACCOUNT | full [options] CACHE ACCOUNT")
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// weaponAssumptions are weapons to assume the player owns beyond those in the file.
type weaponAssumptions struct {
	fourStars  bool // R1 of every 4-star weapon
	threeStars bool // R5 of every 3-star weapon, unlimited copies
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
func config(rotationPath, accountPath string, opt weaponAssumptions) error {
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
	opt.apply(acc, cat)
	cfg, _, err := buildConfig(rot, acc, cat)
	if err != nil {
		return err
	}
	fmt.Println(cfg)
	return nil
}

// apply adds the assumed weapons to the account.
func (opt weaponAssumptions) apply(acc *good.Account, cat *catalog.Catalog) {
	if opt.fourStars {
		acc.AssumeFourStars(cat)
	}
	if opt.threeStars {
		acc.AssumeThreeStars(cat)
	}
}

// errMissingCharacter means the account cannot field the team.
type errMissingCharacter string

func (e errMissingCharacter) Error() string { return "the account does not have " + string(e) }

// buildConfig writes the simulator config for the player's version of a team
// and lists each member's weapon.
//
// Weapons are a stand-in until weapon allocation is written: each member gets
// the rotation's example weapon if the player owns it, at their best
// refinement, and otherwise their highest-rarity weapon of the right type.
func buildConfig(rot *rotation.Rotation, acc *good.Account, cat *catalog.Catalog) (string, []string, error) {
	owned := map[string]good.Character{}
	for _, c := range acc.Characters {
		owned[c.Key] = c
	}
	loadout := map[string]rotation.Slot{}
	var weapons []string
	for _, m := range rot.Members {
		c, ok := owned[m.Key]
		if !ok {
			return "", nil, errMissingCharacter(m.Key)
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
			return "", nil, fmt.Errorf("the account has no %s for %s", ch.WeaponType, m.Key)
		}
		loadout[m.Key] = rotation.Slot{Constellation: c.Constellation, Level: c.Level, Weapon: weapon, Refinement: refinement}
		weapons = append(weapons, fmt.Sprintf("%s: %s R%d", m.Key, weapon, refinement))
	}
	cfg, err := rot.Config(loadout)
	return cfg, weapons, err
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
