#!/usr/bin/env bash
# UserPromptSubmit hook：检测会话内 git 分支切换（同一仓库 checkout 切分支），
# 分支变化时自动注入新分支的上下文缓存。未变化则静默跳过。
# stdin = hook 输入 JSON。永远以 0 退出。
export PATH="/usr/bin:/bin:/opt/homebrew/bin:/usr/local/bin:$PATH"
export LANG="${LANG:-en_US.UTF-8}"
. "$HOME/.claude/hooks/branch-cache-lib.sh" 2>/dev/null || exit 0

input=$(cat)
cwd=$(printf '%s' "$input" | jq -r '.cwd // empty' 2>/dev/null)
sid=$(printf '%s' "$input" | jq -r '.session_id // empty' 2>/dev/null)

[ -z "$cwd" ] && cwd="$PWD"
[ -z "$sid" ] && exit 0
bc_resolve "$cwd" || exit 0

sdir="$HOME/.claude/branch-cache/.state"
sfile="$sdir/$sid"
prev=$(cat "$sfile" 2>/dev/null)

# 分支(及仓库)未变 → 跳过（SessionStart 已处理启动分支）
[ "$prev" = "$BC_KEY" ] && exit 0

mkdir -p "$sdir" 2>/dev/null
printf '%s' "$BC_KEY" > "$sfile" 2>/dev/null

# 分支已切换（或本会话首次未经 SessionStart）→ 注入新分支缓存
bc_emit_context "$sid" "UserPromptSubmit" "检测到当前 git 分支为 \`$BC_BRANCH\`，自动加载该分支最近的工作任务上下文缓存（历史快照，可能不是最新，以实际代码为准）："
exit 0
