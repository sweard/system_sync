#!/usr/bin/env bash
# SessionStart hook：按「仓库身份(origin)+分支」注入最近 3 份快照；
# 并记录本会话当前分支，供 UserPromptSubmit 检测会话内分支切换。
export PATH="/usr/bin:/bin:/opt/homebrew/bin:/usr/local/bin:$PATH"
export LANG="${LANG:-en_US.UTF-8}"
. "$HOME/.claude/hooks/branch-cache-lib.sh" 2>/dev/null || exit 0

input=$(cat)
cwd=$(printf '%s' "$input" | jq -r '.cwd // empty' 2>/dev/null)
src=$(printf '%s' "$input" | jq -r '.source // empty' 2>/dev/null)
sid=$(printf '%s' "$input" | jq -r '.session_id // empty' 2>/dev/null)

[ -z "$cwd" ] && cwd="$PWD"
# resume 已恢复完整会话，无需再注入
[ "$src" = "resume" ] && exit 0

bc_resolve "$cwd" || exit 0

# 记录本会话「已处理分支」，供 watch hook 比对切换
sdir="$HOME/.claude/branch-cache/.state"
mkdir -p "$sdir" 2>/dev/null
printf '%s' "$BC_KEY" > "$sdir/$sid" 2>/dev/null

bc_emit_context "$sid" "SessionStart" "以下是当前分支 \`$BC_BRANCH\` 最近的工作任务上下文缓存（由 hook 自动加载，供延续工作参考；内容为历史快照，可能不是最新，以实际代码为准）："
exit 0
