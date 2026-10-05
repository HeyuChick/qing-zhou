#!/usr/bin/env bash
# Apply exactly the reviewed three-file upstream buffer-preservation backport.
set -euo pipefail
root=$(cd "$(dirname "$0")" && pwd)
source "$root/singbox-pins.sh"
(($# == 1)) || { echo "Usage: $0 SOURCE_DIR" >&2; exit 2; }
source_dir=$(cd "$1" && pwd)
patch="$root/transport-buffer/07512b10-selected.patch"
manifest="$root/transport-buffer/manifest.json"
[[ $(git -C "$source_dir" rev-parse HEAD) == "$SB_COMMIT" ]] || { echo 'Unreviewed sing-box base' >&2; exit 1; }
[[ $(sha256sum "$patch" | cut -d' ' -f1) == "$TRANSPORT_PATCH_SHA256" ]] || { echo 'Transport patch hash mismatch' >&2; exit 1; }
python3 - "$manifest" "$source_dir" "$SB_COMMIT" "$TRANSPORT_UPSTREAM_COMMIT" "$TRANSPORT_PATCH_SHA256" before_sha256 <<'PY'
import hashlib,json,pathlib,sys
m=json.load(open(sys.argv[1]));root=pathlib.Path(sys.argv[2])
assert m['base_commit']==sys.argv[3] and m['upstream_commit']==sys.argv[4] and m['patch_sha256']==sys.argv[5]
expected={'transport/v2rayhttpupgrade/client.go','transport/v2rayhttpupgrade/server.go','transport/v2raywebsocket/client.go'}
assert {f['path'] for f in m['files']}==expected
for f in m['files']: assert hashlib.sha256((root/f['path']).read_bytes()).hexdigest()==f[sys.argv[6]], 'unexpected source content: '+f['path']
PY
git -C "$source_dir" apply --check "$patch"
git -C "$source_dir" apply "$patch"
python3 - "$manifest" "$source_dir" <<'PY'
import hashlib,json,pathlib,sys
m=json.load(open(sys.argv[1]));root=pathlib.Path(sys.argv[2])
for f in m['files']: assert hashlib.sha256((root/f['path']).read_bytes()).hexdigest()==f['after_sha256'], 'unexpected patched content: '+f['path']
print('Verified selected upstream transport patch '+m['upstream_commit']+' sha256='+m['patch_sha256'])
PY
