#!/usr/bin/env bash

set -Eeuo pipefail
IFS=$'\n\t'

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
PROJECT_ROOT="$(cd -- "$SCRIPT_DIR/.." && pwd -P)"
STATE_ROOT="$PROJECT_ROOT/.system-sync/history"
MAX_DEMOTIONS=25

MANAGER="${SYSTEM_SYNC_REASON_MANAGER:-}"
case "$MANAGER" in
  apt)
    PROFILE_NAME="debian"
    MANAGER_LABEL="APT"
    MANDATORY_ROOT="base-files"
    ;;
  dnf)
    PROFILE_NAME="fedora"
    MANAGER_LABEL="DNF"
    MANDATORY_ROOT="filesystem"
    ;;
  *)
    printf '错误：linux-reason-converge.sh 只能由 apt-converge.sh 或 dnf-converge.sh 调用\n' >&2
    exit 1
    ;;
esac

PROFILE_DIR="${SYSTEM_SYNC_PROFILE_DIR:-$PROJECT_ROOT/packages/linux/$PROFILE_NAME}"
ALLOWLIST_FILE="$PROFILE_DIR/protected-packages.txt"
MODE="dry-run"
MODE_EXPLICIT=0
ALLOW_LARGE_CHANGE=0
DNF_COMMAND=""
DNF_MARK_STYLE=""

usage() {
  printf '用法：%s-converge.sh [选项]\n\n' "$MANAGER"
  cat <<'EOF'
默认不修改系统，等同于 --dry-run。

  --status              查看声明、手工根包和当前 orphan
  --dry-run             预览安装原因变化和当前 autoremove 事务
  --apply               交互式调整安装原因，再清理真正 orphan
  --allow-large-change  允许一次降级超过安全阈值（仍需两次确认）
  -h, --help            显示帮助

脚本不会 purge 配置文件。删除阶段交给包管理器自己的 autoremove，并保留其最终确认。
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

copy_if_present() {
  local source_file="$1"
  local target_file="$2"
  [[ ! -f "$source_file" ]] || cp -- "$source_file" "$target_file"
}

read_allowlist() {
  awk '
    {
      line = $0
      sub(/\r$/, "", line)
      sub(/[[:space:]]*#.*/, "", line)
      sub(/^[[:space:]]+/, "", line)
      sub(/[[:space:]]+$/, "", line)
      if (line != "") print line
    }
  ' "$ALLOWLIST_FILE"
}

query_package_state() {
  case "$MANAGER" in
    apt)
      LC_ALL=C dpkg-query -W -f='${db:Status-Abbrev}\t${binary:Package}\n' \
        | awk -F '\t' 'substr($1, 1, 2) == "ii" { print $2 }' \
        | LC_ALL=C sort -u > "$INSTALLED_FILE"
      LC_ALL=C apt-mark showmanual | LC_ALL=C sort -u > "$TMP_DIR/explicit-raw"
      LC_ALL=C apt-mark showauto | LC_ALL=C sort -u > "$TMP_DIR/dependencies-raw"
      LC_ALL=C comm -12 "$INSTALLED_FILE" "$TMP_DIR/explicit-raw" > "$EXPLICIT_FILE"
      LC_ALL=C comm -12 "$INSTALLED_FILE" "$TMP_DIR/dependencies-raw" > "$DEPENDENCY_FILE"
      ;;
    dnf)
      LC_ALL=C rpm -qa --qf '%{NAME}\n' | LC_ALL=C sort -u > "$INSTALLED_FILE"
      if ! LC_ALL=C "$DNF_COMMAND" -q repoquery --installed --userinstalled \
        --queryformat '%{name}' | LC_ALL=C sort -u > "$TMP_DIR/explicit-raw"; then
        die "无法查询 DNF 的 user-installed 包；请确认 repoquery 可用"
      fi
      LC_ALL=C comm -12 "$INSTALLED_FILE" "$TMP_DIR/explicit-raw" > "$EXPLICIT_FILE"
      LC_ALL=C comm -23 "$INSTALLED_FILE" "$EXPLICIT_FILE" > "$DEPENDENCY_FILE"
      ;;
  esac

  validate_package_file "$INSTALLED_FILE" "已安装包列表"
  validate_package_file "$EXPLICIT_FILE" "手工根包列表"
  validate_package_file "$DEPENDENCY_FILE" "依赖包列表"
}

