#!/usr/bin/env python3
"""Targeted native package operations followed by checked manifest edits (Python 3.11+).

This is not a package-manager hook or a generic Ruby/TOML rewriter. Only this
repository's static, single-line declarations are editable. mise validates TOML;
unsupported layouts fail before invoking a mutating package command.
"""

import argparse
from contextlib import nullcontext
from datetime import datetime, timezone
import difflib
import json
import os
from pathlib import Path
import re
import shlex
import shutil
import subprocess
import sys
import tempfile
from xml.etree import ElementTree

from sync_runtime import RuntimeFailure, operation_lock, preflight


ROOT = Path(__file__).resolve().parents[1]
TOKEN = r"[A-Za-z0-9][A-Za-z0-9@+._-]*"
BREW_NAME = re.compile(rf"{TOKEN}(?:/{TOKEN}/{TOKEN})?\Z")
LINUX_NAME = re.compile(r"[A-Za-z0-9][A-Za-z0-9+._-]*(?::[a-z0-9][a-z0-9-]*)?\Z")
BREW_LINE = re.compile(
    rf'''\s*(tap|brew|cask)\s+(["'])({TOKEN}(?:/{TOKEN}){{0,2}})\2'''
    r"((?:\s*,\s*(?:trusted|link):\s*(?:true|false))*)\s*(?:#.*)?$"
)
TOML_LINE = re.compile(r'''\s*(["'])((?:pacman|aur|apt|dnf|zypper):[^"']+)\1\s*=\s*(.+)$''')
MANAGERS = {"arch": "pacman", "debian": "apt", "fedora": "dnf", "opensuse": "zypper"}
ESSENTIAL = {
    "macos": {"git", "mise"},
    "arch": set("base bash coreutils filesystem glibc pacman shadow systemd util-linux".split()),
    "debian": set("apt base-files base-passwd bash coreutils dash dpkg libc6 login passwd sed systemd systemd-sysv tar util-linux".split()),
    "fedora": set("bash coreutils dnf dnf5 filesystem glibc rpm setup shadow-utils systemd util-linux".split()),
    "opensuse": set("bash coreutils filesystem glibc openSUSE-release rpm shadow systemd util-linux zypper".split()),
}
BOOT_PACKAGES = {
    "arch": r"^(linux($|-)|amd-ucode$|intel-ucode$|grub$|refind$|limine$|syslinux$)",
    "debian": r"^(linux-(image|headers|modules)|grub|shim|initramfs-tools|cryptsetup|lvm2|mdadm)",
    "fedora": r"^(kernel($|-)|grub2|shim|dracut|cryptsetup|lvm2|mdadm)",
    "opensuse": r"^(kernel($|-)|grub2|shim|dracut|cryptsetup|lvm2|mdadm)",
}


class Error(RuntimeError):
    pass


class Runner:
    def run(self, command, capture=True):
        environment = dict(os.environ, LC_ALL="C", HOMEBREW_NO_AUTO_UPDATE="1",
                           HOMEBREW_NO_INSTALL_CLEANUP="1", HOMEBREW_NO_AUTOREMOVE="1",
                           HOMEBREW_NO_INSTALL_UPGRADE="1")
        result = subprocess.run(
            command, env=environment, text=True, check=False,
            stdout=subprocess.PIPE if capture else None,
            stderr=subprocess.PIPE if capture else None,
            stdin=subprocess.DEVNULL if capture else None,
        )
        if result.returncode:
            detail = ((result.stderr or "") + (result.stdout or "")).strip()
            raise Error(f"命令失败（{result.returncode}）：{shlex.join(command)}\n{detail}")
        return result.stdout or ""


def read_lines(source):
    return [line.split("#", 1)[0].strip() for line in source.splitlines()
            if line.split("#", 1)[0].strip()]


def append_line(source, line):
    return source + ("\n" if source and not source.endswith("\n") else "") + line + "\n"


def brew_entries(source):
    entries = []
    seen = set()
    for index, line in enumerate(source.splitlines(keepends=True)):
        if not line.strip() or line.lstrip().startswith("#"):
            continue
        match = BREW_LINE.fullmatch(line.rstrip("\r\n"))
        if not match:
            raise Error(f"Brewfile 第 {index + 1} 行不是支持的静态声明；请手工处理动态 Ruby、多行或其他选项")
        kind, _, name, options = match.groups()
        if (kind == "tap" and name.count("/") != 1) or (kind != "tap" and not BREW_NAME.fullmatch(name)):
            raise Error(f"无效的 Brewfile 名称：{name}")
        if (kind, name) in seen:
            raise Error(f"Brewfile 重复声明：{kind} {name}")
        seen.add((kind, name))
        entries.append((index, kind, name, options))
    return entries


