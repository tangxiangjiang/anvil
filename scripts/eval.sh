#!/usr/bin/env bash
# Evaluation harness entry point (run from Git Bash or Linux).
#   scripts/eval.sh                  compare every task with source/eval/baseline.json
#   scripts/eval.sh --suite invariants
#   scripts/eval.sh --suite invariants --mode single,multi,complex   (default: all three)
#   scripts/eval.sh --update         record scores (refused under CI=1)
#   scripts/eval.sh --live           add the opt-in live report (needs ANVIL_EVAL_LIVE=1 and a configured model)
set -euo pipefail
cd "$(dirname "$0")/../source"

run='TestBaseline|TestInvariants|TestFixtures|TestReplay|TestCheckTask|TestBaselineRoundTrip'
args=()
modes=""
while [ $# -gt 0 ]; do
  case "$1" in
    --update) args+=(-update) ;;
    --live) args+=(-live) ;;
    --mode)
      shift
      modes="${1:-}"
      [ -n "$modes" ] || { echo "--mode needs a list: single,multi,complex" >&2; exit 2; }
      IFS=, read -ra picked <<<"$modes"
      for m in "${picked[@]}"; do
        case "$m" in single|multi|complex) ;; *) echo "unknown mode: $m (single, multi, complex)" >&2; exit 2 ;; esac
      done ;;
    --suite)
      shift
      [ "${1:-}" = invariants ] || { echo "unknown suite: ${1:-}" >&2; exit 2; }
      run='TestInvariants' ;;
    *) echo "unknown option: $1" >&2; exit 2 ;;
  esac
  shift
done
# --mode only selects which modes the invariants suite runs; every mode runs by default.
[ -z "$modes" ] || export ANVIL_EVAL_MODES="$modes"
if [ "${#args[@]}" -gt 0 ] && [[ " ${args[*]} " == *" -live "* ]]; then run="$run|TestLive"; fi

out=$(mktemp)
trap 'rm -f "$out"' EXIT
status=0
go test -tags anvileval -count=1 -v ./eval/... -run "$run" ${args[@]+"${args[@]}"} >"$out" 2>&1 || status=$?
echo "task                                   result"
grep -E '^\s+--- (PASS|FAIL|SKIP): Test(Baseline|Invariants)/' "$out" | sed -E 's/^\s+--- ([A-Z]+): Test[A-Za-z]+\/([^ ]+).*/\2 \1/' | awk '{printf "%-38s %s\n", $1, $2}'
grep -E 'note:' "$out" || true
if [ "$status" -ne 0 ]; then grep -vE '^(=== |\s+--- PASS)' "$out" | tail -40; fi
exit "$status"