query_orphans() {
  local output_file="$1"
  local raw_file="$2"
  local error_file="$3"
  local rc

  : > "$output_file"
  : > "$raw_file"
  : > "$error_file"
  case "$MANAGER" in
    apt)
      set +e
      LC_ALL=C apt-get -s autoremove > "$raw_file" 2> "$error_file"
      rc=$?
      set -e
      if [[ "$rc" -ne 0 ]]; then
        sed 's/^/  /' "$error_file" >&2
        die "apt-get autoremove 模拟失败（退出码 $rc）"
      fi
      awk '$1 == "Remv" { print $2 }' "$raw_file" | LC_ALL=C sort -u > "$output_file"
      ;;
    dnf)
      set +e
      LC_ALL=C "$DNF_COMMAND" -q repoquery --installed --unneeded \
        --queryformat '%{name}' > "$output_file" 2> "$error_file"
      rc=$?
      set -e
      if [[ "$rc" -ne 0 ]]; then
        sed 's/^/  /' "$error_file" >&2
        die "DNF orphan 查询失败（退出码 $rc）"
      fi
      LC_ALL=C sort -u -o "$output_file" "$output_file"
      cp -- "$output_file" "$raw_file"
      ;;
  esac
  validate_package_file "$output_file" "autoremove 事务"
}

build_protected_list() {
  case "$MANAGER" in
    apt)
      printf '%s\n' \
        apt base-files base-passwd bash coreutils dash dpkg libc6 login passwd \
        sed systemd systemd-sysv tar util-linux
      ;;
    dnf)
      printf '%s\n' \
        bash coreutils dnf dnf5 filesystem glibc rpm setup shadow-utils systemd util-linux
      ;;
  esac > "$PROTECTED_FILE"

  read_allowlist >> "$PROTECTED_FILE"
  case "$MANAGER" in
    apt)
      grep -E '^(linux-(image|headers|modules)|grub|shim|initramfs-tools|cryptsetup|lvm2|mdadm)' \
        "$INSTALLED_FILE" >> "$PROTECTED_FILE" || true
      ;;
    dnf)
      grep -E '^(kernel($|-)|grub2|shim|dracut|cryptsetup|lvm2|mdadm)' \
        "$INSTALLED_FILE" >> "$PROTECTED_FILE" || true
      ;;
  esac
  LC_ALL=C sort -u -o "$PROTECTED_FILE" "$PROTECTED_FILE"
  validate_package_file "$PROTECTED_FILE" "保护清单"
}

compute_diffs() {
  LC_ALL=C comm -12 "$PROTECTED_FILE" "$INSTALLED_FILE" > "$PROTECTED_INSTALLED_FILE"
  LC_ALL=C comm -23 "$DESIRED_FILE" "$INSTALLED_FILE" > "$MISSING_DESIRED_FILE"
  LC_ALL=C comm -23 "$EXPLICIT_FILE" "$DESIRED_FILE" > "$EXTRA_ALL_FILE"
  LC_ALL=C comm -23 "$EXTRA_ALL_FILE" "$PROTECTED_FILE" > "$DEMOTE_FILE"
  LC_ALL=C comm -12 "$EXTRA_ALL_FILE" "$PROTECTED_FILE" > "$PROTECTED_EXTRA_FILE"
  LC_ALL=C sort -u "$DESIRED_FILE" "$PROTECTED_INSTALLED_FILE" > "$TMP_DIR/wanted-roots"
  LC_ALL=C comm -12 "$TMP_DIR/wanted-roots" "$DEPENDENCY_FILE" > "$PROMOTE_FILE"
}

