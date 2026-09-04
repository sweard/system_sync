#!/usr/bin/env bash

set -Eeuo pipefail
IFS=$'\n\t'

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
PROJECT_ROOT="$(cd -- "$SCRIPT_DIR/.." && pwd -P)"
PROFILE_DIR="${SYSTEM_SYNC_PROFILE_DIR:-$PROJECT_ROOT/platforms/linux/opensuse}"
PACKAGES_FILE="$PROFILE_DIR/packages.txt"
ALLOWLIST_FILE="$PROFILE_DIR/protected-packages.txt"
STATE_ROOT="$PROJECT_ROOT/.system-sync/history"
MAX_REMOVALS=25

MODE="dry-run"
MODE_EXPLICIT=0
ALLOW_LARGE_CHANGE=0

usage() {
  cat <<'EOF'
用法：zypper-converge.sh [选项]

默认不修改系统，等同于 --dry-run。

  --status              查看声明、缺失项和完整移除事务
  --dry-run             完整预览；不会安装或卸载
  --apply               交互式安装缺失项并移除未声明的 user-installed 包
  --allow-large-change  允许超过安全阈值的移除事务（仍需精确确认）
  -h, --help            显示帮助

mise 暂无内置 zypper manager，因此本脚本读取 packages.txt，并直接使用 zypper。
它不会自动删除 orphaned（仓库已消失）包，只处理未声明根包及 unneeded 依赖。
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

set_mode() {
  local requested="$1"
  if [[ "$MODE_EXPLICIT" -eq 1 && "$MODE" != "$requested" ]]; then
    die "一次只能指定一种运行模式"
  fi
  MODE="$requested"
  MODE_EXPLICIT=1
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
    if [[ ! "$package" =~ ^[A-Za-z0-9][A-Za-z0-9@._:+-]*$ ]]; then
      die "$label 中存在非法或不可安全传参的包名：$package"
    fi
  done < "$file"
}

normalize_list() {
  local source_file="$1"
  local output_file="$2"
  awk '
    {
      line = $0
      sub(/\r$/, "", line)
      sub(/[[:space:]]*#.*/, "", line)
      sub(/^[[:space:]]+/, "", line)
      sub(/[[:space:]]+$/, "", line)
      if (line != "") print line
    }
  ' "$source_file" | LC_ALL=C sort -u > "$output_file"
}

copy_if_present() {
  local source_file="$1"
  local target_file="$2"
  [[ ! -f "$source_file" ]] || cp -- "$source_file" "$target_file"
}

query_zypper_packages() {
  local selector="$1"
  local xml_file="$2"
  local output_file="$3"
  local error_file="$4"

  if ! LC_ALL=C zypper --xmlout --no-refresh packages "$selector" \
    > "$xml_file" 2> "$error_file"; then
    sed 's/^/  /' "$error_file" >&2
    die "zypper packages $selector 查询失败"
  fi
  if ! python3 "$SCRIPT_DIR/parse-zypper-xml.py" solvables "$xml_file" > "$output_file"; then
    die "无法解析 zypper packages $selector 输出"
  fi
  LC_ALL=C sort -u -o "$output_file" "$output_file"
  validate_package_file "$output_file" "zypper 查询结果"
}

query_state() {
  LC_ALL=C rpm -qa --qf '%{NAME}\n' | LC_ALL=C sort -u > "$INSTALLED_FILE"
  validate_package_file "$INSTALLED_FILE" "RPM 已安装包列表"
  query_zypper_packages --userinstalled "$USER_XML_FILE" "$EXPLICIT_FILE" "$USER_ERROR_FILE"
  query_zypper_packages --unneeded "$UNNEEDED_XML_FILE" "$UNNEEDED_FILE" "$UNNEEDED_ERROR_FILE"
}

build_protected_list() {
  printf '%s\n' \
    bash coreutils filesystem glibc openSUSE-release rpm shadow systemd util-linux zypper \
    > "$PROTECTED_FILE"
  normalize_list "$ALLOWLIST_FILE" "$TMP_DIR/protected-user"
  awk 'NF { print }' "$TMP_DIR/protected-user" >> "$PROTECTED_FILE"
  grep -E '^(kernel($|-)|grub2|shim|dracut|cryptsetup|lvm2|mdadm)' \
    "$INSTALLED_FILE" >> "$PROTECTED_FILE" || true
  LC_ALL=C sort -u -o "$PROTECTED_FILE" "$PROTECTED_FILE"
  validate_package_file "$PROTECTED_FILE" "保护清单"
}

compute_diffs() {
  LC_ALL=C comm -23 "$DESIRED_FILE" "$INSTALLED_FILE" > "$MISSING_FILE"
  LC_ALL=C comm -23 "$EXPLICIT_FILE" "$DESIRED_FILE" > "$EXTRA_ALL_FILE"
  LC_ALL=C comm -23 "$EXTRA_ALL_FILE" "$PROTECTED_FILE" > "$EXTRA_FILE"
  LC_ALL=C comm -12 "$EXTRA_ALL_FILE" "$PROTECTED_FILE" > "$PROTECTED_EXTRA_FILE"
  LC_ALL=C sort -u "$EXTRA_FILE" "$UNNEEDED_FILE" > "$REMOVE_TARGETS_FILE"
  LC_ALL=C comm -23 "$REMOVE_TARGETS_FILE" "$PROTECTED_FILE" > "$TMP_DIR/targets-unprotected"
  LC_ALL=C comm -23 "$TMP_DIR/targets-unprotected" "$DESIRED_FILE" > "$REMOVE_TARGETS_FILE"
}

preview_removal() {
  local target
  local rc
  REMOVE_PACKAGES=()
  while IFS= read -r target; do
    [[ -z "$target" ]] || REMOVE_PACKAGES+=("$target")
  done < "$REMOVE_TARGETS_FILE"

  : > "$REMOVE_XML_FILE"
  : > "$REMOVE_ERROR_FILE"
  : > "$REMOVE_TRANSACTION_FILE"
  if [[ "${#REMOVE_PACKAGES[@]}" -eq 0 ]]; then
    return 0
  fi

  set +e
  LC_ALL=C zypper --xmlout --non-interactive --no-refresh \
    remove --dry-run --clean-deps "${REMOVE_PACKAGES[@]}" \
    > "$REMOVE_XML_FILE" 2> "$REMOVE_ERROR_FILE"
  rc=$?
  set -e
  if [[ "$rc" -ne 0 ]]; then
    sed 's/^/  /' "$REMOVE_ERROR_FILE" >&2
    die "zypper remove dry-run 失败（退出码 $rc）"
  fi
  if ! python3 "$SCRIPT_DIR/parse-zypper-xml.py" to-remove \
    "$REMOVE_XML_FILE" > "$REMOVE_TRANSACTION_FILE"; then
    die "无法解析 zypper 移除事务"
  fi
  validate_package_file "$REMOVE_TRANSACTION_FILE" "zypper 移除事务"
  [[ -s "$REMOVE_TRANSACTION_FILE" ]] || die "zypper 对非空目标返回了空移除事务"

  LC_ALL=C sort -u "$DESIRED_FILE" "$PROTECTED_FILE" > "$TMP_DIR/removal-roots"
  LC_ALL=C comm -12 "$REMOVE_TRANSACTION_FILE" "$TMP_DIR/removal-roots" \
    > "$REMOVE_CONFLICTS_FILE"
  if [[ -s "$REMOVE_CONFLICTS_FILE" ]]; then
    print_list "zypper 事务拟移除的声明/保护包" "$REMOVE_CONFLICTS_FILE"
    die "zypper 移除事务触及声明或保护包；拒绝继续"
  fi

  LC_ALL=C comm -23 "$REMOVE_TARGETS_FILE" "$REMOVE_TRANSACTION_FILE" \
    > "$TMP_DIR/removal-missing-targets"
  [[ ! -s "$TMP_DIR/removal-missing-targets" ]] \
    || die "zypper dry-run 没有包含全部移除目标；拒绝继续"
}

check_large_change() {
  local transaction_count
  transaction_count="$(count_lines "$REMOVE_TRANSACTION_FILE")"
  if [[ "$transaction_count" -gt "$MAX_REMOVALS" ]]; then
    if [[ "$ALLOW_LARGE_CHANGE" -ne 1 ]]; then
      die "zypper 事务拟移除 $transaction_count 个包，超过安全阈值 $MAX_REMOVALS；核对 baseline 后使用 --apply --allow-large-change"
    fi
    warn "已显式允许大范围变更：zypper 拟移除 $transaction_count 个包"
  fi
}

refresh_plan() {
  query_state
  build_protected_list
  compute_diffs
  preview_removal
}

while [[ "$#" -gt 0 ]]; do
  case "$1" in
    --status)
      set_mode "status"
      ;;
    --dry-run)
      set_mode "dry-run"
      ;;
    --apply)
      set_mode "apply"
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
command -v zypper >/dev/null 2>&1 || die "找不到 zypper"
command -v rpm >/dev/null 2>&1 || die "找不到 rpm"
command -v python3 >/dev/null 2>&1 || die "找不到 python3"
command -v comm >/dev/null 2>&1 || die "找不到 comm"
[[ -r "$PACKAGES_FILE" && -s "$PACKAGES_FILE" ]] || die "软件清单不存在或为空：$PACKAGES_FILE"
[[ -r "$ALLOWLIST_FILE" ]] || die "保护清单不可读：$ALLOWLIST_FILE"

