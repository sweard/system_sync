#!/usr/bin/env bash
# 公共库：解析「仓库身份(origin)+分支」缓存目录，并提供上下文注入。
# 供 branch-cache-save.sh / branch-cache-load.sh / branch-context-watch.sh source。
CLAUDE_HOOK_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
. "$CLAUDE_HOOK_DIR/hook-runtime.sh" 2>/dev/null || {
  return 1 2>/dev/null || exit 1
}
unset CLAUDE_HOOK_DIR
#
# bc_resolve <cwd>：成功 return 0 并设置：
#   BC_BRANCH 当前分支名
#   BC_KEY    "<仓库名>-<origin短hash>__<分支(已sanitize)>"
#   BC_DIR    缓存目录绝对路径
# 非 git / detached HEAD / 解析失败 → return 1。
#
# 仓库身份用 origin url 而非物理路径：同一仓库的多个工作副本（如 rn1/android 与
# rn2/android 同 origin）共享同一身份，再按分支区分，符合「同仓库按分支缓存」。
bc_resolve() {
  local cwd="$1" top branch remote norm name h safe
  [ -z "$cwd" ] && cwd="$PWD"
  top=$(git -C "$cwd" rev-parse --show-toplevel 2>/dev/null) || return 1
  [ -z "$top" ] && return 1
  branch=$(git -C "$cwd" rev-parse --abbrev-ref HEAD 2>/dev/null)
  { [ -z "$branch" ] || [ "$branch" = "HEAD" ]; } && return 1
  remote=$(git -C "$cwd" remote get-url origin 2>/dev/null)
  if [ -n "$remote" ]; then
    norm=$(printf '%s' "$remote" | sed -E 's#\.git$##')
    name=$(basename "$norm")
    h=$(printf '%s' "$norm" | claude_hook_sha1 2>/dev/null | cut -c1-8)
  else
    name=$(basename "$top")
    h=$(printf '%s' "$top" | claude_hook_sha1 2>/dev/null | cut -c1-8)
  fi
  [ -n "$h" ] || return 1
  safe=$(printf '%s' "$branch" | sed 's#[^A-Za-z0-9._-]#_#g')
  BC_BRANCH="$branch"
  BC_KEY="${name}-${h}__${safe}"
  BC_DIR="$HOME/.claude/branch-cache/${BC_KEY}"
  return 0
}

# bc_emit_context <session_id> <hookEventName> <prefix>
# 注入 BC_DIR 下最近 3 份快照（排除当前会话自身），每份截断 1500 字符。
# 通过 stdout 输出 additionalContext JSON。无可注入内容 → return 1。
bc_emit_context() {
  local sid="$1" ev="$2" prefix="$3" files body chunk f ctx
  [ -d "$BC_DIR" ] || return 1
  files=$(ls -1t "$BC_DIR"/*.md 2>/dev/null | grep -v "/${sid}\.md$" | head -3)
  [ -z "$files" ] && return 1
  body=""
  while IFS= read -r f; do
    [ -f "$f" ] || continue
    chunk=$(jq -Rsr 'if length>1500 then .[0:1500]+"…（已截断）" else . end' "$f" 2>/dev/null)
    body="${body}${chunk}

---
"
  done <<< "$files"
  [ -z "$body" ] && return 1
  ctx="${prefix}

${body}"
  jq -n --arg c "$ctx" --arg e "$ev" '{hookSpecificOutput:{hookEventName:$e,additionalContext:$c}}'
  return 0
}
