#!/usr/bin/env python3
"""Read-only machine/configuration audit, using Python 3.11+ and existing CLIs."""

import argparse
from collections import Counter
from concurrent.futures import ThreadPoolExecutor
import json
import os
from pathlib import Path
import re
import subprocess
import sys
from threading import Lock


ROOT = Path(__file__).resolve().parents[1]
SECTIONS = ("tools", "packages", "path", "config")
KINDS = {"ok": "已核对", "drift": "漂移", "candidate": "待纳管候选",
         "local": "本机保留", "error": "未完成核对"}
MARKERS = {"ok": "[+]", "drift": "[!]", "candidate": "[?]", "local": "[-]", "error": "[x]"}
COLORS = {"drift": "\033[31m", "candidate": "\033[33m", "error": "\033[1;31m"}
COMMANDS = {"node": "node", "python": "python3", "ruby": "ruby", "java": "java",
            "rust": "rustc", "bun": "bun", "uv": "uv", "npm:yarn": "yarn",
            "go": "go", "deno": "deno", "flutter": "flutter"}
SYSTEM_PATHS = {"/usr/bin", "/bin", "/usr/sbin", "/sbin", "/usr/local/bin",
                "/usr/local/sbin", "/opt/homebrew/bin", "/opt/homebrew/sbin",
                "/home/linuxbrew/.linuxbrew/bin", "/home/linuxbrew/.linuxbrew/sbin",
                "/Library/Apple/usr/bin", "/System/Cryptexes/App/usr/bin", "/snap/bin"}
CONFIG_CANDIDATES = (".gitconfig", ".config/git/config", ".tmux.conf", ".config/starship.toml",
                     ".config/ghostty/config", ".config/nvim/init.lua", ".config/nvim/init.vim",
                     "Library/Application Support/Code/User/keybindings.json",
                     "Library/Application Support/Code/User/snippets",
                     ".config/Code/User/keybindings.json", ".config/Code/User/snippets")

# These are the repository's own --status headings, not upstream CLI output.
PACKAGE_GROUPS = {
    "Brewfile formula 根声明": "ok", "Brewfile cask 声明": "ok", "Brewfile tap 声明": "ok",
    "尚未安装的 formula": "drift", "尚未安装的 cask": "drift", "尚未添加的 tap": "drift",
    "Homebrew 可识别的未声明 requested formula": "candidate",
    "未声明的已安装 cask": "candidate", "未声明的 tap": "candidate",
    "受保护但未在 Brewfile 声明的项目": "drift",
    "mise 最终生效声明": "ok", "声明但尚未以同名具体包安装": "drift",
    "当前显式安装包中未声明、但受保护而保留": "local",
    "apply 时会从 dependency 提升为 explicit 的声明/保护包": "drift",
    "apply 时会从 explicit 降级为 dependency 的未声明包": "candidate",
    "当前真正 orphan 中可清理的包": "candidate",
    "当前 orphan 中因声明或保护规则而跳过的包": "local",
    "当前手工根包中未声明、但受保护而保留": "local",
    "apply 时会提升为手工根包的声明/保护包": "drift",
    "apply 时会降级为依赖的未声明手工根包": "candidate",
    "当前 autoremove 事务中的可清理包": "candidate",
    "当前 autoremove 事务中因声明或保护规则冲突的包": "drift",
    "声明的根包": "ok", "声明但尚未安装": "drift",
    "未声明但受保护的 user-installed 包": "local",
    "拟直接移除的未声明 user-installed 包": "candidate",
    "zypper 当前认定的 unneeded 包": "candidate", "zypper dry-run 的完整移除事务": "candidate",
}
PACKAGE_REQUIRED = (
    {"Brewfile formula 根声明", "Brewfile cask 声明", "Brewfile tap 声明",
     "尚未安装的 formula", "尚未安装的 cask", "尚未添加的 tap",
     "Homebrew 可识别的未声明 requested formula", "未声明的已安装 cask", "未声明的 tap",
     "受保护但未在 Brewfile 声明的项目"},
    {"mise 最终生效声明", "声明但尚未以同名具体包安装",
     "当前显式安装包中未声明、但受保护而保留",
     "apply 时会从 dependency 提升为 explicit 的声明/保护包",
     "apply 时会从 explicit 降级为 dependency 的未声明包",
     "当前真正 orphan 中可清理的包", "当前 orphan 中因声明或保护规则而跳过的包"},
    {"mise 最终生效声明", "声明但尚未以同名具体包安装",
     "当前手工根包中未声明、但受保护而保留", "apply 时会提升为手工根包的声明/保护包",
     "apply 时会降级为依赖的未声明手工根包", "当前 autoremove 事务中的可清理包",
     "当前 autoremove 事务中因声明或保护规则冲突的包"},
    {"声明的根包", "声明但尚未安装", "未声明但受保护的 user-installed 包",
     "拟直接移除的未声明 user-installed 包", "zypper 当前认定的 unneeded 包",
     "zypper dry-run 的完整移除事务"},
)


