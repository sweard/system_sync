"""Keep timing, platform selection and parallel execution observable and fail-closed."""

import io
import json
import os
from pathlib import Path
import shlex
import shutil
import subprocess
import tempfile
import textwrap
import unittest
from contextlib import redirect_stderr, redirect_stdout
from unittest.mock import patch

import check_scope
import run_checks


class CheckScopeTest(unittest.TestCase):
    def test_current_scope_filters_os_distro_and_architecture_and_all_keeps_everything(self):
        choices = ("macos", "arch", "debian", "fedora", "opensuse")
        for selected in choices:
            with self.subTest(profile=selected), patch.dict(os.environ, {check_scope.PROFILE_ENV: selected}):
                self.assertEqual(check_scope.selected_platforms(choices), (selected,))
                self.assertEqual(check_scope.matches(("linux",)), selected != "macos")
                self.assertTrue(check_scope.matches(("common",)))
                with patch.object(check_scope, "current_target", return_value="fixture_target"):
                    self.assertEqual(check_scope.selected_targets(), ("fixture_target",))
                with self.assertRaises(unittest.SkipTest):
                    check_scope.selected_platforms(("missing-platform",))
        with patch.dict(os.environ, {check_scope.PROFILE_ENV: "all"}):
            self.assertEqual(check_scope.selected_platforms(choices), choices)
            self.assertEqual(check_scope.selected_targets(), check_scope.TARGETS)
        with patch.dict(os.environ, {check_scope.PROFILE_ENV: "unknown"}):
            with self.assertRaises(ValueError):
                check_scope.scope()

    def test_method_metadata_can_narrow_a_platform_specific_class(self):
        @check_scope.for_platforms("linux")
        class LinuxCase(unittest.TestCase):
            def test_shared(self):
                pass

            @check_scope.for_platforms("opensuse")
            def test_zypper(self):
                pass

        self.assertTrue(check_scope.applies(LinuxCase("test_shared"), "arch"))
        self.assertFalse(check_scope.applies(LinuxCase("test_shared"), "macos"))
        self.assertFalse(check_scope.applies(LinuxCase("test_zypper"), "arch"))
        self.assertTrue(check_scope.applies(LinuxCase("test_zypper"), "opensuse"))
        self.assertTrue(check_scope.applies(LinuxCase("test_zypper"), "all"))
        with self.assertRaises(ValueError):
            check_scope.for_platforms("typo-profile")

    def test_host_detection_uses_the_executing_os_architecture_and_linux_detector(self):
        for system, machine, target in (("darwin", "arm64", "darwin_arm64"),
                                        ("darwin", "x86_64", "darwin_amd64"),
                                        ("linux", "aarch64", "linux_arm64"),
                                        ("linux", "x86_64", "linux_amd64")):
            with self.subTest(system=system, machine=machine), patch.object(check_scope.sys, "platform", system), \
                 patch.object(check_scope.platform, "machine", return_value=machine):
                self.assertEqual(check_scope.current_target(), target)
        with patch.object(check_scope.sys, "platform", "darwin"):
            self.assertEqual(check_scope.current_profile(), "macos")
        for profile in check_scope.LINUX_PROFILES:
            output = subprocess.CompletedProcess([], 0, profile + "\n", "")
            with self.subTest(profile=profile), patch.object(check_scope.sys, "platform", "linux"), \
                 patch.object(check_scope.subprocess, "run", return_value=output) as command:
                self.assertEqual(check_scope.current_profile(), profile)
                self.assertTrue(command.call_args.args[0][1].endswith("scripts/detect-linux-profile.sh"))
        with patch.object(check_scope.sys, "platform", "linux"), \
             patch.object(check_scope.subprocess, "run", return_value=subprocess.CompletedProcess([], 1, "", "private detail")):
            with self.assertRaisesRegex(ValueError, "无法识别"):
                check_scope.current_profile()

    def test_real_catalog_preserves_full_coverage_and_excludes_foreign_package_workflows(self):
        full, excluded = run_checks.discover_tests(run_checks.TESTS, "all")
        self.assertEqual(excluded, [])
        for profile in check_scope.PROFILES:
            included, excluded = run_checks.discover_tests(run_checks.TESTS, profile)
            self.assertEqual(set(included) | set(excluded), set(full))
            if profile == "macos":
                self.assertFalse(any(name.startswith("test_converge.") for name in included))
                self.assertTrue(any(name.startswith("test_homebrew_preview.") for name in included))
            else:
                self.assertFalse(any(name.startswith("test_homebrew_preview.") for name in included))
                zypper = "test_converge.ConvergeTest.test_zypper_native_install_then_protected_cleanup"
                self.assertEqual(zypper in included, profile == "opensuse")


