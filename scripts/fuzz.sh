#!/usr/bin/env bash
# Runs every native Go fuzz target in this module for FUZZTIME each
# (default 60s; the nightly CI job uses 30m split across targets).
# A crasher is saved under <pkg>/testdata/fuzz/<Target>/ and fails the run.
#
#   FUZZTIME=2m scripts/fuzz.sh            # all targets
#   FUZZTIME=2m scripts/fuzz.sh ./kanban   # one package
set -euo pipefail
cd "$(dirname "$0")/.."
FUZZTIME=${FUZZTIME:-60s}
pkgs=("$@")
[ ${#pkgs[@]} -gt 0 ] || pkgs=(./kanban ./canvas ./filesystem)
status=0
for pkg in "${pkgs[@]}"; do
  for target in $(go test "$pkg" -list '^Fuzz' | grep '^Fuzz' || true); do
    echo "== $pkg $target ($FUZZTIME)"
    if ! go test "$pkg" -run '^$' -fuzz "^${target}\$" -fuzztime "$FUZZTIME"; then
      status=1
    fi
  done
done
exit $status
