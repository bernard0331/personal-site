"""Give characters artifacts with the HiGHS integer programming solver.

The Go gear package runs this helper with a problem on stdin and reads the
answer from stdout, both JSON. It needs highspy (and numpy, which highspy
installs).

Input:
  {"chars": [{"team": 0,
              "cands": [{"piece": 12, "slot": "flower", "score": 410.5, "er": 0}, ...],
              "layouts": [{"value": 1200.0, "er": 20, "er_need": 160.0,
                           "need": [{"pieces": [12, 40], "count": 4}]}, ...]}, ...],
                                                (a layout's er_need null: no floor)
   "team_base": [250000.0, ...],
   "objective": "sum" | "min", "time_limit": 60}

Model, for character c, candidate piece p and layout l:
  x[c,p], y[c,l] in {0, 1}
  each slot of c:            sum of x[c,p] over its candidates = 1
  each piece p:              sum of x[c,p] over characters <= 1
  each character c:          sum of y[c,l] = 1
  each need of layout l:     sum of x[c,p] over the need's pieces >= count * y[c,l]
  each character's ER floor (its chosen layout's):
                             sum er_p x[c,p] + sum (er_l - er_need_l) y[c,l] >= 0
  team t's value: base_t + sum of score x + sum of value y over its characters
  "sum": maximise the teams' total;  "min": maximise the weakest team.

Output: {"chars": [{"pieces": [ids], "layout": index, "er_short": 0.0}, ...], "optimal": true|false,
         "status": "...", "infeasible": true|false}
"""

import json
import os
import sys

import highspy
import numpy as np

INF = highspy.kHighsInf


def main():
    # HiGHS prints a banner to file descriptor 1; keep it out of the answer by
    # sending descriptor 1 to stderr and writing the answer to a saved copy.
    answer = os.fdopen(os.dup(1), "w")
    os.dup2(2, 1)
    p = json.load(sys.stdin)
    if p.get("mode") == "builds":
        json.dump(solve_builds(p), answer)
        answer.flush()
        return
    # Hard ER floors first (the owner's rule); only if no allocation meets
    # them all, each point missed costs what the character's ER sweep says
    # it is worth in team DPS.
    out = solve(p, soft=False)
    if out["infeasible"]:
        out = solve(p, soft=True)
        out["soft"] = True
    json.dump(out, answer)
    answer.flush()


def solve(p, soft):
    h = highspy.Highs()
    h.setOptionValue("output_flag", False)
    h.setOptionValue("threads", 1)
    h.setOptionValue("time_limit", float(p.get("time_limit", 60)))

    cols = []  # (lo, hi, cost); costs set later
    def col():
        cols.append(0.0)
        return len(cols) - 1

    x, y, short = [], [], []  # x[c] = [(col, cand)], y[c] = [col per layout], short[c]: ER missed
    for ch in p["chars"]:
        x.append([(col(), cand) for cand in ch["cands"]])
        y.append([col() for _ in ch["layouts"]])
        short.append(col())
    teams = sorted({ch["team"] for ch in p["chars"]})
    z = col() if p["objective"] == "min" else None

    n = len(cols)
    h.addCols(n, np.zeros(n), np.zeros(n), np.ones(n), 0, np.array([], np.int32), np.array([], np.int32), np.array([]))
    ints = np.arange(n, dtype=np.int32)
    for s in short:  # continuous, 0 or more; 0 when the floors are hard
        h.changeColBounds(s, 0, INF if soft else 0)
    ints = np.array([i for i in ints if i not in set(short)], dtype=np.int32)
    if z is not None:
        h.changeColBounds(z, -INF, INF)
        ints = ints[ints != z]
    h.changeColsIntegrality(len(ints), ints, np.full(len(ints), highspy.HighsVarType.kInteger))

    lo, hi, starts, idx, vals = [], [], [], [], []
    def row(l, u, terms):
        lo.append(l); hi.append(u); starts.append(len(idx))
        for c, v in terms:
            idx.append(c); vals.append(v)

    users = {}
    for ci, ch in enumerate(p["chars"]):
        by_slot = {}
        for c, cand in x[ci]:
            by_slot.setdefault(cand["slot"], []).append(c)
            users.setdefault(cand["piece"], []).append(c)
        for cs in by_slot.values():
            row(1, 1, [(c, 1.0) for c in cs])
        row(1, 1, [(c, 1.0) for c in y[ci]])
        for li, lay in enumerate(ch["layouts"]):
            for need in lay.get("need") or []:
                want = set(need.get("pieces") or [])
                terms = [(c, 1.0) for c, cand in x[ci] if cand["piece"] in want]
                row(0, INF, terms + [(y[ci][li], -float(need["count"]))])
        # ER floor of the chosen layout: sum er_p x + sum (er_l - need_l) y >= 0
        # short[c] is ER the floor is missed by: 0 in the hard solve; in the
        # soft one each point costs the character's er_value.
        if any(lay.get("er_need") is not None for lay in ch["layouts"]):
            terms = [(c, float(cand["er"])) for c, cand in x[ci] if cand["er"]]
            terms += [(y[ci][li], float(lay["er"]) - float(lay.get("er_need") or 0))
                      for li, lay in enumerate(ch["layouts"])]
            row(0, INF, terms + [(short[ci], 1.0)])
        # CRIT Rate past 100% does nothing, but the measured values can't see
        # the cap: keep the artifacts' CRIT Rate within what's left of it.
        if ch.get("cr_cap") is not None:
            terms = [(c, float(cand["cr"])) for c, cand in x[ci] if cand.get("cr")]
            if terms:
                row(-INF, float(ch["cr_cap"]), terms)
    for cs in users.values():
        if len(cs) > 1:
            row(-INF, 1, [(c, 1.0) for c in cs])

    cost = np.zeros(n)
    for ci, s in enumerate(short):
        cost[s] = -float(p["chars"][ci].get("er_value") or 0)
    if z is None:
        for ci, ch in enumerate(p["chars"]):
            for c, cand in x[ci]:
                cost[c] = cand["score"]
            for li, lay in enumerate(ch["layouts"]):
                cost[y[ci][li]] = lay["value"]
    else:
        # z <= base_t + team t's contributions, for every team t
        cost[z] = 1.0
        for t in teams:
            terms = [(z, 1.0)]
            for ci, ch in enumerate(p["chars"]):
                if ch["team"] != t:
                    continue
                terms += [(c, -cand["score"]) for c, cand in x[ci]]
                terms += [(y[ci][li], -lay["value"]) for li, lay in enumerate(ch["layouts"])]
            row(-INF, float(p["team_base"][t]), terms)

    if lo:
        st = h.addRows(len(lo), np.array(lo, float), np.array(hi, float), len(idx),
                       np.array(starts, np.int32), np.array(idx, np.int32), np.array(vals, float))
        if st != highspy.HighsStatus.kOk:
            raise RuntimeError(f"HiGHS rejected the constraints: {st}")
    h.changeColsCost(n, np.arange(n, dtype=np.int32), cost)
    h.changeObjectiveSense(highspy.ObjSense.kMaximize)
    h.run()

    status = h.modelStatusToString(h.getModelStatus())
    out = {"status": status, "optimal": h.getModelStatus() == highspy.HighsModelStatus.kOptimal,
           "infeasible": h.getModelStatus() == highspy.HighsModelStatus.kInfeasible, "chars": []}
    if h.getInfo().primal_solution_status == 2:
        v = h.getSolution().col_value
        for ci, ch in enumerate(p["chars"]):
            pieces = [cand["piece"] for c, cand in x[ci] if v[c] > 0.5]
            layout = next((li for li, c in enumerate(y[ci]) if v[c] > 0.5), -1)
            out["chars"].append({"pieces": pieces, "layout": layout, "er_short": max(0.0, v[short[ci]])})
    return out


