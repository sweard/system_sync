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


ROOT = Path(__file__).resolve().parents[1]
MISE = shutil.which("mise")


def workflow_test_commands():
    workflow = (ROOT / ".github/workflows/check.yml").read_text()
    step = re.search(r"(?m)^      - name: Run isolated tests\n((?:^        .*\n|^          .*\n|^\n)+)", workflow)
    if not step or "run: |\n" not in step[1]:
        raise AssertionError("CI test step must expose its shell commands")
    return textwrap.dedent(step[1].split("run: |\n", 1)[1]).strip()


@unittest.skipUnless(MISE and sys.platform in ("darwin", "linux"), "mise or supported OS unavailable")
class CleanCIEnvironmentTest(unittest.TestCase):
    def test_workflow_selects_installed_go_without_global_configuration(self):
        compiler = subprocess.run([MISE, "where", "go@1.27.3"], cwd=ROOT,
                                  capture_output=True, text=True, timeout=15)
        self.assertEqual(compiler.returncode, 0, compiler.stderr)
        installed = Path(compiler.stdout.strip()).resolve()
        self.assertTrue((installed / "bin/go").is_file())
        commands = workflow_test_commands()
        for inherited in ("no-go", "managed-go-on-path", "old-system-go"):
            with self.subTest(inherited=inherited), tempfile.TemporaryDirectory(prefix="system-sync-ci-go-") as temporary:
                base = Path(temporary).resolve()
                home, project, fallback = (base / name for name in ("home", "project", "fallback"))
                for directory in (home, project, fallback):
                    directory.mkdir()
                tool = base / "data/installs/go/1.27.3"
                tool.parent.mkdir(parents=True)
                tool.symlink_to(installed, target_is_directory=True)
                # Preserve the real project's task/environment settings; replace
                # only the expensive check body with the build's Go preflight.
                source = (ROOT / "mise.toml").read_text()
                source, count = re.subn(r"(?ms)^\[tasks\.check\]\n.*?(?=^\[)",
                                       '[tasks.check]\nrun = "command -v go; test $(go env GOVERSION) = go1.27.3; go env GOVERSION"\n\n',
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
                self.assertEqual(lines[-1], "go1.27.3")
                self.assertEqual(Path(lines[0]).resolve(), (installed / "bin/go").resolve())
                self.assertEqual((project / "mise.toml").read_bytes(), before)
                self.assertFalse((base / "config/config.toml").exists(), "test selected Go by writing global configuration")

    def test_build_errors_recommend_the_matching_task_and_explicit_tool(self):
        with tempfile.TemporaryDirectory(prefix="system-sync-ci-build-errors-") as temporary:
            base = Path(temporary).resolve()
            scripts = base / "helper/scripts"
            scripts.mkdir(parents=True)
            (base / "scripts").mkdir()
            for name in ("build.sh", "common.sh"):
                shutil.copy2(ROOT / "helper/scripts" / name, scripts / name)
            shutil.copy2(ROOT / "scripts/task-output.sh", base / "scripts/task-output.sh")
            tools = base / "bin"
            tools.mkdir()
            for name in ("bash", "dirname"):
                (tools / name).symlink_to(Path(shutil.which(name)).resolve())
            env = {"HOME": str(base), "PATH": str(tools)}
            for state in ("missing", "wrong-version", "version-query-failed"):
                if state != "missing":
                    go = tools / "go"
                    go.write_text('#!/bin/sh\n' + ('exit 17\n' if state == "version-query-failed" else 'printf "go1.26.0\\n"\n'))
                    go.chmod(0o755)
                for mode, task in (("--native", "helper-build"), ("--dist", "helper-dist"), ("--bundle", "helper-bundle")):
                    with self.subTest(state=state, mode=mode):
                        result = subprocess.run([str(scripts / "build.sh"), mode], cwd=base, env=env,
                                                capture_output=True, text=True, timeout=10)
                        self.assertNotEqual(result.returncode, 0)
                        self.assertIn("mise install go@1.27.3", result.stderr)
                        self.assertIn(shlex.join(["mise", "run", "--tool", "go@1.27.3", task]), result.stderr)
                        self.assertNotIn("mise exec", result.stderr)
                        expected = {"missing": "当前 PATH 中没有 go", "wrong-version": "当前为 go1.26.0",
                                    "version-query-failed": "无法读取当前 Go 版本"}[state]
                        self.assertIn(expected, result.stderr)
                        self.assertFalse((base / ".system-sync").exists())
