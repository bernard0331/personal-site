# The Genshin team optimizer's website as one container: the genshin-web
# server, KQM Sim (with this project's patches), and a Python with HiGHS for
# choosing teams. Built by deploy/genshin-web/build.sh from a context it
# assembles: src/ (the repository's tracked files) and data/ (local-only
# files: the cached KQM Sim Database, the weapon ranking and the images).

# ---- Build: both Go programs, as static Linux binaries ----
# The exact Go version: the same source must give the same simulator binary,
# which the shared cache is keyed on.
FROM golang:1.27.4 AS build
ENV CGO_ENABLED=0 GOOS=linux GOARCH=amd64
WORKDIR /src
COPY src/ .
RUN go build -trimpath -o /out/genshin-web ./services/genshin-web
# build.sh clones KQM Sim at the pinned release and applies the patches.
RUN sh services/genshin-calc/simulator/build.sh /out/kqmsim

# ---- Run ----
FROM python:3.12-slim
# procps: the simulator's memory watchdog reads each process's size with ps.
RUN apt-get update && apt-get install -y --no-install-recommends procps \
    && rm -rf /var/lib/apt/lists/* \
    && python -m venv /opt/highs \
    && /opt/highs/bin/pip install --no-cache-dir highspy==1.16.0 numpy==2.4.6 \
    && useradd --uid 10001 --create-home app
# One thread each for numpy's libraries: four solves already run at once.
ENV OMP_NUM_THREADS=1 OPENBLAS_NUM_THREADS=1
COPY --from=build /out/ /app/
COPY src/services/genshin-calc/rotations/ /app/rotations/
COPY data/ /app/data/
USER 10001
EXPOSE 8080
ENTRYPOINT ["/app/genshin-web"]
# /data is the Cloud Storage mount (runs and the cache's copy); the cache
# itself lives on the instance's disk. 8 vCPUs: 7 run simulations, one is
# left for the server and the solver.
CMD ["-addr", ":8080", \
     "-img", "/app/data/img", \
     "-db", "/app/data/kqm-db.jsonl", \
     "-ranking", "/app/data/ranking.json", \
     "-own", "/app/rotations", \
     "-sim", "/app/kqmsim", \
     "-highs", "/opt/highs/bin/python", \
     "-data", "/data", \
     "-cache", "/tmp/cache.jsonl", \
     "-cache-copy", "/data/cache.jsonl", \
     "-cpus", "7", \
     "-trust-proxy"]
