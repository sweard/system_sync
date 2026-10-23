"""Observe feedback before blocked work finishes, using isolated native CLIs."""

import json
import os
from pathlib import Path
import select
import shlex
import shutil
import signal
import subprocess
import sys
import tempfile
import time
import unittest

from helper_fixture import GO_VERSION, prepare_go_config, prepare_helper


ROOT = Path(__file__).resolve().parents[1]
BACKEND = r'''
import json, os, sys, time
from pathlib import Path
program, args = sys.argv[1], sys.argv[2:]
if program == "uname":
    print("Darwin")
    sys.exit(0)
if program == "mise" and args[:3] == ["config", "get", "--file"]:
    import tomllib
    print(tomllib.loads(Path(args[3]).read_text())["tools"]["go"])
    sys.exit(0)
if program == "mise" and args == ["--version"]:
    if os.environ.get("FEEDBACK_FAIL_VERSION"):
        print("private-feedback-version", file=sys.stderr)
        sys.exit(19)
    if os.environ.get("FEEDBACK_BAD_VERSION"):
        print("private-feedback-invalid-version")
        sys.exit(0)
    print("2026.10.15")
    sys.exit(0)
if program == "go" and args[:1] == ["env"]:
    print({"GOVERSION":os.environ["FEEDBACK_GO_VERSION"], "GOHOSTOS":"darwin", "GOHOSTARCH":"arm64"}[args[1]])
    sys.exit(0)
if program == "git":
    if args[-2:] == ["--short=12", "HEAD"]: print("0123456789ab")
    sys.exit(0)
Path(os.environ["FEEDBACK_ENTERED"]).touch()
deadline = time.monotonic() + 8
while not Path(os.environ["FEEDBACK_RELEASE"]).exists():
    if time.monotonic() > deadline: sys.exit(96)
    time.sleep(0.01)
if os.environ.get("FEEDBACK_FAIL") or program == "go":
    print("private-feedback-output")
    print("private-feedback-diagnostic", file=sys.stderr)
    sys.exit(19 if program != "go" else 42)
if program == "mise":
    if args[:3] == ["bootstrap", "dotfiles", "status"]:
        print(json.dumps({"files":[], "edits":[]}))
    elif args[:5] == ["dot", "--local", "history", "ls", "--json"]:
        print("[]")
    elif args[:1] == ["ls"]: print("{}")
    else: sys.exit(91)
elif program == "brew":
    if args[:2] == ["list", "--formula"]: print("git")
    elif args[:2] == ["list", "--cask"]: pass
    elif args[:2] == ["bundle", "dump"]:
        target = next(item.split("=", 1)[1] for item in args if item.startswith("--file="))
        Path(target).write_text('brew "git"\n')
    else: sys.exit(92)
elif program == "code" and args == ["--list-extensions", "--show-versions"]:
    print("example.shared@1.2.3")
else: sys.exit(93)
'''


