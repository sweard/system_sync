"""Run the workflow's task invocation without the developer's global config."""

import os
from pathlib import Path
import re
import shlex
import shutil
import subprocess
import sys
import tempfile
import textwrap
import unittest

from helper_fixture import GO_VERSION


ROOT = Path(__file__).resolve().parents[1]
MISE = shutil.which("mise")


def workflow_commands(name):
    workflow = (ROOT / ".github/workflows/check.yml").read_text()
    step = re.search(r"(?m)^      - name: " + re.escape(name) + r"\n((?:^        .*\n|^          .*\n|^\n)+)", workflow)
    if not step or "run: |\n" not in step[1]:
        raise AssertionError("CI test step must expose its shell commands")
    return textwrap.dedent(step[1].split("run: |\n", 1)[1]).strip()


def prepare_toolchain_fixture(project):
    for name in ("helper/GO_VERSION", "helper/scripts/go-version.sh", "helper/scripts/common.sh",
                 "helper/scripts/build.sh", "helper/scripts/test.sh", "scripts/task-output.sh"):
        target = project / name
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(ROOT / name, target)


@unittest.skipUnless(MISE and sys.platform in ("darwin", "linux"), "mise or supported OS unavailable")
class CleanCIEnvironmentTest(unittest.TestCase):
    def test_workflow_selects_installed_go_without_global_configuration(self):
        compiler = subprocess.run([MISE, "where", "go@" + GO_VERSION], cwd=ROOT,
                                  capture_output=True, text=True, timeout=15)
        self.assertEqual(compiler.returncode, 0, compiler.stderr)
        installed = Path(compiler.stdout.strip()).resolve()
        self.assertTrue((installed / "bin/go").is_file())
        commands = workflow_commands("Run isolated tests")
        for inherited in ("no-go", "managed-go-on-path", "old-system-go"):
            with self.subTest(inherited=inherited), tempfile.TemporaryDirectory(prefix="system-sync-ci-go-") as temporary:
                base = Path(temporary).resolve()
                home, project, fallback = (base / name for name in ("home", "project", "fallback"))
                for directory in (home, project, fallback):
                    directory.mkdir()
                prepare_toolchain_fixture(project)
                tool = base / "data/installs/go" / GO_VERSION
                tool.parent.mkdir(parents=True)
                tool.symlink_to(installed, target_is_directory=True)
                # Preserve the real project's task/environment settings; replace
                # only the expensive check body with the build's Go preflight.
                source = (ROOT / "mise.toml").read_text()
                source, count = re.subn(r"(?ms)^\[tasks\.check\]\n.*?(?=^\[)",
                                       f'[tasks.check]\nrun = "command -v go; test $(go env GOVERSION) = go{GO_VERSION}; go env GOVERSION"\n\n',
                                       source, count=1)
                self.assertEqual(count, 1)
                (project / "mise.toml").write_text(source)
                paths = [str(Path(MISE).parent), "/usr/bin", "/bin", "/usr/sbin", "/sbin"]
                if inherited == "managed-go-on-path":
                    paths.insert(0, str(tool / "bin"))
                elif inherited == "old-system-go":
                    old_go = fallback / "go"
                    old_go.write_text('#!/bin/sh\nprintf "go1.26.0\\n"\n')
                    old_go.chmod(0o755)
                    paths.insert(0, str(fallback))
                env = {
                    "HOME": str(home), "PATH": os.pathsep.join(paths), "NO_COLOR": "1", "TERM": "dumb",
                    "MISE_CONFIG_DIR": str(base / "config"), "MISE_SYSTEM_CONFIG_DIR": str(base / "system"),
                    "MISE_DATA_DIR": str(base / "data"), "MISE_STATE_DIR": str(base / "state"),
                    "MISE_CACHE_DIR": str(base / "cache"), "MISE_CEILING_PATHS": str(base),
                    "MISE_TRUSTED_CONFIG_PATHS": str(base), "MISE_AUTO_INSTALL": "false",
                    "MISE_TASK_RUN_AUTO_INSTALL": "false", "MISE_OFFLINE": "1", "GOTOOLCHAIN": "local",
                    "GOTELEMETRY": "off", "GOENV": "off", "GOCACHE": str(base / "go-cache"),
                }
                before = (project / "mise.toml").read_bytes()
                result = subprocess.run(["/bin/bash", "--noprofile", "--norc", "-e", "-o", "pipefail", "-c", commands],
                                        cwd=project, env=env, capture_output=True, text=True, timeout=30)
                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                lines = result.stdout.strip().splitlines()
                self.assertEqual(lines[-1], "go" + GO_VERSION)
                self.assertEqual(Path(lines[0]).resolve(), (installed / "bin/go").resolve())
                self.assertEqual((project / "mise.toml").read_bytes(), before)
                self.assertFalse((base / "config/config.toml").exists(), "test selected Go by writing global configuration")

    def test_build_and_test_errors_recommend_the_matching_task_and_explicit_tool(self):
        with tempfile.TemporaryDirectory(prefix="system-sync-ci-build-errors-") as temporary:
            base = Path(temporary).resolve()
            prepare_toolchain_fixture(base)
            scripts = base / "helper/scripts"
            tools = base / "bin"
            tools.mkdir()
            for name in ("bash", "dirname", "cat"):
                (tools / name).symlink_to(Path("/bin/bash") if name == "bash" else Path(shutil.which(name)).resolve())
            env = {"HOME": str(base), "PATH": str(tools), "LC_ALL": "C"}
            for state in ("missing", "wrong-version", "version-query-failed"):
                if state != "missing":
                    go = tools / "go"
                    go.write_text('#!/bin/sh\n' + ('exit 17\n' if state == "version-query-failed" else 'printf "go1.26.0\\n"\n'))
                    go.chmod(0o755)
                for script, args, task in (("build.sh", ("--native",), "helper-build"),
                                          ("build.sh", ("--dist",), "helper-dist"),
                                          ("build.sh", ("--bundle",), "helper-bundle"),
                                          ("test.sh", (), "check")):
                    with self.subTest(state=state, script=script, args=args):
                        result = subprocess.run([str(scripts / script), *args], cwd=base, env=env,
                                                capture_output=True, text=True, timeout=10)
                        self.assertNotEqual(result.returncode, 0)
                        self.assertIn("mise install go@" + GO_VERSION, result.stderr)
                        self.assertIn(shlex.join(["mise", "run", "--tool", "go@" + GO_VERSION, task]), result.stderr)
                        self.assertNotIn("mise exec", result.stderr)
                        expected = {"missing": "当前 PATH 中没有 go", "wrong-version": "当前为 go1.26.0",
                                    "version-query-failed": "无法读取当前 Go 版本"}[state]
                        self.assertIn(expected, result.stderr)
                        self.assertFalse((base / ".system-sync").exists())


