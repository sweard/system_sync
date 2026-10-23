"""Select platform-specific acceptance scenarios; plain unittest keeps all cases."""

import os
from pathlib import Path
import platform
import subprocess
import sys
import unittest


PROFILE_ENV = "SYSTEM_SYNC_TEST_PROFILE"
LINUX_PROFILES = ("arch", "debian", "fedora", "opensuse")
PROFILES = ("macos", *LINUX_PROFILES)
TARGETS = ("darwin_arm64", "darwin_amd64", "linux_arm64", "linux_amd64")


def current_profile():
    if sys.platform == "darwin":
        return "macos"
    if sys.platform != "linux":
        raise ValueError("检查仅支持 macOS / Linux")
    root = Path(__file__).resolve().parents[1]
    result = subprocess.run(["bash", str(root / "scripts/detect-linux-profile.sh")], cwd=root,
                            capture_output=True, text=True, timeout=15)
    if result.returncode or result.stdout.strip() not in LINUX_PROFILES:
        raise ValueError("无法识别当前 Linux profile；请运行 scripts/detect-linux-profile.sh 核对")
    return result.stdout.strip()


def current_target():
    system = {"darwin": "darwin", "linux": "linux"}.get(sys.platform)
    architecture = {"arm64": "arm64", "aarch64": "arm64", "x86_64": "amd64", "amd64": "amd64"}.get(platform.machine().lower())
    if not system or not architecture:
        raise ValueError("检查仅支持 macOS/Linux 的 ARM64/x86_64")
    return f"{system}_{architecture}"


def scope():
    value = os.environ.get(PROFILE_ENV, "all")
    if value not in ("all", *PROFILES):
        raise ValueError(f"未知测试范围：{value}")
    return value


def matches(names, selected=None):
    selected = scope() if selected is None else selected
    return selected == "all" or "common" in names or selected in names or (selected in LINUX_PROFILES and "linux" in names)


def for_platforms(*names):
    if not names or set(names) - {"all", "common", "linux", *PROFILES}:
        raise ValueError("测试平台标记必须是 common、all、macos、linux 或已支持的 Linux profile")

    def decorate(value):
        value.check_platforms = frozenset(names)
        return value
    return decorate


def serial(value):
    value.check_serial = True
    return value


def is_serial(test):
    method = getattr(test, test._testMethodName)
    return getattr(method, "check_serial", getattr(type(test), "check_serial", False))


def applies(test, selected):
    method = getattr(test, test._testMethodName)
    names = getattr(method, "check_platforms", getattr(type(test), "check_platforms", None))
    return names is None or matches(names, selected)


def selected_platforms(values, key=lambda value: value):
    selected = tuple(value for value in values if matches((key(value),)))
    if not selected:
        raise unittest.SkipTest("该平台场景保留在 --all-platforms 验收中")
    return selected


def selected_targets():
    return TARGETS if scope() == "all" else (current_target(),)
