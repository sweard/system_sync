#!/usr/bin/env python3
"""从 zypper --xmlout 的查询或 dry-run 中安全提取包名。"""

from __future__ import annotations

import argparse
import re
import sys
import xml.etree.ElementTree as ET
from pathlib import Path
from typing import Iterable


PACKAGE_RE = re.compile(r"^[A-Za-z0-9][A-Za-z0-9@._+:\-]*$")


class ZypperXmlError(ValueError):
    pass


def local_name(tag: str) -> str:
    return tag.rsplit("}", 1)[-1]


def package_name(element: ET.Element) -> str:
    name = element.attrib.get("name", "")
    if not name:
        for child in element:
            if local_name(child.tag) == "name" and child.text:
                name = child.text.strip()
                break
    if not name or not PACKAGE_RE.fullmatch(name):
        raise ZypperXmlError(f"solvable 包名缺失或包含不安全字符：{name!r}")
    return name


def solvable_kind(element: ET.Element) -> str:
    kind = element.attrib.get("kind", "")
    if not kind:
        for child in element:
            if local_name(child.tag) == "kind" and child.text:
                kind = child.text.strip()
                break
    return kind or "package"


def query_solvables(root: ET.Element) -> Iterable[str]:
    for element in root.iter():
        if local_name(element.tag) != "solvable":
            continue
        if solvable_kind(element) != "package":
            continue
        yield package_name(element)


def transaction_removals(root: ET.Element) -> Iterable[str]:
    for group in root.iter():
        if local_name(group.tag) != "to-remove":
            continue
        for element in group.iter():
            if local_name(element.tag) != "solvable":
                continue
            kind = solvable_kind(element)
            if kind != "package":
                raise ZypperXmlError(
                    f"移除事务包含非 package 对象：{kind}:{package_name(element)}"
                )
            yield package_name(element)


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("mode", choices=("solvables", "to-remove"))
    parser.add_argument("xml_file", help="zypper XML 文件，或 - 表示标准输入")
    args = parser.parse_args()

    try:
        if args.xml_file == "-":
            root = ET.parse(sys.stdin).getroot()
        else:
            root = ET.parse(Path(args.xml_file)).getroot()
        if local_name(root.tag) != "stream":
            raise ZypperXmlError(f"无法识别的 zypper XML 根节点：{root.tag}")
        names = (
            query_solvables(root)
            if args.mode == "solvables"
            else transaction_removals(root)
        )
        for name in sorted(set(names)):
            print(name)
    except (OSError, ET.ParseError, ZypperXmlError) as exc:
        print(f"错误：无法安全解析 zypper XML：{exc}", file=sys.stderr)
        return 2
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