filter_orphans() {
  local source_file="$1"
  local removable_file="$2"
  local protected_file="$3"
  LC_ALL=C sort -u "$DESIRED_FILE" "$PROTECTED_FILE" > "$TMP_DIR/all-roots"
  LC_ALL=C comm -23 "$source_file" "$TMP_DIR/all-roots" > "$removable_file"
  LC_ALL=C comm -12 "$source_file" "$TMP_DIR/all-roots" > "$protected_file"
}

check_large_change() {
  local explicit_count="$1"
  local demote_count="$2"
  local large_by_ratio=0

  if [[ "$explicit_count" -gt 0 && "$demote_count" -ge 5 ]]; then
    if (( demote_count * 100 / explicit_count >= 50 )); then
      large_by_ratio=1
    fi
  fi
  if [[ "$demote_count" -gt "$MAX_DEMOTIONS" || "$large_by_ratio" -eq 1 ]]; then
    if [[ "$ALLOW_LARGE_CHANGE" -ne 1 ]]; then
      die "本次拟降级 $demote_count/$explicit_count 个手工根包，触发大范围变更保护。核对 baseline 后使用 --apply --allow-large-change"
    fi
    warn "已显式允许大范围变更：拟降级 $demote_count/$explicit_count 个手工根包"
  fi
}

mark_packages() {
  local direction="$1"
  shift
  [[ "$#" -gt 0 ]] || return 0

  case "$MANAGER:$direction" in
    apt:dependency)
      "${ROOT_COMMAND[@]}" apt-mark auto "$@"
      ;;
    apt:explicit)
      "${ROOT_COMMAND[@]}" apt-mark manual "$@"
      ;;
    dnf:dependency)
      if [[ "$DNF_MARK_STYLE" == "dnf5" ]]; then
        "${ROOT_COMMAND[@]}" "$DNF_COMMAND" -y mark dependency "$@"
      else
        "${ROOT_COMMAND[@]}" "$DNF_COMMAND" -y mark remove "$@"
      fi
      ;;
    dnf:explicit)
      if [[ "$DNF_MARK_STYLE" == "dnf5" ]]; then
        "${ROOT_COMMAND[@]}" "$DNF_COMMAND" -y mark user "$@"
      else
        "${ROOT_COMMAND[@]}" "$DNF_COMMAND" -y mark install "$@"
      fi
      ;;
  esac
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
command -v mise >/dev/null 2>&1 || die "找不到 mise"
command -v python3 >/dev/null 2>&1 || die "找不到 python3"
command -v comm >/dev/null 2>&1 || die "找不到 comm（通常由 coreutils 提供）"
[[ -r "$PROFILE_DIR/mise.toml" ]] || die "profile 配置不可读：$PROFILE_DIR/mise.toml"
[[ -r "$ALLOWLIST_FILE" ]] || die "保护清单不可读：$ALLOWLIST_FILE"

case "$MANAGER" in
  apt)
    command -v apt-get >/dev/null 2>&1 || die "找不到 apt-get"
    command -v apt-mark >/dev/null 2>&1 || die "找不到 apt-mark"
    command -v dpkg-query >/dev/null 2>&1 || die "找不到 dpkg-query"
    ;;
  dnf)
    command -v dnf >/dev/null 2>&1 || die "找不到 dnf"
    command -v rpm >/dev/null 2>&1 || die "找不到 rpm"
    DNF_COMMAND="$(command -v dnf)"
    if LC_ALL=C "$DNF_COMMAND" mark dependency --help >/dev/null 2>&1; then
      DNF_MARK_STYLE="dnf5"
    elif LC_ALL=C "$DNF_COMMAND" mark remove --help >/dev/null 2>&1; then
      DNF_MARK_STYLE="dnf4"
    else
      die "无法识别当前 DNF 的安装原因命令"
    fi
    ;;
esac

