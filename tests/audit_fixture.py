"""Synthetic native responses for the real Go audit; no audit rules live here."""

import json
from pathlib import Path
import shlex
import subprocess
import sys
import tempfile
import unittest

from helper_fixture import prepare_helper

SECTIONS = ("tools", "packages", "path", "config")
PACKAGE_REQUIRED = (
    {"Brewfile formula 根声明", "Brewfile cask 声明", "Brewfile tap 声明",
     "尚未安装的 formula", "尚未安装的 cask", "尚未添加的 tap",
     "Homebrew 可识别的未声明 requested formula", "未声明的已安装 cask", "未声明的 tap",
     "受保护但未在 Brewfile 声明的项目"},
    {"mise 最终生效声明", "声明但尚未以同名具体包安装", "当前显式安装包中未声明、但受保护而保留",
     "apply 时会从 dependency 提升为 explicit 的声明/保护包", "apply 时会从 explicit 降级为 dependency 的未声明包"},
    {"mise 最终生效声明", "声明但尚未以同名具体包安装", "当前手工根包中未声明、但受保护而保留",
     "apply 时会提升为手工根包的声明/保护包", "apply 时会降级为依赖的未声明手工根包"},
    {"声明的根包", "声明但尚未安装", "未声明但受保护的 user-installed 包", "拟直接移除的未声明 user-installed 包"},
)


def package_status(required, entries=None):
    entries = entries or {}
    return "\n".join(f"{title}（{len(entries.get(title, []))}）\n" +
                     ("".join("  - " + item + "\n" for item in entries[title])
                      if entries.get(title) else "  （无）\n") for title in sorted(required))


BACKEND = r'''
import json, os, sys
from pathlib import Path
state = json.loads(Path(sys.argv[1]).read_text())
program, args = sys.argv[2], sys.argv[3:]
command = (["/bin/bash", "--noprofile"] if program == "policy" else
           [state["status_script"], *args] if program == "packages" else [program, *args])
with Path(state["log"]).open("a") as stream:
    stream.write(json.dumps({"command": command, "environment": dict(os.environ)}) + "\n")
if command in state["failures"]:
    print("private-query-output")
    print("private-query-diagnostic", file=sys.stderr)
    sys.exit(17)
if program == "policy": print(state["policy"], end="")
elif program == "packages": print(state["packages"], end="")
elif args == ["ls", "--current", "--json"]: print(json.dumps(state["tools"]))
elif args[:1] == ["which"]: print(state["paths"][args[-1]])
elif args == ["bootstrap", "dotfiles", "status", "--json"]: print(json.dumps(state["config"]))
else: sys.exit(91)
'''


class AuditResponses:
    def __init__(self, fixture):
        self.fixture = fixture
        self.tools, self.paths = {}, {}
        self.config = {"files": [], "edits": []}
        self.packages = package_status(PACKAGE_REQUIRED[0])
        self.policy = "/usr/bin:/bin:/usr/sbin:/sbin"
        self.failures = set()

    @property
    def calls(self):
        if not self.fixture.log.exists():
            return []
        return [(row["command"], row["environment"]) for row in
                (json.loads(line) for line in self.fixture.log.read_text().splitlines())]


class AuditFixture(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory(prefix="system-sync-audit-")
        self.addCleanup(temporary.cleanup)
        self.base = Path(temporary.name).resolve()
        self.root, self.home = self.base / "project", self.base / "home"
        self.root.mkdir()
        self.home.mkdir()
        self.binary = prepare_helper(self.root)
        self.log = self.base / "queries.jsonl"
        self.runner = AuditResponses(self)
        self.environment = {"HOME": str(self.home), "PATH": "/usr/bin:/bin", "PRIVATE_API_TOKEN": "fixture-private-token"}
        # Keep expected reports independent of binaries installed on CI runners.
        self.system_paths = {}
        for name in ("/usr/bin", "/bin", "/usr/sbin", "/sbin"):
            path = self.base / "system" / name.strip("/").replace("/", "-")
            path.mkdir(parents=True)
            self.system_paths[name] = str(path)

    def executable(self, path):
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text("#!/bin/sh\nexit 99\n")
        path.chmod(0o755)
        return path

    def tool(self, name="node", command="node", installed=True):
        directory = self.home / ".local/share/mise/installs" / name / "1.0.0"
        executable = self.executable(directory / "bin" / command)
        self.runner.tools[name] = [{"version": "1.0.0", "installed": installed, "install_path": str(directory),
                                    "source": {"type": "mise.toml", "path": str(self.root / "mise.toml")}}]
        self.runner.paths[command] = str(executable)
        return executable

    def prepare(self):
        def isolated_path(value):
            return ":".join(self.system_paths.get(part, part) for part in value.split(":"))
        commands = self.root / "query-bin"
        commands.mkdir(exist_ok=True)
        state, backend = commands / "state.json", commands / "backend.py"
        status = self.root / "scripts/system-sync.sh"
        state.write_text(json.dumps({"tools": self.runner.tools, "paths": self.runner.paths,
                                     "config": self.runner.config, "packages": self.runner.packages,
                                     "policy": isolated_path(self.runner.policy), "log": str(self.log),
                                     "status_script": str(status), "failures": [list(row) for row in self.runner.failures]}))
        backend.write_text(BACKEND)
        command = " ".join(shlex.quote(str(part)) for part in (sys.executable, "-I", "-B", backend, state))
        for path, program in ((commands / "mise", "mise"), (status, "packages")):
            path.write_text(f'#!/bin/sh\nexec {command} {program} "$@"\n')
            path.chmod(0o755)
        source = self.root / "dotfiles/common/shell/path.sh"
        source.parent.mkdir(parents=True, exist_ok=True)
        source.write_text(f'_system_sync_user_path() {{ PATH="$({command} policy)"; export PATH; }}\n')
        environment = dict(self.environment, PATH=str(commands) + ":" + isolated_path(self.environment["PATH"]))
        if "SYSTEM_SYNC_AUDIT_CALLER_PATH" in environment:
            environment["SYSTEM_SYNC_AUDIT_CALLER_PATH"] = isolated_path(environment["SYSTEM_SYNC_AUDIT_CALLER_PATH"])
        return environment

    def invoke(self, selected=SECTIONS, strict=False, text=False):
        command = [str(self.binary), "--root", str(self.root), "audit"]
        command += ["--color", "never"] if text else ["--json"]
        for section in selected:
            command += ["--only", section]
        if strict:
            command.append("--strict")
        result = subprocess.run(command, cwd=self.base, env=self.prepare(), stdin=subprocess.DEVNULL,
                                capture_output=True, text=True, timeout=30)
        self.assertIn(result.returncode, (0, 1, 2), result.stdout + result.stderr)
        return result

    def audit(self, selected=SECTIONS, strict=False, text=False):
        result = self.invoke(selected, strict, text)
        self.exit_code = result.returncode
        return result.stdout if text else json.loads(result.stdout)

    def normalize(self, value):
        if isinstance(value, dict):
            return {key: self.normalize(item) for key, item in value.items()}
        if isinstance(value, list):
            return [self.normalize(item) for item in value]
        if isinstance(value, str):
            return value.replace(str(self.base), "<fixture>")
        return value
