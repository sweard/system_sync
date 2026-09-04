#!/usr/bin/env bash

set -Eeuo pipefail
IFS=$'\n\t'

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
PROJECT_ROOT="$(cd -- "$SCRIPT_DIR/.." && pwd -P)"
BREWFILE="$PROJECT_ROOT/platforms/macos/Brewfile"
TRUSTED_FORMULAE_FILE="$PROJECT_ROOT/platforms/macos/trusted-formulae.txt"
PROTECTED_FILE="$PROJECT_ROOT/config/homebrew-protected.txt"
STATE_ROOT="$PROJECT_ROOT/.system-sync/history"

MODE="dry-run"
MODE_EXPLICIT=0

usage() {
  cat <<'EOF'
用法：homebrew-converge.sh [选项]

默认不修改系统，等同于 --dry-run。

  --status   查看 Brewfile、缺失项和 cleanup 预演
  --dry-run  完整预览；不会安装、卸载、untap 或重置信任
  --apply    安装缺失项，再由 Homebrew 展示最终 cleanup 并确认
  --upgrade  单独升级 Brewfile 中的软件，不执行 cleanup
  -h, --help 显示帮助

脚本只收敛 Brewfile 中的 formula、cask 和 tap，不处理 MAS、Cargo、npm、
VS Code 扩展等其他 Brew Bundle 类型。cleanup 不使用 --zap。
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

bundle_list() {
  local type="$1"
  local output_file="$2"
  if ! HOMEBREW_NO_AUTO_UPDATE=1 brew bundle list \
    --file="$BREWFILE" "--$type" > "$output_file"; then
    die "无法读取 Brewfile 中的 ${type} 声明"
  fi
  LC_ALL=C sort -u -o "$output_file" "$output_file"
}

read_protected() {
  awk '
    {
      line = $0
      sub(/\r$/, "", line)
      sub(/[[:space:]]*#.*/, "", line)
      sub(/^[[:space:]]+/, "", line)
      sub(/[[:space:]]+$/, "", line)
      if (line != "") print line
    }
  ' "$PROTECTED_FILE" | LC_ALL=C sort -u > "$PROTECTED_NORMALIZED_FILE"

  local entry
  local type
  local name
  while IFS= read -r entry; do
    type="${entry%%:*}"
    name="${entry#*:}"
    if [[ "$entry" == "$name" || -z "$name" ]]; then
      die "保护清单格式错误：$entry"
    fi
    case "$type" in
      formula|cask|tap)
        ;;
      *)
        die "保护清单存在未知类型：$entry"
        ;;
    esac
    if [[ ! "$name" =~ ^[A-Za-z0-9][A-Za-z0-9@._+/-]*$ ]]; then
      die "保护清单名称包含不支持的字符：$entry"
    fi
  done < "$PROTECTED_NORMALIZED_FILE"
}

find_missing_protected() {
  : > "$MISSING_PROTECTED_FILE"
  local entry
  local type
  local name
  local desired_file
  while IFS= read -r entry; do
    type="${entry%%:*}"
    name="${entry#*:}"
    case "$type" in
      formula)
        desired_file="$DESIRED_FORMULAE_FILE"
        ;;
      cask)
        desired_file="$DESIRED_CASKS_FILE"
        ;;
      tap)
        desired_file="$DESIRED_TAPS_FILE"
        ;;
    esac
    if ! grep -Fxq "$name" "$desired_file"; then
      printf '%s\n' "$entry" >> "$MISSING_PROTECTED_FILE"
    fi
  done < "$PROTECTED_NORMALIZED_FILE"
}

