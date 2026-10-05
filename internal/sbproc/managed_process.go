package sbproc

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// ManagedProcess identifies a live systemd invocation and the current bytes at
// its sole config argument. It is NOT proof that the process loaded those bytes:
// callers must additionally witness a successful restart and an epoch change.
type ManagedProcess struct{ Epoch, ConfigHash string }

var managedEpochRE = regexp.MustCompile(`^[a-fA-F0-9-]{16,64}:[a-fA-F0-9]{32}$`)
var managedHashRE = regexp.MustCompile(`^[a-fA-F0-9]{64}$`)

func processQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }

// ManagedProcessScript only accepts a single explicit, absolute config path.
// Config directories, merged files and relative paths cannot establish which
// bytes the process loaded, so they deliberately remain unsupported.
func ManagedProcessScript(unit, path string) (string, error) {
	if !strings.HasPrefix(path, "/") || strings.ContainsAny(path, "\r\n") || unit == "" {
		return "", fmt.Errorf("managed systemd unit and absolute single config path required")
	}
	q := processQuote(unit)
	return `set -eu
boot=$(cat /proc/sys/kernel/random/boot_id)
inv1=$(systemctl show --property=InvocationID --value -- ` + q + `)
systemctl is-active --quiet -- ` + q + `
pid1=$(systemctl show --property=MainPID --value -- ` + q + `)
case "$pid1" in ''|*[!0-9]*|0) exit 1;; esac
# NUL separates argv. A newline in an argument is unsupported, never a substring match.
tr '\000' '\n' < "/proc/$pid1/cmdline" | (
 count=0; pending=0; run=0
 while IFS= read -r arg; do
  if [ "$pending" = 1 ]; then
   test "$arg" = ` + processQuote(path) + ` || exit 1
   count=$((count+1)); pending=0; continue
  fi
  case "$arg" in
   run) run=1;;
   -c|--config) pending=1;;
   --config=*) test "$arg" = ` + processQuote("--config="+path) + ` || exit 1; count=$((count+1));;
   -C|--config-directory|-C*|--config-directory=*|-c?*) exit 1;;
  esac
 done
 test "$pending" = 0; test "$count" = 1; test "$run" = 1
)
digest=$(sha256sum -- ` + processQuote(path) + `); digest=${digest%% *}
pid2=$(systemctl show --property=MainPID --value -- ` + q + `)
inv2=$(systemctl show --property=InvocationID --value -- ` + q + `)
systemctl is-active --quiet -- ` + q + `
test "$pid1" = "$pid2"; test "$inv1" = "$inv2"
printf '%s:%s %s' "$boot" "$inv1" "$digest"`, nil
}

func ParseManagedProcess(out string) (ManagedProcess, error) {
	fields := strings.Fields(out)
	if len(fields) != 2 || !managedEpochRE.MatchString(fields[0]) || !managedHashRE.MatchString(fields[1]) {
		return ManagedProcess{}, fmt.Errorf("cannot verify running systemd process and managed config")
	}
	return ManagedProcess{Epoch: fields[0], ConfigHash: fields[1]}, nil
}

func InspectManagedProcess(ctx context.Context, unit, path string) (ManagedProcess, error) {
	script, err := ManagedProcessScript(unit, path)
	if err != nil {
		return ManagedProcess{}, err
	}
	cmd := exec.CommandContext(ctx, "sh", "-c", script)
	cmd.WaitDelay = 2 * time.Second
	out, err := cmd.Output()
	if err != nil {
		return ManagedProcess{}, fmt.Errorf("cannot verify running systemd process and managed config: %w", err)
	}
	return ParseManagedProcess(string(out))
}