class TaskFeedbackTest(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory(prefix="system-sync-feedback-")
        self.addCleanup(temporary.cleanup)
        self.base = Path(temporary.name).resolve()
        self.root, self.home, self.bin = (self.base / name for name in ("repo 中文", "home", "bin"))
        for directory in (self.root, self.home, self.bin):
            directory.mkdir()
        prepare_helper(self.root)
        prepare_go_config(self.root)
        backend = self.base / "native.py"
        backend.write_text(BACKEND)
        for name in ("mise", "uname", "brew", "code", "go", "git"):
            path = self.bin / name
            path.write_text(f'#!/bin/sh\nexec {shlex.quote(sys.executable)} -I -B {shlex.quote(str(backend))} {name} "$@"\n')
            path.chmod(0o755)
        packages = self.root / "packages/macos"
        packages.mkdir(parents=True)
        (packages / "Brewfile").write_text('brew "git"\n')
        (packages / "protected-packages.txt").write_text("formula:git\n")
        extensions = self.root / "dotfiles/common/vscode/extensions.toml"
        extensions.parent.mkdir(parents=True)
        extensions.write_text('[common]\n"example.shared"="1.2.3"\n')
        (self.root / "mise.toml").write_text("[dotfiles]\n")
        self.release, self.entered = self.base / "release", self.base / "entered"
        self.env = dict(os.environ, HOME=str(self.home), PATH=str(self.bin) + ":/usr/bin:/bin:/usr/sbin:/sbin",
                        MISE_CONFIG_DIR=str(self.home / ".config/mise"),
                        MISE_STATE_DIR=str(self.home / "state"), XDG_STATE_HOME=str(self.home / "state"),
                        FEEDBACK_RELEASE=str(self.release), FEEDBACK_ENTERED=str(self.entered),
                        FEEDBACK_GO_VERSION="go" + GO_VERSION)

    def blocked(self, command, label):
        self.release.unlink(missing_ok=True)
        self.entered.unlink(missing_ok=True)
        process = subprocess.Popen(command, env=self.env, cwd=self.root, stdin=subprocess.DEVNULL,
                                   stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        prefix = bytearray()
        deadline = time.monotonic() + 6
        try:
            while time.monotonic() < deadline:
                ready, _, _ = select.select([process.stderr], [], [], 0.02)
                if ready:
                    chunk = os.read(process.stderr.fileno(), 65536)
                    if not chunk:
                        self.fail("task ended before feedback: " + prefix.decode(errors="replace"))
                    prefix.extend(chunk)
                if label in prefix.decode(errors="replace") and self.entered.exists():
                    self.assertIsNone(process.poll(), prefix.decode(errors="replace"))
                    self.release.touch()
                    stdout, stderr = process.communicate(timeout=8)
                    return subprocess.CompletedProcess(command, process.returncode, stdout.decode(), (prefix + stderr).decode())
            self.fail("no feedback while command was blocked: " + prefix.decode(errors="replace"))
        finally:
            self.release.touch()
            if process.poll() is None:
                process.kill()
                process.wait()
            process.stdout.close()
            process.stderr.close()

    def cases(self):
        code = str(self.bin / "code")
        return (
            (("config", "status", "--json"), "查询受管配置状态"),
            (("config-history", "list", "--json"), "查询本机配置历史"),
            (("config", "diff"), "读取受管配置状态与差异"),
            (("config-detach", "--dry-run"), "读取配置迁出状态"),
            (("vscode", "status", "--code", code), "查询 VS Code 已安装扩展"),
            (("vscode", "status", "--capture", "--code", code), "采集 VS Code 扩展与本机版本"),
            (("package-capture",), "采集本机软件清单与来源"),
            (("machine-init", "--only", "packages"), "读取机器接入计划"),
            (("audit", "--only", "tools"), "只读审计：tools"),
        )

    def test_readonly_tasks_report_progress_before_native_queries_finish(self):
        for arguments, label in self.cases():
            with self.subTest(task=arguments):
                result = self.blocked([str(self.root / "helper/scripts/run.sh"), *arguments], label)
                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                self.assertNotIn("[~] " + label, result.stdout)
                if "--json" in arguments:
                    json.loads(result.stdout)
        self.assertFalse((self.root / ".system-sync/history").exists())
        self.assertFalse((self.root / ".system-sync/capture").exists())

    def test_query_errors_keep_failure_category_and_hide_native_diagnostics(self):
        self.env["FEEDBACK_FAIL"] = "1"
        self.release.touch()
        for arguments, _ in self.cases():
            with self.subTest(task=arguments):
                result = subprocess.run([str(self.root / "helper/scripts/run.sh"), *arguments], cwd=self.root,
                                        env=self.env, capture_output=True, text=True, timeout=8)
                self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
                self.assertIn("19", result.stdout + result.stderr)
                self.assertNotIn("private-feedback", result.stdout + result.stderr)

    def test_machine_readable_previews_stay_parseable_and_quiet(self):
        self.release.touch()
        for arguments in (("audit", "--only", "tools", "--json"), ("package-capture", "--json"),
                          ("machine-init", "--only", "packages", "--json")):
            result = subprocess.run([str(self.root / "helper/scripts/run.sh"), *arguments], cwd=self.root,
                                    env=self.env, capture_output=True, text=True, timeout=8)
            self.assertEqual(result.returncode, 0, result.stderr)
            json.loads(result.stdout)
            self.assertNotIn("[~]", result.stderr)

    def test_failed_version_query_is_not_reported_as_an_outdated_version(self):
        for flag, message in (("FEEDBACK_FAIL_VERSION", "退出码 19"), ("FEEDBACK_BAD_VERSION", "版本输出无法识别")):
            with self.subTest(flag=flag):
                environment = dict(self.env, **{flag:"1"})
                result = subprocess.run([str(self.root / "helper/scripts/run.sh"), "package-capture", "--json"], cwd=self.root,
                                        env=environment, capture_output=True, text=True, timeout=8)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn(message, result.stderr)
                self.assertNotIn("请先升级", result.stderr)
                self.assertNotIn("private-feedback", result.stdout + result.stderr)

    def test_native_help_failure_returns_nonzero_and_actionable_diagnostic(self):
        self.env["FEEDBACK_FAIL"] = "1"
        self.release.touch()
        result = subprocess.run([str(self.root / "helper/scripts/run.sh"), "config", "diff", "--help"], cwd=self.root,
                                env=self.env, capture_output=True, text=True, timeout=8)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("退出码 19", result.stderr)
        self.assertIn("mise bootstrap dotfiles diff --help", result.stderr)
        self.assertNotIn("private-feedback", result.stdout + result.stderr)

    def test_silent_build_reports_target_before_completion_and_preserves_exit_code(self):
        shutil.copy2(ROOT / "helper/scripts/build.sh", self.root / "helper/scripts/build.sh")
        result = self.blocked([str(self.root / "helper/scripts/build.sh")], "构建 helper：darwin_arm64")
        self.assertEqual(result.returncode, 42, result.stdout + result.stderr)
        self.assertIn("darwin_arm64，退出码 42", result.stderr)
        self.assertFalse((self.root / "helper/prebuilt").exists())

    def test_shell_heartbeat_does_not_pollute_captured_data_or_leave_open_pipes(self):
        sleep = self.bin / "sleep"
        tick = self.base / "tick"
        sleep.write_text('#!/bin/sh\nif [ "$1" = 10 ] && [ ! -e ' + shlex.quote(str(tick)) + ' ]; then\n'
                         ' touch ' + shlex.quote(str(tick)) + '\n exit 0\nfi\nexec /bin/sleep 30\n')
        sleep.chmod(0o755)
        script = self.base / "operation.sh"
        script.write_text('#!/bin/bash\nset -Eeuo pipefail\nsource ' + shlex.quote(str(self.root / "scripts/task-output.sh")) + '\n'
                          'system_sync_capture "fixture phase" "$1" "$2" /bin/sh -c \'printf private-out; printf private-err >&2; '
                          'touch "$FEEDBACK_ENTERED"; while [ ! -e "$FEEDBACK_RELEASE" ]; do /bin/sleep 0.01; done; exit 7\'\n')
        output, errors = self.base / "stdout-data", self.base / "stderr-data"
        result = self.blocked(["/bin/bash", str(script), str(output), str(errors)], "仍在执行")
        self.assertEqual(result.returncode, 7, result.stderr)
        self.assertEqual(result.stdout, "")
        self.assertEqual(output.read_text(), "private-out")
        self.assertEqual(errors.read_text(), "private-err")
        self.assertNotIn("private-", result.stderr)

    def test_fast_shell_steps_finish_in_strict_mode_without_waiting_for_a_tick(self):
        result = subprocess.run(["/bin/bash", "-c",
                                 'set -Eeuo pipefail; source "$1"; value="$(system_sync_step fast printf native-data)"; printf "%s" "$value"',
                                 "fixture", str(self.root / "scripts/task-output.sh")], cwd=self.root, env=self.env,
                                capture_output=True, text=True, timeout=3)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, "native-data")
        self.assertIn("[~] fast", result.stderr)
        self.assertNotIn("unbound variable", result.stderr)

    def test_ticker_stops_sleep_when_interrupted_immediately_after_launch(self):
        child = self.base / "sleep-process.json"
        paused = self.base / "ticker-paused"
        resume = self.base / "ticker-resume"
        sleeper = self.bin / "sleep"
        sleeper.write_text(f'#!{sys.executable}\nimport json, os, time\n'
                           f'with open({str(child)!r}, "w") as stream:\n'
                           ' json.dump({"pid": os.getpid(), "parent": os.getppid()}, stream)\n'
                           'time.sleep(30)\n')
        sleeper.chmod(0o755)
        # DEBUG stops the ticker at its first command after launching sleep,
        # before it can record $! or enter wait. No scheduler timing is assumed.
        script = r'''set -Eeuo pipefail
set -T
trap 'if [[ "$BASH_SUBSHELL" == 2 && ! -e "$FEEDBACK_TICKER_PAUSED" ]] && [[ -n "$(jobs -pr)" ]]; then
  : > "$FEEDBACK_TICKER_PAUSED"
  while [[ ! -e "$FEEDBACK_TICKER_RESUME" ]]; do /bin/sleep 0.01; done
fi' DEBUG
source "$1"
system_sync_step fixture /bin/sh -c 'touch "$FEEDBACK_ENTERED"; while [ ! -e "$FEEDBACK_RELEASE" ]; do /bin/sleep 0.01; done; printf native-data'
'''
        environment = dict(self.env, FEEDBACK_TICKER_PAUSED=str(paused), FEEDBACK_TICKER_RESUME=str(resume))
        process = subprocess.Popen(["/bin/bash", "-c", script, "fixture", str(self.root / "scripts/task-output.sh")],
                                   cwd=self.root, env=environment, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                   start_new_session=True)
        try:
            deadline = time.monotonic() + 6
            while not (child.exists() and paused.exists() and self.entered.exists()):
                if process.poll() is not None or time.monotonic() >= deadline:
                    self.fail("ticker did not reach the interruption boundary")
                time.sleep(0.01)
            # The file can be visible before its JSON write completes.
            while True:
                try:
                    ticker = json.loads(child.read_text())["parent"]
                    break
                except json.JSONDecodeError:
                    if time.monotonic() >= deadline:
                        raise
                    time.sleep(0.01)
            os.kill(ticker, signal.SIGTERM)
            self.release.touch()
            stdout, stderr = process.communicate(timeout=3)
            self.assertEqual(process.returncode, 0, stderr.decode())
            self.assertEqual(stdout, b"native-data")
            self.assertIn(b"[~] fixture", stderr)
        finally:
            resume.touch()
            self.release.touch()
            try:
                os.killpg(process.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
            process.wait()
            process.stdout.close()
            process.stderr.close()