def solve_builds(p):
    """Pick one build per character, no piece in two builds.

    Input: {"mode": "builds", "chars": [{"team": 0, "builds": [{"pieces": [ids], "value": v}, ...]}, ...],
            "team_base": [...], "objective": "sum" | "min", "time_limit": 60}
    Output: {"chosen": [build index per character], "optimal": ..., "infeasible": ...}
    """
    h = highspy.Highs()
    h.setOptionValue("output_flag", False)
    h.setOptionValue("threads", 1)
    h.setOptionValue("time_limit", float(p.get("time_limit", 60)))
    cols = []
    for ci, ch in enumerate(p["chars"]):
        for bi, b in enumerate(ch["builds"]):
            cols.append((ci, bi, b))
    z = len(cols) if p["objective"] == "min" else None
    n = len(cols) + (1 if z is not None else 0)
    h.addCols(n, np.zeros(n), np.zeros(n), np.ones(n), 0, np.array([], np.int32), np.array([], np.int32), np.array([]))
    ints = np.arange(len(cols), dtype=np.int32)
    h.changeColsIntegrality(len(ints), ints, np.full(len(ints), highspy.HighsVarType.kInteger))
    if z is not None:
        h.changeColBounds(z, -INF, INF)
    lo, hi, starts, idx, vals = [], [], [], [], []
    def row(l, u, terms):
        lo.append(l); hi.append(u); starts.append(len(idx))
        for c, v in terms:
            idx.append(c); vals.append(v)
    per_char, users = {}, {}
    for c, (ci, bi, b) in enumerate(cols):
        per_char.setdefault(ci, []).append(c)
        for pid in b["pieces"]:
            users.setdefault(pid, []).append(c)
    for cs in per_char.values():
        row(1, 1, [(c, 1.0) for c in cs])
    for cs in users.values():
        if len(cs) > 1:
            row(-INF, 1, [(c, 1.0) for c in cs])
    cost = np.zeros(n)
    if z is None:
        for c, (ci, bi, b) in enumerate(cols):
            cost[c] = b["value"]
    else:
        cost[z] = 1.0
        for t in sorted({ch["team"] for ch in p["chars"]}):
            terms = [(z, 1.0)] + [(c, -b["value"]) for c, (ci, bi, b) in enumerate(cols) if p["chars"][ci]["team"] == t]
            row(-INF, float(p["team_base"][t]), terms)
    h.addRows(len(lo), np.array(lo, float), np.array(hi, float), len(idx),
              np.array(starts, np.int32), np.array(idx, np.int32), np.array(vals, float))
    h.changeColsCost(n, np.arange(n, dtype=np.int32), cost)
    h.changeObjectiveSense(highspy.ObjSense.kMaximize)
    h.run()
    st = h.getModelStatus()
    out = {"status": h.modelStatusToString(st), "optimal": st == highspy.HighsModelStatus.kOptimal,
           "infeasible": st == highspy.HighsModelStatus.kInfeasible, "chosen": []}
    if h.getInfo().primal_solution_status == 2:
        v = h.getSolution().col_value
        chosen = [-1] * len(p["chars"])
        for c, (ci, bi, b) in enumerate(cols):
            if v[c] > 0.5:
                chosen[ci] = bi
        out["chosen"] = chosen
    return out


if __name__ == "__main__":
    main()
