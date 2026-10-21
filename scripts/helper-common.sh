# Shared by explicit Go builds, offline installation and the checked binary launcher.
HELPER_ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"

helper_error() {
  printf '错误：%s\n' "$*" >&2
  exit 1
}

helper_version() {
  local version
  version="$(cat "$HELPER_ROOT/helper-version.txt")"
  [[ "$version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || helper_error "helper-version.txt 格式不正确"
  printf '%s\n' "$version"
}

helper_checksum() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum -- "$1" | awk '{ print $1 }'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 -- "$1" | awk '{ print $1 }'
  else
    helper_error "需要 sha256sum 或 shasum 校验二进制"
  fi
}

helper_target() {
  local system architecture
  case "$(uname -s)" in
    Darwin) system=darwin ;;
    Linux) system=linux ;;
    *) helper_error "预编译工具仅支持 macOS / Linux" ;;
  esac
  case "$(uname -m)" in
    arm64|aarch64) architecture=arm64 ;;
    x86_64|amd64) architecture=amd64 ;;
    *) helper_error "预编译工具仅支持 ARM64 / x86_64" ;;
  esac
  printf '%s_%s\n' "$system" "$architecture"
}

helper_state() {
  [[ ! -L "$HELPER_ROOT/.system-sync" && ! -L "$HELPER_ROOT/.system-sync/helper" ]] \
    || helper_error "helper 的本机目录不能是符号链接"
  mkdir -p -- "$HELPER_ROOT/.system-sync/helper"
}

# Record public build inputs, never configuration, local state or test fixtures.
helper_source_manifest() {
  (
    cd -- "$HELPER_ROOT"
    {
      printf '%s\n' go.mod helper-version.txt licenses/Go-LICENSE.txt scripts/helper-build.sh scripts/helper-common.sh
      [[ ! -f go.sum ]] || printf '%s\n' go.sum
      find cmd internal -type f -name '*.go' ! -name '*_test.go'
      find vendor -type f
    } | LC_ALL=C sort | while IFS= read -r file; do
      printf '%s  %s\n' "$(helper_checksum "$file")" "$file"
    done
  )
}
