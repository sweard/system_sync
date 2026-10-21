#!/usr/bin/env bash
# Remove only reproducible repository-local validation environments and caches.
set -Eeuo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
PROJECT_ROOT="$(cd -- "$SCRIPT_DIR/.." && pwd -P)"
TARGETS=(
  .system-sync/rebuild/home
  .system-sync/vscode-validation
  .system-sync/flutter-validation/project
  .system-sync/flutter-validation/pub-cache
  .system-sync/go/build
  .system-sync/dist
)

usage() {
  cat <<'EOF'
用法：clean.sh [--dry-run | --apply [--yes]]

默认仅预览固定清理范围和预计回收空间。
  --dry-run    仅预览，不删除文件
  --apply      预览后输入 yes 确认清理
  --yes        与 --apply 一起使用，跳过交互确认
  -h, --help   显示帮助

清理重建 HOME、VS Code 隔离环境、Flutter 副本和 pub 缓存、Go 构建缓存、旧导出包。
保留迁移备份、操作历史、重建/Flutter 验证结果、Go 模块缓存和已安装的 helper。
请先结束相关验证、构建和隔离 VS Code 进程。
EOF
}

fail() {
  printf '[x] %s\n' "$*" >&2
  exit 1
}

usage_error() {
  printf '[x] %s\n' "$*" >&2
  usage >&2
  exit 2
}

MODE=dry-run
EXPLICIT_MODE=""
YES=false
for argument in "$@"; do
  case "$argument" in
    --dry-run|--apply)
      requested="${argument#--}"
      [[ -z "$EXPLICIT_MODE" || "$EXPLICIT_MODE" == "$requested" ]] \
        || usage_error "一次只能指定一种运行模式"
      MODE="$requested"
      EXPLICIT_MODE="$requested"
      ;;
    --yes) YES=true ;;
    -h|--help) usage; exit 0 ;;
    *) usage_error "未知参数：$argument" ;;
  esac
done
[[ "$YES" == false || "$MODE" == apply ]] || usage_error "--yes 必须与 --apply 一起使用"

# Reject links in every component, including .system-sync and a missing target's parents.
validate_target() {
  local relative="$1" component current="$PROJECT_ROOT"
  local components
  IFS=/ read -r -a components <<< "$relative"
  for component in "${components[@]}"; do
    current="$current/$component"
    [[ ! -L "$current" ]] || fail "清理路径不能是符号链接：${current#"$PROJECT_ROOT/"}"
    if [[ -e "$current" && ! -d "$current" ]]; then
      fail "清理路径必须是目录：${current#"$PROJECT_ROOT/"}"
    fi
  done
}

format_size() {
  awk -v kib="$1" 'BEGIN {
    bytes = kib * 1024
    if (bytes >= 1000000000) printf "%.2f GB", bytes / 1000000000
    else if (bytes >= 1000000) printf "%.1f MB", bytes / 1000000
    else printf "%.1f KB", bytes / 1000
  }'
}

# Finder can recreate .DS_Store after rm has enumerated a directory. Retry only
# ENOTEMPTY, and keep permission/other failures visible instead of escalating.
only_not_empty_errors() {
  local errors="$1" line
  [[ -n "$errors" ]] || return 1
  while IFS= read -r line; do
    case "$line" in
      *': Directory not empty') ;;
      *) return 1 ;;
    esac
  done <<< "$errors"
}

remove_target() {
  local relative="$1" target="$PROJECT_ROOT/$1" attempt errors=""
  for attempt in 1 2 3; do
    validate_target "$relative"
    if errors="$(LC_ALL=C rm -rf -- "$target" 2>&1)"; then
      [[ -e "$target" || -L "$target" ]] || return 0
      # A writer may recreate the target even when rm itself returned success.
      errors="rm: $target: Directory not empty"
    fi
    [[ "$attempt" -lt 3 ]] && only_not_empty_errors "$errors" || break
    printf '[!] 目录仍非空，重试清理（%s/3）：%s\n' "$((attempt + 1))" "$relative"
    sleep 0.2
  done
  printf '%s\n' "$errors" >&2
  fail "清理失败，已完成项不会恢复：$relative"
}

PENDING=()
COUNT=0
TOTAL_KIB=0
# Validate the whole allowlist before printing a plan or removing any target.
for relative in "${TARGETS[@]}"; do
  validate_target "$relative"
done
for relative in "${TARGETS[@]}"; do
  target="$PROJECT_ROOT/$relative"
  [[ -d "$target" ]] || continue
  usage_kib="$(du -sk -- "$target")" || fail "无法统计目录占用：$relative"
  read -r size _ <<< "$usage_kib"
  [[ "$size" =~ ^[0-9]+$ ]] || fail "无法识别目录占用：$relative"
  PENDING[COUNT]="$relative"
  COUNT=$((COUNT + 1))
  TOTAL_KIB=$((TOTAL_KIB + size))
  printf '[!] %s  %s\n' "$(format_size "$size")" "$relative"
done
if [[ "$COUNT" -eq 0 ]]; then
  printf '[+] 无需清理：没有可清理的验证目录或缓存。\n'
  exit 0
fi
printf '\n[!] 待清理 %s 项，预计可回收约 %s。\n' "$COUNT" "$(format_size "$TOTAL_KIB")"
if [[ "$MODE" == dry-run ]]; then
  printf '[+] 以上仅为预览，未删除文件；执行清理请使用 --apply。\n'
  exit 0
fi
if [[ "$YES" == false ]]; then
  if [[ ! -t 0 ]]; then
    printf '[x] 非交互环境未执行清理；审阅预览后使用 --apply --yes。\n' >&2
    exit 2
  fi
  printf '输入 yes 确认清理（直接回车或其他输入取消）：'
  answer=""
  if ! IFS= read -r answer || [[ "$answer" != yes ]]; then
    printf '[+] 已取消，未删除文件。\n'
    exit 2
  fi
fi

# Recheck after confirmation so changed paths stop the whole cleanup before deletion.
for relative in "${PENDING[@]}"; do
  validate_target "$relative"
done
for relative in "${PENDING[@]}"; do
  remove_target "$relative"
  printf '[+] 已清理：%s\n' "$relative"
done
printf '[+] 清理完成；需要的构建缓存会在下次构建时重新生成。\n'