def toml_entries(source):
    """Locate edit spans, not TOML values. mise separately parses the whole file."""
    entries = {}
    active = False
    insertion = None
    for index, line in enumerate(source.splitlines(keepends=True)):
        stripped = line.strip()
        if not stripped or stripped.startswith("#"):
            continue
        if stripped.startswith("["):
            if re.fullmatch(r"\[bootstrap\.packages\]\s*(?:#.*)?", stripped):
                if insertion is not None:
                    raise Error("重复的 [bootstrap.packages]")
                active = True
                insertion = index + 1
            else:
                if re.match(r"\[bootstrap\.packages[.\[]", stripped):
                    raise Error("包声明必须使用 [bootstrap.packages] 下的单行 key = value 格式")
                active = False
            continue
        if active:
            match = TOML_LINE.fullmatch(line.rstrip("\r\n"))
            if not match:
                raise Error(f"包清单第 {index + 1} 行无法安全编辑；不支持多行值或非静态包名")
            key = match.group(2)
            if not LINUX_NAME.fullmatch(key.split(":", 1)[1]) or key in entries:
                raise Error(f"无效或重复的包声明：{key}")
            entries[key] = index
            insertion = index + 1
    if insertion is None:
        raise Error("缺少独立的 [bootstrap.packages]；不会猜测其他 TOML 布局")
    return entries, insertion


def replace_lines(source, remove, addition=None, insertion=None):
    lines = source.splitlines(keepends=True)
    if addition is not None:
        index = len(lines) if insertion is None else insertion
        if index and not lines[index - 1].endswith("\n"):
            lines[index - 1] += "\n"
        lines.insert(index, addition + "\n")
        remove = {i + (i >= index) for i in remove}
    return "".join(line for index, line in enumerate(lines) if index not in remove)


