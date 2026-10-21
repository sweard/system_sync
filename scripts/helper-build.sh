#!/usr/bin/env bash
# The only compilation entry. --bundle refreshes Git-managed offline bundles.
set -Eeuo pipefail
source "$(dirname -- "${BASH_SOURCE[0]}")/helper-common.sh"

MODE="${1:---native}"
case "$MODE" in
  --native|--dist|--bundle) [[ $# -le 1 ]] || helper_error "用法：helper-build.sh [--native|--dist|--bundle]" ;;
  -h|--help) printf '用法：helper-build.sh [--native|--dist|--bundle]\n需要 Go 1.27.3；源码保留在 cmd/ 和 internal/。\n'; exit 0 ;;
  *) helper_error "用法：helper-build.sh [--native|--dist|--bundle]" ;;
esac
command -v go >/dev/null 2>&1 || helper_error "构建需要 Go 1.27.3；可通过 mise exec go@1.27.3 -- mise run helper-build 显式准备"
export GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off GOTELEMETRY=off
export GOENV=off GOWORK=off GOFLAGS="" GOEXPERIMENT="" GOAMD64=v1 GOARM64=v8.0
export GOCACHE="$HELPER_ROOT/.system-sync/go/build" GOMODCACHE="$HELPER_ROOT/.system-sync/go/mod"
[[ "$(go env GOVERSION)" == go1.27.3 ]] || helper_error "固定构建工具链为 Go 1.27.3，请用 mise exec go@1.27.3 -- mise run helper-build"
VERSION="$(helper_version)"
REVISION="$(git -C "$HELPER_ROOT" rev-parse --short=12 HEAD 2>/dev/null || printf unknown)"
if ! git -C "$HELPER_ROOT" diff --quiet -- .; then REVISION="$REVISION-dirty"; fi
helper_state
STAGING="$(mktemp -d "$HELPER_ROOT/.system-sync/helper/.build-XXXXXX")"
trap 'rm -rf -- "$STAGING"' EXIT
cd -- "$HELPER_ROOT"
if [[ "$MODE" == --dist || "$MODE" == --bundle ]]; then
  TARGETS=(darwin_arm64 darwin_amd64 linux_arm64 linux_amd64)
else
  TARGETS=("$(go env GOHOSTOS)_$(go env GOHOSTARCH)")
fi
: > "$STAGING/SHA256SUMS"
for TARGET in "${TARGETS[@]}"; do
  mkdir -p -- "$STAGING/$TARGET"
  CGO_ENABLED=0 GOOS="${TARGET%_*}" GOARCH="${TARGET#*_}" \
    go build -mod=vendor -buildvcs=false -trimpath \
    -ldflags "-s -w -X main.version=$VERSION -X main.revision=$REVISION" \
    -o "$STAGING/$TARGET/system-sync-helper" ./cmd/system-sync-helper
  printf '%s\n' "$VERSION" > "$STAGING/$TARGET/VERSION"
  printf '%s\n' "$REVISION" > "$STAGING/$TARGET/REVISION"
  cp -- "$HELPER_ROOT/licenses/Go-LICENSE.txt" "$STAGING/$TARGET/LICENSE-Go"
  cp -- "$HELPER_ROOT/vendor/github.com/pelletier/go-toml/v2/LICENSE" "$STAGING/$TARGET/LICENSE-go-toml"
  printf '%s  system-sync-helper\n' "$(helper_checksum "$STAGING/$TARGET/system-sync-helper")" > "$STAGING/$TARGET/SHA256SUMS"
  ARCHIVE="system-sync-helper_${VERSION}_${TARGET}.tar.gz"
  COPYFILE_DISABLE=1 tar -czf "$STAGING/$ARCHIVE" -C "$STAGING/$TARGET" system-sync-helper VERSION REVISION SHA256SUMS LICENSE-Go LICENSE-go-toml
  printf '%s  %s\n' "$(helper_checksum "$STAGING/$ARCHIVE")" "$ARCHIVE" >> "$STAGING/SHA256SUMS"
done
if [[ "$MODE" == --dist || "$MODE" == --bundle ]]; then
  helper_source_manifest > "$STAGING/SOURCE_SHA256SUMS"
  if [[ "$MODE" == --bundle ]]; then
    OUTPUT="$HELPER_ROOT/prebuilt/$VERSION"
  else
    OUTPUT="$HELPER_ROOT/.system-sync/dist/$VERSION"
  fi
  mkdir -p -- "$OUTPUT"
  for TARGET in "${TARGETS[@]}"; do
    ARCHIVE="system-sync-helper_${VERSION}_${TARGET}.tar.gz"
    cp -- "$STAGING/$ARCHIVE" "$OUTPUT/$ARCHIVE"
  done
  cp -- "$STAGING/SHA256SUMS" "$OUTPUT/SHA256SUMS"
  cp -- "$STAGING/SOURCE_SHA256SUMS" "$OUTPUT/SOURCE_SHA256SUMS"
  printf '[+] 四个平台的归档与 SHA256SUMS 已生成：%s\n' "$OUTPUT"
else
  "$HELPER_ROOT/scripts/helper-install.sh" "$STAGING"
fi