preview_cleanup() {
  local rc
  CLEANUP_GRAPH_WARNING=0
  CLEANUP_HAS_MANAGED_REMOVALS=0
  : > "$CLEANUP_PREVIEW_FILE"
  mkdir -p -- "$PREVIEW_CONFIG_HOME/homebrew"

  # Homebrew cleanup 计算依赖闭包时需要加载第三方 formula。先在临时 XDG
  # 配置中授予精确 formula 信任；不会读取或改写用户的真实信任库。
  local formula
  while IFS= read -r formula; do
    XDG_CONFIG_HOME="$PREVIEW_CONFIG_HOME" HOMEBREW_NO_AUTO_UPDATE=1 \
      brew trust --formula "$formula" >/dev/null
  done < "$TRUSTED_NORMALIZED_FILE"

  set +e
  XDG_CONFIG_HOME="$PREVIEW_CONFIG_HOME" HOMEBREW_NO_AUTO_UPDATE=1 \
    brew bundle cleanup \
    --file="$BREWFILE" --formula --cask --tap \
    < /dev/null > "$CLEANUP_PREVIEW_FILE" 2>&1
  rc=$?
  set -e

  printf '\nHomebrew cleanup 精确预演\n'
  if [[ -s "$CLEANUP_PREVIEW_FILE" ]]; then
    sed 's/^/  /' "$CLEANUP_PREVIEW_FILE"
  else
    printf '  （无输出）\n'
  fi

  # Homebrew 在发现待清理内容、但没有获得确认时按约定返回 1。
  if [[ "$rc" -gt 1 ]] || grep -Eiq '(^|[[:space:]])Error:' "$CLEANUP_PREVIEW_FILE"; then
    die "Homebrew cleanup 预演失败；为避免误判，拒绝继续"
  fi

  if grep -Fq 'Formulae dependency graph sorting found a circular dependency:' \
    "$CLEANUP_PREVIEW_FILE"; then
    CLEANUP_GRAPH_WARNING=1
    warn "Homebrew 报告 formula 依赖图循环；若预演包含受管项目卸载，apply 将失败关闭"
  fi
  if grep -Eq '^Would uninstall (casks|formulae):|^Would untap:' "$CLEANUP_PREVIEW_FILE"; then
    CLEANUP_HAS_MANAGED_REMOVALS=1
  fi
}

guard_cleanup_graph() {
  if [[ "$CLEANUP_GRAPH_WARNING" -eq 1 && "$CLEANUP_HAS_MANAGED_REMOVALS" -eq 1 ]]; then
    die "依赖图循环警告下出现 formula/cask/tap 移除计划；修复 Homebrew 元数据后再 apply"
  fi
}

save_before_state() {
  local mode="$1"
  local run_id
  run_id="$(date -u '+%Y%m%dT%H%M%SZ')-$$-${mode}"
  RUN_LOG_DIR="$STATE_ROOT/$run_id"
  mkdir -p -- "$RUN_LOG_DIR"
  cp -- "$BREWFILE" "$RUN_LOG_DIR/Brewfile.before"
  cp -- "$INSTALLED_FORMULAE_FILE" "$RUN_LOG_DIR/formulae.before.txt"
  cp -- "$INSTALLED_CASKS_FILE" "$RUN_LOG_DIR/casks.before.txt"
  cp -- "$INSTALLED_TAPS_FILE" "$RUN_LOG_DIR/taps.before.txt"
  cp -- "$CLEANUP_PREVIEW_FILE" "$RUN_LOG_DIR/cleanup.preview.txt"
  printf 'mode=%s\ncreated_utc=%s\n' "$mode" "$run_id" > "$RUN_LOG_DIR/run.meta"
  info "本次操作前的清单已保存到 $RUN_LOG_DIR"
}

capture_after_state() {
  HOMEBREW_NO_AUTO_UPDATE=1 brew list --formula --full-name \
    | LC_ALL=C sort -u > "$RUN_LOG_DIR/formulae.after.txt"
  HOMEBREW_NO_AUTO_UPDATE=1 brew list --cask \
    | LC_ALL=C sort -u > "$RUN_LOG_DIR/casks.after.txt"
  HOMEBREW_NO_AUTO_UPDATE=1 brew tap \
    | LC_ALL=C sort -u > "$RUN_LOG_DIR/taps.after.txt"
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
    --upgrade)
      set_mode "upgrade"
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

[[ "$(uname -s)" == "Darwin" ]] || die "此脚本只允许在 macOS 上运行"
command -v brew >/dev/null 2>&1 || die "找不到 Homebrew"
command -v comm >/dev/null 2>&1 || die "找不到 comm"
[[ -r "$BREWFILE" && -s "$BREWFILE" ]] || die "Brewfile 不存在或为空：$BREWFILE"
[[ -r "$PROTECTED_FILE" ]] || die "保护清单不可读：$PROTECTED_FILE"
[[ -r "$TRUSTED_FORMULAE_FILE" ]] || die "临时信任清单不可读：$TRUSTED_FORMULAE_FILE"

TMP_DIR="$(mktemp -d)"
[[ -n "$TMP_DIR" && -d "$TMP_DIR" ]] || die "无法创建临时目录"
trap 'rm -rf -- "$TMP_DIR"' EXIT

