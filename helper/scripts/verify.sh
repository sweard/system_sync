#!/usr/bin/env bash
# Check bundled artifacts and build inputs without a compiler or network.
set -Eeuo pipefail
source "$(dirname -- "${BASH_SOURCE[0]}")/common.sh"

TARGETS=(darwin_arm64 darwin_amd64 linux_arm64 linux_amd64)
if [[ $# -eq 1 && "$1" == --current-platform ]]; then
  TARGETS=("$(helper_target)")
else
  [[ $# -eq 0 ]] || helper_error "用法：verify.sh [--current-platform]"
fi
VERSION="$(helper_version)"
ARTIFACT_DIR="$HELPER_DIR/prebuilt/$VERSION"
[[ -f "$ARTIFACT_DIR/SOURCE_SHA256SUMS" && -f "$ARTIFACT_DIR/SHA256SUMS" ]] \
  || helper_error "缺少随仓库分发的 helper 清单，请运行 mise run helper-bundle"
[[ "$(system_sync_step '核对 helper 源码与构建输入' helper_source_manifest)" == "$(cat "$ARTIFACT_DIR/SOURCE_SHA256SUMS")" ]] \
  || helper_error "Go 源码或构建输入已变化，请运行 mise run helper-bundle 并一同提交 helper/prebuilt/$VERSION"
VERIFIED=""
for TARGET in "${TARGETS[@]}"; do
  printf '[~] 校验 helper 归档：%s\n' "$TARGET" >&2
  ARCHIVE="system-sync-helper_${VERSION}_${TARGET}.tar.gz"
  EXPECTED="$(awk -v name="$ARCHIVE" '$2 == name { print $1 }' "$ARTIFACT_DIR/SHA256SUMS")"
  [[ -f "$ARTIFACT_DIR/$ARCHIVE" && "$EXPECTED" =~ ^[0-9a-f]{64}$ ]] \
    || helper_error "缺少 $ARCHIVE 或有效的 SHA256"
  [[ "$(helper_checksum "$ARTIFACT_DIR/$ARCHIVE")" == "$EXPECTED" ]] \
    || helper_error "$ARCHIVE 的 SHA256 校验失败"
  VERIFIED+="$EXPECTED  $ARCHIVE"$'\n'
done
if [[ ${#TARGETS[@]} -eq 4 ]]; then
  [[ "$(cat "$ARTIFACT_DIR/SHA256SUMS")"$'\n' == "$VERIFIED" ]] \
    || helper_error "SHA256SUMS 必须仅包含四个平台的归档校验值"
  printf '[+] 四个平台预编译包校验通过，源码与构建输入清单一致。\n'
else
  printf '[+] 当前平台 %s 的预编译包校验通过，源码与构建输入清单一致。\n' "${TARGETS[0]}"
fi