class AuditError(RuntimeError):
    pass


class Runner:
    def __init__(self, root, environment):
        self.root, self.environment = root, environment

    def run(self, command, environment=None):
        try:
            result = subprocess.run(
                command, cwd=self.root, env=environment if environment is not None else self.environment,
                stdin=subprocess.DEVNULL, capture_output=True, text=True, errors="replace", timeout=60,
            )
        except subprocess.TimeoutExpired:
            raise AuditError("查询超过 60 秒；该项尚未核对") from None
        except OSError:
            raise AuditError("查询命令不可用；该项尚未核对") from None
        if result.returncode:
            # CLI diagnostics may quote private configuration values.
            raise AuditError(f"查询退出码为 {result.returncode}；请单独运行对应状态命令检查")
        return result.stdout


def parse_package_status(output):
    groups = []
    seen = set()
    pattern = r"^([^\n]+)（([0-9]+)）\n((?:  [^\n]*(?:\n|$))*)"
    for match in re.finditer(pattern, output.replace("\r\n", "\n"), re.MULTILINE):
        title, count, body = match.groups()
        if title not in PACKAGE_GROUPS or title in seen:
            raise AuditError("系统包状态区段无法识别；请单独运行 mise run status")
        items = [line[4:] for line in body.splitlines() if line.startswith("  - ")]
        if len(items) != int(count):
            raise AuditError("系统包状态计数与列表不一致；报告尚未完整")
        if any(not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9@+._:/ -]*(?:\[[a-z_]+\])?", item)
               for item in items):
            raise AuditError("系统包状态包含无法识别的名称")
        seen.add(title)
        groups.append((title, PACKAGE_GROUPS[title], items))
    if not any(required <= seen for required in PACKAGE_REQUIRED):
        raise AuditError("系统包状态缺少必需区段；不能判定同步完成")
    return groups


def redact(value, environment):
    secrets = {content for name, content in environment.items() if content and (
        re.search(r"TOKEN|SECRET|PASSWORD|API_KEY|CREDENTIAL", name, re.IGNORECASE) or
        name in {"CASE_SUBMITTER", "DASHBOARD_HOST"})}

    def clean(item):
        if isinstance(item, dict):
            return {key: content if key in {"kind", "code", "path_source"} else clean(content)
                    for key, content in item.items()}
        if isinstance(item, list):
            return [clean(content) for content in item]
        if isinstance(item, str):
            for secret in sorted(secrets, key=len, reverse=True):
                if item == secret:
                    item = "[REDACTED]"
                elif len(secret) >= 4:
                    item = item.replace(secret, "[REDACTED]")
            return re.sub(r"[\x00-\x08\x0b-\x1f\x7f]", "?", item)
        return item

    return clean(value)


