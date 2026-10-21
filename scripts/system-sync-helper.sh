#!/usr/bin/env bash
set -Eeuo pipefail
source "$(dirname -- "${BASH_SOURCE[0]}")/helper-common.sh"

VERSION="$(helper_version)"
CURRENT="$HELPER_ROOT/.system-sync/helper/current"
[[ ! -L "$HELPER_ROOT/.system-sync" && ! -L "$HELPER_ROOT/.system-sync/helper" ]] \
  || helper_error "helper 的本机目录不能是符号链接"
if [[ ! -e "$CURRENT" && ! -L "$CURRENT" ]]; then
  # Keep command stdout (especially JSON/TSV) free of installation messages.
  "$HELPER_ROOT/scripts/helper-install.sh" >&2
fi
[[ -f "$CURRENT" && ! -L "$CURRENT" ]] || helper_error "helper/current 必须是普通文件"
IFS=' ' read -r INSTALLED_VERSION CHECKSUM EXTRA < "$CURRENT" || helper_error "Go helper 安装记录不完整，请运行 mise run helper-install"
[[ "$INSTALLED_VERSION" == "$VERSION" && "$CHECKSUM" =~ ^[0-9a-f]{64}$ && -z "${EXTRA:-}" ]] \
  || helper_error "Go helper 版本与仓库不符；请重新运行 helper-build 或 helper-install"
INSTALL_DIR="$HELPER_ROOT/.system-sync/helper/$VERSION/$CHECKSUM"
BINARY="$INSTALL_DIR/system-sync-helper"
[[ ! -L "$HELPER_ROOT/.system-sync/helper/$VERSION" && ! -L "$INSTALL_DIR" && -f "$BINARY" && ! -L "$BINARY" && -x "$BINARY" ]] \
  || helper_error "Go helper 缺失或安装目录异常；请重新安装"
[[ "$(helper_checksum "$BINARY")" == "$CHECKSUM" ]] || helper_error "Go helper 校验失败；未执行任务，请重新构建或安装"
INFO="$("$BINARY" --version)"
[[ "$INFO" == "system-sync-helper $VERSION protocol=1 "* ]] || helper_error "Go helper 协议或版本不匹配"
exec "$BINARY" --root "$HELPER_ROOT" "$@"
