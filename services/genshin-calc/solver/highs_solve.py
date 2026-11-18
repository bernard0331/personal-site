"""Choose teams with the HiGHS integer programming solver.

The Go solver package runs this helper with a problem on stdin and reads the
answer from stdout, both JSON. It needs the highspy package (HiGHS's official
Python interface, MIT licence): pip install highspy.

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


def build(options, capacity, keep, require=()):
    """A model over the options in keep, with the resource rows; each required
    resource (a pinned character) is used by exactly one chosen option."""
    h = highspy.Highs()
    h.setOptionValue("output_flag", False)
    x = {i: h.addBinary() for i in keep}
    rows = {}
    for i in keep:
        for r in options[i]["uses"]:
            rows.setdefault(r, []).append(x[i])
    for r, xs in rows.items():
        cap = capacity.get(r, 1)
        if r in require:
            h.addConstr(sum(xs) == 1)
        elif cap >= 0 and len(xs) > cap:
            h.addConstr(sum(xs) <= cap)
    for r in require:
        if r not in rows:  # no team has this character at all
            h.addConstr(h.addBinary() + 1 <= 0)  # infeasible
    return h, x


def solve(h, time_limit):
    h.setOptionValue("time_limit", float(time_limit))
    h.run()
    status = h.modelStatusToString(h.getModelStatus())
    return status, status == "Optimal"


def chosen(h, x):
    vals = h.getSolution().col_value
    return [i for i, v in x.items() if vals[v.index] > 0.5]


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
    h, x = build(options, capacity, every, req)
    h.addConstr(sum(x.values()) <= k)
    h.maximize(sum(x.values()))
    status, optimal = solve(h, limit)
    if status == "Infeasible":
        print(json.dumps({"chosen": [], "optimal": True, "status": status, "infeasible": True}))
        return
    n = len(chosen(h, x))
    if n == 0:
        print(json.dumps({"chosen": [], "optimal": optimal, "status": status}))
        return

    keep = every
    if p["objective"] == "min":
        # 2. The strongest weakest team among n fitting teams.
        h, x = build(options, capacity, every, req)
        h.addConstr(sum(x.values()) == n)
        big = max(o["dps"] for o in options)
        z = h.addVariable(lb=0, ub=big)
        for i in every:
            h.addConstr(z <= options[i]["dps"] + big * (1 - x[i]))
        h.maximize(z)
        status, ok = solve(h, limit)
        optimal = optimal and ok
        weakest = min(options[i]["dps"] for i in chosen(h, x))
        keep = [i for i in every if options[i]["dps"] >= weakest or req.intersection(options[i]["uses"])]

    # 3. The highest total (among teams at least as strong as the weakest).
    h, x = build(options, capacity, keep, req)
    h.addConstr(sum(x.values()) == n)
    h.maximize(sum(options[i]["dps"] * x[i] for i in keep))
    status, ok = solve(h, limit)
    print(json.dumps({"chosen": sorted(chosen(h, x)), "optimal": optimal and ok, "status": status}))


if __name__ == "__main__":
    main()
