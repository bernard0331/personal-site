#!/usr/bin/env python3
"""Write a rotation file from a KQM quick guide's sample rotation.

KQM's guides give each example team a rotation in their combo notation, e.g.
    Furina ED Q > Ororon EQ > Sayu shEQ > Ifa combo
Most of that maps straight onto simulator actions. "<Carry> combo" is the
carry's own combo, which a spec supplies as script lines (usually from the
guide's Combos section or a KQM Sim Database rotation, credited). Example builds
(KQM-standard substats) come from database rotations, so the file runs on its
own; the optimizer replaces them with each player's own.

usage: guide_rotation.py SPEC.json [OUT_DIR]

SPEC.json is a list of teams:
  {"file": "ifa-ororon-furina-sayu",
   "guide": "Ifa", "url": "https://keqingmains.com/q/ifa-quickguide/",
   "site": "KQM",                                    # whose guide (default KQM; e.g. Prydwen)
   "team": "Ifa — Ororon — Furina — Sayu",          # the guide's team heading
   "variant": "",                                    # e.g. "C6 Faruzan"; optional
   "rotation": "Furina ED Q > Ororon EQ > Sayu shEQ > Ifa combo",
   "combos": {"ifa": ["ifa skill;", ...]},           # script lines per carry
   "combo_source": "the guide's Q E N13 (P)",        # credit for the combo
   "first": "furina",                                # active character at the start
   "notes": ["..."],                                 # extra comment lines
   "loops": 6,
   "builds": {"lohen": ["lohen char ...;", ...]},    # example build, when no file has one
   "gear": {"yaemiko": {"weapon": "thewidsith", "refine": 1, "sets": {"disenchantmentindeepshadow": 4},
            "main": "hp=4780 atk=311 atk%=0.466 atk%=0.466 cd=0.622"}}}  # archetype gear

Notation (KQM's): E skill, tE tap skill, hE hold skill, shE short hold skill,
Q burst, sQ special burst, Nk k normal attacks, C charged attack, A aimed shot,
P/hP high plunge, lP low plunge, D dash, J jump, W walk, (X) only when ready,
k[...] repeat k times. Iansan in the team adds the movement helper mv(): her
buff needs the active character's movement reported (see the Varesa/Iansan
rotation); combos then call mv() themselves.
"""
import json, re, sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
DB = ROOT / "local/rotations/kqm-db.jsonl"
CATALOG = ROOT / "services/genshin-calc/catalog/catalog.json"
DISPLAY = ROOT / "services/genshin-web/display.json"
OWN = ROOT / "services/genshin-calc/rotations"

cat = json.load(open(CATALOG))
SHORT = {c["key"]: c["key"] for c in cat["characters"]}
SHORT.update(cat["shortcuts"]["characters"])
NAMES = {}  # display name (lower case) -> key
for k, v in json.load(open(DISPLAY))["characters"].items():
    NAMES[v["name"].lower()] = k
for el in ("anemo", "geo", "electro", "dendro", "hydro", "pyro", "cryo"):
    NAMES[f"{el} traveler"] = "lumine" + el
NAMES["yae"] = "yaemiko"
NAMES["kazuha"] = "kaedeharakazuha"
NAMES["raiden"] = "raidenshogun"


def key(name):
    k = name.strip().lower()
    if k in NAMES:
        return NAMES[k]
    k = SHORT.get(k.replace(" ", ""), k.replace(" ", ""))
    return "lumine" + k[6:] if k.startswith("aether") else k


# ---------- example builds from the database ----------

STD5 = {"def%": .062, "def": 19.68, "hp": 253.94, "hp%": .0496, "atk": 16.54, "atk%": .0496,
        "er": .0551, "em": 19.82, "cr": .0331, "cd": .0662}


def standard_line(body):
    tot = {}
    for k, v, n in re.findall(r"([a-z%]+)=([0-9.]+)(?:\*([0-9]+))?", body):
        if k in STD5:
            tot[k] = tot.get(k, 0) + float(v) * (int(n) if n else 1)
    if len(tot) < 6:
        return False
    for scale in (1, .84):
        if all(v / (STD5[k] * scale) >= 1.5 and abs(v / (STD5[k] * scale) - round(v / (STD5[k] * scale))) < .03 for k, v in tot.items()):
            return True
    return False


_db = None


