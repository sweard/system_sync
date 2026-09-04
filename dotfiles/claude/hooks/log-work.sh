#!/usr/bin/env bash
# 记录每次用户提交给 Claude 的需求，用于后期统计 / 总结由 AI 完成的工作。
# 由 UserPromptSubmit hook 调用，stdin 为 hook 输入的 JSON。
# 永远以 0 退出，绝不阻塞用户提交。
export PATH="/usr/bin:/bin:/opt/homebrew/bin:/usr/local/bin:$PATH"
export LANG="${LANG:-en_US.UTF-8}"

LOG="$HOME/.claude/work-log.md"

input=$(cat)
prompt=$(printf '%s' "$input" | jq -r '.prompt // empty' 2>/dev/null)
cwd=$(printf '%s' "$input" | jq -r '.cwd // empty' 2>/dev/null)

# 没有有效的 prompt 内容则不记录
[ -z "$prompt" ] && exit 0

project=$(basename "${cwd:-unknown}")
day=$(date +%Y-%m-%d)
clock=$(date +%H:%M)
header="## $day — $project"

# 单行化：换行/制表符/回车转空格，压缩连续空格，过长则截断
oneline=$(printf '%s' "$prompt" | tr '\n\t\r' '   ' | tr -s ' ')
[ ${#oneline} -gt 500 ] && oneline="${oneline:0:500}…"

# 若日志中最后一个分段标题与当前不同（换了一天或换了项目），就新起一段
last_header=$(grep '^## ' "$LOG" 2>/dev/null | tail -1)
[ "$last_header" != "$header" ] && printf '\n%s\n' "$header" >> "$LOG"

printf -- '- %s [需求] %s\n' "$clock" "$oneline" >> "$LOG"

exit 0