DESIRED_FORMULAE_FILE="$TMP_DIR/desired-formulae"
DESIRED_CASKS_FILE="$TMP_DIR/desired-casks"
DESIRED_TAPS_FILE="$TMP_DIR/desired-taps"
INSTALLED_FORMULAE_FILE="$TMP_DIR/installed-formulae"
REQUESTED_FORMULAE_FILE="$TMP_DIR/requested-formulae"
INSTALLED_CASKS_FILE="$TMP_DIR/installed-casks"
INSTALLED_TAPS_FILE="$TMP_DIR/installed-taps"
MISSING_FORMULAE_FILE="$TMP_DIR/missing-formulae"
MISSING_CASKS_FILE="$TMP_DIR/missing-casks"
MISSING_TAPS_FILE="$TMP_DIR/missing-taps"
EXTRA_REQUESTED_FORMULAE_FILE="$TMP_DIR/extra-requested-formulae"
EXTRA_CASKS_FILE="$TMP_DIR/extra-casks"
EXTRA_TAPS_FILE="$TMP_DIR/extra-taps"
PROTECTED_NORMALIZED_FILE="$TMP_DIR/protected"
MISSING_PROTECTED_FILE="$TMP_DIR/missing-protected"
TRUSTED_NORMALIZED_FILE="$TMP_DIR/trusted-formulae"
BUNDLE_CHECK_FILE="$TMP_DIR/bundle-check"
CLEANUP_PREVIEW_FILE="$TMP_DIR/cleanup-preview"
PREVIEW_CONFIG_HOME="$TMP_DIR/xdg-config"
RUN_LOG_DIR=""
CLEANUP_GRAPH_WARNING=0
CLEANUP_HAS_MANAGED_REMOVALS=0

info "读取 macOS Brewfile 声明"
bundle_list formula "$DESIRED_FORMULAE_FILE"
bundle_list cask "$DESIRED_CASKS_FILE"
bundle_list tap "$DESIRED_TAPS_FILE"

DESIRED_TOTAL=$((
  $(count_lines "$DESIRED_FORMULAE_FILE") +
  $(count_lines "$DESIRED_CASKS_FILE") +
  $(count_lines "$DESIRED_TAPS_FILE")
))
[[ "$DESIRED_TOTAL" -gt 0 ]] || die "Brewfile 没有 formula/cask/tap 声明，拒绝继续"

read_protected
find_missing_protected

