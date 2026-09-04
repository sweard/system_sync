#!/usr/bin/env bash
# Claude Hook 的跨平台运行环境。兼容 macOS Homebrew、Linuxbrew 和系统工具路径。

claude_hook_prepend_path() {
  local directory="$1"
  [[ -d "$directory" ]] || return 0
  case ":${PATH:-}:" in
    *":$directory:"*) ;;
    *) PATH="$directory${PATH:+:$PATH}" ;;
  esac
}

for hook_bin in \
  /usr/local/bin \
  /usr/bin \
  /bin \
  /home/linuxbrew/.linuxbrew/bin \
  "$HOME/.linuxbrew/bin" \
  /opt/homebrew/bin
do
  claude_hook_prepend_path "$hook_bin"
done
unset hook_bin

export PATH
export LANG="${LANG:-C}"

claude_hook_sha1() {
  if command -v shasum >/dev/null 2>&1; then
    shasum
  elif command -v sha1sum >/dev/null 2>&1; then
    sha1sum
  else
    return 1
  fi
}
