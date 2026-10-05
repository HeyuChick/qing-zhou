#!/usr/bin/env bash
# Protocol-level regression only. Full generated-core TCP/UDP integration must
# separately use the binary produced by build-singbox.sh.
set -euo pipefail
GO=${GO:-go}
root=$(cd "$(dirname "$0")/.." && pwd)
check_baseline=false
case "${1:-}" in
  '') ;;
  --check-baseline) check_baseline=true; shift ;;
  -h|--help) echo "Usage: $0 [--check-baseline]"; exit 0 ;;
  *) echo "Usage: $0 [--check-baseline]" >&2; exit 2 ;;
esac
(($# == 0)) || { echo 'Unexpected arguments' >&2; exit 2; }
work=$(mktemp -d "${TMPDIR:-/tmp}/qz-vision-framing.XXXXXXXX")
trap 'rm -rf -- "$work"' EXIT
cp "$root/scripts/vision-framing/"* "$work/"
cd "$work"
"$GO" mod download -json github.com/sagernet/sing-vmess@v0.2.9-0.20260929152519-9b95ab8c9478 > module.json
python3 - <<'PY'
import json
info = json.load(open('module.json'))
assert info['Sum'] == 'h1:q2eQn4nq8oWGxRm9zXesXSlPr9GEgPrOGzf4iPtzG9A='
assert info['GoModSum'] == 'h1:P11scgTxMxVVQ8dlM27yNm3Cro40mD0+gHbnqrNGDuY='
if info.get('Origin'):
    assert info['Origin']['Hash'] == '9b95ab8c9478f8e8ebe5758d325ec8cda8197c5c'
PY
"$GO" test -mod=readonly -count=1 -v ./...
if "$check_baseline"; then
  "$GO" mod edit -require=github.com/sagernet/sing-vmess@v0.2.8
  "$GO" mod download -json github.com/sagernet/sing-vmess@v0.2.8 > baseline-module.json
  python3 - <<'PY'
import json
info = json.load(open('baseline-module.json'))
assert info['Sum'] == 'h1:xd5nnDOMlC76RgrLksS4jlk3eMt3c3CvQY3NsjWPWeI='
assert info['GoModSum'] == 'h1:P11scgTxMxVVQ8dlM27yNm3Cro40mD0+gHbnqrNGDuY='
PY
  # A compiler/network failure is not the expected negative control. Require
  # all four named subtests to have executed and failed on the old dependency.
  if "$GO" test -mod=readonly -count=1 -json ./... > baseline.json; then
    echo 'Old dependency unexpectedly passed the negative control' >&2
    exit 1
  fi
  python3 - <<'PY'
import json
failed = {e.get('Test') for line in open('baseline.json') if (e := json.loads(line)).get('Action') == 'fail'}
expected = {'TestVisionFragmentedPaddingHeader/' + str(i) for i in range(1, 5)}
assert expected <= failed, 'baseline did not reproduce all four expected parser failures'
print('Confirmed old v0.2.8 fails each of the four fragmented-header cases')
PY
fi
