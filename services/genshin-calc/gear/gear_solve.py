"""Give characters artifacts with the HiGHS integer programming solver.

The Go gear package runs this helper with a problem on stdin and reads the
answer from stdout, both JSON. It needs highspy (and numpy, which highspy
installs).

Input:
  {"chars": [{"team": 0,
              "cands": [{"piece": 12, "slot": "flower", "score": 410.5, "er": 0}, ...],
              "layouts": [{"value": 1200.0, "er": 20, "need": [{"pieces": [12, 40], "count": 4}]}, ...],
              "er_need": 160.0}, ...],          (er_need null: no floor)
   "team_base": [250000.0, ...],
   "objective": "sum" | "min", "time_limit": 60}

Model, for character c, candidate piece p and layout l:
  x[c,p], y[c,l] in {0, 1}
  each slot of c:            sum of x[c,p] over its candidates = 1
  each piece p:              sum of x[c,p] over characters <= 1
  each character c:          sum of y[c,l] = 1
  each need of layout l:     sum of x[c,p] over the need's pieces >= count * y[c,l]
  each character's ER floor: sum er_p x[c,p] + sum er_l y[c,l] >= er_need
  team t's value: base_t + sum of score x + sum of value y over its characters
  "sum": maximise the teams' total;  "min": maximise the weakest team.

Output: {"chars": [{"pieces": [ids], "layout": index}, ...], "optimal": true|false,
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
    h = highspy.Highs()
    h.setOptionValue("output_flag", False)
    h.setOptionValue("threads", 1)
    h.setOptionValue("time_limit", float(p.get("time_limit", 60)))

    cols = []  # (lo, hi, cost); costs set later
    def col():
        cols.append(0.0)
        return len(cols) - 1

    x, y = [], []  # x[c] = [(col, cand)], y[c] = [col per layout]
    for ch in p["chars"]:
        x.append([(col(), cand) for cand in ch["cands"]])
        y.append([col() for _ in ch["layouts"]])
    teams = sorted({ch["team"] for ch in p["chars"]})
    z = col() if p["objective"] == "min" else None

    n = len(cols)
    h.addCols(n, np.zeros(n), np.zeros(n), np.ones(n), 0, np.array([], np.int32), np.array([], np.int32), np.array([]))
    ints = np.arange(n, dtype=np.int32)
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
        if ch.get("er_need") is not None:
            terms = [(c, float(cand["er"])) for c, cand in x[ci] if cand["er"]]
            terms += [(y[ci][li], float(lay["er"])) for li, lay in enumerate(ch["layouts"]) if lay["er"]]
            row(float(ch["er_need"]), INF, terms)
    for cs in users.values():
        if len(cs) > 1:
            row(-INF, 1, [(c, 1.0) for c in cs])

    cost = np.zeros(n)
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
            out["chars"].append({"pieces": pieces, "layout": layout})
    json.dump(out, answer)
    answer.flush()


if __name__ == "__main__":
    main()
