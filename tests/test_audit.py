"""Audit the real Go CLI with isolated native responses and explicit assertions."""

import json
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest

from helper_fixture import prepare_helper
from audit_fixture import AuditFixture, PACKAGE_REQUIRED, package_status

ROOT = Path(__file__).resolve().parents[1]
MISE = shutil.which("mise")


class AuditTest(AuditFixture):
    def codes(self, report, section):
        return {item["code"] for item in report["sections"][section]}

    def test_missing_tools_are_reported_without_installing(self):
        self.tool(installed=False)
        report = self.audit(("tools",))
        self.assertEqual(report["summary"]["drift"], 1)
        self.assertIn("tool_missing", self.codes(report, "tools"))
        self.assertEqual([command for command, _ in self.runner.calls], [["mise", "ls", "--current", "--json"]])


    def test_shadowed_commands_and_other_installers_are_candidates(self):
        selected = self.tool()
        shadow = self.executable(self.home / ".local/bin/node")
        self.executable(self.home / ".nvm/versions/node/v0.1.0/bin/node")
        self.environment["PATH"] = f"{shadow.parent}:{selected.parent}:/usr/bin:/bin"
        report = self.audit(("tools", "path"))
        self.assertIn("command_shadowed", self.codes(report, "path"))
        self.assertIn("external_installations", self.codes(report, "tools"))
        self.assertGreater(report["summary"]["candidate"], 0)


    def test_shims_and_rustup_backend_are_not_duplicate_management(self):
        selected = self.tool("rust", "rustc")
        cargo = self.executable(self.home / ".cargo/bin/rustc")
        selected.unlink()
        selected.symlink_to(cargo)
        shim = self.executable(self.home / ".local/share/mise/shims/rustc")
        self.environment["PATH"] = f"{shim.parent}:{cargo.parent}:/usr/bin:/bin"
        self.runner.policy = str(cargo.parent) + ":/usr/bin:/bin:/usr/sbin:/sbin"
        report = self.audit(("path",))
        self.assertIn("command_selected", self.codes(report, "path"))
        self.assertNotIn("command_shadowed", self.codes(report, "path"))
        self.assertNotIn("alternative_commands", self.codes(report, "path"))


    def test_linked_external_installation_is_retained(self):
        original = self.executable(self.home / ".nvm/versions/node/v1.0.0/bin/node")
        directory = self.home / ".local/share/mise/installs/node/1.0.0"
        directory.parent.mkdir(parents=True)
        directory.symlink_to(original.parent.parent)
        self.runner.tools = {"node": [{"version": "1.0.0", "installed": True, "install_path": str(directory)}]}
        report = self.audit(("tools",))
        self.assertIn("linked_install", self.codes(report, "tools"))
        self.assertNotIn("external_installations", self.codes(report, "tools"))


    def test_virtual_environment_is_an_intentional_python_override(self):
        selected = self.tool("python", "python3")
        virtual = self.executable(self.root / ".venv/bin/python3")
        self.environment.update(VIRTUAL_ENV=str(virtual.parent.parent), PATH=f"{virtual.parent}:{selected.parent}:/usr/bin:/bin")
        report = self.audit(("path",))
        self.assertIn("virtual_environment", self.codes(report, "path"))
        self.assertNotIn("command_shadowed", self.codes(report, "path"))


    def test_path_checks_caller_environment_and_filters_private_variables(self):
        selected = self.tool()
        absent = self.home / "absent"
        self.environment["PATH"] = str(selected.parent) + ":/usr/bin:/bin"
        self.environment["SYSTEM_SYNC_AUDIT_CALLER_PATH"] = f"{self.home}::relative:{absent}:{self.home}"
        report = self.audit(("path",))
        codes = self.codes(report, "path")
        self.assertEqual(report["context"]["path_source"], "caller")
        self.assertTrue({"unsafe_path", "missing_directory", "duplicate_path", "command_missing"} <= codes)
        policy_environment = next(environment for command, environment in self.runner.calls if command[1:2] == ["--noprofile"])
        self.assertNotIn("PRIVATE_API_TOKEN", policy_environment)


    def test_expected_paths_and_preserved_flutter_are_reported(self):
        flutter = self.executable(self.home / "development/flutter/bin/flutter")
        self.runner.policy = f"{flutter.parent}:/usr/bin:/bin"
        report = self.audit(("path",))
        self.assertIn("policy_path_missing", self.codes(report, "path"))
        self.assertIn("retained_flutter", self.codes(report, "path"))


    def test_host_injected_paths_are_kept_separate_from_unmanaged_paths(self):
        self.environment["PATH"] = ":".join((
            str(self.home / ".codex/tmp/ephemeral/bin"), "/pkg/env/global/bin",
            str(self.home / "retired-tool/bin")))
        report = self.audit(("path",))
        missing = [item for item in report["sections"]["path"] if item["code"] == "missing_directory"]
        self.assertEqual([item["kind"] for item in missing], ["local", "local", "drift"])


    def test_configs_are_compared_only_on_owned_states_and_whitelisted_paths(self):
        private = self.home / ".config/mise/secrets.env"
        private.parent.mkdir(parents=True)
        private.write_text("private-content-that-must-never-be-read")
        unknown = self.home / "private-application/token.json"
        unknown.parent.mkdir()
        unknown.write_text("other-private-content")
        candidate = self.home / ".gitconfig"
        candidate.write_text("git-private-value")
        self.runner.config = {
            "files": [{"target": "~/.zshrc", "state": "missing"}],
            "edits": [{"path": "~/.codex/config.toml", "state": "applied", "edit": {"value": "hidden-merge-value"}}],
        }
        before = private.read_bytes()
        report = self.audit(("config",))
        output = json.dumps(report) + self.audit(("config",), text=True)
        self.assertEqual(report["summary"]["drift"], 1)
        self.assertIn("unmanaged_config", self.codes(report, "config"))
        self.assertIn("private_local", self.codes(report, "config"))
        for secret in ("private-content-that-must-never-be-read", "other-private-content", "git-private-value", "hidden-merge-value"):
            self.assertNotIn(secret, output)
        self.assertNotIn("private-application", output)
        self.assertEqual(private.read_bytes(), before)


    def test_all_dotfile_drift_states_and_invalid_state_are_handled(self):
        for state in ("missing", "differs", "source_missing", "would_remove", "future_state", []):
            with self.subTest(state=state):
                self.runner.config = {"files": [{"target": "~/.zshrc", "state": state}], "edits": []}
                report = self.audit(("config",))
                self.assertEqual(report["summary"]["drift"] + report["summary"]["error"], 1)


    def test_package_status_all_platform_contracts_are_supported(self):
        for required in PACKAGE_REQUIRED:
            with self.subTest(required=required):
                self.runner.packages = package_status(required)
                report = self.audit(("packages",))
                self.assertEqual(report["summary"]["error"], 0)
                self.assertEqual(report["summary"]["drift"], 0)
                self.assertEqual(self.runner.calls[-1][0], [str(self.root / "scripts/system-sync.sh"), "--status"])


    def test_packages_distinguish_missing_candidates_and_protected_roots(self):
        entries = {"声明但尚未以同名具体包安装": ["missing-tool"],
                   "当前显式安装包中未声明、但受保护而保留": ["kernel"],
                   "apply 时会从 explicit 降级为 dependency 的未声明包": ["old-tool"]}
        self.runner.packages = package_status(PACKAGE_REQUIRED[1], entries)
        report = self.audit(("packages",))
        self.assertEqual(report["summary"]["drift"], 1)
        self.assertEqual(report["summary"]["candidate"], 1)
        self.assertEqual(report["summary"]["local"], 1)


    def test_package_version_drift_and_broken_reports_do_not_look_clean(self):
        self.runner.packages = package_status(PACKAGE_REQUIRED[1], {"mise 最终生效声明": ["pacman:git [version_mismatch]"]})
        self.assertIn("package_version", self.codes(self.audit(("packages",)), "packages"))
        for output in ("", "future format", "声明的根包（1）\n  （无）\n"):
            with self.subTest(output=output):
                self.runner.packages = output
                self.assertEqual(self.audit(("packages",))["summary"]["error"], 1)


    def test_collector_failure_does_not_hide_other_sections(self):
        self.runner.failures.add(("mise", "ls", "--current", "--json"))
        report = self.audit(("tools", "packages", "config"))
        self.assertEqual(report["summary"]["error"], 1)
        self.assertTrue(report["sections"]["packages"])
        self.assertEqual(self.exit_code, 2)


    def test_unknown_tool_and_config_schemas_are_incomplete(self):
        self.runner.tools = []
        self.runner.config = {"future": []}
        report = self.audit(("tools", "config"))
        self.assertEqual(report["summary"]["error"], 2)
        self.assertEqual(self.exit_code, 2)


    def test_redaction_and_exit_codes_preserve_machine_readable_schema(self):
        self.environment["EXTRA_SECRET"] = "ok"
        self.audit(("tools",))
        self.assertEqual(self.exit_code, 0)
        self.tool("fixture-private-token", "fixture-command", installed=False)
        report = self.audit(("tools",))
        self.assertEqual(self.exit_code, 0)
        self.assertNotIn("fixture-private-token", json.dumps(report))
        self.assertEqual(report["sections"]["tools"][0]["kind"], "drift")
        self.audit(("tools",), strict=True)
        self.assertEqual(self.exit_code, 1)

    def test_failed_tool_snapshot_is_shared_but_retried_on_the_next_audit(self):
        command = ("mise", "ls", "--current", "--json")
        self.runner.failures.add(command)
        report = self.audit()
        self.assertEqual(report["summary"]["error"], 2)
        self.assertTrue(report["sections"]["packages"])
        self.assertEqual(sum(tuple(call) == command for call, _ in self.runner.calls), 1)
        self.runner.failures.clear()
        self.assertEqual(self.audit()["summary"]["error"], 0)
        self.assertEqual(sum(tuple(call) == command for call, _ in self.runner.calls), 2)