class PackageCommand:
    def __init__(self, root, profile, args, runner=None):
        self.root, self.profile, self.args = root, profile, args
        self.runner = runner or Runner()
        self.manager = "brew" if profile == "macos" else MANAGERS[profile]
        self.kind = "cask" if args.cask else "formula"
        if args.aur:
            if profile != "arch":
                raise Error("--aur 仅适用于 Arch 系 Linux")
            self.manager = "aur"
        if profile != "macos" and (args.cask or args.formula or args.trust):
            raise Error("--cask、--formula、--trust 仅适用于 macOS Homebrew")
        self.directory = root / "packages" / ("macos" if profile == "macos" else f"linux/{profile}")
        filename = "Brewfile" if profile == "macos" else "mise.toml"
        self.manifest = self.directory / filename
        self.before = {}
        self.after = {}
        self.name = args.package
        self.aliases = {self.name}
        self.audit = None
        self.validate_name(self.name)

    def validate_name(self, name):
        pattern = BREW_NAME if self.profile == "macos" else LINUX_NAME
        if not pattern.fullmatch(name) or (self.profile != "debian" and ":" in name):
            raise Error(f"请指定一个准确包名，不接受路径、URL、通配符、版本表达式或原生命令选项：{name}")

    def read(self, path):
        if path not in self.before:
            if path.is_symlink() or not path.is_file() or path.resolve().parent != self.directory.resolve():
                raise Error(f"要求普通的本地清单文件：{path}")
            self.before[path] = path.read_text(encoding="utf-8")
        return self.before[path]

    def installed(self):
        if self.profile == "macos":
            cmd = ["brew", "list", f"--{self.kind}"]
            if self.kind == "formula":
                cmd.append("--full-name")
            return set(self.runner.run(cmd).splitlines())
        if self.profile == "arch":
            return set(self.runner.run(["pacman", "-Qq"]).splitlines())
        if self.profile == "debian":
            output = self.runner.run(["dpkg-query", "-W", "-f=${Package}:${Architecture}\t${db:Status-Status}\n"])
            names = set()
            for line in output.splitlines():
                name, status = line.split("\t", 1)
                if status == "installed":
                    names.update((name, name.split(":", 1)[0]))
            return names
        return set(self.runner.run(["rpm", "-qa", "--qf", "%{NAME}\n"]).splitlines())

    def present(self, names):
        # Homebrew cask list emits short tokens, even for third-party casks.
        key = self.name.rsplit("/", 1)[-1] if self.kind == "cask" and self.profile == "macos" else self.name
        return key in names

    def resolve_brew(self):
        if (self.name.count("/") == 2 and self.args.action == "install" and
                not self.name.startswith(("homebrew/core/", "homebrew/cask/"))):
            # Loading untrusted third-party Ruby can itself fail, even if tapped.
            # Keep previews read-only; the installer performs exact-item trust
            # after confirmation. Verify this canonical name after installation.
            self.aliases.add(self.name.rsplit("/", 1)[-1])
            return
        item = self.brew_info()
        canonical = item["full_token" if self.kind == "cask" else "full_name"]
        self.validate_name(canonical)
        self.aliases.update(item.get("aliases", []))
        self.aliases.update(item.get("old_tokens" if self.kind == "cask" else "oldnames", []))
        self.aliases.update((canonical, canonical.rsplit("/", 1)[-1]))
        self.name = canonical

    def brew_info(self):
        data = json.loads(self.runner.run(["brew", "info", "--json=v2", f"--{self.kind}", self.name]))
        values = data["casks" if self.kind == "cask" else "formulae"]
        if len(values) != 1:
            raise Error("Homebrew 包名未解析成唯一项目，请使用准确的 formula/cask 名称")
        return values[0]

    def protected(self):
        protected = set(read_lines(self.read(self.directory / "protected-packages.txt")))
        if self.profile == "macos":
            tokens = {self.name, self.name.rsplit("/", 1)[-1]} | self.aliases
            blocked = any(f"{self.kind}:{name}" in protected for name in tokens)
            blocked |= self.kind == "formula" and bool(tokens & ESSENTIAL["macos"])
        else:
            name = self.name.split(":", 1)[0]
            blocked = name in protected | ESSENTIAL[self.profile] or bool(re.match(BOOT_PACKAGES[self.profile], name))
        if self.args.action == "uninstall" and blocked:
            raise Error(f"拒绝卸载受保护包：{self.name}；保护清单/内置启动保护不能由此入口绕过")

    def edit_brew(self, source):
        entries = brew_entries(source)
        declaration = "cask" if self.kind == "cask" else "brew"
        matches = [entry for entry in entries if entry[1] == declaration and entry[2] in self.aliases | {self.name}]
        if any(kind == declaration and name.count("/") == 2 and self.name.count("/") == 2
               and name != self.name and name.rsplit("/", 1)[-1] == self.name.rsplit("/", 1)[-1]
               for _, kind, name, _ in entries):
            raise Error("同名包已从另一个 tap 声明；请人工确认来源切换")
        if len(matches) > 1:
            raise Error("同一包有多条声明（含别名）；请先手工去重")
        full = self.name.count("/") == 2 and not self.name.startswith(("homebrew/core/", "homebrew/cask/"))
        trust_file = self.directory / ("trusted-casks.txt" if self.kind == "cask" else "trusted-formulae.txt")
        trusted_source = self.read(trust_file)
        trusted = set(read_lines(trusted_source))
        remove = {entry[0] for entry in matches}
        if self.args.action == "uninstall":
            result = replace_lines(source, remove)
            self.after[trust_file] = replace_lines(trusted_source, {
                i for i, line in enumerate(trusted_source.splitlines())
                if line.split("#", 1)[0].strip() in self.aliases | {self.name}
            })
        else:
            if full:
                already_trusted = (self.name in trusted and bool(matches) and
                                   re.fullmatch(r"\s*,\s*trusted:\s*true", matches[0][3]) is not None)
                if not (self.args.trust or already_trusted):
                    raise Error("第三方包需要显式 --trust；仅信任这一完整包名，不信任整个 tap")
                if matches and matches[0][3] and not re.fullmatch(r"\s*,\s*trusted:\s*(true|false)", matches[0][3]):
                    raise Error("第三方声明含其他选项，无法满足现有精确信任格式；请手工调整")
                entry = f'{declaration} "{self.name}", trusted: true'
                result = replace_lines(source, remove, entry, matches[0][0] if matches else None)
                if self.name not in trusted:
                    self.after[trust_file] = append_line(trusted_source, self.name)
                tap = self.name.rsplit("/", 1)[0]
                if not any(kind == "tap" and name == tap for _, kind, name, _ in entries):
                    result = f'tap "{tap}"\n' + result
            elif matches:
                result = source  # Preserve versions/options/comments, e.g. link: false.
            else:
                result = append_line(source, f'{declaration} "{self.name}"')
        if not any(kind in {"brew", "cask"} for _, kind, _, _ in brew_entries(result)):
            raise Error("拒绝生成空的软件包清单")
        return result

    def edit_linux(self, source):
        entries, insertion = toml_entries(source)
        allowed = {"pacman", "aur"} if self.profile == "arch" else {self.manager}
        if any(k.split(":", 1)[0] not in allowed for k in entries):
            raise Error("此 profile 包含其他平台的 manager 声明；请先整理清单")
        key = f"{self.manager}:{self.name}"
        matches = {k: i for k, i in entries.items() if k.split(":", 1)[1] == self.name}
        if self.args.action == "install":
            if matches and key not in matches:
                raise Error("同名包已由另一 manager 声明；不会自动切换 pacman/AUR 来源")
            if key in entries:
                value = TOML_LINE.fullmatch(source.splitlines()[entries[key]]).group(3)
                if not re.fullmatch(r'''(?:["']latest["']|\{\s*version\s*=\s*["']latest["']\s*\})\s*(?:#.*)?''', value):
                    raise Error("目标包带有版本固定或其他选项；此入口仅安装 latest，请手工处理该声明")
            result = source if key in entries else replace_lines(source, set(), f'"{key}" = "latest"', insertion)
        else:
            result = replace_lines(source, set(matches.values()))
        if not toml_entries(result)[0]:
            raise Error("拒绝生成空的软件包清单")
        return result

    def validate_toml(self, source):
        # Official parser, independent of Python's optional tomllib/tomli availability.
        with tempfile.TemporaryDirectory(prefix="system-sync-toml-") as temporary:
            path = Path(temporary) / "mise.toml"
            path.write_text(source, encoding="utf-8")
            self.runner.run(["mise", "config", "get", "--file", str(path), "bootstrap.packages"])

    def prepare(self):
        source = self.read(self.manifest)
        if self.profile == "macos":
            brew_entries(source)  # Fail on unsupported input before any native operation.
            self.resolve_brew()
        else:
            self.validate_toml(source)
        self.protected()
        candidate = self.edit_brew(source) if self.profile == "macos" else self.edit_linux(source)
        if self.profile != "macos":
            self.validate_toml(candidate)
        self.after[self.manifest] = candidate
        self.after = {path: content for path, content in self.after.items() if self.before[path] != content}

    def command(self):
        action, name = self.args.action, self.name
        if self.profile == "macos":
            cmd = ["brew", action, f"--{self.kind}"]
            if action == "install" and self.kind == "formula":
                if any(kind == "brew" and package in self.aliases | {name} and re.search(r"link:\s*false", opts)
                       for _, kind, package, opts in brew_entries(self.read(self.manifest))):
                    cmd.append("--skip-link")
            return cmd + [name]
        prefix = [] if os.geteuid() == 0 else ["sudo"]
        if self.profile == "arch":
            if action == "install" and self.manager == "aur":
                helper = shutil.which("paru") or shutil.which("yay")
                if not helper:
                    raise Error("--aur 需要先安装 paru 或 yay")
                if os.geteuid() == 0:
                    raise Error("AUR helper 必须由普通用户运行，不能 sudo mise")
                return [helper, "-S", "--needed", "--asexplicit", "--", name]
            return prefix + ["pacman"] + (["-S", "--needed", "--asexplicit"] if action == "install" else ["-R"]) + ["--", name]
        if self.profile == "debian":
            options = ["--no-auto-remove", "-o", "APT::Get::Purge=false"]
            if action == "install":
                options += ["--no-upgrade", "--no-remove"]
            return prefix + ["apt-get", *options, "install" if action == "install" else "remove", "--", name]
        if self.profile == "fedora":
            return prefix + ["dnf", "--setopt=clean_requirements_on_remove=False", "install" if action == "install" else "remove", "--", name]
        return prefix + ["zypper", "install" if action == "install" else "remove", "--type", "package",
                         "--name" if action == "install" else "--no-clean-deps", "--", name]

    def check_native_target(self, installed):
        if self.profile == "debian":
            if self.args.action == "install":
                data = self.runner.run(["apt-cache", "show", "--", self.name])
                if self.name.split(":", 1)[0] not in re.findall(r"^Package: (\S+)$", data, re.M):
                    raise Error("APT 入口只接受准确的实际包名，不接受虚拟包或匹配表达式")
            elif self.present(installed):
                output = self.runner.run(["apt-get", "--simulate", "--no-auto-remove", "-o", "APT::Get::Purge=false", "remove", "--", self.name])
                removals = re.findall(r"^(?:Remv|Purg) (\S+)", output, re.M)
                if not removals or any(n.split(":", 1)[0] != self.name.split(":", 1)[0] for n in removals):
                    raise Error("APT 将连带移除其他包或返回未知预演格式；拒绝执行\n" + output)
        elif self.profile == "arch" and self.args.action == "install":
            manager = self.command()[0] if self.manager == "aur" else "pacman"
            self.runner.run([manager, "-Si", "--", self.name])  # Reject package groups.
        elif self.profile in {"fedora", "opensuse"} and self.args.action == "uninstall" and self.present(installed):
            # Check reverse dependencies without removing anything. No --nodeps.
            self.runner.run(["rpm", "-e", "--test", "--", self.name])
        elif self.profile == "fedora" and self.args.action == "install":
            names = self.runner.run(["dnf", "repoquery", "--queryformat", "%{name}", "--", self.name]).splitlines()
            if self.name not in names:
                raise Error("DNF 入口只接受准确的实际包名，不接受虚拟 capability 或包组")
        elif self.profile == "opensuse" and self.args.action == "install":
            output = self.runner.run(["zypper", "--xmlout", "--no-refresh", "search", "--match-exact", "--type", "package", "--", self.name])
            try:
                names = {item.get("name") for item in ElementTree.fromstring(output).iter("solvable")}
            except ElementTree.ParseError as error:
                raise Error("无法解析 zypper 包查询 XML") from error
            if self.name not in names:
                raise Error("zypper 本地元数据找不到这个准确包名；请检查名称或先刷新仓库")

    def unchanged(self):
        for path, content in self.before.items():
            if path.is_symlink() or path.read_text(encoding="utf-8") != content:
                raise Error(f"操作期间清单被修改，拒绝覆盖：{path}")

    def save_audit(self, phase, command, detail=""):
        (self.audit / "transaction.json").write_text(json.dumps({
            "action": self.args.action, "profile": self.profile, "package": self.name,
            "command": command, "phase": phase, "detail": detail,
        }, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")

    def backup(self, command):
        history = self.root / ".system-sync/history"
        history.mkdir(parents=True, exist_ok=True)
        prefix = datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%SZ-package-")
        self.audit = Path(tempfile.mkdtemp(prefix=prefix, dir=history))
        for path, source in self.before.items():
            (self.audit / (path.name + ".before")).write_text(source, encoding="utf-8")
        for path, source in self.after.items():
            (self.audit / (path.name + ".after")).write_text(source, encoding="utf-8")
        self.save_audit("pending", command)

    @staticmethod
    def atomic_write(path, content):
        descriptor, temporary = tempfile.mkstemp(prefix=".package-command-", dir=path.parent)
        try:
            with os.fdopen(descriptor, "w", encoding="utf-8") as output:
                output.write(content)
                output.flush()
                os.fsync(output.fileno())
            os.chmod(temporary, path.stat().st_mode & 0o777)
            os.replace(temporary, path)
        finally:
            if os.path.exists(temporary):
                os.unlink(temporary)

    def commit_files(self):
        self.unchanged()
        saved = []
        try:
            for path, source in self.after.items():
                if path.read_text(encoding="utf-8") != self.before[path]:
                    raise Error(f"清单已改变：{path}")
                self.atomic_write(path, source)
                saved.append(path)
        except (OSError, Error):
            for path in saved:
                if path.read_text(encoding="utf-8") == self.after[path]:
                    self.atomic_write(path, self.before[path])
            raise

    def execute(self, confirm=input):
        self.prepare()
        command = self.command()
        installed = self.installed()
        self.check_native_target(installed)
        skip_native = self.args.action == "uninstall" and not self.present(installed)
        print(f"==> {self.profile} / {self.manager} / {self.name}")
        print("包已不在已安装列表中；确认后仅移除声明。" if skip_native else "$ " + shlex.join(command))
        if self.profile == "macos" and self.args.action == "install" and self.name.count("/") == 2:
            print("将按完整包名安装；Homebrew 可能添加对应 tap，并信任此具体 formula/cask。")
        for path, content in self.after.items():
            relative = str(path.relative_to(self.root))
            print("".join(difflib.unified_diff(self.before[path].splitlines(True), content.splitlines(True),
                                             fromfile=relative, tofile=relative)), end="")
        if not self.after:
            print("声明已符合目标，无需修改文件。")
        if self.args.dry_run:
            print("预览结束：未执行安装/卸载，也未修改清单。依赖下载与安装细节以包管理器实际事务为准。")
            return
        phrase = f"{self.args.action.upper()} {self.name}"
        if confirm(f"输入 {phrase} 以执行（其他输入取消）：").strip() != phrase:
            print("已取消；未执行安装/卸载，未修改清单。")
            return
        self.unchanged()
        if self.installed() != installed:
            raise Error("确认期间安装状态已改变，请重新运行以计算计划")
        self.backup(command)
        try:
            if not skip_native:
                self.runner.run(command, capture=False)
            remaining = self.installed()
            if self.present(remaining) != (self.args.action == "install"):
                raise Error("包管理器返回成功，但安装状态未达到目标；配置保持不变")
            removed = installed - remaining
            allowed_removals = set()
            if self.args.action == "uninstall":
                if self.profile == "debian":
                    allowed_removals = {n for n in installed if n.split(":", 1)[0] == self.name.split(":", 1)[0]}
                else:
                    allowed_removals = {self.name, self.name.rsplit("/", 1)[-1]}
            if removed - allowed_removals:
                raise Error("检测到未计划的其他包移除，请先人工核对：" + ", ".join(sorted(removed - allowed_removals)))
            if self.profile == "macos" and self.args.action == "install":
                info = self.brew_info()
                if info["full_token" if self.kind == "cask" else "full_name"] != self.name:
                    raise Error("安装后的规范包名与预演不同；请用实际完整名称重试，配置保持不变")
            self.save_audit("native-succeeded", command)
            self.commit_files()
            self.save_audit("complete", command)
        except (Error, OSError, ValueError, KeyError, KeyboardInterrupt) as error:
            self.save_audit("needs-review", command, str(error))
            raise Error(f"{error}\n系统包操作不能自动回滚。请检查实际安装状态及审计备份：{self.audit}；不要直接运行整机收敛。") from error
        print(f"完成；只同步了指定根包，未运行整机 cleanup/autoremove。审计：{self.audit}")


def parse_args(argv=None):
    parser = argparse.ArgumentParser(description="安装/卸载一个系统根包，确认成功后同步本平台清单。请勿 sudo mise。")
    parser.add_argument("action", choices=("install", "uninstall"))
    parser.add_argument("package", help="一个准确包名；Homebrew 第三方包使用 owner/tap/name")
    parser.add_argument("--dry-run", action="store_true", help="仅预览命令与声明差异")
    kind = parser.add_mutually_exclusive_group()
    kind.add_argument("--cask", action="store_true", help="Homebrew cask（默认为 formula）")
    kind.add_argument("--formula", action="store_true", help="显式选择 Homebrew formula")
    parser.add_argument("--aur", action="store_true", help="Arch AUR 根包，使用 paru 或 yay")
    parser.add_argument("--trust", action="store_true", help="审核后明确同意信任这一第三方 Homebrew 包，并记录精确信任")
    args = parser.parse_args(argv)
    if args.trust and args.action != "install":
        parser.error("--trust 只适用于 install")
    return args


def main():
    args = parse_args()
    profile = preflight(ROOT)
    runner = Runner()
    with nullcontext() if args.dry_run else operation_lock(ROOT):
        PackageCommand(ROOT, profile, args, runner).execute()


if __name__ == "__main__":
    try:
        main()
    except (Error, RuntimeFailure, OSError, ValueError, KeyError, EOFError) as error:
        print(f"错误：{error}", file=sys.stderr)
        sys.exit(1)
    except KeyboardInterrupt:
        print("\n已中断。若包管理器已经开始执行，请检查安装状态后重试。", file=sys.stderr)
        sys.exit(130)
