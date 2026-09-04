#!/usr/bin/env bash

set -Eeuo pipefail
IFS=$'\n\t'

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
PROJECT_ROOT="$(cd -- "$SCRIPT_DIR/.." && pwd -P)"

die() {
  printf '错误：%s\n' "$*" >&2
  exit 1
}

MODE="dry-run"
MODE_EXPLICIT=0

set_mode() {
  local requested="$1"
  if [[ "$MODE_EXPLICIT" -eq 1 && "$MODE" != "$requested" ]]; then
    die "一次只能指定一种运行模式"
  fi
  MODE="$requested"
  MODE_EXPLICIT=1
}

for argument in "$@"; do
  case "$argument" in
    --status)
      set_mode "status"
      ;;
    --dry-run)
      set_mode "dry-run"
      ;;
    --apply)
      set_mode "apply"
      ;;
    --upgrade)
      set_mode "upgrade"
      ;;
    -h|--help)
      set_mode "help"
      ;;
    --allow-large-change)
      ;;
    *)
      die "未知参数：$argument"
      ;;
  esac
done

case "$(uname -s)" in
  Darwin)
    exec "$SCRIPT_DIR/homebrew-converge.sh" "$@"
    ;;
  Linux)
    [[ "$MODE" != "upgrade" ]] || die "统一升级任务尚未覆盖 Arch；请使用 pacman 的常规升级流程"
    if [[ "$MODE" == "help" ]]; then
      exec "$SCRIPT_DIR/pacman-converge.sh" --help
    fi
    command -v mise >/dev/null 2>&1 || die "找不到 mise"
    case "$MODE" in
      status)
        (cd -- "$PROJECT_ROOT" && LC_ALL=C mise bootstrap packages status)
        ;;
      dry-run)
        (cd -- "$PROJECT_ROOT" && LC_ALL=C mise bootstrap packages apply --dry-run)
        ;;
      apply)
        (cd -- "$PROJECT_ROOT" && LC_ALL=C mise bootstrap packages apply)
        ;;
      *)
        die "无法识别运行模式"
        ;;
    esac
    exec "$SCRIPT_DIR/pacman-converge.sh" "$@"
    ;;
  *)
    die "不支持的操作系统：$(uname -s)"
    ;;
esac
