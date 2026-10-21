#!/usr/bin/env python3
"""Python reference for extension regression tests; daily tasks use the Go helper."""

import argparse
import os
from pathlib import Path
import re
import signal
import shutil
import subprocess
import sys
import tomllib


ROOT = Path(__file__).resolve().parents[1]
IDENTIFIER = re.compile(r"[a-z0-9][a-z0-9-]*\.[a-z0-9][a-z0-9-]*\Z")
VERSION = re.compile(r"[0-9]+\.[0-9]+\.[0-9]+(?:-[a-zA-Z0-9.-]+)?\Z")


def declarations(path, platform):
    data = tomllib.loads(path.read_text())
    if set(data) - {"common", "macos", "linux"}:
        raise ValueError("扩展清单只支持 common、macos 和 linux 分组")
    wanted = {}
    # Validate inactive platform sections too, before invoking VS Code.
    for group, entries in data.items():
        if not isinstance(entries, dict):
            raise ValueError("扩展分组必须是表")
        for name, version in entries.items():
            if not IDENTIFIER.fullmatch(name) or not isinstance(version, str) or not VERSION.fullmatch(version):
                raise ValueError("扩展必须声明 publisher.name 和完整版本号")
            if group in ("common", platform):
                if name in wanted and wanted[name] != version:
                    raise ValueError("同一平台的扩展版本冲突")
                wanted[name] = version
    if not wanted:
        raise ValueError("当前平台的扩展清单为空，已停止")
    return dict(sorted(wanted.items()))


def invoke(command, *args, timeout=3600):
    # Language extensions can exceed 100 MB. Stop the CLI shell and its child
    # processes together if a download still exceeds the one-hour deadline.
    with subprocess.Popen([*command, *args], stdin=subprocess.DEVNULL, text=True,
                          stdout=subprocess.PIPE, stderr=subprocess.PIPE, start_new_session=True) as process:
        try:
            stdout, _ = process.communicate(timeout=timeout)
        except subprocess.TimeoutExpired:
            try:
                os.killpg(process.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
            process.communicate()
            raise ValueError("VS Code 命令超时，已停止本次 CLI 及其子进程；已安装项保留") from None
    if process.returncode:
        # Native output may contain machine configuration; do not echo it.
        raise ValueError(f"VS Code 命令失败（退出码 {process.returncode}），请检查 CLI、网络或扩展可用性")
    return stdout


def installed(command):
    versions = {}
    for line in invoke(command, "--list-extensions", "--show-versions").splitlines():
        name, separator, version = line.strip().rpartition("@")
        if not separator or not IDENTIFIER.fullmatch(name.lower()) or not VERSION.fullmatch(version):
            raise ValueError("VS Code 返回了无法识别的扩展列表，已停止")
        if name.lower() in versions:
            raise ValueError("VS Code 返回重复扩展，已停止")
        versions[name.lower()] = version
    return versions


def main():
    parser = argparse.ArgumentParser(description="核对或安装公共 VS Code 扩展，保留清单外扩展")
    parser.add_argument("action", choices=("status", "sync"))
    parser.add_argument("--dry-run", action="store_true")
    parser.add_argument("--manifest", type=Path, default=ROOT / "dotfiles/common/vscode/extensions.toml")
    parser.add_argument("--code", help="VS Code CLI 绝对路径；默认查找 code 或标准 macOS 应用")
    parser.add_argument("--user-data-dir", type=Path)
    parser.add_argument("--extensions-dir", type=Path)
    args = parser.parse_args()
    try:
        platform = {"darwin": "macos", "linux": "linux"}.get(sys.platform)
        if not platform:
            raise ValueError("仅支持 macOS / Linux")
        if bool(args.user_data_dir) != bool(args.extensions_dir):
            raise ValueError("隔离验收必须同时指定 --user-data-dir 和 --extensions-dir")
        wanted = declarations(args.manifest, platform)
        code = args.code or shutil.which("code")
        if not code and platform == "macos":
            code = "/Applications/Visual Studio Code.app/Contents/Resources/app/bin/code"
        if not code or not Path(code).is_file():
            raise ValueError("未找到 VS Code CLI，请安装 code 或用 --code 指定")
        command = [code]
        if args.user_data_dir:
            command += ["--user-data-dir", str(args.user_data_dir.resolve()),
                        "--extensions-dir", str(args.extensions_dir.resolve())]
        current = installed(command)
        pending = {name: version for name, version in wanted.items() if current.get(name) != version}
        for name, version in pending.items():
            print(f"[!] {name}: {current.get(name, '未安装')} -> {version}")
        extra = len(current.keys() - wanted.keys())
        if extra:
            print(f"[~] 保留 {extra} 个清单外扩展")
        if not pending:
            print(f"[+] {len(wanted)} 个公共扩展均符合清单，无需同步。")
        elif args.action == "sync" and not args.dry_run:
            for name, version in pending.items():
                invoke(command, "--install-extension", f"{name}@{version}", "--force")
            after = installed(command)
            if any(after.get(name) != version for name, version in wanted.items()):
                raise ValueError("安装后版本与清单不符，请重新核对；清单未修改")
            print(f"[+] 已同步 {len(pending)} 个公共扩展")
        return 0
    except (ValueError, OSError, subprocess.TimeoutExpired) as exc:
        print(f"[x] {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