TMP_DIR="$(mktemp -d)"
[[ -n "$TMP_DIR" && -d "$TMP_DIR" ]] || die "无法创建临时目录"
trap 'rm -rf -- "$TMP_DIR"' EXIT

DESIRED_FILE="$TMP_DIR/desired"
INSTALLED_FILE="$TMP_DIR/installed"
EXPLICIT_FILE="$TMP_DIR/user-installed"
UNNEEDED_FILE="$TMP_DIR/unneeded"
PROTECTED_FILE="$TMP_DIR/protected"
MISSING_FILE="$TMP_DIR/missing"
EXTRA_ALL_FILE="$TMP_DIR/extra-all"
EXTRA_FILE="$TMP_DIR/extra"
PROTECTED_EXTRA_FILE="$TMP_DIR/protected-extra"
REMOVE_TARGETS_FILE="$TMP_DIR/remove-targets"
REMOVE_TRANSACTION_FILE="$TMP_DIR/remove-transaction"
REMOVE_CONFLICTS_FILE="$TMP_DIR/remove-conflicts"
USER_XML_FILE="$TMP_DIR/user-installed.xml"
USER_ERROR_FILE="$TMP_DIR/user-installed.stderr"
UNNEEDED_XML_FILE="$TMP_DIR/unneeded.xml"
UNNEEDED_ERROR_FILE="$TMP_DIR/unneeded.stderr"
REMOVE_XML_FILE="$TMP_DIR/remove.xml"
REMOVE_ERROR_FILE="$TMP_DIR/remove.stderr"
RUN_LOG_DIR=""

