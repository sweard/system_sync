# Linux 交互 shell 差异。存在旧管理器时保持兼容；迁移到 mise 后可删除对应段落。
if [[ -r "$HOME/.nvm/nvm.sh" ]]; then
  export NVM_DIR="$HOME/.nvm"
  source "$NVM_DIR/nvm.sh"
fi

export PYENV_ROOT="$HOME/.pyenv"
[[ -d "$PYENV_ROOT/bin" ]] && path=("$PYENV_ROOT/bin" "${path[@]}")
if command -v pyenv >/dev/null 2>&1; then
  eval "$(pyenv init -)"
fi

typeset -U path PATH
export PATH
