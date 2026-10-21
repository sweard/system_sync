#!/usr/bin/env python3
"""Compatibility runtime for remaining Python writers; lock format matches Go."""

import argparse
from contextlib import contextmanager
from datetime import datetime, timezone
import fcntl
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile


MIN_MISE_VERSION = (2026, 10, 11)


class RuntimeFailure(RuntimeError):
    pass


def preflight(root):
    if sys.version_info < (3, 11):
        raise RuntimeFailure("需要 Python 3.11+，请先准备符合要求的 python3")
    if not shutil.which("mise"):
        raise RuntimeFailure("找不到 mise；需要 mise >= 2026.10.11")
    result = subprocess.run(["mise", "--version"], text=True, capture_output=True)
    match = re.match(r"(?:mise\s+)?(\d+)\.(\d+)\.(\d+)\b", result.stdout)
    if result.returncode or not match or tuple(map(int, match.groups())) < MIN_MISE_VERSION:
        raise RuntimeFailure("需要 mise >= 2026.10.11；请先升级 mise")
    system = subprocess.check_output(["uname", "-s"], text=True).strip()
    if system == "Darwin":
        profile, commands = "macos", ["brew"]
    elif system == "Linux":
        profile = subprocess.check_output(
            ["bash", str(root / "scripts/detect-linux-profile.sh")], text=True
        ).strip()
        commands = {
            "arch": ["pacman"],
            "debian": ["apt-get", "apt-mark", "dpkg-query"],
            "fedora": ["dnf", "rpm"],
            "opensuse": ["zypper", "rpm"],
        }.get(profile)
        if commands is None:
            raise RuntimeFailure(f"未知的 Linux profile：{profile}")
    else:
        raise RuntimeFailure(f"不支持的平台：{system}")
    missing = [name for name in ["bash", "comm", *commands] if not shutil.which(name)]
    if missing:
        raise RuntimeFailure("缺少前置命令：" + ", ".join(missing))
    return profile


def inherited_lock(root):
    """Only reuse an inherited descriptor for this repository's actual lock inode."""
    try:
        descriptor = int(os.environ.get("SYSTEM_SYNC_LOCK_FD", ""))
        if descriptor < 3:
            return None
        actual = os.fstat(descriptor)
        expected = (root / ".system-sync/operation.lock").stat()
        if (actual.st_dev, actual.st_ino) != (expected.st_dev, expected.st_ino):
            return None
        fcntl.flock(descriptor, fcntl.LOCK_EX | fcntl.LOCK_NB)
        return descriptor
    except (ValueError, OSError):
        return None


@contextmanager
def operation_lock(root):
    inherited = inherited_lock(root)
    if inherited is not None:
        yield inherited
        return
    state = root / ".system-sync"
    if state.is_symlink():
        raise RuntimeFailure(".system-sync 不能是符号链接")
    state.mkdir(exist_ok=True)
    lock_file = state / "operation.lock"
    if lock_file.is_symlink():
        raise RuntimeFailure("operation.lock 不能是符号链接")
    with lock_file.open("a") as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError as error:
            raise RuntimeFailure("另一条包操作正在运行（package-install/package-uninstall/system-sync/upgrade），请稍后重试") from error
        yield lock.fileno()


def journal(directory, phase, **fields):
    target = directory / "transaction.json"
    content = json.loads(target.read_text()) if target.exists() else {}
    if content.get("phase") and content["phase"] != phase:
        content["last_phase"] = content["phase"]
    content.update(phase=phase, updated_utc=datetime.now(timezone.utc).isoformat(), **fields)
    descriptor, temporary = tempfile.mkstemp(prefix=".journal-", dir=directory)
    try:
        with os.fdopen(descriptor, "w", encoding="utf-8") as output:
            json.dump(content, output, ensure_ascii=False, indent=2)
            output.write("\n")
            output.flush()
            os.fsync(output.fileno())
        os.replace(temporary, target)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


def run_guarded(root, command):
    if not sys.stdin.isatty():
        raise RuntimeFailure("修改系统包必须在交互式终端运行；尚未执行安装、卸载或升级")
    profile = preflight(root)
    with operation_lock(root) as descriptor:
        history = root / ".system-sync/history"
        history.mkdir(parents=True, exist_ok=True)
        prefix = datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%SZ-") + profile + "-"
        directory = Path(tempfile.mkdtemp(prefix=prefix, dir=history))
        package_dir = root / "packages" / ("macos" if profile == "macos" else f"linux/{profile}")
        journal(directory, "preflight", command=command, profile=profile)
        print(f"==> 全程审计：{directory}", file=sys.stderr, flush=True)
        environment = dict(os.environ, SYSTEM_SYNC_LOCK_FD=str(descriptor), SYSTEM_SYNC_RUN_LOG_DIR=str(directory))
        try:
            for source in package_dir.iterdir():
                if source.is_file():
                    shutil.copy2(source, directory / (source.name + ".before"))
            code = subprocess.call(command, env=environment, pass_fds=(descriptor,))
        except KeyboardInterrupt:
            journal(directory, "interrupted", exit_code=130)
            return 130
        except OSError as error:
            journal(directory, "failed", detail=str(error))
            raise
        if code == 0 and json.loads((directory / "transaction.json").read_text())["phase"] != "complete":
            print("错误：平台脚本未记录事务完成，按失败处理；请检查审计记录", file=sys.stderr)
            code = 1
        phase = "complete" if code == 0 else ("cancelled" if code == 2 else "failed")
        journal(directory, phase, exit_code=code)
        return code if code >= 0 else 128 - code


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, default=Path(__file__).resolve().parents[1])
    action = parser.add_mutually_exclusive_group()
    action.add_argument("--check", action="store_true")
    action.add_argument("--inherited-lock", action="store_true")
    action.add_argument("--phase")
    parser.add_argument("command", nargs=argparse.REMAINDER)
    args = parser.parse_args()
    root = args.root.resolve()
    if args.inherited_lock:
        return 0 if inherited_lock(root) is not None else 1
    if args.phase:
        if inherited_lock(root) is None:
            raise RuntimeFailure("阶段更新要求持有本仓库操作锁")
        directory = Path(os.environ["SYSTEM_SYNC_RUN_LOG_DIR"])
        journal(directory, args.phase)
        return 0
    if args.check:
        print(f"前置检查通过：Python {sys.version.split()[0]}，mise >= 2026.10.11，profile={preflight(root)}")
        return 0
    command = args.command[1:] if args.command[:1] == ["--"] else args.command
    if not command:
        parser.error("缺少受保护的命令")
    return run_guarded(root, command)


if __name__ == "__main__":
    try:
        sys.exit(main())
    except (RuntimeFailure, OSError, subprocess.CalledProcessError) as error:
        print(f"错误：{error}", file=sys.stderr)
        sys.exit(1)
