#!/usr/bin/env python3
"""Keep deployed configuration in place without depending on this repository."""

from dataclasses import dataclass, replace
from datetime import datetime, timezone
import argparse
import json
import os
from pathlib import Path
import shutil
import stat
import subprocess
import sys
import tempfile
import tomllib

from sync_runtime import RuntimeFailure, journal, operation_lock


ROOT = Path(__file__).resolve().parents[1]


@dataclass(frozen=True)
class Node:
    kind: str
    mode: int
    value: object


def snapshot(path, repository=None, seen=(), follow_root=False):
    """Read a tree without following external links; optionally localize repo links."""
    if path.is_symlink():
        if repository is None:
            return Node("link", 0, os.readlink(path))
        resolved = path.resolve(strict=True)
        if follow_root or resolved.is_relative_to(repository):
            return snapshot(resolved, repository, seen)
        # A relative external link would point elsewhere after moving the tree.
        return Node("link", 0, str(resolved))
    if not path.exists():
        return None
    info = path.stat()
    mode = stat.S_IMODE(info.st_mode)
    if stat.S_ISREG(info.st_mode):
        return Node("file", mode, path.read_bytes())
    if stat.S_ISDIR(info.st_mode):
        resolved = path.resolve()
        if resolved in seen:
            raise RuntimeFailure(f"目录链接形成循环，未修改配置：{path}")
        children = tuple((child.name, snapshot(child, repository, (*seen, resolved)))
                         for child in sorted(path.iterdir()))
        return Node("directory", mode, children)
    raise RuntimeFailure(f"配置中有不支持的特殊文件，未修改配置：{path}")


def write_node(node, path, private=False):
    if node.kind == "link":
        path.symlink_to(node.value)
    elif node.kind == "file":
        path.write_bytes(node.value)
        path.chmod(0o600 if private else node.mode)
    else:
        path.mkdir(mode=0o700)
        for name, child in node.value:
            write_node(child, path / name, private)
        path.chmod(0o700 if private else node.mode)


def remove_node(path):
    if path.is_dir() and not path.is_symlink():
        shutil.rmtree(path)
    elif path.exists() or path.is_symlink():
        path.unlink()


def node_metadata(node):
    if node is None:
        return None
    result = {"kind": node.kind, "mode": node.mode}
    if node.kind == "directory":
        result["children"] = {name: node_metadata(child) for name, child in node.value}
    return result


@dataclass
class Change:
    target: Path
    before: object
    after: object
    reason: str
    source: object = None
    source_before: object = None