normalize_list "$PACKAGES_FILE" "$DESIRED_FILE"
validate_package_file "$DESIRED_FILE" "openSUSE 声明"
[[ -s "$DESIRED_FILE" ]] || die "声明包集合为空，拒绝继续"
grep -Fxq 'filesystem' "$DESIRED_FILE" || die "packages.txt 未声明 filesystem，拒绝继续"

info "读取 openSUSE packages.txt 与 zypper 状态"
refresh_plan
print_list "声明的根包" "$DESIRED_FILE"
print_list "声明但尚未安装" "$MISSING_FILE"
print_list "未声明但受保护的 user-installed 包" "$PROTECTED_EXTRA_FILE"
print_list "拟直接移除的未声明 user-installed 包" "$EXTRA_FILE"
print_list "zypper 当前认定的 unneeded 包" "$UNNEEDED_FILE"
print_list "zypper dry-run 的完整移除事务" "$REMOVE_TRANSACTION_FILE"

if [[ "$MODE" == "status" ]]; then
  printf '\n状态模式：未做任何修改。\n'
  exit 0
fi
if [[ "$MODE" == "dry-run" ]]; then
  printf '\n预览模式：未做任何修改。\n'
  printf 'openSUSE 没有受支持的安装原因降级接口；apply 会在完整 dry-run 校验后直接 remove --clean-deps。\n'
  exit 0
fi