def builds(member):
    """The member's char and add lines from the best-rated database rotation in
    which every member has KQM-standard substats. Returns (lines, source id)."""
    global _db
    if _db is None:
        _db = [json.loads(l) for l in open(DB)]
    best = None
    for e in _db:
        if e.get("targets") != 1:
            continue
        cfg = e["config"]
        per = {}
        for m in re.finditer(r"^\s*(\w+)\s+(char|add)\b[^;]*;", cfg, re.M):
            per.setdefault(key(m.group(1)), []).append(m.group(0).strip())
        mine = per.get(member)
        if not mine:
            continue
        subs = [l for l in mine if " add stats" in l]
        if not subs or not any(standard_line(l) for l in subs):
            continue
        if best is None or (e.get("dps") or 0) > best[0]:
            best = (e.get("dps") or 0, e["id"], mine)
    if best is None:
        # Characters the database lacks (e.g. Iansan): our own rotation files.
        for f in sorted(OWN.glob("*.txt")):
            mine = [m.group(0).strip() for m in re.finditer(r"^\s*(\w+)\s+(char|add)\b[^;]*;", f.read_text(), re.M)
                    if key(m.group(1)) == member]
            if any(" add stats" in l for l in mine):
                best = (0, "own:" + f.stem, mine)
                break
    if best is None:
        raise SystemExit(f"no KQM-standard build for {member}")
    lines = [re.sub(r"^\w+", member, l) for l in best[2]]
    lines = [re.sub(r"\s*#.*$", "", l) for l in lines]
    return lines, best[1]


def regear(member, lines, g):
    """The member's build with another weapon, sets and main stats; the
    KQM-standard substats come from a database build with the same main stats."""
    global _db
    if _db is None:
        _db = [json.loads(l) for l in open(DB)]
    main = g["main"]
    subs = None
    for e in _db:
        L = e["config"].splitlines()
        for i, l in enumerate(L):
            if re.search(r"add stats " + re.escape(main) + r";", l):
                ch = l.split()[0]
                for n in L[i + 1:i + 3]:
                    body = n.split(" add stats ", 1)[-1]
                    if n.startswith(ch + " add stats") and standard_line(n):
                        subs = re.sub(r"\s*#.*$", "", body)
                        break
            if subs:
                break
        if subs:
            break
    if subs is None:
        raise SystemExit(f"no KQM-standard substats for main stats {main}")
    char = [l for l in lines if " char " in l]
    out = char + [f'{member} add weapon="{g["weapon"]}" refine={g["refine"]} lvl=90/90;']
    out += [f'{member} add set="{s}" count={n};' for s, n in g["sets"].items()]
    out += [f"{member} add stats {main};", f"{member} add stats {subs}"]
    return out


# ---------- notation ----------

TOKEN = re.compile(r"\d+\[[^\]]*\]|\([^)]*\)|shE|hE|tE|sQ|hP|lP|N\d+|[EQCADPJW]")


BOW = {c["key"] for c in cat["characters"] if c["weapon_type"] == "bow"}


# Supports' bursts: the guides assume enough ER to burst every rotation; with
# example builds that often stalls a rotation for minutes. By default a
# support's Q happens when ready (a spec can set "strict_bursts").
WHEN_READY = True


def actions(member, text):
    """Translate one character's part of a rotation into script lines."""
    out = []
    for t in TOKEN.findall(text.replace(" ", "")):
        m = re.fullmatch(r"(\d+)\[(.*)\]", t)
        if m:
            inner = actions(member, m.group(2))
            out.append(f"for let r=0; r<{m.group(1)}; r=r+1 {{ " + " ".join(inner) + " }")
            continue
        if t.startswith("("):
            inner = t[1:-1]
            if inner in ("Q", "sQ"):
                out.append(f"if .{member}.burst.ready {{ {member} burst; }}")
            elif inner in ("E", "tE", "hE"):
                out.append(f"if .{member}.skill.ready {{ {' '.join(actions(member, inner))} }}")
            else:  # optional attacks: done
                out.extend(actions(member, inner))
            continue
        if t in ("Q", "sQ") and WHEN_READY:
            out.append(f"if .{member}.burst.ready {{ {member} burst; }}")
            continue
        act = HOLD.get(member, ONE["hE"]) if t == "hE" else ONE[t] if t in ONE else "attack:" + t[1:]
        if t == "C" and member in BOW:
            act = "aim"  # a bow user's charged attack is an aimed shot
        out.append(f"{member} {act};")
    return out


ONE = {"E": "skill", "tE": "skill", "hE": "skill[hold=1]", "shE": "skill[short_hold=1]", "Q": "burst",
       "sQ": "burst", "C": "charge", "A": "aim", "D": "dash", "J": "jump", "W": "walk",
       "P": "high_plunge", "hP": "high_plunge", "lP": "low_plunge"}


# Characters whose hold skill takes another parameter than hold=1.
HOLD = {"beidou": "skill[counter=2]"}  # counter 2: the full hold (two hits taken)


def translate(rotation, combos):
    """Script lines for one rotation, and the members in order of appearance."""
    lines, members = [], []
    for part in rotation.split(">"):
        part = part.strip()
        name_actions = re.match(r"(.+?)\s+((?:combo.*)|(?:[\d\[\]()hstlNEQCADPJW ].*))$", part)
        if not name_actions:
            raise SystemExit(f"cannot read {part!r}")
        member, acts = key(name_actions.group(1)), name_actions.group(2).strip()
        if member not in members:
            members.append(member)
        if "combo" in acts:
            before, _, after = acts.partition("combo")
            lines.extend(actions(member, before))
            lines.extend(combos[member])
            lines.extend(actions(member, after))
        else:
            lines.extend(actions(member, acts))
            if member == "iansan" and "burst" in " ".join(lines[-3:]):  # (also when conditional)
                lines.append("last_f = f();")
    return lines, members