class Audit:
    def __init__(self, root=ROOT, environment=None, runner=None):
        self.root = Path(root).resolve()
        self.environment = dict(os.environ if environment is None else environment)
        self.home = Path(self.environment.get("HOME", str(Path.home()))).absolute()
        self.data = Path(self.environment.get("MISE_DATA_DIR", str(
            Path(self.environment.get("XDG_DATA_HOME", str(self.home / ".local/share"))) / "mise")))
        self.caller_path = self.environment.get("SYSTEM_SYNC_AUDIT_CALLER_PATH", self.environment.get("PATH", ""))
        query_environment = dict(self.environment, MISE_AUTO_INSTALL="false", MISE_TASK_RUN_AUTO_INSTALL="false",
                                 PYTHONDONTWRITEBYTECODE="1", HOMEBREW_NO_AUTO_UPDATE="1", NO_COLOR="1", TERM="dumb")
        self.runner = runner or Runner(self.root, query_environment)
        self.sections = {name: [] for name in SECTIONS}
        self.tools = None
        self.tools_error = None
        self.tools_lock = Lock()

    def display(self, value):
        value = str(value)
        if value.startswith("~"):
            return value
        for prefix, label in ((str(self.root), "<repo>"), (str(self.home), "~")):
            if value == prefix or value.startswith(prefix + "/"):
                return label + value[len(prefix):]
        return value

    def expand(self, value):
        return self.home / value[2:] if value.startswith("~/") else Path(value)

    def add(self, section, kind, code, item, message, **details):
        self.sections[section].append(dict(kind=kind, code=code, item=self.display(item), message=message, **details))

    def query_json(self, command):
        try:
            return json.loads(self.runner.run(command))
        except (ValueError, TypeError):
            raise AuditError("状态 JSON 无法识别；报告尚未完成") from None

    def load_tools(self):
        # tools and PATH share one snapshot, including a failed query.
        with self.tools_lock:
            if self.tools_error is not None:
                raise AuditError(self.tools_error)
            if self.tools is None:
                try:
                    self.tools = self.read_tools()
                except AuditError as error:
                    self.tools_error = str(error)
                    raise
            return self.tools

    def read_tools(self):
        data = self.query_json(["mise", "ls", "--current", "--json"])
        if not isinstance(data, dict):
            raise AuditError("工具状态顶层结构无法识别")
        rows = []
        for name, versions in data.items():
            if not isinstance(name, str) or not isinstance(versions, list) or not versions:
                raise AuditError("工具版本列表无法识别")
            for version in versions:
                if (not isinstance(version, dict) or not isinstance(version.get("version"), str) or
                        not isinstance(version.get("installed"), bool)):
                    raise AuditError("工具版本状态缺少必需字段")
                rows.append(dict(version, tool=name))
        return rows

    def command_paths(self, rows):
        commands = list(dict.fromkeys(COMMANDS[row["tool"]] for row in rows
                                     if row["installed"] and row["tool"] in COMMANDS))

        def query(command):
            try:
                return self.runner.run(["mise", "which", command]).strip()
            except AuditError as error:
                return error

        paths = {}
        with ThreadPoolExecutor(max_workers=4) as pool:
            for start in range(0, len(commands), 4):
                batch = commands[start:start + 4]
                values = list(pool.map(query, batch))
                paths.update(zip(batch, values))
                # Preserve partial reporting and avoid scheduling more queries after failure.
                if any(isinstance(value, AuditError) for value in values):
                    break
        return paths

    def collect_tools(self):
        rows = self.load_tools()
        for row in rows:
            name, version = row["tool"], row["version"]
            source = row.get("source") or {}
            if not isinstance(source, dict):
                raise AuditError("工具配置来源结构无法识别")
            source = source.get("path") or source.get("type") or "unknown"
            if not isinstance(source, str):
                raise AuditError("工具配置来源路径无法识别")
            self.add("tools", "ok" if row["installed"] else "drift", "tool_installed" if row["installed"] else "tool_missing",
                     name, f"声明版本 {version}" + (" 已安装" if row["installed"] else " 尚未安装"),
                     version=version, source=self.display(source))
            location = row.get("install_path")
            if location is not None and not isinstance(location, str):
                raise AuditError("工具安装路径无法识别")
            if row["installed"] and isinstance(location, str) and Path(location).is_symlink():
                self.add("tools", "local", "linked_install", name, "已注册的链接安装；原来源需要保留",
                         path=self.display(Path(location).resolve()))
        installed = [Path(row["install_path"]).resolve() for row in rows
                     if row["installed"] and isinstance(row.get("install_path"), str)]
        patterns = {
            "nvm": ".nvm/versions/node/*/bin/node", "pyenv": ".pyenv/versions/*/bin/python3",
            "uv Python": ".local/share/uv/python/*/bin/python3", "rbenv": ".rbenv/versions/*/bin/ruby",
            "asdf": ".asdf/installs/*/*/bin/*", "Bun installer": ".bun/bin/bun",
        }
        for source, pattern in patterns.items():
            candidates = sorted({self.display(path) for path in self.home.glob(pattern)
                                 if path.is_file() and os.access(path, os.X_OK) and
                                 not any(path.resolve().is_relative_to(prefix) for prefix in installed)})
            if candidates:
                self.add("tools", "candidate", "external_installations", source,
                         "发现其他安装来源；可能是历史版本或备用安装，需要核对意图", items=candidates, count=len(candidates))
        if not rows:
            self.add("tools", "ok", "no_tools", "[tools]", "当前配置没有工具版本声明")

    def collect_packages(self):
        output = self.runner.run([str(self.root / "scripts/system-sync.sh"), "--status"])
        for title, kind, items in parse_package_status(output):
            if items or kind == "ok":
                self.add("packages", kind, "package_group", title, f"{len(items)} 项", items=items, count=len(items))
            if title == "mise 最终生效声明":
                mismatched = [item for item in items if re.search(r"\[(version_mismatch|needs_repair)\]$", item)]
                if mismatched:
                    self.add("packages", "drift", "package_version", title, "存在版本不符或需要修复的声明", items=mismatched)
        result = re.search(r"bundle check 退出码为 ([0-9]+)", output)
        if result and int(result.group(1)):
            self.add("packages", "drift", "bundle_check", "Homebrew", "bundle check 未通过，需单独运行 mise run status")
        if "Homebrew cleanup 精确预演\n" in output:
            preview = output.split("Homebrew cleanup 精确预演\n", 1)[1].split("状态模式：", 1)[0].strip()
            if preview and preview != "（无输出）":
                self.add("packages", "candidate", "cleanup_review", "Homebrew", "cleanup 预演含额外输出，需单独运行 mise run status 审阅")

    def policy_paths(self):
        source = self.root / "dotfiles/common/shell/path.sh"
        if not source.is_file():
            raise AuditError("共享 PATH 来源文件缺失")
        environment = {name: self.environment[name] for name in
                       ("HOME", "ANDROID_HOME", "CARGO_HOME", "MISE_DATA_DIR", "XDG_DATA_HOME") if name in self.environment}
        environment.update(HOME=str(self.home), PATH="/usr/bin:/bin:/usr/sbin:/sbin",
                           MISE_AUTO_INSTALL="false", MISE_TASK_RUN_AUTO_INSTALL="false")
        bash = "/bin/bash" if Path("/bin/bash").is_file() else "bash"
        output = self.runner.run([bash, "--noprofile", "--norc", "-c",
                                  'set -e; . "$1"; _system_sync_user_path; printf "%s" "$PATH"',
                                  "audit-path", str(source)], environment=environment)
        return set(output.split(":")) - {"/usr/bin", "/bin", "/usr/sbin", "/sbin"}

    def retained_path(self, entry):
        path = Path(entry)
        if entry in SYSTEM_PATHS or entry.startswith((
                "/var/run/com.apple.security.cryptexd/", "/Applications/Xcode.app/",
                "/opt/homebrew/", "/home/linuxbrew/.linuxbrew/")):
            return "系统、IDE 或包管理器提供的路径"
        runtime_roots = (self.home / ".codex/tmp", self.home / ".cache/codex-runtimes",
                         Path("/Applications/ChatGPT.app/Contents"), Path("/pkg/env"))
        if any(path.is_relative_to(prefix) for prefix in runtime_roots):
            return "宿主应用或临时运行环境提供的路径"
        return None

    def collect_path(self):
        entries = self.caller_path.split(":")
        counts = Counter(entries)
        for entry in dict.fromkeys(entries):
            if not entry or not Path(entry).is_absolute():
                self.add("path", "drift", "unsafe_path", entry or "<空项>", "空或相对 PATH 项依赖当前目录")
            elif not Path(entry).is_dir():
                retained = self.retained_path(entry)
                self.add("path", "local" if retained else "drift", "missing_directory", entry,
                         (retained + "；当前目录不存在或不可访问") if retained else "PATH 目录不存在或不可访问")
            if counts[entry] > 1:
                self.add("path", "drift", "duplicate_path", entry or "<空项>", f"重复 {counts[entry]} 次")
        declared = self.policy_paths()
        actual = set(entries)
        for missing in sorted(declared - actual):
            self.add("path", "drift", "policy_path_missing", missing, "共享 PATH 规则声明的现有目录未进入调用环境")
        for entry in dict.fromkeys(entries):
            path = Path(entry)
            if not entry or not path.is_absolute() or not path.is_dir() or entry in declared:
                continue
            if path.is_relative_to(self.data):
                continue
            retained = self.retained_path(entry)
            if retained:
                self.add("path", "local", "system_path", entry, retained)
            else:
                self.add("path", "candidate", "unmanaged_path", entry, "未由共享 PATH 规则声明，需要核对来源")
        rows = self.load_tools()
        requested = {row["tool"] for row in rows}
        expected_paths = self.command_paths(rows)
        for row in rows:
            command = COMMANDS.get(row["tool"])
            if not row["installed"] or not command:
                continue
            expected = expected_paths[command]
            if isinstance(expected, AuditError):
                raise expected
            if not expected or "\n" in expected or not Path(expected).is_absolute():
                raise AuditError("mise which 返回了无法识别的命令路径")
            found = []
            for entry in entries:
                if not entry or not Path(entry).is_absolute():
                    continue
                candidate = Path(entry) / command
                if candidate.is_file() and os.access(candidate, os.X_OK):
                    if not any(candidate.resolve() == previous.resolve() for previous in found):
                        found.append(candidate)
            if not found:
                self.add("path", "drift", "command_missing", command, "调用环境找不到已声明工具的主命令")
                continue
            first = found[0]
            shim = self.data / "shims" / command
            virtual = self.environment.get("VIRTUAL_ENV")
            virtual_python = row["tool"] == "python" and virtual and first.is_relative_to(Path(virtual))
            if virtual_python:
                self.add("path", "local", "virtual_environment", command, "当前虚拟环境接管 Python", path=self.display(first))
            elif first != shim and first.resolve() != Path(expected).resolve():
                self.add("path", "drift", "command_shadowed", command, "PATH 中优先命令与 mise 选择不一致",
                         actual=self.display(first), expected=self.display(expected))
            else:
                self.add("path", "ok", "command_selected", command, "命令来源与 mise 选择一致", path=self.display(first))
            other_paths = [path for path in found[1:] if path != shim and not path.is_relative_to(self.data)
                           and path.resolve() != Path(expected).resolve()]
            alternatives = [self.display(path) for path in other_paths]
            if alternatives:
                kind = "local" if all(str(path).startswith(("/usr/bin/", "/bin/", "/Applications/Xcode.app/"))
                                       for path in other_paths) else "candidate"
                self.add("path", kind, "alternative_commands", command, "PATH 中另有同名命令；不等同于重复管理",
                         items=alternatives, count=len(alternatives))
        flutter = self.home / "development/flutter/bin/flutter"
        if "flutter" not in requested and flutter.is_file() and str(flutter.parent) in declared:
            self.add("path", "local", "retained_flutter", "flutter", "共享 PATH 明确保留原安装，版本尚未由 mise 管理",
                     path=self.display(flutter))

    def collect_config(self):
        data = self.query_json(["mise", "bootstrap", "dotfiles", "status", "--json"])
        if (not isinstance(data, dict) or not isinstance(data.get("files"), list) or
                not isinstance(data.get("edits"), list)):
            raise AuditError("配置映射状态结构无法识别")
        targets = []
        for section, field in (("files", "target"), ("edits", "path")):
            for item in data[section]:
                if not isinstance(item, dict) or not isinstance(item.get(field), str):
                    raise AuditError("配置映射缺少目标路径")
                target, state = item[field], item.get("state")
                if not isinstance(state, str):
                    raise AuditError("配置映射状态无法识别")
                targets.append(self.expand(target))
                if state in {"applied", "absent"}:
                    kind = "ok"
                elif state in {"missing", "source_missing", "differs", "would_remove"}:
                    kind = "drift"
                elif state in {"tracked", "not_on_this_machine"}:
                    kind = "local"
                else:
                    raise AuditError("配置映射出现未知状态；不能判定同步完成")
                self.add("config", kind, "dotfile_state", target, f"映射状态：{state}")
        orphaned = data.get("orphaned", [])
        if not isinstance(orphaned, list):
            raise AuditError("已取消部署的配置列表无法识别")
        for item in orphaned:
            if not isinstance(item, dict) or not isinstance(item.get("target", item.get("path")), str):
                raise AuditError("已取消部署的配置路径无法识别")
            self.add("config", "candidate", "orphaned_mapping", item.get("target", item.get("path")), "以前部署过的文件不再由当前映射管理")
        for relative in CONFIG_CANDIDATES:
            path = self.home / relative
            if path.exists() and not any(path == target or path.is_relative_to(target) for target in targets):
                if path.is_dir() and not any(path.iterdir()):
                    continue
                self.add("config", "candidate", "unmanaged_config", path, "已存在，但不在当前配置映射中；仅列路径")
        for path in (self.home / ".config/mise/secrets.env", self.home / ".zshrc.local", self.root / "mise.local.toml"):
            if path.exists():
                self.add("config", "local", "private_local", path, "按约定保留在本机，不读取内容")

    def collect_section(self, section):
        try:
            getattr(self, "collect_" + section)()
        except (AuditError, OSError, ValueError, RuntimeError) as error:
            message = str(error) if isinstance(error, AuditError) else "文件或状态无法读取；该项尚未核对"
            self.add(section, "error", "collector_failed", section, message)

    def execute(self, selected=SECTIONS):
        with ThreadPoolExecutor(max_workers=4) as pool:
            list(pool.map(self.collect_section, (section for section in SECTIONS if section in selected)))
        sections = {name: self.sections[name] for name in SECTIONS if name in selected}
        summary = dict.fromkeys(KINDS, 0)
        summary.update(Counter(item["kind"] for items in sections.values() for item in items))
        report = {"schema_version": 1, "context": {"root": str(self.root),
                  "path_source": "caller" if "SYSTEM_SYNC_AUDIT_CALLER_PATH" in self.environment else "process",
                  "path_entries": len(self.caller_path.split(":"))}, "summary": summary, "sections": sections}
        return redact(report, self.environment)


