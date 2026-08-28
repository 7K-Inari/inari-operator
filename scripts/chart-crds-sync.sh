#!/usr/bin/env bash
# Sync generated CRDs (config/crd/bases) into the inari-operator-crds chart.
# Usage:
#   scripts/chart-crds-sync.sh          # copy CRDs into the chart
#   scripts/chart-crds-sync.sh --check  # fail if the chart is out of sync
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SRC_DIR="$REPO_ROOT/config/crd/bases"
DST_DIR="$REPO_ROOT/charts/inari-operator-crds/templates"

fail() { echo "chart-crds-sync: $*" >&2; exit 1; }

[ -d "$SRC_DIR" ] || fail "source dir not found: $SRC_DIR"
[ -d "$DST_DIR" ] || fail "chart templates dir not found: $DST_DIR"

shopt -s nullglob
src_files=("$SRC_DIR"/platform.inari.io_*.yaml)
[ "${#src_files[@]}" -gt 0 ] || fail "no CRDs found in $SRC_DIR"

if [ "${1:-}" = "--check" ]; then
  drift=0
  for src in "${src_files[@]}"; do
    name="$(basename "$src")"
    dst="$DST_DIR/$name"
    if [ ! -f "$dst" ]; then
      echo "MISSING in chart: $name" >&2
      drift=1
      continue
    fi
    if ! diff -u "$src" "$dst" > /dev/null; then
      echo "DRIFT: $name (run: make chart-crds)" >&2
      drift=1
    fi
  done
  for dst in "$DST_DIR"/platform.inari.io_*.yaml; do
    name="$(basename "$dst")"
    if [ ! -f "$SRC_DIR/$name" ]; then
      echo "STALE in chart (no matching generated CRD): $name" >&2
      drift=1
    fi
  done
  [ "$drift" -eq 0 ] || fail "chart CRDs are out of sync with config/crd/bases"
  echo "chart CRDs are in sync with config/crd/bases"
  exit 0
fi

for src in "${src_files[@]}"; do
  cp "$src" "$DST_DIR/$(basename "$src")"
  echo "synced $(basename "$src")"
done