class CheckTimingTest(unittest.TestCase):
    def test_timing_records_cleanup_errors_skips_and_subtest_failures(self):
        cleaned = []

        class Cases(unittest.TestCase):
            def test_pass(self):
                self.addCleanup(cleaned.append, "done")

            def test_subtest_failure(self):
                with self.subTest(example="failure"):
                    self.fail("synthetic assertion")

            def test_error(self):
                raise ValueError("synthetic exception")

            @unittest.skip("synthetic skip")
            def test_skip(self):
                self.fail("must not execute")

        suite = unittest.defaultTestLoader.loadTestsFromTestCase(Cases)
        result = unittest.TextTestRunner(stream=io.StringIO(), resultclass=run_checks.TimedResult).run(suite)
        self.assertFalse(result.wasSuccessful())
        self.assertEqual(cleaned, ["done"])
        outcomes = {case["id"].rsplit(".", 1)[-1]: case["outcome"] for case in result.timings}
        self.assertEqual(outcomes, {"test_pass": "passed", "test_subtest_failure": "failed",
                                    "test_error": "error", "test_skip": "skipped"})
        self.assertTrue(all(case["seconds"] >= 0 for case in result.timings))

    def test_report_is_complete_json_and_replaces_a_previous_result(self):
        with tempfile.TemporaryDirectory() as temporary:
            path = Path(temporary) / "timing/report.json"
            run_checks.write_report(path, {"exit_code": 1})
            run_checks.write_report(path, {"exit_code": 0, "tests": ["完整记录"]})
            self.assertEqual(json.loads(path.read_text()), {"exit_code": 0, "tests": ["完整记录"]})
            self.assertEqual(list(path.parent.iterdir()), [path])

    def test_stage_failure_and_exception_are_recorded_and_stop_later_checks(self):
        for failure in (7, RuntimeError("synthetic stage failure")):
            with self.subTest(failure=failure), tempfile.TemporaryDirectory() as temporary:
                path = Path(temporary) / "report.json"
                options = {"side_effect": failure} if isinstance(failure, Exception) else {"return_value": failure}
                with patch.object(run_checks, "run_command", **options) as command, \
                     redirect_stdout(io.StringIO()), redirect_stderr(io.StringIO()):
                    code = run_checks.main(["--all-platforms", "--jobs", "1", "--report", str(path)])
                report = json.loads(path.read_text())
                self.assertEqual(code, 1 if isinstance(failure, Exception) else failure)
                self.assertEqual(report["exit_code"], code)
                self.assertEqual(len(report["stages"]), 1)
                self.assertEqual(report["stages"][0]["exit_code"], code)
                self.assertGreaterEqual(report["stages"][0]["seconds"], 0)
                self.assertEqual(command.call_count, 1)

        with tempfile.TemporaryDirectory() as temporary:
            path = Path(temporary) / "current.json"
            expected_profile = check_scope.current_profile()
            with patch.object(run_checks, "run_command", return_value=7) as command, \
                 redirect_stdout(io.StringIO()), redirect_stderr(io.StringIO()):
                self.assertEqual(run_checks.main(["--report", str(path)]), 7)
            report = json.loads(path.read_text())
            self.assertEqual(report["profile"], expected_profile)
            self.assertEqual(report["target"], check_scope.current_target())
            self.assertIn("--current-platform", command.call_args.args[0])