@unittest.skipUnless(MISE and sys.platform in ("darwin", "linux"), "real mise unavailable")
class AuditTaskTest(unittest.TestCase):
    def test_native_task_does_not_install_and_preserves_caller_path(self):
        with tempfile.TemporaryDirectory(prefix="system-sync-audit-task-") as temporary:
            base = Path(temporary).resolve()
            home, project = base / "home", base / "project"
            config = home / ".config/mise"
            config.mkdir(parents=True)
            (project / "scripts").mkdir(parents=True)
            prepare_helper(project)
            source = project / "dotfiles/common/shell/path.sh"
            source.parent.mkdir(parents=True)
            shutil.copy2(ROOT / "dotfiles/common/shell/path.sh", source)
            environment = {
                "HOME": str(home), "MISE_CONFIG_DIR": str(config), "MISE_SYSTEM_CONFIG_DIR": str(base / "system"),
                "MISE_DATA_DIR": str(base / "data"), "MISE_STATE_DIR": str(base / "state"),
                "MISE_CACHE_DIR": str(base / "cache"), "MISE_CEILING_PATHS": str(base),
                "MISE_TRUSTED_CONFIG_PATHS": f"{base}:{ROOT}", "MISE_OFFLINE": "1",
            }
            caller = base / "caller bin"
            caller.mkdir()
            environment["PATH"] = f"{caller}:{Path(MISE).parent}:/usr/bin:/bin:/usr/sbin:/sbin"
            python = base / "task-python/bin/python3"
            python.parent.mkdir(parents=True)
            python.symlink_to(sys.executable)

            def read_config(key):
                result = subprocess.run([MISE, "config", "get", "--file", str(ROOT / "mise.toml"), key],
                                        cwd=project, env=environment, text=True, capture_output=True)
                self.assertEqual(result.returncode, 0, result.stderr)
                return result.stdout

            task = read_config("tasks.audit")
            (project / "mise.toml").write_text(
                '[settings.task]\nrun_auto_install = ' + read_config("settings.task.run_auto_install").strip() +
                '\n[tools]\nnode = "0.0.0"\npython = "path:' + str(python.parent.parent) + '"\n[tasks.audit]\n' + task)
            result = subprocess.run([MISE, "run", "--quiet", "audit", "--only", "tools", "--only", "path", "--json"],
                                    cwd=project, env=environment, text=True, capture_output=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            report = json.loads(result.stdout)
            self.assertEqual(report["summary"]["error"], 0)
            self.assertTrue(any(item["code"] == "tool_missing" for item in report["sections"]["tools"]))
            self.assertTrue(any(item["item"] == str(caller) for item in report["sections"]["path"]))
            self.assertEqual(report["context"]["path_source"], "caller")
            self.assertEqual(report["context"]["path_entries"], len(environment["PATH"].split(":")))
            self.assertFalse((base / "data/installs/node/0.0.0").exists())
            forced_json = subprocess.run(
                [MISE, "run", "--quiet", "audit", "--only", "tools", "--only", "path", "--json", "--color", "always"],
                cwd=project, env=environment, text=True, capture_output=True)
            self.assertEqual(forced_json.returncode, 0, forced_json.stderr)
            self.assertEqual(json.loads(forced_json.stdout), report)
            for mode in ("auto", "always", "never"):
                with self.subTest(color=mode):
                    colored = subprocess.run(
                        [MISE, "run", "--quiet", "audit", "--only", "tools", "--color", mode],
                        cwd=project, env=dict(environment, TERM="dumb", NO_COLOR="1"),
                        text=True, capture_output=True)
                    self.assertEqual(colored.returncode, 0, colored.stderr)
                    self.assertIn("[!] 漂移 · node", colored.stdout)
                    self.assertIn("[+] 已核对 · python", colored.stdout)
                    self.assertEqual("\033[" in colored.stdout, mode == "always")
            # Exercise the ordinary task entry in a real terminal, including mise's output handling.
            import errno
            import os
            import pty
            import select
            for extra, expected_color in (({}, True), ({"NO_COLOR": "1"}, False), ({"TERM": "dumb"}, False)):
                with self.subTest(terminal_environment=extra):
                    command = [MISE, "run", "audit", "--only", "tools"]
                    terminal_environment = {**environment, "TERM": "xterm-256color", **extra}
                    pid, master = pty.fork()
                    if pid == 0:
                        try:
                            os.chdir(project)
                            os.execve(MISE, command, terminal_environment)
                        finally:
                            os._exit(127)
                    chunks = []
                    try:
                        while True:
                            if not select.select([master], [], [], 15)[0]:
                                os.kill(pid, 9)
                                self.fail("terminal audit did not finish")
                            try:
                                chunk = os.read(master, 8192)
                            except OSError as error:
                                if error.errno == errno.EIO:
                                    break
                                raise
                            if not chunk:
                                break
                            chunks.append(chunk)
                    finally:
                        os.close(master)
                        _, status = os.waitpid(pid, 0)
                    output = b"".join(chunks).decode()
                    self.assertEqual(os.waitstatus_to_exitcode(status), 0)
                    self.assertIn("[!] 漂移 · node", output)
                    self.assertEqual("\033[31m" in output, expected_color)
            strict = subprocess.run([MISE, "run", "--quiet", "audit", "--only", "tools", "--strict", "--json"],
                                    cwd=project, env=environment, text=True, capture_output=True)
            self.assertNotEqual(strict.returncode, 0)
            self.assertEqual(json.loads(strict.stdout)["summary"]["error"], 0)


if __name__ == "__main__":
    unittest.main()