TMP_DIR="$(mktemp -d)"
[[ -n "$TMP_DIR" && -d "$TMP_DIR" ]] || die "无法创建临时目录"
trap 'rm -rf -- "$TMP_DIR"' EXIT

STATUS_JSON="$TMP_DIR/mise-status.json"
STATUS_ERROR="$TMP_DIR/mise-status.stderr"
DECLARED_TSV="$TMP_DIR/declared.tsv"
DESIRED_FILE="$TMP_DIR/desired"
INSTALLED_FILE="$TMP_DIR/installed"
EXPLICIT_FILE="$TMP_DIR/explicit"
DEPENDENCY_FILE="$TMP_DIR/dependencies"
PROTECTED_FILE="$TMP_DIR/protected"
PROTECTED_INSTALLED_FILE="$TMP_DIR/protected-installed"
MISSING_DESIRED_FILE="$TMP_DIR/desired-missing"
EXTRA_ALL_FILE="$TMP_DIR/extra-all"
DEMOTE_FILE="$TMP_DIR/demote"
PROTECTED_EXTRA_FILE="$TMP_DIR/protected-extra"
PROMOTE_FILE="$TMP_DIR/promote"
CURRENT_ORPHANS_FILE="$TMP_DIR/orphans-current"
CURRENT_ORPHANS_RAW_FILE="$TMP_DIR/orphans-current.raw"
CURRENT_ORPHANS_ERROR_FILE="$TMP_DIR/orphans-current.stderr"
CURRENT_REMOVABLE_FILE="$TMP_DIR/orphans-current-removable"
CURRENT_PROTECTED_ORPHANS_FILE="$TMP_DIR/orphans-current-protected"
RUN_LOG_DIR=""

info "读取 $PROFILE_NAME profile 最终生效的 mise $MANAGER 配置"
if ! (cd -- "$PROFILE_DIR" && LC_ALL=C mise bootstrap packages status --json) \
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
fi

if ! python3 "$SCRIPT_DIR/extract-mise-packages.py" \
  --manager "$MANAGER" "$STATUS_JSON" > "$DECLARED_TSV"; then
  die "无法从 mise 最终生效配置取得可靠的 $MANAGER 声明"
fi
awk -F '\t' '{ print $2 }' "$DECLARED_TSV" | LC_ALL=C sort -u > "$DESIRED_FILE"
validate_package_file "$DESIRED_FILE" "mise 声明"
[[ -s "$DESIRED_FILE" ]] || die "声明包集合为空，拒绝继续"
grep -Fxq "$MANDATORY_ROOT" "$DESIRED_FILE" \
  || die "最终生效配置未声明 $MANAGER:$MANDATORY_ROOT，拒绝继续"

query_package_state
build_protected_list
compute_diffs

VIRTUAL_ERROR=0
while IFS=$'\t' read -r manager package state; do
  case "$state" in
    installed|version_mismatch|needs_repair)
      if ! grep -Fxq "$package" "$INSTALLED_FILE"; then
        warn "$manager:$package 状态为 $state，但数据库中没有同名包；请声明具体 provider"
        VIRTUAL_ERROR=1
      fi
      ;;
  esac
done < "$DECLARED_TSV"
[[ "$VIRTUAL_ERROR" -eq 0 ]] || die "检测到无法安全映射的 virtual package 声明"

query_orphans "$CURRENT_ORPHANS_FILE" "$CURRENT_ORPHANS_RAW_FILE" "$CURRENT_ORPHANS_ERROR_FILE"
filter_orphans "$CURRENT_ORPHANS_FILE" "$CURRENT_REMOVABLE_FILE" "$CURRENT_PROTECTED_ORPHANS_FILE"

awk -F '\t' '{ printf "%s:%s [%s]\n", $1, $2, $3 }' "$DECLARED_TSV" \
  | LC_ALL=C sort -u > "$TMP_DIR/declared-display"
