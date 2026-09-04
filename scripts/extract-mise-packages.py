#!/usr/bin/env python3
"""从 `mise bootstrap packages status --json` 提取 pacman/AUR 声明。

当前官方结构是：
  {"pacman": {"available": true, "packages": [{"package": "base", ...}]}}

兼容逻辑只接受少量、可明确验证的旧/替代形状。无法确认结构时失败关闭，
绝不返回一个可能错误的空列表给删除脚本。
"""

from __future__ import annotations

import argparse
import json
import re
import sys
from pathlib import Path
from typing import Any, Iterable, List, Tuple


SUPPORTED_MANAGERS = ("pacman", "aur")
PACKAGE_RE = re.compile(r"^[A-Za-z0-9][A-Za-z0-9@._+\-]*$")


class StatusFormatError(ValueError):
    pass


def _package_from_item(manager: str, item: Any) -> Tuple[str, str, str]:
    if isinstance(item, str):
        name = item
        state = "unknown"
    elif isinstance(item, dict):
        name = item.get("package", item.get("name"))
        state = item.get("state", "unknown")
    else:
        raise StatusFormatError(
            f"{manager!r} 的 packages 项必须是对象或字符串，实际为 {type(item).__name__}"
        )

    if not isinstance(name, str) or not name:
        raise StatusFormatError(f"{manager!r} 的 package/name 字段缺失或不是字符串")
    if not PACKAGE_RE.fullmatch(name):
        raise StatusFormatError(f"包名包含不受支持的字符：{manager}:{name!r}")
    if not isinstance(state, str):
        state = "unknown"
    return manager, name, state


def _parse_manager_section(manager: str, section: Any) -> Iterable[Tuple[str, str, str]]:
    # 当前 mise JSON：manager -> {available, reason?, packages?}
    if isinstance(section, dict):
        if section.get("available") is False:
            reason = section.get("reason", "原因未知")
            raise StatusFormatError(
                f"mise 报告 {manager} manager 不可用：{reason}。为避免空列表误删，已停止。"
            )
        if "packages" not in section:
            raise StatusFormatError(
                f"mise 的 {manager!r} 区段没有 packages 字段；JSON 格式可能已变化"
            )
        packages = section["packages"]
    # 兼容：manager -> [{package/name, state?}, ...]
    elif isinstance(section, list):
        packages = section
    else:
        raise StatusFormatError(
            f"mise 的 {manager!r} 区段类型无法识别：{type(section).__name__}"
        )

    if not isinstance(packages, list):
        raise StatusFormatError(f"mise 的 {manager!r}.packages 不是数组")
    for item in packages:
        yield _package_from_item(manager, item)


def parse_status(data: Any) -> List[Tuple[str, str, str]]:
    entries: List[Tuple[str, str, str]] = []

    if isinstance(data, dict) and any(manager in data for manager in SUPPORTED_MANAGERS):
        for manager in SUPPORTED_MANAGERS:
            if manager in data:
                entries.extend(_parse_manager_section(manager, data[manager]))
    else:
        # 兼容：[{"manager": "pacman", "package": "base", ...}, ...]
        flat = data.get("packages") if isinstance(data, dict) else data
        if not isinstance(flat, list):
            raise StatusFormatError("无法识别 mise packages status JSON 的顶层结构")
        for item in flat:
            if not isinstance(item, dict):
                raise StatusFormatError("扁平 packages 项必须是对象")
            manager = item.get("manager")
            if manager in SUPPORTED_MANAGERS:
                entries.append(_package_from_item(manager, item))

    if not entries:
        raise StatusFormatError(
            "没有解析到任何 pacman/AUR 声明；拒绝把空结果用于系统包收敛"
        )

    # 去重并保持确定性；同名包跨 manager 仍分别保留，shell 侧再取名称并集。
    return sorted(set(entries), key=lambda row: (row[0], row[1], row[2]))


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("json_file", type=Path, help="mise status JSON 文件")
    args = parser.parse_args()

    try:
        with args.json_file.open("r", encoding="utf-8") as handle:
            data = json.load(handle)
        entries = parse_status(data)
    except (OSError, json.JSONDecodeError, StatusFormatError) as exc:
        print(f"错误：无法安全解析 mise 包状态：{exc}", file=sys.stderr)
        return 2

    for manager, package, state in entries:
        print(f"{manager}\t{package}\t{state}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
