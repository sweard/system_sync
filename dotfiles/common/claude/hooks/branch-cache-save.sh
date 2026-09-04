#!/usr/bin/env bash
# Stop hook：按「仓库身份(origin)+分支」生成当前会话上下文快照，每分支保留最近 4 份。
# stdin = hook 输入 JSON。永远以 0 退出，绝不阻塞。
CLAUDE_HOOK_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
. "$CLAUDE_HOOK_DIR/branch-cache-lib.sh" 2>/dev/null || exit 0
unset CLAUDE_HOOK_DIR

input=$(cat)
cwd=$(printf '%s' "$input" | jq -r '.cwd // empty' 2>/dev/null)
sid=$(printf '%s' "$input" | jq -r '.session_id // empty' 2>/dev/null)
tpath=$(printf '%s' "$input" | jq -r '.transcript_path // empty' 2>/dev/null)

[ -z "$cwd" ] && cwd="$PWD"
[ -z "$sid" ] && exit 0
if [ -z "$tpath" ] || [ ! -f "$tpath" ]; then exit 0; fi

bc_resolve "$cwd" || exit 0
mkdir -p "$BC_DIR" || exit 0

# 从 transcript 提取：标题 / 用户需求时间线 / 最近结论
title=$(jq -rc 'select(.type=="ai-title")|.aiTitle // empty' "$tpath" 2>/dev/null | tail -1)
[ -z "$title" ] && title="(未命名任务)"

prompts=$(jq -rc 'select(.type=="last-prompt")|(.lastPrompt // "")|gsub("[\n\r\t]";" ")|if length>200 then .[0:200]+"…" else . end' "$tpath" 2>/dev/null \
  | awk 'NF && $0!=prev{printf "%d. %s\n", ++n, $0; prev=$0}')

concl=$(jq -rc 'select(.type=="assistant")|.message.content[]?|select(.type=="text")|.text' "$tpath" 2>/dev/null | tail -1)
concl=$(printf '%s' "$concl" | jq -Rsr 'if length>600 then .[0:600]+"…" else . end' 2>/dev/null)

short_sid=${sid:0:8}
now=$(date +"%Y-%m-%d %H:%M")
{
  printf '### 任务：%s\n' "$title"
  printf -- '- 分支 %s · 会话 %s · 更新 %s\n\n' "$BC_BRANCH" "$short_sid" "$now"
  printf '**需求时间线：**\n'
  if [ -n "$prompts" ]; then printf '%s\n' "$prompts"; else printf '（无）\n'; fi
  printf '\n**最近结论摘要：**\n%s\n' "$concl"
} > "$BC_DIR/$sid.md" 2>/dev/null

# 仅保留最近 4 份（3 历史 + 当前）
ls -1t "$BC_DIR"/*.md 2>/dev/null | tail -n +5 | while IFS= read -r f; do rm -f "$f"; done

exit 0
