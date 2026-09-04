#!/usr/bin/env bash

set -Eeuo pipefail
IFS=$'\n\t'

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
PROJECT_ROOT="$(cd -- "$SCRIPT_DIR/.." && pwd -P)"
ALLOWLIST_FILE="$PROJECT_ROOT/config/protected-packages.txt"
STATE_ROOT="$PROJECT_ROOT/.system-sync/history"
MAX_DEMOTIONS=25

MODE="dry-run"
ALLOW_LARGE_CHANGE=0

usage() {
  cat <<'EOF'
用法：pacman-converge.sh [选项]

默认不修改系统，等同于 --dry-run。

  --status              只读状态报告
  --dry-run             预览将降级的显式包和当前 orphan
  --apply               交互式执行；需要输入精确确认短语
  --allow-large-change  允许一次降级超过安全阈值（仍需交互确认）
  -h, --help            显示帮助

脚本只管理 pacman 数据库中的包。它不会处理 Flatpak、Snap 或手工安装文件。
EOF
}

info() {
  printf '==> %s\n' "$*"
}

warn() {
  printf '警告：%s\n' "$*" >&2
}

die() {
  printf '错误：%s\n' "$*" >&2
  exit 1
}

count_lines() {
  awk 'NF { count++ } END { print count + 0 }' "$1"
}

print_list() {
  local title="$1"
  local file="$2"
  local count
  count="$(count_lines "$file")"
  printf '\n%s（%s）\n' "$title" "$count"
  if [[ "$count" -eq 0 ]]; then
    printf '  （无）\n'
  else
    sed 's/^/  - /' "$file"
  fi
}

validate_package_file() {
  local file="$1"
  local label="$2"
  local package
  while IFS= read -r package; do
    [[ -z "$package" ]] && continue
    if [[ ! "$package" =~ ^[A-Za-z0-9][A-Za-z0-9@._+-]*$ ]]; then
      die "$label 中存在非法或不可安全传参的包名：$package"
    fi
  done < "$file"
}

query_orphans() {
  local output_file="$1"
  local error_file="$2"
  local rc

  : > "$output_file"
  : > "$error_file"
  set +e
  LC_ALL=C pacman -Qdtq > "$output_file" 2> "$error_file"
  rc=$?
  set -e

  # pacman 在“没有匹配包”时通常返回 1 且无输出；这是正常空集合。
  if [[ "$rc" -ne 0 && ( -s "$output_file" || -s "$error_file" ) ]]; then
    sed 's/^/  /' "$error_file" >&2
    die "pacman -Qdtq 查询失败（退出码 ${rc}）"
  fi
  LC_ALL=C sort -u -o "$output_file" "$output_file"
}

filter_orphans() {
  local source_file="$1"
  local removable_file="$2"
  local protected_file="$3"
  local roots_file="$TMP_DIR/all-roots"

  LC_ALL=C sort -u "$DESIRED_FILE" "$PROTECTED_FILE" > "$roots_file"
  LC_ALL=C comm -23 "$source_file" "$roots_file" > "$removable_file"
  LC_ALL=C comm -12 "$source_file" "$roots_file" > "$protected_file"
}

copy_if_present() {
  local source_file="$1"
  local target_file="$2"
  if [[ -f "$source_file" ]]; then
    cp -- "$source_file" "$target_file"
  fi
}

while [[ "$#" -gt 0 ]]; do
  case "$1" in
    --status)
      MODE="status"
      ;;
    --dry-run)
      MODE="dry-run"
      ;;
    --apply)
      MODE="apply"
      ;;
    --allow-large-change)
      ALLOW_LARGE_CHANGE=1
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      die "未知参数：$1（使用 --help 查看用法）"
      ;;
  esac
  shift
done

if [[ "$ALLOW_LARGE_CHANGE" -eq 1 && "$MODE" != "apply" ]]; then
  die "--allow-large-change 只可与 --apply 一起使用"
fi

[[ "$(uname -s)" == "Linux" ]] || die "此脚本只允许在 Linux 上运行"
command -v pacman >/dev/null 2>&1 || die "找不到 pacman"
command -v mise >/dev/null 2>&1 || die "找不到 mise"
command -v python3 >/dev/null 2>&1 || die "找不到 python3；先安装 Arch 的 python 包"
command -v comm >/dev/null 2>&1 || die "找不到 comm（通常由 coreutils 提供）"
[[ -r "$ALLOWLIST_FILE" ]] || die "保护清单不可读：$ALLOWLIST_FILE"