class ParallelCheckTest(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory(prefix="system-sync-check-runner-")
        self.addCleanup(temporary.cleanup)
        self.directory = Path(temporary.name).resolve()
        self.module = "test_fixture_" + self.directory.name.replace("-", "_")

    def run_cases(self, source, jobs=2):
        (self.directory / (self.module + ".py")).write_text(textwrap.dedent(source))
        report = {"tests": []}
        with redirect_stdout(io.StringIO()), redirect_stderr(io.StringIO()):
            code = run_checks.integration_tests(report, "all", jobs, self.directory / "report.json",
                                                directory=self.directory)
        return code, report

    def test_independent_processes_overlap_and_every_case_has_a_duration(self):
        # Each first case waits for a marker from another process: a serial
        # implementation fails rather than passing via a timing threshold.
        code, report = self.run_cases('''
            import os, time, unittest
            from pathlib import Path
            class Cases(unittest.TestCase):
                def overlap(self):
                    directory = Path(__file__).parent
                    (directory / ("process-" + str(os.getpid()))).touch()
                    deadline = time.monotonic() + 5
                    while len(list(directory.glob("process-*"))) < 2:
                        self.assertLess(time.monotonic(), deadline, "workers did not overlap")
                        time.sleep(0.01)
                def test_one(self): self.overlap()
                def test_two(self): self.overlap()
                def test_three(self): self.overlap()
                def test_four(self): self.overlap()
        ''')
        self.assertEqual(code, 0)
        self.assertEqual(report["tests_run"], 4)
        self.assertEqual(len({case["id"] for case in report["tests"]}), 4)
        self.assertTrue(all(case["outcome"] == "passed" and case["seconds"] >= 0 for case in report["tests"]))
        self.assertEqual(len(list(self.directory.glob("process-*"))), 2)

    def test_failure_and_subtest_failure_keep_nonzero_exit_and_complete_coverage(self):
        code, report = self.run_cases('''
            import unittest
            class Cases(unittest.TestCase):
                def test_fail(self):
                    with self.subTest(value=1): self.fail("synthetic failure")
                def test_pass(self): pass
        ''')
        self.assertEqual(code, 1)
        self.assertEqual(report["tests_run"], 2)
        self.assertEqual({case["outcome"] for case in report["tests"]}, {"passed", "failed"})

    def test_timing_sensitive_cases_run_after_parallel_workers_finish(self):
        code, report = self.run_cases('''
            import time, unittest
            from pathlib import Path
            from check_scope import serial
            directory = Path(__file__).parent
            @serial
            class A_sensitive(unittest.TestCase):
                def test_exclusive(self):
                    self.assertTrue((directory / "parallel-done").exists())
                    self.assertFalse((directory / "parallel-active").exists())
            class B_parallel(unittest.TestCase):
                def test_payload(self):
                    (directory / "parallel-active").touch()
                    time.sleep(0.02)
                    (directory / "parallel-active").unlink()
                    (directory / "parallel-done").touch()
        ''')
        self.assertEqual(code, 0)
        self.assertEqual(report["tests_run"], 2)
        self.assertEqual(len(report["tests_serial"]), 1)

    def test_zero_exit_without_worker_results_is_a_failure(self):
        code, report = self.run_cases('''
            import os, unittest
            class Cases(unittest.TestCase):
                def test_exit(self): os._exit(0)
        ''', jobs=1)
        self.assertEqual(code, 1)
        self.assertEqual(report["tests_run"], 0)

    def test_interruption_reaps_workers(self):
        (self.directory / (self.module + ".py")).write_text(
            "import time, unittest\nclass Cases(unittest.TestCase):\n def test_wait(self): time.sleep(30)\n")
        native_popen = subprocess.Popen
        processes = []

        def start(*args, **kwargs):
            process = native_popen(*args, **kwargs)
            processes.append(process)
            return process

        with patch.object(run_checks.subprocess, "Popen", side_effect=start), \
             patch.object(run_checks.time, "sleep", side_effect=KeyboardInterrupt), \
             redirect_stdout(io.StringIO()), redirect_stderr(io.StringIO()):
            with self.assertRaises(KeyboardInterrupt):
                run_checks.integration_tests({"tests": []}, "all", 1, self.directory / "report.json",
                                             directory=self.directory)
        self.assertTrue(processes)
        self.assertTrue(all(process.poll() is not None for process in processes))

    def test_previous_durations_balance_groups_without_dropping_or_duplicating_cases(self):
        cases = ["a", "b", "c", "d"]
        previous = [{"id": name, "seconds": seconds} for name, seconds in zip(cases, (8, 4, 3, 1))]
        groups = run_checks.test_groups(cases, 2, previous)
        self.assertEqual(sorted(name for group in groups for name in group), cases)
        weights = {item["id"]: item["seconds"] for item in previous}
        self.assertEqual(sorted(sum(weights[name] for name in group) for group in groups), [8, 8])
        self.assertEqual(run_checks.test_groups(cases, 1, previous), [cases])


class PlatformBundleCheckTest(unittest.TestCase):
    def test_current_flag_checks_each_executing_target_without_requiring_foreign_archives(self):
        import hashlib
        root = Path(__file__).resolve().parents[1]
        checksum = shutil.which("sha256sum") or shutil.which("shasum")
        options = "" if Path(checksum).name == "sha256sum" else "-a 256"
        with tempfile.TemporaryDirectory(prefix="system-sync-check-bundle-") as temporary:
            base = Path(temporary)
            scripts = base / "helper/scripts"
            scripts.mkdir(parents=True)
            shutil.copy2(root / "helper/scripts/verify.sh", scripts / "verify.sh")
            bundle = base / "helper/prebuilt/v0.0.0"
            bundle.mkdir(parents=True)
            names = {target: f"system-sync-helper_v0.0.0_{target}.tar.gz" for target in check_scope.TARGETS}
            payload = b"synthetic archive content"
            digest = hashlib.sha256(payload).hexdigest()
            (bundle / "SHA256SUMS").write_text("".join(f"{digest}  {name}\n" for name in names.values()))
            for target in check_scope.TARGETS:
                with self.subTest(target=target):
                    for archive in bundle.glob("*.tar.gz"):
                        archive.unlink()
                    archive = bundle / names[target]
                    archive.write_bytes(payload)
                    (bundle / "SOURCE_SHA256SUMS").write_text("source-record\n")
                    (scripts / "common.sh").write_text(
                        f'HELPER_DIR={shlex.quote(str(base / "helper"))}\n'
                        'helper_error() { printf "%s\\n" "$*" >&2; exit 1; }\n'
                        'helper_version() { printf "v0.0.0\\n"; }\n'
                        f'helper_target() {{ printf "{target}\\n"; }}\n'
                        'helper_source_manifest() { printf "source-record\\n"; }\n'
                        f'helper_checksum() {{ {shlex.quote(checksum)} {options} "$1" | awk \'{{ print $1 }}\'; }}\n'
                        'system_sync_step() { shift; "$@"; }\n')

                    def verify(*args):
                        return subprocess.run(["/bin/bash", str(scripts / "verify.sh"), *args],
                                              capture_output=True, text=True, timeout=5)

                    current = verify("--current-platform")
                    self.assertEqual(current.returncode, 0, current.stderr)
                    self.assertIn(target, current.stdout)
                    self.assertNotEqual(verify().returncode, 0)
                    archive.write_bytes(payload + b"corrupt")
                    self.assertIn("SHA256 校验失败", verify("--current-platform").stderr)
                    archive.write_bytes(payload)
                    (bundle / "SOURCE_SHA256SUMS").write_text("stale-source-record\n")
                    self.assertIn("构建输入已变化", verify("--current-platform").stderr)
