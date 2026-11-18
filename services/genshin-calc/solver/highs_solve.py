"""Choose teams with the HiGHS integer programming solver.

The Go solver package runs this helper with a problem on stdin and reads the
answer from stdout, both JSON. It needs the highspy package (HiGHS's official
Python interface, MIT licence) and numpy, which highspy installs: pip install highspy.

Input:
  {"options": [{"dps": 213000.0, "uses": ["c:bennett", "w:azurelight", ...]}, ...],
   "capacity": {"c:bennett": 1, "w:azurelight": 1, ...},
   "k": 10, "objective": "sum" | "min", "time_limit": 300,
   "require": ["c:arlecchino", ...]}   (optional: resources every answer must use)

Each option is one team with one choice of weapons; "uses" lists what it uses
up (characters, limited weapon copies, and for one team per element the
element). The model:

  x_i in {0, 1}                       take option i or not
  sum of x_i over options using r <= capacity[r]   for every resource r
  sum of x_i  = n                     n = the most teams that fit, at most k

  "sum": maximise sum of dps_i x_i            (highest average)
  "min": maximise z with z <= dps_i + M (1 - x_i) for every i  (best weakest),
         then, keeping only options with dps >= z, maximise the sum.

Output: {"chosen": [indices], "optimal": true|false, "status": "...", "infeasible": true|false}
"""

import json
import os
import sys

import highspy
import numpy as np

INF = highspy.kHighsInf


class Model:
    """A HiGHS model built from arrays in a few calls: building it one Python
    expression at a time took longer than solving it (20-28 s for 12,000
    options). x[i] is the column of option i."""

    def __init__(self, options, capacity, keep, require=()):
        h = highspy.Highs()
        h.setOptionValue("output_flag", False)
        self.h, self.keep, self.n = h, list(keep), len(keep)
        n = self.n
        h.addCols(n, np.zeros(n), np.zeros(n), np.ones(n), 0, np.array([], np.int32), np.array([], np.int32), np.array([]))
        h.changeColsIntegrality(n, np.arange(n, dtype=np.int32), np.full(n, highspy.HighsVarType.kInteger))
        self.x = {i: c for c, i in enumerate(self.keep)}
        # rows[r][c]: copies of resource r option c uses (two members can hold
        # copies of the same weapon).
        rows = {}
        for c, i in enumerate(self.keep):
            for r in options[i]["uses"]:
                rows.setdefault(r, {})
                rows[r][c] = rows[r].get(c, 0) + 1
        lo, hi, starts, idx, vals = [], [], [], [], []
        for r, cs in rows.items():
            cap = capacity.get(r, 1)
            if r in require:  # a pinned character: in exactly one chosen option
                lo.append(1); hi.append(1)
            elif cap >= 0 and sum(cs.values()) > cap:
                lo.append(-INF); hi.append(cap)
            else:
                continue
            starts.append(len(idx)); idx.extend(cs); vals.extend(cs.values())
        if any(r not in rows for r in require):  # no team has this character at all
            lo.append(1); hi.append(1); starts.append(len(idx))  # an empty row that must be 1: infeasible
        self.add_rows(lo, hi, starts, idx, vals)

    def add_rows(self, lo, hi, starts, idx, vals):
        if lo:
            st = self.h.addRows(len(lo), np.array(lo, float), np.array(hi, float), len(idx),
                                np.array(starts, np.int32), np.array(idx, np.int32), np.array(vals, float))
            if st != highspy.HighsStatus.kOk:
                raise RuntimeError(f"HiGHS rejected the constraints: {st}")

    def count(self, lo, hi):
        """lo <= number of chosen options <= hi."""
        self.add_rows([lo], [hi], [0], list(range(self.n)), [1.0] * self.n)

    def maximize(self, costs):
        h = self.h
        h.changeColsCost(len(costs), np.arange(len(costs), dtype=np.int32), np.array(costs, float))
        h.changeObjectiveSense(highspy.ObjSense.kMaximize)

    def chosen(self):
        vals = self.h.getSolution().col_value
        return [i for i, c in self.x.items() if vals[c] > 0.5]


def solve(m, time_limit):
    m.h.setOptionValue("time_limit", float(time_limit))
    m.h.run()
    status = m.h.modelStatusToString(m.h.getModelStatus())
    return status, status == "Optimal"


def main():
    # HiGHS prints a banner to file descriptor 1; keep it out of the answer by
    # sending descriptor 1 to stderr and writing the answer to a saved copy.
    answer = os.fdopen(os.dup(1), "w")
    os.dup2(2, 1)
    global print
    print = lambda s: answer.write(s + "\n") or answer.flush()
    p = json.load(sys.stdin)
    options, capacity, k = p["options"], p["capacity"], p["k"]
    req = set(p.get("require") or [])
    limit = p.get("time_limit", 300)
    every = list(range(len(options)))
    if not options:
        print(json.dumps({"chosen": [], "optimal": True, "status": "empty"}))
        return

    # 1. The most teams that fit together, at most k.
    m = Model(options, capacity, every, req)
    m.count(-INF, k)
    m.maximize([1.0] * m.n)
    status, optimal = solve(m, limit)
    if status == "Infeasible":
        print(json.dumps({"chosen": [], "optimal": True, "status": status, "infeasible": True}))
        return
    n = len(m.chosen())
    if n == 0:
        print(json.dumps({"chosen": [], "optimal": optimal, "status": status}))
        return

    keep = every
    if p["objective"] == "min":
        # 2. The strongest weakest team among n fitting teams: an extra column
        # z with z <= dps_i + big (1 - x_i), i.e. z + big x_i <= dps_i + big.
        m = Model(options, capacity, every, req)
        m.count(n, n)
        big = max(o["dps"] for o in options)
        m.h.addCols(1, np.array([0.0]), np.array([0.0]), np.array([big]), 0, np.array([], np.int32), np.array([], np.int32), np.array([]))
        z = m.n
        lo, hi, starts, idx, vals = [], [], [], [], []
        for i, c in m.x.items():
            lo.append(-INF); hi.append(options[i]["dps"] + big); starts.append(len(idx))
            idx += [z, c]; vals += [1.0, big]
        m.add_rows(lo, hi, starts, idx, vals)
        costs = [0.0] * (m.n + 1); costs[z] = 1.0
        m.maximize(costs)
        status, ok = solve(m, limit)
        optimal = optimal and ok
        weakest = min(options[i]["dps"] for i in m.chosen())
        keep = [i for i in every if options[i]["dps"] >= weakest or req.intersection(options[i]["uses"])]

    # 3. The highest total (among teams at least as strong as the weakest).
    m = Model(options, capacity, keep, req)
    m.count(n, n)
    m.maximize([options[i]["dps"] for i in m.keep])
    status, ok = solve(m, limit)
    print(json.dumps({"chosen": sorted(m.chosen()), "optimal": optimal and ok, "status": status}))

if __name__ == "__main__":
    main()
