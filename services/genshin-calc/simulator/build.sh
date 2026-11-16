#!/bin/sh
# Build the simulator this service uses: KQM Sim at a pinned release, plus the
# patches in ./patches, which are changes merged into upstream gcsim that the
# KQM Sim release does not have yet.
#
# Usage: services/genshin-calc/simulator/build.sh [OUTPUT]
#   OUTPUT defaults to bin/kqmsim at the repository root.
#
# Needs git and Go. The source is checked out to a temporary folder and removed
# afterwards; the exact source that was built is the tag plus the patch files.
set -eu

RELEASE=kqmsim-v7.3.3
HERE=$(cd "$(dirname "$0")" && pwd)
ROOT=$(cd "$HERE/../../.." && pwd)
OUT=${1:-$ROOT/bin/kqmsim}
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

git clone --quiet --depth 1 --branch "$RELEASE" https://github.com/KQM-git/kqmsim.git "$WORK/kqmsim"
cd "$WORK/kqmsim"
for p in "$HERE"/patches/*.patch; do
  git -c user.name=build -c user.email=build@localhost am --quiet --3way "$p"
done
mkdir -p "$(dirname "$OUT")"
go build -o "$OUT" ./cmd/gcsim
echo "built $OUT from $RELEASE with $(ls "$HERE"/patches/*.patch | wc -l | tr -d ' ') patches"
