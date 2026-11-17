package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bernard0331/personal-site/services/genshin-calc/catalog"
	"github.com/bernard0331/personal-site/services/genshin-calc/coarse"
	"github.com/bernard0331/personal-site/services/genshin-calc/rotation"
	"github.com/bernard0331/personal-site/services/genshin-calc/weapons"
)

const weaponsUsage = "usage: genshin-calc weapons [options] DATABASE_CACHE ACCOUNT"

// weaponsCmd runs the weapon trials and writes the ranking the passes
// allocate weapons by (-weapons). For each of the account's characters it
// picks a few of the character's most common teams, simulates each with every
// candidate weapon on that character (other members on stand-ins), and ranks
// the candidates by team DPS. Trial results are cached like the passes'.
func weaponsCmd(args []string) error {
	fs := flag.NewFlagSet("weapons", flag.ExitOnError)
	var opt weaponAssumptions
	fs.BoolVar(&opt.fourStars, "four-stars", false, "assume R1 of every 4-star weapon the file leaves out")
	fs.BoolVar(&opt.threeStars, "three-stars", false, "assume every 3-star weapon at R5, unlimited copies")
	energyFlag(fs, &opt.energyRate)
	out := fs.String("out", "local/weapons/ranking.json", "where to write the ranking")
	cache := fs.String("cache", "local/weapons/trials.jsonl", "trial results cache (JSON lines, appended to)")
	sim := fs.String("sim", "bin/kqmsim", "simulator binary")
	own := fs.String("own", "services/genshin-calc/rotations", "directory of this project's own rotation files (empty for none)")
	iterations := fs.Int("iterations", 100, "simulator iterations per trial")
	lim := limitFlags(fs, 2*time.Minute)
	workers := fs.Int("workers", 0, "simulations to run at once (default: one per core in -cpus)")
	teams := fs.Int("teams", 2, "trial teams per character")
	frequent := fs.Int("frequent", 3, "also try the N weapons the rotations give a character most often")
	only := fs.String("only", "", "comma-separated characters to try (default: all)")
	dryRun := fs.Bool("dry-run", false, "list the trials and stop")
	fs.Usage = func() { fmt.Fprintln(os.Stderr, weaponsUsage); fs.PrintDefaults() }
	fs.Parse(args)
	if fs.NArg() != 2 {
		fs.Usage()
		os.Exit(2)
	}

	cat, err := catalog.Load()
	if err != nil {
		return err
	}
	acc, _, err := readAccount(fs.Arg(1), cat)
	if err != nil {
		return err
	}
	rule, err := opt.rule(acc, cat) // no ranking: everyone else stands in
	if err != nil {
		return err
	}
	sources, err := readRotations(fs.Arg(0), *own)
	if err != nil {
		return err
	}

	// Parse every single-target rotation the account can field; count weapon
	// usage and team popularity over all of them.
	type parsed struct {
		s   source
		rot *rotation.Rotation
	}
	var fieldable []parsed
	usage := weapons.Usage{}
	popularity := map[string]int{}
	for _, s := range sources {
		if s.targets != 1 {
			continue
		}
		rot, err := rotation.Parse(s.config, cat)
		if err != nil {
			continue
		}
		for _, m := range rot.Members {
			if m.Weapon != "" {
				usage.Add(m.Key, m.Weapon)
			}
		}
		popularity[coarse.TeamKey(rot.Team())]++
		if _, _, err := buildConfig(rot, acc, cat, rule); err == nil {
			fieldable = append(fieldable, parsed{s, rot})
		}
	}
	// Our own rotations first (written for these characters), then the most
	// common teams; the id breaks ties so the choice is stable.
	sort.SliceStable(fieldable, func(i, j int) bool {
		a, b := fieldable[i], fieldable[j]
		if (a.s.from == "own") != (b.s.from == "own") {
			return a.s.from == "own"
		}
		pa, pb := popularity[coarse.TeamKey(a.rot.Team())], popularity[coarse.TeamKey(b.rot.Team())]
		if pa != pb {
			return pa > pb
		}
		return a.s.id < b.s.id
	})

	wanted := map[string]bool{}
	for _, c := range strings.Split(*only, ",") {
		if c != "" {
			wanted[c] = true
		}
	}
	trialTeams := map[string][]parsed{}
	seenTeam := map[string]map[string]bool{}
	for _, p := range fieldable {
		tk := coarse.TeamKey(p.rot.Team())
		for _, m := range p.rot.Members {
			if m.WeaponLocked {
				continue // the script depends on this member's weapon: no trial here
			}
			if (len(wanted) > 0 && !wanted[m.Key]) || len(trialTeams[m.Key]) >= *teams {
				continue
			}
			if seenTeam[m.Key] == nil {
				seenTeam[m.Key] = map[string]bool{}
			}
			if seenTeam[m.Key][tk] {
				continue // another rotation of a team already chosen
			}
			seenTeam[m.Key][tk] = true
			trialTeams[m.Key] = append(trialTeams[m.Key], p)
		}
	}

	ranking := weapons.Ranking{}
	var jobs []coarse.Job
	var chars []string
	for c := range trialTeams {
		chars = append(chars, c)
	}
	sort.Strings(chars)
	for _, c := range chars {
		ch, _ := cat.Character(c)
		var extra []string
		for _, p := range trialTeams[c] {
			for _, m := range p.rot.Members {
				if m.Key == c {
					extra = append(extra, m.Weapon)
				}
			}
		}
		cands := weapons.Candidates(ch, cat, rule.inv, usage, *frequent, extra...)
		if len(cands) == 1 {
			ranking[c] = []weapons.Choice{{Weapon: cands[0], Score: 1}}
			continue
		}
		for _, p := range trialTeams[c] {
			for _, w := range cands {
				rule.force = map[string]string{c: w}
				cfg, list, err := buildConfig(p.rot, acc, cat, rule)
				if err != nil {
					continue // e.g. the weapon's only copy is needed by a teammate
				}
				jobs = append(jobs, coarse.Job{
					ID: c + "|" + w + "|" + p.s.from + "/" + p.s.id, Source: "trial",
					Team: p.rot.Team(), Weapons: list, Config: coarse.SetIterations(cfg, *iterations),
				})
			}
		}
		rule.force = nil
	}
	fmt.Fprintf(os.Stderr, "%d characters to rank, %d trial simulations\n", len(chars), len(jobs))
	if *dryRun {
		for _, j := range jobs {
			fmt.Println(j.ID)
		}
		return nil
	}

	bin, err := coarse.NewBinary(*sim)
	if err != nil {
		return fmt.Errorf("simulator: %w", err)
	}
	if err := runJobs(jobs, bin, share(bin, lim, *workers), *cache, 50); err != nil {
		return err
	}

	// Rank from the cache: the results for exactly these trial configs.
	results, err := coarse.ReadResults(*cache)
	if err != nil {
		return err
	}
	byKey := map[string]coarse.Result{}
	for _, r := range results {
		byKey[r.Key] = r
	}
	trials := map[string]map[string]map[string]float64{} // character -> team -> weapon -> DPS
	for _, j := range jobs {
		r, ok := byKey[coarse.Key(j.Config, bin.Fingerprint())]
		if !ok || r.Error != "" {
			continue
		}
		parts := strings.SplitN(j.ID, "|", 3)
		c, w, team := parts[0], parts[1], parts[2]
		if trials[c] == nil {
			trials[c] = map[string]map[string]float64{}
		}
		if trials[c][team] == nil {
			trials[c][team] = map[string]float64{}
		}
		trials[c][team][w] = r.DPS
	}
	for c, byTeam := range trials {
		var dps []map[string]float64
		for _, t := range byTeam {
			dps = append(dps, t)
		}
		ranking[c] = weapons.Score(dps)
	}
	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		return err
	}
	if err := ranking.Write(*out); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "ranked weapons for %d characters; wrote %s\n", len(ranking), *out)
	return nil
}