[[ -t 0 ]] || die "--apply 必须在交互式终端运行"
check_large_change
printf '\n即将安装缺失项，并重新计算 zypper 移除事务。请输入精确短语 “APPLY ZYPPER” 继续：'
IFS= read -r FIRST_CONFIRMATION
[[ "$FIRST_CONFIRMATION" == "APPLY ZYPPER" ]] || die "确认不匹配，未做任何修改"

if [[ "$EUID" -ne 0 ]]; then
  command -v sudo >/dev/null 2>&1 || die "需要 root 权限，但找不到 sudo"
  sudo -v
  ROOT_COMMAND=(sudo)
else
  ROOT_COMMAND=()
fi

RUN_ID="$(date -u '+%Y%m%dT%H%M%SZ')-$$-zypper-apply"
RUN_LOG_DIR="$STATE_ROOT/$RUN_ID"
mkdir -p -- "$RUN_LOG_DIR"
copy_if_present "$DESIRED_FILE" "$RUN_LOG_DIR/desired.txt"
copy_if_present "$INSTALLED_FILE" "$RUN_LOG_DIR/installed.before.txt"
copy_if_present "$EXPLICIT_FILE" "$RUN_LOG_DIR/user-installed.before.txt"
copy_if_present "$REMOVE_TARGETS_FILE" "$RUN_LOG_DIR/remove-targets.before.txt"
copy_if_present "$REMOVE_TRANSACTION_FILE" "$RUN_LOG_DIR/remove-transaction.before.txt"
copy_if_present "$REMOVE_XML_FILE" "$RUN_LOG_DIR/remove-transaction.before.xml"
printf 'mode=apply\nmanager=zypper\ncreated_utc=%s\n' "$RUN_ID" > "$RUN_LOG_DIR/run.meta"
info "本次操作前清单已保存到 $RUN_LOG_DIR"

MISSING_PACKAGES=()
while IFS= read -r package; do
  [[ -z "$package" ]] || MISSING_PACKAGES+=("$package")
done < "$MISSING_FILE"
if [[ "${#MISSING_PACKAGES[@]}" -gt 0 ]]; then
  info "交给 zypper 安装缺失声明；请核对 zypper 自己显示的事务"
  "${ROOT_COMMAND[@]}" zypper install "${MISSING_PACKAGES[@]}"
fi

info "安装后重新计算完整移除事务"
refresh_plan
copy_if_present "$REMOVE_TARGETS_FILE" "$RUN_LOG_DIR/remove-targets.final.txt"
copy_if_present "$REMOVE_TRANSACTION_FILE" "$RUN_LOG_DIR/remove-transaction.final.txt"
copy_if_present "$REMOVE_XML_FILE" "$RUN_LOG_DIR/remove-transaction.final.xml"
print_list "安装后的 zypper 完整移除事务" "$REMOVE_TRANSACTION_FILE"
check_large_change

REMOVE_COUNT="$(count_lines "$REMOVE_TRANSACTION_FILE")"
if [[ "$REMOVE_COUNT" -eq 0 ]]; then
  printf '\n完成：声明包均已安装，没有包需要移除。\n'
  exit 0
fi

printf '\n即将用 zypper remove --clean-deps 移除上面 %s 个包。\n' "$REMOVE_COUNT"
printf '请输入精确短语 “REMOVE %s” 继续：' "$REMOVE_COUNT"
IFS= read -r SECOND_CONFIRMATION
[[ "$SECOND_CONFIRMATION" == "REMOVE $REMOVE_COUNT" ]] \
  || die "确认不匹配；未执行卸载，缺失包安装可能已经完成"

info "交给 zypper 显示并执行最终事务；请再次核对包管理器自己的确认列表"
set +e
"${ROOT_COMMAND[@]}" zypper --no-refresh remove --clean-deps "${REMOVE_PACKAGES[@]}"
CLEANUP_RC=$?
set -e
if [[ "$CLEANUP_RC" -ne 0 ]]; then
  warn "zypper remove 已取消或失败（退出码 $CLEANUP_RC）"
  exit "$CLEANUP_RC"
fi

LC_ALL=C rpm -qa --qf '%{NAME}\n' | LC_ALL=C sort -u > "$RUN_LOG_DIR/installed.after.txt"
printf '\n完成。审计清单：%s\n' "$RUN_LOG_DIR"
