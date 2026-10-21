#!/usr/bin/env bash
# Check bundled artifacts and build inputs without a compiler or network.
set -Eeuo pipefail
source "$(dirname -- "${BASH_SOURCE[0]}")/helper-common.sh"

[[ $# -eq 0 ]] || helper_error "用法：helper-verify.sh"
VERSION="$(helper_version)"
ARTIFACT_DIR="$HELPER_ROOT/prebuilt/$VERSION"
[[ -f "$ARTIFACT_DIR/SOURCE_SHA256SUMS" && -f "$ARTIFACT_DIR/SHA256SUMS" ]] \
  || helper_error "缺少随仓库分发的 helper 清单，请运行 mise run helper-bundle"
[[ "$(helper_source_manifest)" == "$(cat "$ARTIFACT_DIR/SOURCE_SHA256SUMS")" ]] \
  || helper_error "Go 源码或构建输入已变化，请运行 mise run helper-bundle 并一同提交 prebuilt/$VERSION"
VERIFIED=""
for TARGET in darwin_arm64 darwin_amd64 linux_arm64 linux_amd64; do
  ARCHIVE="system-sync-helper_${VERSION}_${TARGET}.tar.gz"
  EXPECTED="$(awk -v name="$ARCHIVE" '$2 == name { print $1 }' "$ARTIFACT_DIR/SHA256SUMS")"
  [[ -f "$ARTIFACT_DIR/$ARCHIVE" && "$EXPECTED" =~ ^[0-9a-f]{64}$ ]] \
    || helper_error "缺少 $ARCHIVE 或有效的 SHA256"
  [[ "$(helper_checksum "$ARTIFACT_DIR/$ARCHIVE")" == "$EXPECTED" ]] \
    || helper_error "$ARCHIVE 的 SHA256 校验失败"
  VERIFIED+="$EXPECTED  $ARCHIVE"$'\n'
done
[[ "$(cat "$ARTIFACT_DIR/SHA256SUMS")"$'\n' == "$VERIFIED" ]] \
  || helper_error "SHA256SUMS 必须仅包含四个平台的归档校验值"
printf '[+] 四个平台预编译包校验通过，源码与构建输入清单一致。\n'
