# Shared entry guard; source before consuming the caller's arguments.
SYSTEM_SYNC_ORIGINAL_ARGS=("$@")

system_sync_enter() {
  local mode="$1"
  local runtime="$SCRIPT_DIR/sync_runtime.py"
  command -v python3 >/dev/null 2>&1 || { printf '错误：需要 Python 3.11+\n' >&2; exit 1; }
  if [[ "$mode" == "apply" || "$mode" == "upgrade" ]]; then
    if ! python3 "$runtime" --root "$PROJECT_ROOT" --inherited-lock; then
      exec python3 "$runtime" --root "$PROJECT_ROOT" -- \
        "$BASH" "${BASH_SOURCE[1]}" "${SYSTEM_SYNC_ORIGINAL_ARGS[@]}"
    fi
  else
    python3 "$runtime" --root "$PROJECT_ROOT" --check >/dev/null
  fi
}

system_sync_phase() {
  python3 "$SCRIPT_DIR/sync_runtime.py" --root "$PROJECT_ROOT" --phase "$1"
}

system_sync_cancel() {
  printf '已取消：%s\n' "$*" >&2
  exit 2
}