class PinnedGoVersionTest(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory(prefix="system-sync-go-pin-")
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name).resolve() / "repo with spaces 中文"
        self.root.mkdir()
        prepare_toolchain_fixture(self.root)
        self.pin = self.root / "helper/GO_VERSION"
        self.tools = self.root / "bin"
        self.tools.mkdir()
        for name in ("bash", "dirname", "cat"):
            (self.tools / name).symlink_to(Path("/bin/bash") if name == "bash" else Path(shutil.which(name)).resolve())
        self.env = {"HOME": str(self.root), "PATH": str(self.tools), "GOTOOLCHAIN": "local",
                    "GO_VERSION": "99.0.0", "REQUIRED_GO_VERSION": "99.0.0", "LC_ALL": "C"}

    def command(self, script, *args):
        return subprocess.run([str(self.root / "helper/scripts" / script), *args], cwd=self.root.parent,
                              env=self.env, capture_output=True, text=True, timeout=10)

    def test_reader_and_build_help_follow_only_the_file_without_a_compiler(self):
        for version in (GO_VERSION, "9.8.7"):
            with self.subTest(version=version):
                self.pin.write_text(version + "\n")
                result = self.command("go-version.sh")
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(result.stdout, version + "\n")
                self.assertEqual(result.stderr, "")
                result = self.command("build.sh", "--help")
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertIn("需要 Go " + version, result.stdout)
        self.assertFalse((self.root / ".system-sync").exists())

    def test_invalid_or_missing_pin_stops_before_querying_or_installing_tools(self):
        marker = self.root / "queried"
        for name in ("go", "mise"):
            command = self.tools / name
            command.write_text('#!/bin/sh\nprintf unsafe > ' + shlex.quote(str(marker)) + '\nexit 99\n')
            command.chmod(0o755)
        for value in (None, "", "latest\n", "1.2\n", "go1.2.3\n", "1.2.3\n9.8.7\n", "1.2.3;id\n"):
            with self.subTest(pin=value):
                if value is None:
                    self.pin.unlink()
                else:
                    self.pin.write_text(value)
                for script in ("go-version.sh", "build.sh", "test.sh"):
                    result = self.command(script)
                    self.assertNotEqual(result.returncode, 0, result.stdout)
                    self.assertEqual(result.stdout, "")
                    self.assertIn("helper/GO_VERSION", result.stderr)
                for step in ("Prepare pinned Go compiler for explicit builds", "Run isolated tests",
                             "Build four platform archives from public source only"):
                    result = subprocess.run([str(self.tools / "bash"), "-e", "-o", "pipefail", "-c", workflow_commands(step)],
                                            cwd=self.root, env=self.env, capture_output=True, text=True, timeout=10)
                    self.assertNotEqual(result.returncode, 0, result.stdout)
                    self.assertIn("helper/GO_VERSION", result.stderr)
                self.assertFalse(marker.exists(), "invalid pin reached native tools")
                self.assertFalse((self.root / ".system-sync").exists())

    def test_changed_pin_controls_build_and_test_mismatch_diagnostics(self):
        self.pin.write_text("9.8.7\n")
        go = self.tools / "go"
        go.write_text('#!/bin/sh\nprintf "go0.0.0\\n"\n')
        go.chmod(0o755)
        for script, args, task in (("build.sh", ("--native",), "helper-build"),
                                   ("build.sh", ("--dist",), "helper-dist"),
                                   ("build.sh", ("--bundle",), "helper-bundle"),
                                   ("test.sh", (), "check")):
            with self.subTest(script=script, args=args):
                result = self.command(script, *args)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn("Go 9.8.7，当前为 go0.0.0", result.stderr)
                self.assertIn("mise install go@9.8.7", result.stderr)
                self.assertIn("mise run --tool go@9.8.7 " + task, result.stderr)
                self.assertFalse((self.root / ".system-sync").exists())

    def test_workflow_steps_read_changed_pin_for_every_go_invocation(self):
        self.pin.write_text("9.8.7\n")
        log = self.root / "mise.log"
        mise = self.tools / "mise"
        mise.write_text('#!/bin/sh\nprintf "%s\\n" "$*" >> ' + shlex.quote(str(log)) + '\n')
        mise.chmod(0o755)
        for step, expected in (
            ("Prepare pinned Go compiler for explicit builds", ["trust --all", "install go@9.8.7", "exec go@9.8.7 -- go version"]),
            ("Run isolated tests", ["run --tool go@9.8.7 check"]),
            ("Build four platform archives from public source only", ["trust --all", "install go@9.8.7",
                                                                    "exec go@9.8.7 -- ./helper/scripts/build.sh --dist"]),
        ):
            with self.subTest(step=step):
                log.unlink(missing_ok=True)
                result = subprocess.run([str(self.tools / "bash"), "-e", "-o", "pipefail", "-c", workflow_commands(step)],
                                        cwd=self.root, env=self.env, capture_output=True, text=True, timeout=10)
                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                self.assertEqual(log.read_text().splitlines(), expected)
