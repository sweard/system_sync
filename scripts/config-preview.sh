#!/usr/bin/env bash

set -Eeuo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
cd -- "$SCRIPT_DIR/.."
export MISE_AUTO_INSTALL=false MISE_TASK_RUN_AUTO_INSTALL=false

case "${1:-}" in
  -h|--help)
    exec mise bootstrap dotfiles diff "$@"
    ;;
esac

# 原生 diff 会渲染模板，并比较内容、权限及链接；不使用 dry-run 的条件计划。
if config_diff="$(mise bootstrap dotfiles diff "$@")"; then
  if [[ -n "$config_diff" ]]; then
    printf '[!] 检测到以下受管配置差异：\n\n%s\n\n' "$config_diff"
    printf '以上仅为预览，未写入配置。\n'
  else
    printf '[+] 未发现需要应用的配置差异，无需同步。\n'
  fi
else
  config_diff_status=$?
  printf '[x] 配置差异检查失败，无法确认是否需要同步；未写入配置。\n' >&2
  exit "$config_diff_status"
fi
