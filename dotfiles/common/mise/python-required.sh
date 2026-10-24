#!/bin/sh
# Used by UV_PYTHON only when mise's selected Python is not installed. Keep
# bootstrap tasks usable while refusing an unrelated Python from the system.
printf '%s\n' '当前 mise 选择的 Python 尚未安装；请先运行 mise install python，再使用 uv。' >&2
exit 1