MV = """# Iansan's burst buff needs the active character's movement reported (see the
# Varesa/Iansan rotation): mv() reports move_speed metres per second of field time.
let move_speed = 8;
let last_f = 0;
fn mv() {
  movement((f() - last_f) / 60 * move_speed);
  last_f = f();
}
"""


LIB = json.load(open(Path(__file__).parent / "specs/combos.json"))


def resolve(spec):
    """Combos given as "@carry" come from specs/combos.json, with their functions
    and source credit."""
    funcs, sources = list(spec.get("functions", [])), []
    combos = {}
    for m, c in spec.get("combos", {}).items():
        if isinstance(c, str) and c.startswith("@"):
            e = LIB[c[1:]]
            combos[m] = e["lines"]
            if e.get("functions") and e["functions"] not in funcs:
                funcs.append(e["functions"] + "\n")
            sources.append(e["source"])
        else:
            combos[m] = c
    spec = dict(spec, combos=combos, functions=funcs)
    if sources and not spec.get("combo_source"):
        spec["combo_source"] = "; ".join(sources)
    return spec


def write(spec, out_dir):
    global WHEN_READY
    spec = resolve(spec)
    WHEN_READY = not spec.get("strict_bursts", False)
    body, members = translate(spec["rotation"], spec.get("combos", {}))
    for m in spec.get("extra_members", []):
        if m not in members:
            members.append(m)
    if len(members) != 4:
        raise SystemExit(f"{spec['file']}: {len(members)} members {members}")
    build_lines, sources = [], {}
    for m in members:
        if m in spec.get("builds", {}):  # a character the database and our files lack
            lines, src = spec["builds"][m], "this spec (see notes)"
        else:
            lines, src = builds(m)
        if m in spec.get("gear", {}):  # the archetype's gear (e.g. Stellar-Conduct sets)
            lines, src = regear(m, lines, spec["gear"][m]), "this spec's gear (see notes)"
        sources[m] = src
        if m in spec.get("cons", {}):
            lines = [re.sub(r"cons=\d", f"cons={spec['cons'][m]}", l) for l in lines]
        build_lines.append("\n".join(lines))
    team = ", ".join(NAMES_BY_KEY.get(m, m) for m in members)
    head = [f"# Team: {team}",
            "# Written for this project with tools/guide_rotation.py. Not from the KQM Sim Database.",
            f"# Source: {spec.get('site', 'KQM')}'s {spec['guide']} {'quick guide' if spec.get('site', 'KQM') == 'KQM' else 'guide'}, {spec['url']}",
            f"#   Team \"{spec['team']}\", sample rotation{(' (' + spec['variant'] + ')') if spec.get('variant') else ''}:",
            f"#     {spec['rotation']}"]
    if spec.get("combo_source"):
        head.append(f"#   Combo: {spec['combo_source']}.")
    if WHEN_READY:
        head.append("#   Supports burst when ready (the guide assumes enough ER to burst every rotation).")
    for n in spec.get("notes", []):
        head.append(f"#   {n}")
    head.append("#   Example builds (KQM-standard substats) from KQM Sim Database rotations: " +
                ", ".join(f"{m} {s}" for m, s in sources.items()) + ".")
    head.append("#   The optimizer replaces them with each player's own.")
    loops = spec.get("loops", 6)
    script = "\n".join("  " + l for l in body)
    text = "\n".join(head) + "\n\noptions swap_delay=12 iteration=1000;\n\n" + "\n\n".join(build_lines) + \
        "\n\ntarget lvl=100 resist=0.1 radius=2 pos=0,2.4 hp=999999999;\nenergy every interval=480,720 amount=1;\n\n" + \
        (MV + "\n" if "iansan" in members else "") + "".join(spec.get("functions", [])) + \
        f"active {spec.get('first', members[0])};\nfor let i=0; i<{loops}; i=i+1 {{\n{script}\n}}\n"
    path = Path(out_dir) / f"{spec['file']}.txt"
    path.write_text(text)
    return path


NAMES_BY_KEY = {k: v["name"] for k, v in json.load(open(DISPLAY))["characters"].items()}

if __name__ == "__main__":
    specs = json.load(open(sys.argv[1]))
    out = sys.argv[2] if len(sys.argv) > 2 else OWN
    for s in specs:
        try:
            print(write(s, out))
        except SystemExit as e:  # e.g. no example build: skip this team, keep going
            print(f"SKIPPED {s['file']}: {e}", file=sys.stderr)
