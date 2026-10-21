#!/usr/bin/env python3
"""Python reference for local-history regression tests; daily tasks use the Go helper."""

import argparse
import json
import os
from pathlib import Path
import subprocess
import sys
import tomllib


def locations():
    home = Path.home()
    config = Path(os.environ.get("MISE_CONFIG_DIR", str(Path(os.environ.get("XDG_CONFIG_HOME", home / ".config")) / "mise")))
    state = Path(os.environ.get("MISE_STATE_DIR", str(Path(os.environ.get("XDG_STATE_HOME", home / ".local/state")) / "mise")))
    code = {"darwin": home / "Library/Application Support/Code/User/settings.json",
            "linux": home / ".config/Code/User/settings.json"}.get(sys.platform)
    if code is None:
        raise ValueError("仅支持 macOS / Linux")
    return config / "conf.d/system-sync-history.toml", state / "history-local", [home / ".codex/config.toml", code, home / ".zshrc.local"]


def main():
    parser = argparse.ArgumentParser(description="手动、本机保存配置历史；不含 secrets.env 或认证文件")
    parser.add_argument("action", choices=("init", "save"))
    parser.add_argument("-d", "--description", default="manual system-sync checkpoint")
    args = parser.parse_args()
    try:
        enrollment, history, candidates = locations()
        if enrollment.is_symlink() or history.is_symlink():
            raise ValueError("历史登记文件和历史目录不能是链接")
        if args.action == "init":
            entries = {str(p): {"mode": "track-local", "autosave": False}
                       for p in candidates if p.is_file()}
            if not entries:
                raise ValueError("没有可跟踪的目标，请先部署配置")
            if enrollment.exists():
                if tomllib.loads(enrollment.read_text()) != {"dotfiles": entries}:
                    raise ValueError("本机历史范围已有人工修改，请先核对登记文件")
            else:
                enrollment.parent.mkdir(parents=True, exist_ok=True)
                text = "# 本机登记，不部署、不上传；仅显式保存时建立检查点。\n[dotfiles]\n"
                text += "".join(f'{json.dumps(p, ensure_ascii=False)} = {{ mode = "track-local", autosave = false }}\n'
                                for p in entries)
                descriptor = os.open(enrollment, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
                with os.fdopen(descriptor, "w") as stream:
                    stream.write(text)
            enrollment.chmod(0o600)
            history.mkdir(parents=True, exist_ok=True, mode=0o700)
            history.chmod(0o700)
            print(f"[+] 已登记 {len(entries)} 个本机配置；自动捕获关闭。运行 mise run config-save 建立检查点。")
        else:
            if not enrollment.is_file():
                raise ValueError("请先运行 mise run config-history-init")
            data = tomllib.loads(enrollment.read_text())
            entries = data.get("dotfiles", {})
            if set(data) != {"dotfiles"} or not entries or any(
                Path(p) not in candidates or value != {"mode": "track-local", "autosave": False}
                for p, value in entries.items()
            ):
                raise ValueError("历史范围或本机隔离模式不符合约定，已停止")
            paths = [p for p in entries if Path(p).is_file()]
            if not paths:
                raise ValueError("已登记的目标均不存在，未建立检查点")
            result = subprocess.run(["mise", "dot", "save", "--description", args.description, "--", *paths],
                                    stdin=subprocess.DEVNULL, text=True, capture_output=True, timeout=60)
            if result.returncode:
                raise ValueError("mise 保存失败，请用 mise dot --local status 核对本机历史状态")
            print(f"[+] 已手动保存 {len(paths)} 个配置到本机历史。")
        return 0
    except (ValueError, OSError, subprocess.TimeoutExpired) as exc:
        print(f"[x] {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
