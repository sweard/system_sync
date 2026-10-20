# 同时供 mise（bash）和 zsh 启动使用，不调用外部命令。
# 只前置目录，保留原 PATH 后缀，让 mise 能在切换项目时恢复环境。
case "${OSTYPE:-}" in
  darwin*)
    _mise_sdk_default="$HOME/Library/Android/sdk"
    _mise_toolbox_bin="$HOME/Library/Application Support/JetBrains/Toolbox/scripts"
    ;;
  linux*)
    _mise_sdk_default="$HOME/Android/Sdk"
    _mise_toolbox_bin="$HOME/.local/share/JetBrains/Toolbox/scripts"
    ;;
  *)
    _mise_sdk_default=
    _mise_toolbox_bin=
    ;;
esac

if [ -z "${ANDROID_HOME:-}" ] && [ -d "$_mise_sdk_default" ]; then
  export ANDROID_HOME="$_mise_sdk_default"
fi

# mise 的自定义 PATH 优先于工具目录，只在这里前置 SDK / IDE 命令目录。
for _mise_bin in \
  "${ANDROID_HOME:+$ANDROID_HOME/build-tools}" \
  "${ANDROID_HOME:+$ANDROID_HOME/platform-tools}" \
  "${ANDROID_HOME:+$ANDROID_HOME/tools}" \
  "${ANDROID_HOME:+$ANDROID_HOME/emulator}" \
  "$_mise_toolbox_bin"
do
  if [ -d "$_mise_bin" ]; then
    PATH="$_mise_bin${PATH:+:$PATH}"
  fi
done
export PATH
unset _mise_bin _mise_sdk_default _mise_toolbox_bin

# zsh 在激活 mise 前调用，使当前项目的工具优先于用户目录中的旧命令。
# 此函数不在 mise 的 bash 子进程中调用；否则用户目录会反过来遮蔽工具版本。
_system_sync_user_path() {
  for _mise_user_bin in \
    "${CARGO_HOME:-$HOME/.cargo}/bin" \
    "$HOME/.gem/bin" \
    "$HOME/.git-ai/bin" \
    "$HOME/.local/bin" \
    "${MISE_DATA_DIR:-${XDG_DATA_HOME:-$HOME/.local/share}/mise}/shims"
  do
    if [ -d "$_mise_user_bin" ]; then
      PATH="$_mise_user_bin${PATH:+:$PATH}"
    fi
  done
  export PATH
  unset _mise_user_bin
}
