// Package optimizer holds the steps a run is made of: building each
// rotation's config for a player, choosing which rotations to simulate, and
// setting up team selection. The command-line tool (genshin-calc) and the
// website (genshin-web) both use it.
package optimizer

import (
	"fmt"

	"github.com/bernard0331/personal-site/services/genshin-calc/catalog"
	"github.com/bernard0331/personal-site/services/genshin-calc/good"
	"github.com/bernard0331/personal-site/services/genshin-calc/rotation"
	"github.com/bernard0331/personal-site/services/genshin-calc/weapons"
)

// ErrMissingCharacter means the account cannot field the team.
type ErrMissingCharacter string

func (e ErrMissingCharacter) Error() string { return "the account does not have " + string(e) }

// Rule decides which weapon copy each team member wields.
type Rule struct {
	Inv     *weapons.Inventory
	Ranking weapons.Ranking   // from the weapon trials; nil ranks nothing
	Force   map[string]string // character -> weapon to wield regardless (the trials use it)
	Energy  float64           // flat energy drop rate, as a multiple of KQM standard
}

func NewRule(acc *good.Account, ranking weapons.Ranking) *Rule {
	return &Rule{Inv: weapons.NewInventory(acc), Ranking: ranking}
}

// ErrLockedWeapon means the rotation needs a weapon the account lacks.
type ErrLockedWeapon struct{ Character, Weapon string }

func (e ErrLockedWeapon) Error() string {
	return fmt.Sprintf("the rotation needs %s on %s", e.Weapon, e.Character)
}

// BuildConfig writes the simulator config for the player's version of a team
// and lists each member's weapon as "key: weapon Rn".
//
// Members the weapon trials ranked get their best-ranked weapons that are
// still free (no copy is wielded twice, except unlimited ones). Others stand
// in: the rotation's own weapon if owned, otherwise the owned weapon of the
// right type with the highest rarity.
func BuildConfig(rot *rotation.Rotation, acc *good.Account, cat *catalog.Catalog, rule *Rule) (string, []string, error) {
	owned := map[string]good.Character{}
	for _, c := range acc.Characters {
		owned[c.Key] = c
	}
	wants := make([][]weapons.Choice, len(rot.Members))
	fallback := make([][]string, len(rot.Members))
	for i, m := range rot.Members {
		if _, ok := owned[m.Key]; !ok {
			return "", nil, ErrMissingCharacter(m.Key)
		}
		ch, _ := cat.Character(m.Key)
		fallback[i] = weapons.Fallback(ch, cat, rule.Inv, m.Weapon)
		if len(fallback[i]) == 0 {
			return "", nil, fmt.Errorf("the account has no %s for %s", ch.WeaponType, m.Key)
		}
		if m.WeaponLocked {
			// The script waits on this weapon's own effect; any other weapon
			// would make it loop forever.
			if !rule.Inv.Owns(m.Weapon) {
				return "", nil, ErrLockedWeapon{m.Key, m.Weapon}
			}
			wants[i], fallback[i] = []weapons.Choice{{Weapon: m.Weapon, Score: 1}}, []string{m.Weapon}
			continue
		}
		if w, ok := rule.Force[m.Key]; ok {
			wants[i] = []weapons.Choice{{Weapon: w, Score: 1}}
			continue
		}
		for _, c := range rule.Ranking[m.Key] {
			if rule.Inv.Owns(c.Weapon) {
				wants[i] = append(wants[i], c)
			}
		}
	}
	picks, err := weapons.Allocate(wants, fallback, rule.Inv)
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
	return rotation.ScaleEnergy(cfg, rule.Energy), list, err
}