def use_color(mode, environment=None, stream=None):
    if mode != "auto":
        return mode == "always"
    environment = os.environ if environment is None else environment
    stream = sys.stdout if stream is None else stream
    return ("NO_COLOR" not in environment and environment.get("TERM", "").lower() != "dumb"
            and stream.isatty())


def render(report, color=False):
    def style(kind, text):
        return COLORS[kind] + text + "\033[0m" if color and kind in COLORS else text

    lines = ["只读审计：tools / packages / PATH / config",
             "PATH 来源：" + ("任务调整前的调用环境" if report["context"]["path_source"] == "caller" else "当前进程环境")]
    for section, items in report["sections"].items():
        lines.append(f"\n[{section}]")
        for finding in items:
            kind = finding["kind"]
            details = [f"  {MARKERS[kind]} {KINDS[kind]} · {finding['item']}：{finding['message']}"]
            for field in ("source", "path", "actual", "expected"):
                if finding.get(field):
                    details.append(f"    {field}: {finding[field]}")
            for item in finding.get("items", [])[:8]:
                details.append(f"    - {item}")
            if len(finding.get("items", [])) > 8:
                details.append("    更多条目见 --json 输出")
            lines.append(style(kind, "\n".join(details)))
    lines.append("\n汇总（报告条目数）：" + "，".join(
        style(kind, f"{MARKERS[kind]} {label} {report['summary'][kind]}") if report["summary"][kind]
        else f"{MARKERS[kind]} {label} 0" for kind, label in KINDS.items()))
    lines.append("候选项需要人工核对意图；本任务不安装、卸载或改写声明。")
    return "\n".join(lines)


def exit_code(report, strict=False):
    return 2 if report["summary"]["error"] else (1 if strict and report["summary"]["drift"] else 0)


def main(argv=None):
    parser = argparse.ArgumentParser(description="只读汇总本机与仓库的 tools、packages、PATH 和配置差异。")
    parser.add_argument("--json", action="store_true", help="输出仅包含状态与路径的 JSON 报告")
    parser.add_argument("--strict", action="store_true", help="存在漂移时也返回非零；候选和本机保留项不算失败")
    parser.add_argument("--only", action="append", choices=SECTIONS, help="只检查指定类别，可重复")
    parser.add_argument("--color", choices=("auto", "always", "never"), default="auto",
                        help="文字报告颜色：auto 自动识别终端（默认），always 强制，never 关闭；JSON 始终无颜色")
    args = parser.parse_args(argv)
    report = Audit().execute(args.only or SECTIONS)
    print(json.dumps(report, ensure_ascii=False, indent=2) if args.json else render(report, color=use_color(args.color)))
    return exit_code(report, args.strict)


if __name__ == "__main__":
    sys.exit(main())
