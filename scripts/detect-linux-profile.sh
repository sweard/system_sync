#!/usr/bin/env bash

set -Eeuo pipefail
IFS=$'\n\t'

OS_RELEASE_FILE="${1:-/etc/os-release}"

die() {
  printf '错误：%s\n' "$*" >&2
  exit 1
}

normalize_value() {
  local value="$1"
  if [[ "$value" == \"*\" && "${#value}" -ge 2 ]]; then
    value="${value:1:${#value}-2}"
  elif [[ "$value" == \'*\' && "${#value}" -ge 2 ]]; then
    value="${value:1:${#value}-2}"
  fi
  printf '%s\n' "$value" | tr '[:upper:]' '[:lower:]'
}

[[ -r "$OS_RELEASE_FILE" ]] || die "无法读取发行版信息：$OS_RELEASE_FILE"

DISTRO_ID=""
DISTRO_LIKE=""
while IFS='=' read -r key value; do
  case "$key" in
    ID)
      [[ -n "$DISTRO_ID" ]] || DISTRO_ID="$(normalize_value "$value")"
      ;;
    ID_LIKE)
      [[ -n "$DISTRO_LIKE" ]] || DISTRO_LIKE="$(normalize_value "$value")"
      ;;
  esac
done < "$OS_RELEASE_FILE"

[[ -n "$DISTRO_ID" ]] || die "$OS_RELEASE_FILE 缺少 ID"
[[ "$DISTRO_ID" =~ ^[a-z0-9._-]+$ ]] || die "无法识别的发行版 ID：$DISTRO_ID"
[[ -z "$DISTRO_LIKE" || "$DISTRO_LIKE" =~ ^[a-z0-9._[:space:]-]+$ ]] \
  || die "无法识别的发行版 ID_LIKE：$DISTRO_LIKE"

TOKENS=" $DISTRO_ID $DISTRO_LIKE "
case "$DISTRO_ID" in
  arch|manjaro|endeavouros|cachyos)
    printf 'arch\n'
    ;;
  debian|ubuntu|linuxmint|pop|kali|raspbian)
    printf 'debian\n'
    ;;
  fedora|rhel|centos|rocky|almalinux|ol)
    printf 'fedora\n'
    ;;
  opensuse*|sles|sled)
    printf 'opensuse\n'
    ;;
  *)
    if [[ "$TOKENS" == *" arch "* ]]; then
      printf 'arch\n'
    elif [[ "$TOKENS" == *" debian "* || "$TOKENS" == *" ubuntu "* ]]; then
      printf 'debian\n'
    elif [[ "$TOKENS" == *" fedora "* || "$TOKENS" == *" rhel "* ]]; then
      printf 'fedora\n'
    elif [[ "$TOKENS" == *" suse "* || "$TOKENS" == *" opensuse "* ]]; then
      printf 'opensuse\n'
    else
      die "尚未支持此 Linux 发行版：ID=${DISTRO_ID}，ID_LIKE=${DISTRO_LIKE:-（空）}"
    fi
    ;;
esac