TMP_DIR="$(mktemp -d)"
[[ -n "$TMP_DIR" && -d "$TMP_DIR" ]] || die "无法创建临时目录"
trap 'rm -rf -- "$TMP_DIR"' EXIT

STATUS_JSON="$TMP_DIR/mise-status.json"
STATUS_ERROR="$TMP_DIR/mise-status.stderr"
DECLARED_TSV="$TMP_DIR/declared.tsv"
DESIRED_FILE="$TMP_DIR/desired"
MISSING_DESIRED_FILE="$TMP_DIR/desired-missing"
EXPLICIT_FILE="$TMP_DIR/explicit"
INSTALLED_FILE="$TMP_DIR/installed"
DEPENDENCY_FILE="$TMP_DIR/dependencies"
PROTECTED_FILE="$TMP_DIR/protected"
PROTECTED_INSTALLED_FILE="$TMP_DIR/protected-installed"
EXTRA_ALL_FILE="$TMP_DIR/extra-all"
DEMOTE_FILE="$TMP_DIR/demote"
PROTECTED_EXTRA_FILE="$TMP_DIR/protected-extra"
PROMOTE_FILE="$TMP_DIR/promote"
CURRENT_ORPHANS_FILE="$TMP_DIR/orphans-current"
CURRENT_ORPHAN_ERROR="$TMP_DIR/orphans-current.stderr"
CURRENT_REMOVABLE_FILE="$TMP_DIR/orphans-current-removable"
CURRENT_PROTECTED_ORPHANS_FILE="$TMP_DIR/orphans-current-protected"

info "读取当前目录最终生效的 mise pacman/AUR 配置"
if ! (cd -- "$PROJECT_ROOT" && LC_ALL=C mise bootstrap packages status --json) \
  > "$STATUS_JSON" 2> "$STATUS_ERROR"; then
  sed 's/^/  /' "$STATUS_ERROR" >&2
  die "mise 无法生成 packages status JSON；没有可靠声明集时绝不继续"
fi

if [[ -s "$STATUS_ERROR" ]]; then
  sed 's/^/  /' "$STATUS_ERROR" >&2
  if grep -Eiq '(^|[^[:alpha:]])(warn(ing)?|error|invalid|unknown|skipp(ed|ing))([^[:alpha:]]|$)' \
    "$STATUS_ERROR"; then
    die "mise 在生成状态时报告警告/错误；配置可能有条目被跳过，拒绝继续"
  fi
  warn "mise status 产生了附加诊断；请确认上面的信息无异常"
fi

if ! python3 "$SCRIPT_DIR/extract-mise-packages.py" "$STATUS_JSON" > "$DECLARED_TSV"; then
  die "无法从 mise 最终生效配置中取得可靠的 pacman/AUR 声明"
fi

awk -F '\t' '{ print $2 }' "$DECLARED_TSV" | LC_ALL=C sort -u > "$DESIRED_FILE"
validate_package_file "$DESIRED_FILE" "mise 声明"
[[ -s "$DESIRED_FILE" ]] || die "声明包集合为空，拒绝继续"
awk -F '\t' '$1 == "pacman" && $2 == "base" { found = 1 } END { exit !found }' \
  "$DECLARED_TSV" || die "最终生效配置未声明 pacman:base，拒绝继续"

LC_ALL=C pacman -Qqe | LC_ALL=C sort -u > "$EXPLICIT_FILE"
LC_ALL=C pacman -Qq | LC_ALL=C sort -u > "$INSTALLED_FILE"
LC_ALL=C pacman -Qqd | LC_ALL=C sort -u > "$DEPENDENCY_FILE"
LC_ALL=C comm -23 "$DESIRED_FILE" "$INSTALLED_FILE" > "$MISSING_DESIRED_FILE"

# 若 mise 报告某个请求已安装，但本地数据库中没有同名真实包，它很可能是 virtual
# provide。仅凭 request 名无法安全找到 provider，所以要求改为声明具体 provider。
VIRTUAL_ERROR=0
while IFS=$'\t' read -r manager package state; do
  case "$state" in
    installed|version_mismatch|needs_repair)
      if ! grep -Fxq "$package" "$INSTALLED_FILE"; then
        warn "${manager}:${package} 状态为 ${state}，但 pacman 中没有同名包；请在 mise.toml 中改用具体 provider 包名"
        VIRTUAL_ERROR=1
      fi
      ;;
  esac
done < "$DECLARED_TSV"
[[ "$VIRTUAL_ERROR" -eq 0 ]] || die "检测到无法安全映射的 virtual package 声明"