print_list "mise 最终生效声明" "$TMP_DIR/declared-display"
print_list "声明但尚未以同名具体包安装" "$MISSING_DESIRED_FILE"
print_list "当前手工根包中未声明、但受保护而保留" "$PROTECTED_EXTRA_FILE"
print_list "apply 时会提升为手工根包的声明/保护包" "$PROMOTE_FILE"
print_list "apply 时会降级为依赖的未声明手工根包" "$DEMOTE_FILE"
print_list "当前 autoremove 事务中的可清理包" "$CURRENT_REMOVABLE_FILE"
print_list "当前 autoremove 事务中因声明或保护规则冲突的包" "$CURRENT_PROTECTED_ORPHANS_FILE"

if [[ "$MODE" == "status" ]]; then
  printf '\n状态模式：未做任何修改。\n'
  exit 0
fi
if [[ "$MODE" == "dry-run" ]]; then
  printf '\n预览模式：未做任何修改。\n'
  printf 'apply 顺序：调整安装原因 -> 重新计算 autoremove -> 校验保护项 -> 再次确认。\n'
  printf '注意：降级后才出现的新 orphan 无法在不修改安装原因的情况下准确预演。\n'
  exit 0
fi

[[ -t 0 ]] || die "--apply 必须在交互式终端运行"
[[ ! -s "$MISSING_DESIRED_FILE" ]] || die "仍有声明包未安装；请先成功执行 mise bootstrap packages apply"

EXPLICIT_COUNT="$(count_lines "$EXPLICIT_FILE")"
DEMOTE_COUNT="$(count_lines "$DEMOTE_FILE")"
check_large_change "$EXPLICIT_COUNT" "$DEMOTE_COUNT"

printf '\n即将修改 %s 安装原因。请输入精确短语 “APPLY %s” 继续：' "$MANAGER_LABEL" "$DEMOTE_COUNT"
IFS= read -r FIRST_CONFIRMATION
[[ "$FIRST_CONFIRMATION" == "APPLY $DEMOTE_COUNT" ]] || die "确认不匹配，未做任何修改"

if [[ "$EUID" -ne 0 ]]; then
  command -v sudo >/dev/null 2>&1 || die "需要 root 权限，但找不到 sudo"
  sudo -v
  ROOT_COMMAND=(sudo)
else
  ROOT_COMMAND=()
fi

RUN_ID="$(date -u '+%Y%m%dT%H%M%SZ')-$$-${MANAGER}-apply"
RUN_LOG_DIR="$STATE_ROOT/$RUN_ID"
mkdir -p -- "$RUN_LOG_DIR"
copy_if_present "$STATUS_JSON" "$RUN_LOG_DIR/mise-status.json"
copy_if_present "$DESIRED_FILE" "$RUN_LOG_DIR/desired.txt"
copy_if_present "$EXPLICIT_FILE" "$RUN_LOG_DIR/explicit.before.txt"
copy_if_present "$DEMOTE_FILE" "$RUN_LOG_DIR/demoted.txt"
copy_if_present "$PROMOTE_FILE" "$RUN_LOG_DIR/promoted.txt"
copy_if_present "$CURRENT_ORPHANS_FILE" "$RUN_LOG_DIR/orphans.before.txt"
copy_if_present "$CURRENT_ORPHANS_RAW_FILE" "$RUN_LOG_DIR/autoremove.before.raw.txt"
printf 'mode=apply\nmanager=%s\ncreated_utc=%s\n' "$MANAGER" "$RUN_ID" > "$RUN_LOG_DIR/run.meta"
info "本次变更清单已保存到 $RUN_LOG_DIR"

DEMOTE_PACKAGES=()
while IFS= read -r package; do
  [[ -z "$package" ]] || DEMOTE_PACKAGES+=("$package")
done < "$DEMOTE_FILE"
PROMOTE_PACKAGES=()
while IFS= read -r package; do
  [[ -z "$package" ]] || PROMOTE_PACKAGES+=("$package")
done < "$PROMOTE_FILE"

