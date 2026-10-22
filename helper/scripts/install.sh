#!/usr/bin/env bash
# Install from repository bundles or a reviewed local artifact directory; never downloads or compiles.
set -Eeuo pipefail
source "$(dirname -- "${BASH_SOURCE[0]}")/common.sh"

if [[ "${1:-}" == --help || "${1:-}" == -h ]]; then
  printf '用法：install.sh [包含平台 tar.gz 和 SHA256SUMS 的目录]\n默认使用仓库 helper/prebuilt/<版本>/；无需 Go、Python 或联网。\n'
  exit 0
fi
[[ $# -le 1 ]] || helper_error "用法：install.sh [产物目录]"
VERSION="$(helper_version)"
ARTIFACT_DIR="${1:-$HELPER_DIR/prebuilt/$VERSION}"
[[ -d "$ARTIFACT_DIR" ]] || helper_error "尚未准备 Go helper：缺少预编译目录 ${ARTIFACT_DIR}；请完整克隆仓库或指定本机产物目录，见 helper/README.md"
ARTIFACT_DIR="$(cd -- "$ARTIFACT_DIR" && pwd -P)"
TARGET="$(helper_target)"
printf '[~] 校验本机 helper 预编译包：%s\n' "$TARGET" >&2
ARCHIVE="system-sync-helper_${VERSION}_${TARGET}.tar.gz"
[[ -f "$ARTIFACT_DIR/$ARCHIVE" && -f "$ARTIFACT_DIR/SHA256SUMS" ]] || helper_error "缺少当前平台的 $ARCHIVE 或 SHA256SUMS"
EXPECTED="$(awk -v name="$ARCHIVE" '$2 == name { print $1 }' "$ARTIFACT_DIR/SHA256SUMS")"
[[ "$EXPECTED" =~ ^[0-9a-f]{64}$ && "$(helper_checksum "$ARTIFACT_DIR/$ARCHIVE")" == "$EXPECTED" ]] \
  || helper_error "预编译归档 SHA256 校验失败"
CONTENTS="$(tar -tzf "$ARTIFACT_DIR/$ARCHIVE" | LC_ALL=C sort)"
[[ "$CONTENTS" == $'LICENSE-Go\nLICENSE-go-toml\nREVISION\nSHA256SUMS\nVERSION\nsystem-sync-helper' ]] || helper_error "预编译归档包含不符合约定的路径"
tar -tvzf "$ARTIFACT_DIR/$ARCHIVE" | awk 'substr($0, 1, 1) != "-" { invalid = 1 } END { exit invalid }' \
  || helper_error "预编译归档只允许普通文件"
helper_state
# A separate installation lock avoids racing first launches without depending on
# the package-operation lock (a guarded package child may bootstrap this helper).
INSTALL_LOCK="$REPO_ROOT/.system-sync/helper/.install-lock"
LOCKED=false
for ((ATTEMPT = 0; ATTEMPT < 100; ATTEMPT++)); do
  if mkdir -- "$INSTALL_LOCK" 2>/dev/null; then LOCKED=true; break; fi
  [[ ! -L "$INSTALL_LOCK" && -d "$INSTALL_LOCK" ]] || helper_error "helper 安装锁路径异常"
  if [[ "$ATTEMPT" -eq 0 ]]; then printf '[~] 等待另一条 helper 安装释放锁（最多 10 秒）\n' >&2; fi
  sleep 0.1
done
[[ "$LOCKED" == true ]] || helper_error "另一个 helper 安装仍在进行；若进程已退出，请核对后移除 $INSTALL_LOCK 再重试"
STAGING=""
POINTER=""
cleanup() {
  [[ -z "$STAGING" ]] || rm -rf -- "$STAGING"
  [[ -z "$POINTER" ]] || rm -f -- "$POINTER"
  rmdir -- "$INSTALL_LOCK" 2>/dev/null || true
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
STAGING="$(mktemp -d "$REPO_ROOT/.system-sync/helper/.install-XXXXXX")"
printf '[~] 解包并验证 helper 二进制\n' >&2
tar -xzf "$ARTIFACT_DIR/$ARCHIVE" -C "$STAGING"
[[ "$(cat "$STAGING/VERSION")" == "$VERSION" ]] || helper_error "归档版本与仓库不匹配"
BINARY_CHECKSUM="$(awk '$2 == "system-sync-helper" { print $1 }' "$STAGING/SHA256SUMS")"
[[ "$BINARY_CHECKSUM" =~ ^[0-9a-f]{64}$ && "$(helper_checksum "$STAGING/system-sync-helper")" == "$BINARY_CHECKSUM" ]] \
  || helper_error "归档内的二进制 SHA256 校验失败"
chmod 755 "$STAGING/system-sync-helper"
INFO="$("$STAGING/system-sync-helper" --version)"
[[ "$INFO" == "system-sync-helper $VERSION protocol=1 target=${TARGET%_*}/${TARGET#*_} "* ]] || helper_error "二进制版本、协议或平台不匹配"
[[ ! -L "$REPO_ROOT/.system-sync/helper/$VERSION" ]] || helper_error "安装版本目录不能是符号链接"
mkdir -p -- "$REPO_ROOT/.system-sync/helper/$VERSION"
DESTINATION="$REPO_ROOT/.system-sync/helper/$VERSION/$BINARY_CHECKSUM"
[[ ! -L "$REPO_ROOT/.system-sync/helper/current" && ! -d "$REPO_ROOT/.system-sync/helper/current" ]] \
  || helper_error "helper/current 必须是普通文件"
if [[ -e "$DESTINATION" || -L "$DESTINATION" ]]; then
  [[ ! -L "$DESTINATION" && -f "$DESTINATION/system-sync-helper" && ! -L "$DESTINATION/system-sync-helper" && "$(helper_checksum "$DESTINATION/system-sync-helper")" == "$BINARY_CHECKSUM" ]] \
    || helper_error "已有安装与校验值不符，请先核对 $DESTINATION"
else
  mv -- "$STAGING" "$DESTINATION"
fi
POINTER="$(mktemp "$REPO_ROOT/.system-sync/helper/.current-XXXXXX")"
printf '%s %s\n' "$VERSION" "$BINARY_CHECKSUM" > "$POINTER"
mv -f -- "$POINTER" "$REPO_ROOT/.system-sync/helper/current"
printf '[+] 已安装 system-sync-helper %s（%s）；日常 task 不需要 Go 编译器。\n' "$VERSION" "$TARGET"