# 硬保护项不能通过 allowlist 删除；所有已安装的 linux-* 内核/固件类包也动态保护。
printf '%s\n' \
  base bash coreutils filesystem glibc pacman shadow systemd util-linux \
  > "$PROTECTED_FILE"

awk '
  {
    line = $0
    sub(/\r$/, "", line)
    sub(/[[:space:]]*#.*/, "", line)
    sub(/^[[:space:]]+/, "", line)
    sub(/[[:space:]]+$/, "", line)
    if (line != "") print line
  }
' "$ALLOWLIST_FILE" >> "$PROTECTED_FILE"

grep -E '^(linux($|-)|amd-ucode$|intel-ucode$|grub$|refind$|limine$|syslinux$)' \
  "$INSTALLED_FILE" >> "$PROTECTED_FILE" || true
LC_ALL=C sort -u -o "$PROTECTED_FILE" "$PROTECTED_FILE"
validate_package_file "$PROTECTED_FILE" "保护清单"
LC_ALL=C comm -12 "$PROTECTED_FILE" "$INSTALLED_FILE" > "$PROTECTED_INSTALLED_FILE"

LC_ALL=C comm -23 "$EXPLICIT_FILE" "$DESIRED_FILE" > "$EXTRA_ALL_FILE"
LC_ALL=C comm -23 "$EXTRA_ALL_FILE" "$PROTECTED_FILE" > "$DEMOTE_FILE"
LC_ALL=C comm -12 "$EXTRA_ALL_FILE" "$PROTECTED_FILE" > "$PROTECTED_EXTRA_FILE"

# 声明包和保护包是 root；如果它们过去以依赖方式安装，apply 时提升为 explicit，
# 防止后续 pacman -Rs 递归清理时把这些包带走。
LC_ALL=C sort -u "$DESIRED_FILE" "$PROTECTED_INSTALLED_FILE" > "$TMP_DIR/wanted-roots"
LC_ALL=C comm -12 "$TMP_DIR/wanted-roots" "$DEPENDENCY_FILE" > "$PROMOTE_FILE"

query_orphans "$CURRENT_ORPHANS_FILE" "$CURRENT_ORPHAN_ERROR"
filter_orphans \
  "$CURRENT_ORPHANS_FILE" \
  "$CURRENT_REMOVABLE_FILE" \
  "$CURRENT_PROTECTED_ORPHANS_FILE"

awk -F '\t' '{ printf "%s:%s [%s]\n", $1, $2, $3 }' "$DECLARED_TSV" \
  | LC_ALL=C sort -u > "$TMP_DIR/declared-display"

print_list "mise 最终生效声明" "$TMP_DIR/declared-display"
print_list "声明但尚未以同名具体包安装" "$MISSING_DESIRED_FILE"
print_list "当前显式安装包中未声明、但受保护而保留" "$PROTECTED_EXTRA_FILE"
print_list "apply 时会从 dependency 提升为 explicit 的声明/保护包" "$PROMOTE_FILE"
print_list "apply 时会从 explicit 降级为 dependency 的未声明包" "$DEMOTE_FILE"
print_list "当前真正 orphan 中可清理的包" "$CURRENT_REMOVABLE_FILE"
print_list "当前 orphan 中因声明或保护规则而跳过的包" "$CURRENT_PROTECTED_ORPHANS_FILE"

EXPLICIT_COUNT="$(count_lines "$EXPLICIT_FILE")"
DEMOTE_COUNT="$(count_lines "$DEMOTE_FILE")"
if [[ "$MODE" == "status" ]]; then
  printf '\n状态模式：未做任何修改。\n'
  exit 0
fi

if [[ "$MODE" == "dry-run" ]]; then
  printf '\n预览模式：未做任何修改。\n'
  printf 'apply 顺序：调整安装原因 -> 重新查询 pacman -Qdtq -> 再次列出并确认 -> pacman -Rs。\n'
  printf '注意：降级后才出现的新 orphan 无法在不修改 pacman 数据库的情况下准确预演。\n'
  exit 0
fi

[[ -t 0 ]] || die "--apply 必须在交互式终端运行"
grep -Fxq 'base' "$INSTALLED_FILE" || die "base 尚未安装；请先成功执行 mise bootstrap packages apply"
[[ ! -s "$MISSING_DESIRED_FILE" ]] || die "仍有声明包未安装；请先成功执行 mise bootstrap packages apply，再运行收敛"

LARGE_BY_RATIO=0
if [[ "$EXPLICIT_COUNT" -gt 0 && "$DEMOTE_COUNT" -ge 5 ]]; then
  if (( DEMOTE_COUNT * 100 / EXPLICIT_COUNT >= 50 )); then
    LARGE_BY_RATIO=1
  fi
fi
if [[ "$DEMOTE_COUNT" -gt "$MAX_DEMOTIONS" || "$LARGE_BY_RATIO" -eq 1 ]]; then
  if [[ "$ALLOW_LARGE_CHANGE" -ne 1 ]]; then
    die "本次拟降级 $DEMOTE_COUNT/$EXPLICIT_COUNT 个显式包，触发大范围变更保护。核对 baseline 后，如确属预期，使用 --apply --allow-large-change"
  fi
  warn "已显式允许大范围变更：拟降级 $DEMOTE_COUNT/$EXPLICIT_COUNT 个显式包"
fi

printf '\n即将修改 pacman 安装原因。请输入精确短语 “APPLY %s” 继续：' "$DEMOTE_COUNT"
IFS= read -r FIRST_CONFIRMATION
[[ "$FIRST_CONFIRMATION" == "APPLY $DEMOTE_COUNT" ]] || die "确认不匹配，未做任何修改"

if [[ "$EUID" -ne 0 ]]; then
  command -v sudo >/dev/null 2>&1 || die "需要 root 权限，但找不到 sudo"
  sudo -v
  ROOT_COMMAND=(sudo)
else
  ROOT_COMMAND=()
fi

RUN_ID="$(date -u '+%Y%m%dT%H%M%SZ')"
RUN_ID="${RUN_ID}-$$"
RUN_LOG_DIR="$STATE_ROOT/$RUN_ID"
mkdir -p -- "$RUN_LOG_DIR"
copy_if_present "$STATUS_JSON" "$RUN_LOG_DIR/mise-status.json"
copy_if_present "$DESIRED_FILE" "$RUN_LOG_DIR/desired.txt"
copy_if_present "$EXPLICIT_FILE" "$RUN_LOG_DIR/explicit.before.txt"
copy_if_present "$DEMOTE_FILE" "$RUN_LOG_DIR/demoted.txt"
copy_if_present "$PROMOTE_FILE" "$RUN_LOG_DIR/promoted.txt"
copy_if_present "$CURRENT_ORPHANS_FILE" "$RUN_LOG_DIR/orphans.before.txt"
printf 'mode=apply\ncreated_utc=%s\n' "$RUN_ID" > "$RUN_LOG_DIR/run.meta"
info "本次变更清单已保存到 $RUN_LOG_DIR"

DEMOTE_PACKAGES=()
while IFS= read -r package; do
  [[ -n "$package" ]] && DEMOTE_PACKAGES+=("$package")
done < "$DEMOTE_FILE"

PROMOTE_PACKAGES=()
while IFS= read -r package; do
  [[ -n "$package" ]] && PROMOTE_PACKAGES+=("$package")
done < "$PROMOTE_FILE"

if [[ "${#DEMOTE_PACKAGES[@]}" -gt 0 ]]; then
  info "将未声明显式包降级为 dependency（此步骤不卸载文件）"
  "${ROOT_COMMAND[@]}" pacman -D --asdeps "${DEMOTE_PACKAGES[@]}"
fi

if [[ "${#PROMOTE_PACKAGES[@]}" -gt 0 ]]; then
  info "将声明/保护包提升为 explicit root"
  "${ROOT_COMMAND[@]}" pacman -D --asexplicit "${PROMOTE_PACKAGES[@]}"
fi

FINAL_ORPHANS_FILE="$TMP_DIR/orphans-final"
FINAL_ORPHAN_ERROR="$TMP_DIR/orphans-final.stderr"
FINAL_REMOVABLE_FILE="$TMP_DIR/orphans-final-removable"
FINAL_PROTECTED_FILE="$TMP_DIR/orphans-final-protected"
query_orphans "$FINAL_ORPHANS_FILE" "$FINAL_ORPHAN_ERROR"
filter_orphans "$FINAL_ORPHANS_FILE" "$FINAL_REMOVABLE_FILE" "$FINAL_PROTECTED_FILE"
copy_if_present "$FINAL_ORPHANS_FILE" "$RUN_LOG_DIR/orphans.after-reason-change.txt"
copy_if_present "$FINAL_REMOVABLE_FILE" "$RUN_LOG_DIR/orphans.to-remove.txt"

print_list "安装原因调整后，pacman -Qdtq 确认的可清理 orphan" "$FINAL_REMOVABLE_FILE"
print_list "安装原因调整后，因声明或保护规则跳过的 orphan" "$FINAL_PROTECTED_FILE"

FINAL_REMOVE_COUNT="$(count_lines "$FINAL_REMOVABLE_FILE")"
if [[ "$FINAL_REMOVE_COUNT" -eq 0 ]]; then
  LC_ALL=C pacman -Qqe | LC_ALL=C sort -u > "$RUN_LOG_DIR/explicit.after.txt"
  printf '\n完成：安装原因已收敛，没有 orphan 需要卸载。\n'
  exit 0
fi

REMOVE_PACKAGES=()
while IFS= read -r package; do
  [[ -n "$package" ]] && REMOVE_PACKAGES+=("$package")
done < "$FINAL_REMOVABLE_FILE"

REMOVE_TRANSACTION_FILE="$TMP_DIR/remove-transaction"
REMOVE_TRANSACTION_ERROR="$TMP_DIR/remove-transaction.stderr"
if ! LC_ALL=C pacman -Rsp --print-format '%n' "${REMOVE_PACKAGES[@]}" \
  > "$REMOVE_TRANSACTION_FILE" 2> "$REMOVE_TRANSACTION_ERROR"; then
  sed 's/^/  /' "$REMOVE_TRANSACTION_ERROR" >&2
  die "pacman 无法安全预演 -Rs 完整事务；未执行卸载"
fi
LC_ALL=C sort -u -o "$REMOVE_TRANSACTION_FILE" "$REMOVE_TRANSACTION_FILE"
validate_package_file "$REMOVE_TRANSACTION_FILE" "pacman -Rs 预演事务"
[[ -s "$REMOVE_TRANSACTION_FILE" ]] || die "pacman -Rs 预演返回空事务；未执行卸载"

LC_ALL=C sort -u "$DESIRED_FILE" "$PROTECTED_FILE" > "$TMP_DIR/removal-roots"
LC_ALL=C comm -12 "$REMOVE_TRANSACTION_FILE" "$TMP_DIR/removal-roots" \
  > "$TMP_DIR/removal-protected-conflicts"
if [[ -s "$TMP_DIR/removal-protected-conflicts" ]]; then
  print_list "pacman 预演拟递归移除、但属于声明/保护 root 的冲突包" \
    "$TMP_DIR/removal-protected-conflicts"
  die "pacman -Rs 事务触及声明或保护包；已停止，请先检查依赖关系"
fi

LC_ALL=C comm -23 "$FINAL_REMOVABLE_FILE" "$REMOVE_TRANSACTION_FILE" \
  > "$TMP_DIR/removal-missing-targets"
[[ ! -s "$TMP_DIR/removal-missing-targets" ]] \
  || die "pacman -Rs 预演没有包含全部 orphan 目标；未执行卸载"

copy_if_present "$REMOVE_TRANSACTION_FILE" "$RUN_LOG_DIR/remove-transaction.txt"
print_list "pacman -Rs 预演的完整递归移除事务" "$REMOVE_TRANSACTION_FILE"
REMOVE_TRANSACTION_COUNT="$(count_lines "$REMOVE_TRANSACTION_FILE")"

printf '\n即将以 %s 个 orphan 为目标，用 pacman -Rs 移除上面 %s 个包（不会使用 -Rns）。\n' \
  "$FINAL_REMOVE_COUNT" "$REMOVE_TRANSACTION_COUNT"
printf '请输入精确短语 “REMOVE %s” 继续：' "$REMOVE_TRANSACTION_COUNT"
IFS= read -r SECOND_CONFIRMATION
if [[ "$SECOND_CONFIRMATION" != "REMOVE $REMOVE_TRANSACTION_COUNT" ]]; then
  warn "未执行卸载；安装原因调整已经完成，可按日志用 pacman -D --asexplicit 恢复"
  exit 2
fi

info "交给 pacman -Rs 执行最终事务；请再次核对 pacman 自己显示的完整移除列表"
"${ROOT_COMMAND[@]}" pacman -Rs "${REMOVE_PACKAGES[@]}"

LC_ALL=C pacman -Qqe | LC_ALL=C sort -u > "$RUN_LOG_DIR/explicit.after.txt"
LC_ALL=C pacman -Qq | LC_ALL=C sort -u > "$RUN_LOG_DIR/installed.after.txt"
printf '\n完成。审计与恢复清单：%s\n' "$RUN_LOG_DIR"
