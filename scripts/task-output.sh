# Progress stays on stderr, including when command stdout is captured as data.
# A subshell keeps its cleanup traps separate from the calling transaction.
system_sync_step() (
  # Bash 3.2 unwinds function locals before EXIT traps. These variables stay
  # inside this subshell so its ticker is always available during cleanup.
  label="$1"
  started="$SECONDS"
  rc=0
  shift
  printf '[~] %s\n' "$label" >&2
  (
    # A signal can arrive between starting sleep and recording $!. Read the
    # shell's job table during cleanup so that child cannot keep pipes open.
    trap 'trap "" INT TERM; for sleeper in $(jobs -pr); do kill "$sleeper" 2>/dev/null || true; done; wait 2>/dev/null || true; exit 0' INT TERM
    while :; do
      sleep 10 &
      wait "$!" || exit 0
      printf '[~] %s（仍在执行，已等待 %s 秒）\n' "$label" "$((SECONDS - started))" >&2
    done
  ) &
  ticker=$!
  trap 'kill "$ticker" 2>/dev/null || true; wait "$ticker" 2>/dev/null || true' EXIT
  "$@" || rc=$?
  exit "$rc"
)

system_sync_redirect() {
  local output="$1" errors="$2"
  shift 2
  if [[ "$output" == "$errors" ]]; then
    "$@" > "$output" 2>&1
  else
    "$@" > "$output" 2> "$errors"
  fi
}

system_sync_capture() {
  local label="$1"
  shift
  system_sync_step "$label" system_sync_redirect "$@"
}
