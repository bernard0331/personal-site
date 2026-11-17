// Command genshin-calc is the simulation and solver side of the Genshin team
// optimizer. It is being built up one subcommand at a time:
//
//	genshin-calc inspect ACCOUNT             read a GOOD file and say what it contains
//	genshin-calc config [-four-stars] [-three-stars] [-weapons RANKING] ROTATION ACCOUNT
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
//	genshin-calc weapons [options] CACHE ACCOUNT
//	                                         rank each character's candidate weapons by trial simulations,
//	                                         for the passes' -weapons option (see weapons_cmd.go)
//	genshin-calc report [options] RESULTS ACCOUNT
//	                                         choose the best teams from a pass's results and print them
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
	"github.com/bernard0331/personal-site/services/genshin-calc/weapons"
)

func main() {
	var err error
	switch {
	case len(os.Args) == 3 && os.Args[1] == "inspect":
		err = inspect(os.Args[2])
	case len(os.Args) >= 4 && os.Args[1] == "config":
		fs := flag.NewFlagSet("config", flag.ExitOnError)
		var opt weaponAssumptions
		opt.flags(fs)
		fs.Parse(os.Args[2:])
		if fs.NArg() != 2 {
			fmt.Fprintln(os.Stderr, "usage: genshin-calc config [-four-stars] [-three-stars] [-weapons RANKING] ROTATION ACCOUNT")
			os.Exit(2)
		}
		err = config(fs.Arg(0), fs.Arg(1), opt)
	case len(os.Args) == 3 && os.Args[1] == "check-rotations":
		err = checkRotations(os.Args[2])
	case len(os.Args) >= 2 && os.Args[1] == "coarse":
		err = coarseCmd(os.Args[2:])
	case len(os.Args) >= 2 && os.Args[1] == "full":
		err = fullCmd(os.Args[2:])
	case len(os.Args) >= 2 && os.Args[1] == "weapons":
		err = weaponsCmd(os.Args[2:])
	case len(os.Args) >= 2 && os.Args[1] == "report":
		err = reportCmd(os.Args[2:])
	default:
		fmt.Fprintln(os.Stderr, "usage: genshin-calc inspect ACCOUNT | config [-four-stars] [-three-stars] [-weapons RANKING] ROTATION ACCOUNT | check-rotations CACHE | coarse [options] CACHE ACCOUNT | weapons [options] CACHE ACCOUNT | full [options] CACHE ACCOUNT | report [options] RESULTS ACCOUNT")
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// weaponAssumptions are weapons to assume the player owns beyond those in the
// file, and the weapon trials' ranking to allocate them by.
type weaponAssumptions struct {
	fourStars  bool    // R1 of every 4-star weapon
	threeStars bool    // R5 of every 3-star weapon, unlimited copies
	ranking    string  // weapon trials' ranking file; "" for stand-in weapons
	energyRate float64 // how many times as often flat energy drops come (1: KQM standard)
}

// flags registers the weapon options on a subcommand.
func (opt *weaponAssumptions) flags(fs *flag.FlagSet) {
	fs.BoolVar(&opt.fourStars, "four-stars", false, "assume R1 of every 4-star weapon the file leaves out")
	fs.BoolVar(&opt.threeStars, "three-stars", false, "assume every 3-star weapon at R5, unlimited copies")
	fs.StringVar(&opt.ranking, "weapons", "", "the weapon trials' ranking (from the weapons subcommand); empty for stand-in weapons")
	energyFlag(fs, &opt.energyRate)
}

// energyFlag registers -energy-rate.
func energyFlag(fs *flag.FlagSet, rate *float64) {
	fs.Float64Var(rate, "energy-rate", 1, "flat energy drops come this many times as often as KQM standard (a particle every 8-12 s); 2 matches the owner's dummy tests")
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
	rule, err := opt.rule(acc, cat)
	if err != nil {
		return err
	}
	cfg, _, err := buildConfig(rot, acc, cat, rule)
	if err != nil {
		return err
	}
	fmt.Println(cfg)
	return nil
}

// rule adds the assumed weapons to the account and reads the ranking.
func (opt weaponAssumptions) rule(acc *good.Account, cat *catalog.Catalog) (*weaponRule, error) {
	if opt.fourStars {
		acc.AssumeFourStars(cat)
	}
	if opt.threeStars {
		acc.AssumeThreeStars(cat)
	}
	var ranking weapons.Ranking
	if opt.ranking != "" {
		var err error
		if ranking, err = weapons.ReadRanking(opt.ranking); err != nil {
			return nil, err
		}
	}
	rule := newWeaponRule(acc, ranking)
	rule.energy = opt.energyRate
	return rule, nil
}

// errMissingCharacter means the account cannot field the team.
type errMissingCharacter string

func (e errMissingCharacter) Error() string { return "the account does not have " + string(e) }

// weaponRule decides which weapon copy each team member wields.
type weaponRule struct {
	inv     *weapons.Inventory
	ranking weapons.Ranking   // from the weapon trials; nil ranks nothing
	force   map[string]string // character -> weapon to wield regardless (the trials use it)
	energy  float64           // flat energy drop rate, as a multiple of KQM standard
}

func newWeaponRule(acc *good.Account, ranking weapons.Ranking) *weaponRule {
	return &weaponRule{inv: weapons.NewInventory(acc), ranking: ranking}
}

// errLockedWeapon means the rotation needs a weapon the account lacks.
type errLockedWeapon struct{ character, weapon string }

func (e errLockedWeapon) Error() string {
	return fmt.Sprintf("the rotation needs %s on %s", e.weapon, e.character)
}

// buildConfig writes the simulator config for the player's version of a team
// and lists each member's weapon as "key: weapon Rn".
//
// Members the weapon trials ranked get their best-ranked weapons that are
// still free (no copy is wielded twice, except unlimited ones). Others stand
// in: the rotation's own weapon if owned, otherwise the owned weapon of the
// right type with the highest rarity.
func buildConfig(rot *rotation.Rotation, acc *good.Account, cat *catalog.Catalog, rule *weaponRule) (string, []string, error) {
	owned := map[string]good.Character{}
	for _, c := range acc.Characters {
		owned[c.Key] = c
	}
	wants := make([][]weapons.Choice, len(rot.Members))
	fallback := make([][]string, len(rot.Members))
	for i, m := range rot.Members {
		if _, ok := owned[m.Key]; !ok {
			return "", nil, errMissingCharacter(m.Key)
		}
		ch, _ := cat.Character(m.Key)
		fallback[i] = weapons.Fallback(ch, cat, rule.inv, m.Weapon)
		if len(fallback[i]) == 0 {
			return "", nil, fmt.Errorf("the account has no %s for %s", ch.WeaponType, m.Key)
		}
		if m.WeaponLocked {
			// The script waits on this weapon's own effect; any other weapon
			// would make it loop forever.
			if !rule.inv.Owns(m.Weapon) {
				return "", nil, errLockedWeapon{m.Key, m.Weapon}
			}
			wants[i], fallback[i] = []weapons.Choice{{Weapon: m.Weapon, Score: 1}}, []string{m.Weapon}
			continue
		}
		if w, ok := rule.force[m.Key]; ok {
			wants[i] = []weapons.Choice{{Weapon: w, Score: 1}}
			continue
		}
		for _, c := range rule.ranking[m.Key] {
			if rule.inv.Owns(c.Weapon) {
				wants[i] = append(wants[i], c)
			}
		}
	}
	picks, err := weapons.Allocate(wants, fallback, rule.inv)
	if err != nil {
		return "", nil, err
	}
	loadout := map[string]rotation.Slot{}
	var list []string
	for i, m := range rot.Members {
		c := owned[m.Key]
		loadout[m.Key] = rotation.Slot{Constellation: c.Constellation, Level: c.Level, Weapon: picks[i].Weapon, Refinement: picks[i].Refinement}
		list = append(list, fmt.Sprintf("%s: %s R%d", m.Key, picks[i].Weapon, picks[i].Refinement))
	}
	cfg, err := rot.Config(loadout)
	return rotation.ScaleEnergy(cfg, rule.energy), list, err
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
