"""通过脚本入口验证预览信任；所有 brew 调用都由临时替身处理。"""

import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

from test_converge import run_terminal
from helper_fixture import prepare_helper


PROJECT_ROOT = Path(__file__).resolve().parents[1]
TRUSTED_CASK = "abue-ammar/tinycast/tinycast"

BREW_STUB = r'''#!/bin/bash
set -eu
printf '%s|%s\n' "${XDG_CONFIG_HOME:-}" "$*" >> "$SYNC_TEST_LOG"
case "$*" in
  "bundle list "*)
    case "$*" in
      *--formula*) printf '%s\n' git mise fixture/tools/tool ;;
      *--cask*) printf '%s\n' tinycast ;;
      *--tap*) printf '%s\n' fixture/tools abue-ammar/tinycast ;;
      *) exit 90 ;;
    esac
    ;;
  "list --formula "*) printf '%s\n' git mise fixture/tools/tool ;;
  "list --cask")
    if [[ "$SYNC_TEST_INSTALLED" == 1 ]]; then printf '%s\n' tinycast; fi
    ;;
  tap) printf '%s\n' fixture/tools abue-ammar/tinycast ;;
  "bundle check "*)
    if [[ "$SYNC_TEST_INSTALLED" == 1 ]]; then
      printf '%s\n' "The Brewfile's dependencies are satisfied."
    else
      printf '%s\n' 'Cask tinycast needs to be installed.'
      exit 1
    fi
    ;;
  "bundle install "*)
    [[ -f "$SYSTEM_SYNC_RUN_LOG_DIR/Brewfile.before" && -f "$SYSTEM_SYNC_RUN_LOG_DIR/formulae.before.txt" ]] || exit 94
    if [[ "${SYNC_TEST_INSTALL_ERROR:-0}" == 1 ]]; then exit 95; fi
    ;;
  "trust --formula "*|"trust --cask "*)
    [[ -n "${XDG_CONFIG_HOME:-}" && "$XDG_CONFIG_HOME" != "$SYNC_TEST_REAL_CONFIG" ]] || exit 91
    if [[ "$2" == --cask && "${SYNC_TEST_TRUST_ERROR:-0}" == 1 ]]; then exit 92; fi
    mkdir -p "$XDG_CONFIG_HOME/homebrew"
    printf '%s %s\n' "$2" "$3" >> "$XDG_CONFIG_HOME/homebrew/trusted-items.txt"
    ;;
  "bundle cleanup "*)
    # 预览绝不能向替身请求实际 cleanup。
    [[ "$*" != *--force* ]] || exit 93
    if [[ "$XDG_CONFIG_HOME" == "$SYNC_TEST_REAL_CONFIG" ]]; then
      [[ -f "$SYSTEM_SYNC_RUN_LOG_DIR/cleanup.after-install.preview.txt" ]] || exit 96
      exit 0
    fi
    trust_file="$XDG_CONFIG_HOME/homebrew/trusted-items.txt"
    if ! grep -Fxq -- '--formula fixture/tools/tool' "$trust_file"; then
      printf '%s\n' 'Error: Untrusted formula fixture/tools/tool.'
      exit 1
    fi
    if [[ "$SYNC_TEST_INSTALLED" == 1 ]] &&
       ! grep -Fxq -- '--cask abue-ammar/tinycast/tinycast' "$trust_file"; then
      printf '%s\n' 'Error: Refusing to load cask abue-ammar/tinycast/tinycast from untrusted tap.'
      exit 1
    fi
    if [[ "${SYNC_TEST_CLEANUP_ERROR:-0}" == 1 ]]; then
      printf '%s\n' 'Error: Simulated cleanup failure.'
      exit 1
    fi
    if [[ "${SYNC_TEST_GRAPH_WARNING:-0}" == 1 ]]; then
      printf '%s\n' 'Formulae dependency graph sorting found a circular dependency:'
      printf '%s\n' 'Would uninstall formulae:' 'old-fixture'
    fi
    ;;
  *) printf 'Unexpected brew invocation: %s\n' "$*" >&2; exit 99 ;;
esac
'''