class DetachPlan:
    def __init__(self, root, home):
        self.root, self.home = root.resolve(), home.resolve()
        self.changes = {}
        self.retained = set()

    def target(self, value):
        path = Path(os.path.abspath(os.path.expanduser(str(value))))
        if path == self.home or not path.is_relative_to(self.home):
            raise RuntimeFailure(f"只处理 HOME 内的配置目标，未修改配置：{path}")
        return path

    def add(self, target, after, reason, source=None):
        target = self.target(target)
        before = snapshot(target)
        if before == after:
            return
        change = Change(target, before, after, reason, source,
                        snapshot(source, self.root, follow_root=True) if source else None)
        previous = self.changes.get(target)
        if previous and previous.after != after:
            raise RuntimeFailure(f"配置目标有相互冲突的迁出操作：{target}")
        self.changes[target] = change

    def links(self, path):
        path = self.target(path)
        if path.is_symlink():
            if path.resolve().is_relative_to(self.root):
                self.add(path, snapshot(path, self.root, follow_root=True), "链接转为本机文件", path)
        elif path.is_dir():
            for child in sorted(path.iterdir()):
                self.links(child)

    def owns(self, entry):
        if not isinstance(entry, dict):
            raise RuntimeFailure("mise 状态条目结构无法识别，未修改配置")
        origin = entry.get("origin", {})
        config = origin.get("config")
        if not isinstance(config, str):
            raise RuntimeFailure("mise 状态缺少配置来源，未修改配置")
        return Path(config).resolve().is_relative_to(self.root)

    def collect(self, data, enrollment):
        if not isinstance(data, dict) or not all(isinstance(data.get(k), list) for k in ("files", "edits")):
            raise RuntimeFailure("mise 配置状态结构无法识别，未修改配置")
        files = [entry for entry in data["files"] if self.owns(entry)]
        edits = [entry for entry in data["edits"] if self.owns(entry)]
        sources = {}
        for entry in files:
            mode = entry.get("mode")
            if mode in {"track", "track-local", "absent"}:
                continue
            if mode not in {"symlink", "symlink-each", "copy", "template", "content", "permissions"}:
                raise RuntimeFailure(f"尚不支持的配置模式：{mode}")
            target = self.target(entry["target"])
            self.links(target)
            self.retained.add(target)
            source = entry["origin"].get("source")
            if source:
                sources[Path(source).resolve()] = target
        for entry in edits:
            target = self.target(entry["path"])
            self.links(target)
            self.retained.add(target)
        for entry in data.get("orphaned", []):
            if self.owns(entry):
                self.links(entry.get("target", entry.get("path")))
        config = sources.get(self.root / "dotfiles/common/mise/config.toml")
        path_script = sources.get(self.root / "dotfiles/common/shell/path.sh")
        if config and config.is_file() and (not config.is_symlink() or config.resolve().is_relative_to(self.root)):
            self.localize_environment(config, path_script)
        for entry in files:
            target = self.target(entry["target"])
            if target.name.startswith("mise") and target.suffix == ".lock" and target.is_file():
                if target.is_symlink() and not target.resolve().is_relative_to(self.root):
                    continue
                self.copy_sidecars(target)
        self.stop_local_history(enrollment)
        # Directory links already contain their children; never write through them.
        for target in sorted(list(self.changes), key=lambda p: len(p.parts)):
            change = self.changes.get(target)
            if change is None:
                continue
            for parent in target.parents:
                ancestor = self.changes.get(parent)
                if ancestor and ancestor.after and ancestor.after.kind == "directory":
                    if change.reason != "链接转为本机文件":
                        raise RuntimeFailure(f"需要先单独迁出父目录链接：{parent}")
                    del self.changes[target]
                    break
        for target in self.changes:
            self.check_parents(target)
        return self

    def localize_environment(self, config, path_script):
        current = snapshot(config, self.root, follow_root=True)
        data = tomllib.loads(current.value.decode())
        source = data.get("env", {}).get("_", {}).get("source", {})
        old = "{{ config_source | canonicalize | dirname }}/../shell/path.sh"
        if not isinstance(source, dict) or source.get("path") != old:
            if "config_source" in str(source) or str(self.root) in str(source):
                raise RuntimeFailure("环境脚本引用已自定义，请先改为独立的本机路径；未修改配置")
            return
        if path_script is None or not path_script.is_file():
            raise RuntimeFailure("找不到已部署的共享 PATH 脚本，未修改配置")
        text = current.value.decode()
        literal = json.dumps(old, ensure_ascii=False)
        if text.count(literal) != 1:
            raise RuntimeFailure("共享 PATH 引用格式已变化，需要人工核对；未修改配置")
        updated = replace(current, value=text.replace(literal, json.dumps(str(path_script), ensure_ascii=False)).encode())
        # This intentionally supersedes the plain materialization of config.toml.
        self.changes.pop(config, None)
        self.add(config, updated, "将 PATH 脚本引用改为本机位置", config)

    def copy_sidecars(self, lock):
        data = tomllib.loads(lock.read_text())
        for entries in data.get("tools", {}).values():
            for entry in entries:
                for graph in ("aube", "uv"):
                    if graph not in entry:
                        continue
                    relative = Path(entry[graph]["path"])
                    if relative.is_absolute() or ".." in relative.parts or relative == Path("."):
                        raise RuntimeFailure(f"下载锁的依赖目录不是安全相对路径：{lock}")
                    source = lock.resolve().parent / relative
                    if not source.is_dir():
                        raise RuntimeFailure(f"下载锁的依赖文件缺失：{source}")
                    target = self.target(lock.parent / relative)
                    after = snapshot(source, self.root, follow_root=True)
                    before = snapshot(target)
                    if before == after:
                        continue
                    if before and (before.kind != "link" or target.resolve() != source.resolve()):
                        raise RuntimeFailure(f"本机依赖目录已有不同内容，未修改配置：{target}")
                    self.add(target, after, "复制锁文件依赖目录", source)

    def stop_local_history(self, enrollment):
        if not enrollment.exists() and not enrollment.is_symlink():
            return
        if enrollment.is_symlink():
            raise RuntimeFailure("本机历史登记文件不能是链接，未修改配置")
        data = tomllib.loads(enrollment.read_text())
        candidates = {self.home / ".codex/config.toml", self.home / ".zshrc.local",
                      self.home / "Library/Application Support/Code/User/settings.json",
                      self.home / ".config/Code/User/settings.json"}
        entries = data.get("dotfiles", {})
        if set(data) != {"dotfiles"} or not entries or any(
            Path(path) not in candidates or value != {"mode": "track-local", "autosave": False}
            for path, value in entries.items()
        ):
            raise RuntimeFailure("本机历史登记有自定义内容，需先人工核对；未修改配置")
        self.add(enrollment, None, "取消本项目的本机历史登记，保留历史数据")

    def check_parents(self, target):
        for parent in target.parents:
            if parent == self.home:
                break
            if parent.is_symlink():
                raise RuntimeFailure(f"目标的父目录是链接，未修改配置：{parent}")

    def verify(self, changes):
        for change in changes:
            self.check_parents(change.target)
            if snapshot(change.target) != change.before:
                raise RuntimeFailure(f"配置在预览后发生变化，已停止：{change.target}")
            if change.source and snapshot(change.source, self.root, follow_root=True) != change.source_before:
                raise RuntimeFailure(f"配置来源在预览后发生变化，已停止：{change.source}")

    def apply(self, backup_root):
        backup_root = self.target(backup_root)
        if backup_root.is_relative_to(self.root) or any(backup_root.is_relative_to(p) for p in self.changes):
            raise RuntimeFailure("备份必须位于仓库外，且不能放在待迁出的目标内")
        self.check_parents(backup_root / "placeholder")
        changes = sorted(self.changes.values(), key=lambda c: (c.after is None, str(c.target)))
        self.verify(changes)
        backup_root.mkdir(parents=True, exist_ok=True, mode=0o700)
        directory = Path(tempfile.mkdtemp(prefix=datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%SZ-"), dir=backup_root))
        (directory / "before").mkdir(mode=0o700)
        (directory / "after").mkdir(mode=0o700)
        records, staged, created, attempted, unlinked_directories = [], [], [], [], set()
        try:
            for index, change in enumerate(changes):
                for label, node in (("before", change.before), ("after", change.after)):
                    if node:
                        write_node(node, directory / label / str(index), private=True)
                records.append({"target": str(change.target), "reason": change.reason,
                                "before": f"before/{index}" if change.before else None,
                                "after": f"after/{index}" if change.after else None,
                                "before_metadata": node_metadata(change.before),
                                "after_metadata": node_metadata(change.after)})
            journal(directory, "prepared", repository=str(self.root), changes=records)
            for change in changes:
                self.check_parents(change.target)
                missing, parent = [], change.target.parent
                while not parent.exists():
                    missing.append(parent)
                    parent = parent.parent
                for parent in reversed(missing):
                    parent.mkdir(mode=0o700)
                    created.append(parent)
                staging = Path(tempfile.mkdtemp(prefix=".system-sync-detach-", dir=change.target.parent))
                staged.append(staging)
                if change.after:
                    write_node(change.after, staging / "value")
            self.verify(changes)
            journal(directory, "applying", completed=[])
            for change, staging in zip(changes, staged):
                self.check_parents(change.target)
                if snapshot(change.target) != change.before:
                    raise RuntimeFailure(f"配置在替换前发生变化，已停止：{change.target}")
                attempted.append(change)
                if change.after is None:
                    change.target.unlink()
                else:
                    if change.before and change.before.kind == "link" and change.after.kind == "directory":
                        change.target.unlink()
                        unlinked_directories.add(change.target)
                    os.replace(staging / "value", change.target)
                    unlinked_directories.discard(change.target)
                journal(directory, "applying", completed=[str(c.target) for c in attempted])
            journal(directory, "complete")
            return directory
        except (Exception, KeyboardInterrupt):
            conflicts = []
            for change in reversed(attempted):
                try:
                    current = snapshot(change.target)
                    if current == change.before:
                        continue
                    if current != change.after and not (current is None and change.target in unlinked_directories):
                        conflicts.append(str(change.target))
                        continue
                    staging = Path(tempfile.mkdtemp(prefix=".system-sync-rollback-", dir=change.target.parent))
                    staged.append(staging)
                    if change.before:
                        write_node(change.before, staging / "value")
                    remove_node(change.target)
                    if change.before:
                        os.replace(staging / "value", change.target)
                except (OSError, RuntimeFailure):
                    conflicts.append(str(change.target))
            journal(directory, "recovery_required" if conflicts else "rolled_back", conflicts=conflicts)
            print(f"[x] 迁出失败；{'部分目标需要人工恢复' if conflicts else '已恢复本次替换的目标'}。备份：{directory}", file=sys.stderr)
            raise
        finally:
            for staging in staged:
                shutil.rmtree(staging, ignore_errors=True)
            for parent in reversed(created):
                try:
                    parent.rmdir()
                except OSError:
                    pass


def main():
    parser = argparse.ArgumentParser(description="将当前配置保留为本机独立文件，解除对本仓库的链接依赖；保留 mise 开发工具")
    parser.add_argument("--dry-run", action="store_true", help="只显示目标与动作，不修改配置或建立备份")
    parser.add_argument("--yes", action="store_true", help="已审阅预览，跳过交互确认")
    args = parser.parse_args()
    home = Path.home().resolve()
    config = Path(os.environ.get("MISE_CONFIG_DIR", str(Path(os.environ.get("XDG_CONFIG_HOME", home / ".config")) / "mise")))
    environment = dict(os.environ, MISE_AUTO_INSTALL="false", MISE_TASK_RUN_AUTO_INSTALL="false")
    result = subprocess.run(["mise", "bootstrap", "dotfiles", "status", "--json"], cwd=ROOT,
                            env=environment, stdin=subprocess.DEVNULL, text=True, capture_output=True, timeout=60)
    if result.returncode:
        raise RuntimeFailure("mise 配置状态查询失败，未修改配置；请先运行 config-status 核对")
    plan = DetachPlan(ROOT, home).collect(json.loads(result.stdout), config / "conf.d/system-sync-history.toml")
    for change in plan.changes.values():
        print(f"[!] {change.reason}：{change.target}")
    print(f"[+] 保留当前配置内容：{len(plan.retained)} 个目标；待处理 {len(plan.changes)} 项。")
    if args.dry_run:
        print("[+] 以上仅为预览，未修改配置或建立备份。")
        return 0
    if not plan.changes:
        print("[+] 无需迁出，当前配置已是独立文件。")
        return 0
    if not args.yes:
        if not sys.stdin.isatty():
            raise RuntimeFailure("请在交互终端运行；自动化请先审阅 --dry-run，再显式使用 --yes")
        if input("将上述配置迁为本机文件，并取消本项目的本机历史登记。输入 DETACH 确认：").strip() != "DETACH":
            print("[-] 已取消，未修改配置。")
            return 2
    backup_root = Path(os.environ.get("XDG_STATE_HOME", home / ".local/state")) / "system-sync/config-detach"
    with operation_lock(ROOT):
        backup = plan.apply(backup_root)
    print(f"[+] 配置已保留在本机位置；备份与操作记录：{backup}")
    print("[+] 开发工具继续由 mise 管理。重新打开终端加载本机配置；再次 config-sync 会重新接管配置。")
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except KeyboardInterrupt:
        print("[-] 已中断；如已开始替换，请检查备份中的 transaction.json。", file=sys.stderr)
        sys.exit(130)
    except (RuntimeError, OSError, ValueError, KeyError, TypeError, subprocess.TimeoutExpired) as error:
        message = str(error) if isinstance(error, RuntimeFailure) else "配置或状态无法安全读取，已停止；请检查路径、格式和权限"
        print(f"[x] {message}", file=sys.stderr)
        sys.exit(1)
