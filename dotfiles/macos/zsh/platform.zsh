# macOS 交互 shell 差异。迁移对应工具到 mise 后，应删除旧管理器初始化。
if command -v brew >/dev/null 2>&1; then
  nvm_prefix="$(brew --prefix nvm 2>/dev/null)"
  if [[ -n "$nvm_prefix" && -r "$nvm_prefix/nvm.sh" ]]; then
    export NVM_DIR="$HOME/.nvm"
    source "$nvm_prefix/nvm.sh"
  fi
  unset nvm_prefix

  ruby_prefix="$(brew --prefix ruby 2>/dev/null)"
  [[ -n "$ruby_prefix" && -d "$ruby_prefix/bin" ]] && path=("$ruby_prefix/bin" "${path[@]}")
  unset ruby_prefix
fi

export PYENV_ROOT="$HOME/.pyenv"
[[ -d "$PYENV_ROOT/bin" ]] && path=("$PYENV_ROOT/bin" "${path[@]}")
if command -v pyenv >/dev/null 2>&1; then
  eval "$(pyenv init -)"
fi

export LANG="zh"
export LC_ALL="en_US.UTF-8"

typeset -U path PATH
export PATH