awk '
  {
    line = $0
    sub(/\r$/, "", line)
    sub(/[[:space:]]*#.*/, "", line)
    sub(/^[[:space:]]+/, "", line)
    sub(/[[:space:]]+$/, "", line)
    if (line != "") print line
  }
' "$TRUSTED_FORMULAE_FILE" | LC_ALL=C sort -u > "$TRUSTED_NORMALIZED_FILE"

while IFS= read -r formula; do
  if [[ ! "$formula" =~ ^[A-Za-z0-9][A-Za-z0-9@._+-]*/[A-Za-z0-9][A-Za-z0-9@._+-]*/[A-Za-z0-9][A-Za-z0-9@._+-]*$ ]]; then
    die "临时信任清单必须使用完整 owner/tap/formula 名称：$formula"
  fi
  grep -Fxq "$formula" "$DESIRED_FORMULAE_FILE" \
    || die "临时信任 formula 未在 Brewfile 声明：$formula"
  grep -Fxq "brew \"$formula\", trusted: true" "$BREWFILE" \
    || die "Brewfile 中的临时信任 formula 缺少精确 trusted: true 声明：$formula"
done < "$TRUSTED_NORMALIZED_FILE"

HOMEBREW_NO_AUTO_UPDATE=1 brew list --formula --full-name \
  | LC_ALL=C sort -u > "$INSTALLED_FORMULAE_FILE"
HOMEBREW_NO_AUTO_UPDATE=1 brew list --formula --full-name --installed-on-request \
  | LC_ALL=C sort -u > "$REQUESTED_FORMULAE_FILE"
HOMEBREW_NO_AUTO_UPDATE=1 brew list --cask \
  | LC_ALL=C sort -u > "$INSTALLED_CASKS_FILE"
HOMEBREW_NO_AUTO_UPDATE=1 brew tap \
  | LC_ALL=C sort -u > "$INSTALLED_TAPS_FILE"

LC_ALL=C comm -23 "$DESIRED_FORMULAE_FILE" "$INSTALLED_FORMULAE_FILE" > "$MISSING_FORMULAE_FILE"
LC_ALL=C comm -23 "$DESIRED_CASKS_FILE" "$INSTALLED_CASKS_FILE" > "$MISSING_CASKS_FILE"
LC_ALL=C comm -23 "$DESIRED_TAPS_FILE" "$INSTALLED_TAPS_FILE" > "$MISSING_TAPS_FILE"
LC_ALL=C comm -23 "$REQUESTED_FORMULAE_FILE" "$DESIRED_FORMULAE_FILE" > "$EXTRA_REQUESTED_FORMULAE_FILE"
LC_ALL=C comm -23 "$INSTALLED_CASKS_FILE" "$DESIRED_CASKS_FILE" > "$EXTRA_CASKS_FILE"
LC_ALL=C comm -23 "$INSTALLED_TAPS_FILE" "$DESIRED_TAPS_FILE" > "$EXTRA_TAPS_FILE"

print_list "Brewfile formula 根声明" "$DESIRED_FORMULAE_FILE"
print_list "Brewfile cask 声明" "$DESIRED_CASKS_FILE"
print_list "Brewfile tap 声明" "$DESIRED_TAPS_FILE"
print_list "尚未安装的 formula" "$MISSING_FORMULAE_FILE"
print_list "尚未安装的 cask" "$MISSING_CASKS_FILE"
print_list "尚未添加的 tap" "$MISSING_TAPS_FILE"
print_list "Homebrew 可识别的未声明 requested formula" "$EXTRA_REQUESTED_FORMULAE_FILE"
print_list "未声明的已安装 cask" "$EXTRA_CASKS_FILE"
print_list "未声明的 tap" "$EXTRA_TAPS_FILE"
print_list "受保护但未在 Brewfile 声明的项目" "$MISSING_PROTECTED_FILE"

set +e
HOMEBREW_NO_AUTO_UPDATE=1 brew bundle check \
  --no-upgrade --verbose --file="$BREWFILE" > "$BUNDLE_CHECK_FILE" 2>&1
BUNDLE_CHECK_RC=$?
set -e
printf '\nHomebrew bundle check\n'
sed 's/^/  /' "$BUNDLE_CHECK_FILE"

if [[ "$MODE" == "upgrade" ]]; then
  [[ ! -s "$MISSING_PROTECTED_FILE" ]] || die "保护项缺少声明，拒绝升级"
  [[ -t 0 ]] || die "--upgrade 必须在交互式终端运行"
  printf '\n升级不会执行 cleanup。请输入精确短语 “UPGRADE HOMEBREW” 继续：'
  IFS= read -r UPGRADE_CONFIRMATION
  [[ "$UPGRADE_CONFIRMATION" == "UPGRADE HOMEBREW" ]] || die "确认不匹配，未做任何修改"
  : > "$CLEANUP_PREVIEW_FILE"
  save_before_state "upgrade"
  brew bundle install --upgrade --file="$BREWFILE"
  capture_after_state
  printf '\n升级完成。审计清单：%s\n' "$RUN_LOG_DIR"
  exit 0
fi

preview_cleanup

if [[ "$MODE" == "status" ]]; then
  printf '\n状态模式：未做任何修改。bundle check 退出码为 %s。\n' "$BUNDLE_CHECK_RC"
  exit 0
fi

if [[ "$MODE" == "dry-run" ]]; then
  printf '\n预览模式：未做任何修改。\n'
  printf 'apply 顺序：brew bundle install --no-upgrade -> 再次检查 -> Homebrew cleanup 最终确认。\n'
  exit 0
fi

[[ ! -s "$MISSING_PROTECTED_FILE" ]] || die "保护项缺少声明，拒绝 apply"
guard_cleanup_graph
[[ -t 0 ]] || die "--apply 必须在交互式终端运行"

printf '\n即将安装缺失项；随后 Homebrew 会再次显示完整 cleanup 清单并询问。\n'
printf '请输入精确短语 “APPLY HOMEBREW” 继续：'
IFS= read -r APPLY_CONFIRMATION
[[ "$APPLY_CONFIRMATION" == "APPLY HOMEBREW" ]] || die "确认不匹配，未做任何修改"

save_before_state "apply"

info "安装 Brewfile 中缺失的项目；不主动升级已安装项目"
brew bundle install --no-upgrade --file="$BREWFILE"

info "确认 Brewfile 声明均已满足"
brew bundle check --no-upgrade --verbose --file="$BREWFILE"

info "安装后重新计算 cleanup 事务"
preview_cleanup
cp -- "$CLEANUP_PREVIEW_FILE" "$RUN_LOG_DIR/cleanup.after-install.preview.txt"
guard_cleanup_graph

printf '\nHomebrew 将列出最终 cleanup 事务并要求确认。\n'
printf '脚本未使用 --zap，也不会自动回答 Homebrew 的确认问题。\n'
set +e
brew bundle cleanup --file="$BREWFILE" --formula --cask --tap
CLEANUP_RC=$?
set -e

if [[ "$CLEANUP_RC" -ne 0 ]]; then
  warn "cleanup 已取消或失败（退出码 $CLEANUP_RC）；此前的安装步骤可能已经完成"
  warn "没有继续执行其他清理；请查看 $RUN_LOG_DIR"
  exit "$CLEANUP_RC"
fi

capture_after_state
printf '\n同步完成。审计与恢复清单：%s\n' "$RUN_LOG_DIR"