class HomebrewPreviewTrustTest(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="system-sync-preview-")
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.project = self.root / "system sync"
        self.packages = self.project / "packages" / "macos"
        self.packages.mkdir(parents=True)
        scripts = self.project / "scripts"
        scripts.mkdir()
        self.script = scripts / "homebrew-converge.sh"
        shutil.copy2(PROJECT_ROOT / "scripts/homebrew-converge.sh", self.script)
        for name in ("sync-runtime.sh", "system-sync.sh"):
            shutil.copy2(PROJECT_ROOT / "scripts" / name, scripts / name)
        prepare_helper(self.project)

        self.brewfile = self.packages / "Brewfile"
        self.brewfile.write_text(
            'tap "fixture/tools"\n'
            'tap "abue-ammar/tinycast"\n'
            'brew "git"\n'
            'brew "mise"\n'
            'brew "fixture/tools/tool", trusted: true\n'
            f'cask "{TRUSTED_CASK}", trusted: true\n',
            encoding="utf-8",
        )
        (self.packages / "protected-packages.txt").write_text(
            "formula:git\nformula:mise\n", encoding="utf-8"
        )
        (self.packages / "trusted-formulae.txt").write_text(
            "fixture/tools/tool\n", encoding="utf-8"
        )
        self.cask_allowlist = self.packages / "trusted-casks.txt"
        self.cask_allowlist.write_text(f"{TRUSTED_CASK}\n", encoding="utf-8")

        commands = self.root / "bin"
        commands.mkdir()
        for name, source in {
            "brew": BREW_STUB,
            "uname": '#!/bin/bash\nprintf "Darwin\\n"\n',
        }.items():
            path = commands / name
            path.write_text(source, encoding="utf-8")
            path.chmod(0o755)

        self.log = self.root / "brew-calls.log"
        self.real_config = self.root / "user-config"
        self.real_config.mkdir()
        (self.real_config / "homebrew").mkdir()
        self.real_trust = self.real_config / "homebrew" / "trust.json"
        self.real_trust.write_text('{"sentinel": true}\n', encoding="utf-8")
        temporary_work = self.root / "tmp"
        temporary_work.mkdir()
        self.environment = os.environ.copy()
        self.environment.update(
            PATH=f"{commands}{os.pathsep}{os.environ.get('PATH', '')}",
            XDG_CONFIG_HOME=str(self.real_config),
            TMPDIR=str(temporary_work),
            SYNC_TEST_LOG=str(self.log),
            SYNC_TEST_REAL_CONFIG=str(self.real_config),
            SYNC_TEST_INSTALLED="1",
            SYNC_TEST_TRUST_ERROR="0",
            SYNC_TEST_CLEANUP_ERROR="0",
        )

    def run_preview(self, **variables):
        environment = dict(self.environment, **variables)
        return subprocess.run(
            ["bash", str(self.script), "--dry-run"],
            cwd=self.project,
            env=environment,
            stdin=subprocess.DEVNULL,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
            timeout=20,
            check=False,
        )

    def test_before_cask_installation(self):
        result = self.run_preview(SYNC_TEST_INSTALLED="0")
        self.assertEqual(result.returncode, 0, result.stdout)
        self.assertIn("Cask tinycast needs to be installed.", result.stdout)

    def test_status_skips_cleanup_and_temporary_trust_even_when_preview_would_fail(self):
        result = subprocess.run(
            ["bash", str(self.script), "--status"], cwd=self.project,
            env=dict(self.environment, SYNC_TEST_CLEANUP_ERROR="1", SYNC_TEST_TRUST_ERROR="1"),
            stdin=subprocess.DEVNULL, text=True, capture_output=True, timeout=20)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        calls = self.log.read_text()
        self.assertNotIn("|bundle cleanup ", calls)
        self.assertNotIn("|trust ", calls)
        self.assertNotIn("Homebrew cleanup 精确预演", result.stdout)
        self.assertIn("bundle check 退出码为 0", result.stdout)
        self.assertIn("mise run package-dry-run", result.stdout)

    def test_installed_cask_uses_isolated_exact_trust(self):
        result = self.run_preview()
        self.assertEqual(result.returncode, 0, result.stdout)
        calls = self.log.read_text(encoding="utf-8").splitlines()
        trust_calls = [line.split("|", 1) for line in calls if "|trust " in line]
        self.assertEqual(
            {command for _, command in trust_calls},
            {"trust --formula fixture/tools/tool", f"trust --cask {TRUSTED_CASK}"},
        )
        for config, _ in trust_calls:
            self.assertNotEqual(config, str(self.real_config))
            self.assertFalse(Path(config).exists(), "预览临时目录应已回收")
        self.assertEqual(
            self.real_trust.read_text(encoding="utf-8"), '{"sentinel": true}\n'
        )
        self.assertIn("预览模式：未做任何修改。", result.stdout)

    def test_unqualified_cask_is_rejected(self):
        self.cask_allowlist.write_text("tinycast\n", encoding="utf-8")
        result = self.run_preview()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("必须使用完整 owner/tap/cask 名称", result.stdout)
        self.assertNotIn("|trust ", self.log.read_text(encoding="utf-8"))

    def test_undeclared_cask_is_rejected(self):
        self.cask_allowlist.write_text("other/tap/not-declared\n", encoding="utf-8")
        result = self.run_preview()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("临时信任 cask 未在 Brewfile 声明", result.stdout)

    def test_cask_without_trusted_declaration_is_rejected(self):
        source = self.brewfile.read_text(encoding="utf-8")
        self.brewfile.write_text(
            source.replace(f'cask "{TRUSTED_CASK}", trusted: true', f'cask "{TRUSTED_CASK}"'),
            encoding="utf-8",
        )
        result = self.run_preview()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("cask 缺少精确 trusted: true 声明", result.stdout)

    def test_failed_cask_trust_stops_before_cleanup(self):
        result = self.run_preview(SYNC_TEST_TRUST_ERROR="1")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("无法初始化 cleanup 临时 cask 信任", result.stdout)
        self.assertNotIn("|bundle cleanup ", self.log.read_text(encoding="utf-8"))

    def test_cleanup_errors_still_stop_the_preview(self):
        result = self.run_preview(SYNC_TEST_CLEANUP_ERROR="1")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("Homebrew cleanup 预演失败", result.stdout)

    def transaction(self):
        import json
        journals = list((self.project / ".system-sync/history").glob("*/transaction.json"))
        self.assertEqual(len(journals), 1)
        return json.loads(journals[0].read_text())

    def test_wrapper_and_direct_status_each_preflight_once(self):
        version_log = self.root / "version-queries"
        mise = self.root / "bin/mise"
        mise.write_text('#!/bin/bash\nprintf "%s\\n" "$*" >> "$SYNC_TEST_VERSION_LOG"\n'
                        'printf "%s\\n" "${SYNC_TEST_MISE_VERSION:-2026.10.11 fixture}"\n')
        mise.chmod(0o755)
        environment = dict(self.environment, SYNC_TEST_VERSION_LOG=str(version_log))
        for name in ("system-sync.sh", "homebrew-converge.sh"):
            with self.subTest(entry=name):
                version_log.unlink(missing_ok=True)
                result = subprocess.run(["bash", str(self.script.parent / name), "--status"],
                                        env=environment, text=True, stdout=subprocess.PIPE,
                                        stderr=subprocess.STDOUT, timeout=20)
                self.assertEqual(result.returncode, 0, result.stdout)
                self.assertEqual(version_log.read_text().splitlines(), ["--version"])
                self.assertIn("bundle check 退出码为 0", result.stdout)
                self.log.unlink()
                old = subprocess.run(["bash", str(self.script.parent / name), "--status"],
                                     env=dict(environment, SYNC_TEST_MISE_VERSION="2026.10.10 fixture"),
                                     text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=20)
                self.assertNotEqual(old.returncode, 0)
                self.assertFalse(self.log.exists(), "old mise reached Homebrew queries")

    def test_wrapper_apply_retains_a_single_guarded_transaction(self):
        result = run_terminal(["bash", str(self.script.parent / "system-sync.sh"), "--apply"],
                              self.environment, [("输入 yes 继续", "yes")])
        self.assertEqual(result.returncode, 0, result.stdout)
        self.assertEqual(self.transaction()["phase"], "complete")
        self.assertEqual(sum("|bundle install " in line for line in self.log.read_text().splitlines()), 1)

    def test_apply_installs_and_cleans_inside_shared_audit(self):
        result = run_terminal(["bash", str(self.script), "--apply"], self.environment,
                              [("输入 yes 继续", "yes")])
        self.assertEqual(result.returncode, 0, result.stdout)
        self.assertEqual(self.transaction()["phase"], "complete")
        calls = self.log.read_text().splitlines()
        install = next(i for i,line in enumerate(calls) if "|bundle install " in line)
        cleanup = next(i for i,line in enumerate(calls) if line.startswith(str(self.real_config) + "|bundle cleanup "))
        self.assertLess(install, cleanup)

    def test_failed_install_is_audited_without_actual_cleanup(self):
        environment = dict(self.environment, SYNC_TEST_INSTALL_ERROR="1")
        result = run_terminal(["bash", str(self.script), "--apply"], environment,
                              [("输入 yes 继续", "yes")])
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertEqual(self.transaction()["last_phase"], "installing")
        self.assertEqual(self.transaction()["phase"], "failed")
        self.assertFalse(any(line.startswith(str(self.real_config) + "|bundle cleanup ")
                             for line in self.log.read_text().splitlines()))

    def test_upgrade_is_audited_and_does_not_cleanup(self):
        result = run_terminal(["bash", str(self.script), "--upgrade"], self.environment,
                              [("输入 yes 继续", "yes")])
        self.assertEqual(result.returncode, 0, result.stdout)
        self.assertEqual(self.transaction()["phase"], "complete")
        self.assertIn("bundle install --upgrade", self.log.read_text())
        self.assertNotIn("|bundle cleanup ", self.log.read_text())

    def test_only_yes_reaches_homebrew_mutations(self):
        for mode, old_phrase in (("--apply", "APPLY HOMEBREW"), ("--upgrade", "UPGRADE HOMEBREW")):
            for response in ("", "no", "y", old_phrase):
                with self.subTest(mode=mode, response=response):
                    self.log.unlink(missing_ok=True)
                    result = run_terminal(["bash", str(self.script), mode], self.environment,
                                          [("输入 yes 继续", response)])
                    self.assertEqual(result.returncode, 2, result.stdout)
                    calls = self.log.read_text().splitlines()
                    self.assertFalse(any("|bundle install " in line for line in calls))
                    self.assertFalse(any(line.startswith(str(self.real_config) + "|bundle cleanup ")
                                         for line in calls))

    def test_graph_warning_uses_yes_and_retains_separate_cleanup_confirmation(self):
        environment = dict(self.environment, SYNC_TEST_GRAPH_WARNING="1")
        for response in ("no", "yes"):
            with self.subTest(response=response):
                self.log.unlink(missing_ok=True)
                result = run_terminal(["bash", str(self.script), "--apply"], environment,
                                      [("输入 yes 继续（", "yes"), ("输入 yes 继续 cleanup", response)])
                self.assertEqual(result.returncode, 0 if response == "yes" else 2, result.stdout)
                calls = self.log.read_text().splitlines()
                self.assertTrue(any("|bundle install " in line for line in calls))
                self.assertEqual(any(line.startswith(str(self.real_config) + "|bundle cleanup ")
                                     for line in calls), response == "yes")


if __name__ == "__main__":
    unittest.main()