if [[ "${#PROMOTE_PACKAGES[@]}" -gt 0 ]]; then
  info "将声明/保护包提升为手工根包"
  mark_packages explicit "${PROMOTE_PACKAGES[@]}"
fi
if [[ "${#DEMOTE_PACKAGES[@]}" -gt 0 ]]; then
  info "将未声明手工根包降级为 dependency（此步骤不卸载文件）"
  mark_packages dependency "${DEMOTE_PACKAGES[@]}"
fi

FINAL_ORPHANS_FILE="$TMP_DIR/orphans-final"
FINAL_ORPHANS_RAW_FILE="$TMP_DIR/orphans-final.raw"
FINAL_ORPHANS_ERROR_FILE="$TMP_DIR/orphans-final.stderr"
FINAL_REMOVABLE_FILE="$TMP_DIR/orphans-final-removable"
FINAL_PROTECTED_FILE="$TMP_DIR/orphans-final-protected"
query_orphans "$FINAL_ORPHANS_FILE" "$FINAL_ORPHANS_RAW_FILE" "$FINAL_ORPHANS_ERROR_FILE"
filter_orphans "$FINAL_ORPHANS_FILE" "$FINAL_REMOVABLE_FILE" "$FINAL_PROTECTED_FILE"
copy_if_present "$FINAL_ORPHANS_FILE" "$RUN_LOG_DIR/orphans.to-remove.txt"
copy_if_present "$FINAL_ORPHANS_RAW_FILE" "$RUN_LOG_DIR/autoremove.final.raw.txt"

print_list "安装原因调整后的 autoremove 完整事务" "$FINAL_ORPHANS_FILE"
if [[ -s "$FINAL_PROTECTED_FILE" ]]; then
  print_list "autoremove 仍会触及声明或保护包" "$FINAL_PROTECTED_FILE"
  die "最终 autoremove 事务触及声明或保护包；已停止，请按日志恢复安装原因"
fi

FINAL_REMOVE_COUNT="$(count_lines "$FINAL_REMOVABLE_FILE")"
if [[ "$FINAL_REMOVE_COUNT" -eq 0 ]]; then
  query_package_state
  copy_if_present "$EXPLICIT_FILE" "$RUN_LOG_DIR/explicit.after.txt"
  printf '\n完成：安装原因已收敛，没有 orphan 需要卸载。\n'
  exit 0
fi

printf '\n即将由 %s autoremove 移除上面 %s 个包；不会 purge 配置。\n' \
  "$MANAGER_LABEL" "$FINAL_REMOVE_COUNT"
printf '请输入精确短语 “REMOVE %s” 继续：' "$FINAL_REMOVE_COUNT"
IFS= read -r SECOND_CONFIRMATION
if [[ "$SECOND_CONFIRMATION" != "REMOVE $FINAL_REMOVE_COUNT" ]]; then
  warn "未执行卸载；安装原因已经调整，请按 $RUN_LOG_DIR 中的 demoted.txt 恢复需要保留的包"
  exit 2
fi

info "交给 $MANAGER_LABEL 显示并执行最终 autoremove 事务；请再次核对包管理器自己的确认列表"
set +e
case "$MANAGER" in
  apt)
    "${ROOT_COMMAND[@]}" apt-get autoremove
    CLEANUP_RC=$?
    ;;
  dnf)
    "${ROOT_COMMAND[@]}" "$DNF_COMMAND" autoremove
    CLEANUP_RC=$?
    ;;
esac
set -e
if [[ "$CLEANUP_RC" -ne 0 ]]; then
  warn "autoremove 已取消或失败（退出码 $CLEANUP_RC）；安装原因调整可能已经完成"
  exit "$CLEANUP_RC"
fi

query_package_state
copy_if_present "$EXPLICIT_FILE" "$RUN_LOG_DIR/explicit.after.txt"
copy_if_present "$INSTALLED_FILE" "$RUN_LOG_DIR/installed.after.txt"
printf '\n完成。审计与恢复清单：%s\n' "$RUN_LOG_DIR"
