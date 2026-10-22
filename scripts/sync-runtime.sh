# Shared entry guard; source before consuming the caller's arguments.
SYSTEM_SYNC_ORIGINAL_ARGS=("$@")
source "$SCRIPT_DIR/task-output.sh"

system_sync_enter() {
  local mode="$1"
  local helper="$SCRIPT_DIR/../helper/scripts/run.sh"
  if [[ "$mode" == "apply" || "$mode" == "upgrade" ]]; then
    if ! "$helper" runtime --inherited-lock; then
      exec "$helper" runtime -- \
        "$BASH" "${BASH_SOURCE[1]}" "${SYSTEM_SYNC_ORIGINAL_ARGS[@]}"
    fi
  else
    "$helper" runtime --check >/dev/null
  fi
}

system_sync_phase() {
  "$SCRIPT_DIR/../helper/scripts/run.sh" runtime --phase "$1"
}

system_sync_cancel() {
  printf '已取消：%s\n' "$*" >&2
  exit 2
}
