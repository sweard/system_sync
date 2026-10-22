"""Exercise cleanup only in disposable repositories, including links outside its scope."""

import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

from test_converge import run_terminal


ROOT = Path(__file__).resolve().parents[1]
TARGETS = (
    ".system-sync/rebuild/home",
    ".system-sync/vscode-validation",
    ".system-sync/flutter-validation/project",
    ".system-sync/flutter-validation/pub-cache",
    ".system-sync/go/build",
    ".system-sync/dist",
)
PRESERVED = (
    ".system-sync/history/run/transaction.json",
    ".system-sync/tool-migration/original.bak",
    ".system-sync/env-path-takeover/original.sh",
    ".system-sync/app-config-takeover/original.json",
    ".system-sync/go/mod/cache/download/dependency.zip",
    ".system-sync/helper/current",
    ".system-sync/rebuild/result.json",
    ".system-sync/rebuild/install.log",
    ".system-sync/flutter-validation/result.json",
    ".system-sync/flutter-validation/analyze.log",
    "helper/prebuilt/v0.2.0/helper.tar.gz",
    "dotfiles/common/mise/config.toml",
    ".git/objects/keep",
)


class CleanTest(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory(prefix="system-sync-clean-")
        self.addCleanup(temporary.cleanup)
        self.base = Path(temporary.name).resolve()
        self.project = self.base / "repo with spaces 中文"
        (self.project / "scripts").mkdir(parents=True)
        self.script = self.project / "scripts/clean.sh"
        shutil.copy2(ROOT / "scripts/clean.sh", self.script)
        shutil.copy2(ROOT / "scripts/task-output.sh", self.project / "scripts/task-output.sh")
        for relative in TARGETS:
            directory = self.project / relative
            directory.mkdir(parents=True)
            (directory / "cached-file").write_bytes(b"reproducible data")
        for relative in PRESERVED:
            path = self.project / relative
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(b"keep this data")
        self.outside = self.base / "outside"
        self.outside.mkdir()
        (self.outside / "keep").write_bytes(b"outside data")
        self.env = dict(os.environ, HOME=str(self.outside))

    def snapshot(self):
        result = {}
        for path in self.project.rglob("*"):
            if path.is_symlink():
                value = ("link", os.readlink(path))
            elif path.is_dir():
                value = ("directory",)
            else:
                value = ("file", path.read_bytes())
            result[str(path.relative_to(self.project))] = value
        return result

    def run_clean(self, *args, expected=0):
        result = subprocess.run([str(self.script), *args], cwd=self.outside, env=self.env,
                                input="", capture_output=True, text=True, timeout=10)
        self.assertEqual(result.returncode, expected, result.stdout + result.stderr)
        return result

    def assert_preserved(self):
        for relative in PRESERVED:
            self.assertEqual((self.project / relative).read_bytes(), b"keep this data", relative)
        self.assertEqual((self.outside / "keep").read_bytes(), b"outside data")

    def install_rm_stub(self, mode):
        commands = self.base / "commands"
        commands.mkdir(exist_ok=True)
        counter = self.base / "rm-attempts"
        counter.unlink(missing_ok=True)
        stub = commands / "rm"
        stub.write_text('''#!/bin/sh
set -eu
if [ "$#" -ne 3 ] || [ "$1" != -rf ] || [ "$2" != -- ] || [ "$3" != "$CLEAN_TEST_TARGET" ]; then
  exec "$CLEAN_TEST_REAL_RM" "$@"
fi
attempt=0
if [ -f "$CLEAN_TEST_COUNTER" ]; then read -r attempt < "$CLEAN_TEST_COUNTER"; fi
attempt=$((attempt + 1))
printf '%s\\n' "$attempt" > "$CLEAN_TEST_COUNTER"
if [ "$CLEAN_TEST_RM_MODE" != always ] && [ "$attempt" -gt 1 ]; then
  exec "$CLEAN_TEST_REAL_RM" "$@"
fi
case "$CLEAN_TEST_RM_MODE" in
  permission)
    printf 'rm: %s: Permission denied\\n' "$3" >&2
    exit 1 ;;
  mixed)
    printf 'rm: %s: Directory not empty\\nrm: %s: Permission denied\\n' "$3" "$3" >&2
    exit 1 ;;
  link)
    "$CLEAN_TEST_REAL_RM" "$@"
    ln -s "$CLEAN_TEST_OUTSIDE" "$3"
    printf 'rm: %s: Directory not empty\\n' "$3" >&2
    exit 1 ;;
  success-recreated)
    "$CLEAN_TEST_REAL_RM" "$@"
    mkdir -p "$3"
    printf 'metadata' > "$3/.DS_Store"
    exit 0 ;;
esac
"$CLEAN_TEST_REAL_RM" -f -- "$3/cached-file"
printf 'metadata' > "$3/.DS_Store"
printf 'rm: %s: Directory not empty\\n' "$3" >&2
exit 1
''')
        stub.chmod(0o755)
        self.env.update(
            PATH=str(commands) + os.pathsep + os.environ.get("PATH", ""),
            CLEAN_TEST_REAL_RM=shutil.which("rm"),
            CLEAN_TEST_TARGET=str(self.project / TARGETS[0]),
            CLEAN_TEST_COUNTER=str(counter),
            CLEAN_TEST_RM_MODE=mode,
            CLEAN_TEST_OUTSIDE=str(self.outside),
        )
        return counter

    def test_default_and_explicit_preview_do_not_change_any_files(self):
        before = self.snapshot()
        for args in ((), ("--dry-run",)):
            result = self.run_clean(*args)
            for relative in TARGETS:
                self.assertIn(relative, result.stdout)
            self.assertIn("待清理 6 项", result.stdout)
            self.assertIn("预计可回收", result.stdout)
            self.assertIn("以上仅为预览", result.stdout)
            self.assertEqual(self.snapshot(), before)

    def test_apply_from_other_directory_removes_only_the_allowlist(self):
        self.run_clean("--apply", "--yes")
        for relative in TARGETS:
            self.assertFalse((self.project / relative).exists(), relative)
        self.assert_preserved()
        self.assertIn("无需清理", self.run_clean("--apply", "--yes").stdout)

    def test_missing_state_directory_is_a_noop_and_is_not_created(self):
        shutil.rmtree(self.project / ".system-sync")
        before = self.snapshot()
        self.assertIn("无需清理", self.run_clean("--apply", "--yes").stdout)
        self.assertEqual(self.snapshot(), before)
        self.assertFalse((self.project / ".system-sync").exists())

    def test_noninteractive_apply_requires_an_explicit_yes_flag(self):
        before = self.snapshot()
        result = self.run_clean("--apply", expected=2)
        self.assertIn("非交互环境未执行清理", result.stderr)
        self.assertEqual(self.snapshot(), before)

    def test_interactive_confirmation_accepts_only_exact_lowercase_yes(self):
        before = self.snapshot()
        command = [str(self.script), "--apply"]
        for response in ("", "y", "YES", "yes ", "no"):
            with self.subTest(response=response):
                result = run_terminal(command, self.env, [("输入 yes 确认", response)])
                self.assertEqual(result.returncode, 2, result.stdout)
                self.assertEqual(self.snapshot(), before)
        result = run_terminal(command, self.env, [("输入 yes 确认", "yes")])
        self.assertEqual(result.returncode, 0, result.stdout)
        for relative in TARGETS:
            self.assertFalse((self.project / relative).exists(), relative)
        self.assert_preserved()

    def test_invalid_arguments_and_help_leave_files_untouched(self):
        before = self.snapshot()
        for args in (("--yes",), ("--dry-run", "--yes"), ("--apply", "--dry-run"),
                     ("--dry-run", "--apply"), ("--all",), (str(self.outside),)):
            with self.subTest(args=args):
                self.run_clean(*args, expected=2)
                self.assertEqual(self.snapshot(), before)
        self.assertIn("用法：clean.sh", self.run_clean("--help").stdout)
        self.assertEqual(self.snapshot(), before)

    def test_linked_state_parent_or_target_aborts_before_any_deletion(self):
        for relative in (".system-sync", ".system-sync/rebuild", ".system-sync/go/build"):
            with self.subTest(relative=relative):
                path = self.project / relative
                backup = path.with_name(path.name + ".test-backup")
                path.rename(backup)
                path.symlink_to(self.outside, target_is_directory=True)
                try:
                    before = self.snapshot()
                    for args in (("--dry-run",), ("--apply", "--yes")):
                        result = self.run_clean(*args, expected=1)
                        self.assertIn("不能是符号链接", result.stderr)
                        self.assertEqual(self.snapshot(), before)
                        self.assertEqual((self.outside / "keep").read_bytes(), b"outside data")
                finally:
                    path.unlink()
                    backup.rename(path)

    def test_non_directory_target_aborts_before_any_deletion(self):
        path = self.project / ".system-sync/go/build"
        shutil.rmtree(path)
        path.write_bytes(b"unexpected user file")
        before = self.snapshot()
        self.assertIn("必须是目录", self.run_clean("--apply", "--yes", expected=1).stderr)
        self.assertEqual(self.snapshot(), before)

    def test_links_inside_a_cleanup_directory_do_not_remove_external_data(self):
        link = self.project / ".system-sync/go/build/external"
        link.symlink_to(self.outside, target_is_directory=True)
        self.run_clean("--apply", "--yes")
        self.assertFalse(link.exists())
        self.assert_preserved()

    def test_transient_directory_metadata_is_retried_and_cleaned(self):
        counter = self.install_rm_stub("once")
        result = self.run_clean("--apply", "--yes")
        self.assertEqual(counter.read_text(), "2\n")
        self.assertIn("重试清理（2/3）", result.stdout)
        self.assertNotIn("Directory not empty", result.stderr)
        for relative in TARGETS:
            self.assertFalse((self.project / relative).exists(), relative)
        self.assert_preserved()

    def test_persistent_directory_writes_stop_after_three_attempts(self):
        counter = self.install_rm_stub("always")
        result = self.run_clean("--apply", "--yes", expected=1)
        self.assertEqual(counter.read_text(), "3\n")
        self.assertIn("Directory not empty", result.stderr)
        self.assertNotIn("清理完成", result.stdout)
        for relative in TARGETS[1:]:
            self.assertTrue((self.project / relative).exists(), relative)
        self.assert_preserved()

    def test_permission_and_mixed_errors_stop_without_retrying(self):
        for mode in ("permission", "mixed"):
            with self.subTest(mode=mode):
                counter = self.install_rm_stub(mode)
                before = self.snapshot()
                result = self.run_clean("--apply", "--yes", expected=1)
                self.assertEqual(counter.read_text(), "1\n")
                self.assertIn("Permission denied", result.stderr)
                self.assertNotIn("重试清理", result.stdout)
                self.assertEqual(self.snapshot(), before)

    def test_successful_rm_must_also_leave_the_target_absent(self):
        counter = self.install_rm_stub("success-recreated")
        result = self.run_clean("--apply", "--yes")
        self.assertEqual(counter.read_text(), "2\n")
        self.assertIn("重试清理（2/3）", result.stdout)
        self.assertFalse((self.project / TARGETS[0]).exists())
        self.assert_preserved()

    def test_retry_rejects_a_target_replaced_by_an_external_link(self):
        counter = self.install_rm_stub("link")
        result = self.run_clean("--apply", "--yes", expected=1)
        self.assertEqual(counter.read_text(), "1\n")
        self.assertIn("不能是符号链接", result.stderr)
        for relative in TARGETS[1:]:
            self.assertTrue((self.project / relative).exists(), relative)
        self.assert_preserved()
