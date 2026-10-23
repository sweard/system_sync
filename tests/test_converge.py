"""Run the real shell transactions against isolated, stateful package-manager fakes."""

import json
import os
from pathlib import Path
import pty
import select
import shutil
import subprocess
import sys
import tempfile
import time
import unittest

from helper_fixture import operation_lock, prepare_helper
from check_scope import LINUX_PROFILES, for_platforms, matches, scope, selected_platforms


ROOT = Path(__file__).resolve().parents[1]


def run_terminal(command, environment, answers=(), timeout=25):
    """Only stdin needs a PTY; feed answers after seeing the actual prompt."""
    master, slave = pty.openpty()
    process = subprocess.Popen(command, env=environment, stdin=slave, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    os.close(slave)
    output, buffer = bytearray(), ""
    remaining = list(answers)
    deadline = time.monotonic() + timeout
    try:
        while time.monotonic() < deadline:
            ready, _, _ = select.select([process.stdout], [], [], 0.1)
            if ready:
                part = os.read(process.stdout.fileno(), 65536)
                if not part:
                    break
                output.extend(part)
                buffer += part.decode("utf-8", errors="replace")
                if remaining and remaining[0][0] in buffer:
                    _, response = remaining.pop(0)
                    os.write(master, (response + "\n").encode())
                    buffer = ""
            elif process.poll() is not None:
                break
        else:
            raise AssertionError("transaction timed out:\n" + output.decode(errors="replace"))
        code = process.wait(timeout=3)
        return subprocess.CompletedProcess(command, code, output.decode(errors="replace"))
    finally:
        if process.poll() is None:
            process.kill()
            process.wait()
        process.stdout.close()
        os.close(master)


FAKE = r'''#!/usr/bin/env python3
import json, os, sys
from pathlib import Path
name, args = Path(sys.argv[0]).name, sys.argv[1:]
if os.environ.get("FIXTURE_ALL_QUERIES"):
    with Path(os.environ["FIXTURE_ALL_QUERIES"]).open("a") as log:
        log.write(json.dumps([name, *args]) + "\n")
state_file = Path(os.environ["FIXTURE_STATE"])
state = json.loads(state_file.read_text())
profile = state["profile"]
manager = {"arch":"pacman", "debian":"apt", "fedora":"dnf", "opensuse":"zypper"}[profile]
def lines(values):
    print("\n".join(sorted(values)), end="\n" if values else "")
def mutate(kind, installed=None, explicit=None):
    journal = json.loads((Path(os.environ["SYSTEM_SYNC_RUN_LOG_DIR"]) / "transaction.json").read_text())
    before = Path(os.environ["SYSTEM_SYNC_RUN_LOG_DIR"]) / "installed.before.txt"
    if not before.exists():
        sys.exit("mutation happened before installed-state audit")
    with Path(os.environ["FIXTURE_LOG"]).open("a") as log:
        log.write(json.dumps({"kind":kind, "args":[name,*args], "phase":journal["phase"]}) + "\n")
    if installed is not None: state["installed"] = sorted(installed)
    if explicit is not None: state["explicit"] = sorted(explicit)
    state_file.write_text(json.dumps(state))
def orphans():
    final = state.get("marked") or (profile == "opensuse" and state.get("did_install"))
    return state.get("final_orphans", []) if final else state.get("initial_orphans", [])
def mark(dependency, names):
    explicit = set(state["explicit"])
    explicit = explicit - set(names) if dependency else explicit | set(names)
    state["marked"] = True
    mutate("mark", explicit=explicit)
def remove():
    names = set(orphans()) if name != "zypper" else {n for n in args[args.index("remove")+1:] if not n.startswith("-")}
    mutate("remove", set(state["installed"]) - names, set(state["explicit"]) - names)
def xml(names, group="solvable-list"):
    print('<stream><' + group + '>' + ''.join('<solvable name="' + n + '" kind="package"/>' for n in names) + '</' + group + '></stream>')
if name == "uname": print("Linux")
elif name == "sudo":
    if args != ["-v"]: os.execvp(args[0], args)
elif name == "mise":
    if os.environ.get("FIXTURE_QUERY_LOG"):
        with Path(os.environ["FIXTURE_QUERY_LOG"]).open("a") as log:
            log.write(json.dumps(args) + "\n")
    if args == ["--version"]: print(state.get("mise_version", "2026.10.11 fixture"))
    elif args[:3] == ["bootstrap", "packages", "status"]:
        if state.get("json_error"): print('{"changed_schema":true}')
        else:
            packages = [{"package":n, "state":state.get("override_state", "installed" if n in state["installed"] else "missing")} for n in state["declared"]]
            print(json.dumps({manager:{"available":not state.get("unavailable", False), "packages":packages}}))
    elif args[:3] == ["bootstrap", "packages", "apply"]:
        if "--dry-run" in args: print("fixture install preview")
        else:
            if state.get("live_progress"):
                assert "--raw" in args, args
                print("Fixture: native download", flush=True)
                assert input() == "download-seen"
                print("Fixture: native install", file=sys.stderr, flush=True)
                assert input() == "install-seen"
            state["did_install"] = True
            installed = set(state["installed"]) | set(state["declared"])
            if state.get("wrong_install_state"): installed = set(state["installed"])
            mutate("install", installed, set(state["explicit"]) | (installed & set(state["declared"])))
            if state.get("install_failure"): sys.exit(18)
    else: sys.exit("unexpected mise invocation: " + repr(args))
elif name == "dpkg-query": lines(["ii \t" + n for n in state["installed"]])
elif name == "apt-mark":
    if args == ["showmanual"]: lines(state["explicit"])
    elif args == ["showauto"]: lines(set(state["installed"]) - set(state["explicit"]))
    elif args[0] in ("manual", "auto"): mark(args[0] == "auto", args[1:])
    else: sys.exit("unexpected apt-mark")
elif name == "apt-get":
    if args == ["-s", "autoremove"]:
        if state.get("query_failure"): sys.exit(12)
        for n in orphans(): print("Remv " + n + " [1.0]")
    elif args == ["autoremove"]: remove()
    else: sys.exit("unexpected apt-get")
elif name == "dnf":
    if "--help" in args:
        if state.get("dnf4") and "dependency" in args: sys.exit(2)
    elif "repoquery" in args:
        if state.get("query_failure"): sys.exit(12)
        lines(state["explicit"] if "--userinstalled" in args else orphans())
    elif "mark" in args:
        index = args.index("mark")
        mark(args[index+1] in ("dependency", "remove"), args[index+2:])
    elif args == ["autoremove"]: remove()
    else: sys.exit("unexpected dnf")
elif name == "pacman":
    if args == ["-Qqe"]: lines(state["explicit"])
    elif args == ["-Qq"]: lines(state["installed"])
    elif args == ["-Qqd"]: lines(set(state["installed"]) - set(state["explicit"]))
    elif args == ["-Qdtq"]:
        if state.get("query_failure"): sys.exit("query failed")
        lines(orphans())
        if not orphans(): sys.exit(1)
    elif args[0] == "-D": mark(args[1] == "--asdeps", args[2:])
    elif args[0] == "-Rsp": lines(state.get("transaction", orphans()))
    elif args[0] == "-Rs": remove()
    else: sys.exit("unexpected pacman")
elif name == "rpm": lines(state["installed"])
elif name == "zypper":
    if "packages" in args:
        if state.get("query_failure"): sys.exit(12)
        if state.get("xml_error"): print("<broken>")
        else: xml(state["explicit"] if "--userinstalled" in args else orphans())
    elif "--dry-run" in args:
        targets = [n for n in args[args.index("remove")+1:] if not n.startswith("-")]
        transaction = state.get("transaction", targets)
        if state.get("did_install"): transaction = state.get("final_transaction", transaction)
        xml(transaction, "to-remove")
    elif "remove" in args: remove()
    else: sys.exit("unexpected zypper")
else: sys.exit("unexpected executable: " + name)
'''


@for_platforms("linux")
class ConvergeTest(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="system-sync-converge-")
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name) / "system sync"
        shutil.copytree(ROOT / "scripts", self.root / "scripts", ignore=shutil.ignore_patterns("__pycache__"))
        shutil.copytree(ROOT / "packages", self.root / "packages")
        prepare_helper(self.root)
        detector = self.root / "scripts/detect-linux-profile.sh"
        detector.write_text('#!/bin/sh\nprintf "%s\\n" "$FIXTURE_PROFILE"\n')
        commands = self.root / "bin"
        commands.mkdir()
        native = commands / "fixture"
        native.write_text(FAKE)
        native.chmod(0o755)
        for name in ("uname", "mise", "sudo", "dpkg-query", "apt-mark", "apt-get", "dnf", "pacman", "rpm", "zypper"):
            (commands / name).symlink_to(native)
        self.state_file = self.root / "state.json"
        self.log_file = self.root / "calls.jsonl"
        self.environment = dict(os.environ, PATH=str(commands) + os.pathsep + os.environ["PATH"],
                                FIXTURE_STATE=str(self.state_file), FIXTURE_LOG=str(self.log_file),
                                PYTHONDONTWRITEBYTECODE="1")
        self.environment.pop("SYSTEM_SYNC_LOCK_FD", None)
        self.environment.pop("SYSTEM_SYNC_RUN_LOG_DIR", None)

    def configure(self, profile=None, **options):
        if profile is None:
            profile = scope() if scope() in LINUX_PROFILES else "debian"
        if not matches((profile,)):
            self.skipTest("其他 Linux profile 保留在 --all-platforms 验收中")
        mandatory = {"arch":"base", "debian":"base-files", "fedora":"filesystem", "opensuse":"filesystem"}[profile]
        state = dict(profile=profile, installed=[mandatory, "git"], explicit=[mandatory, "git"],
                     declared=[mandatory, "git", "new-tool"])
        state.update(options)
        self.state_file.write_text(json.dumps(state))
        self.environment["FIXTURE_PROFILE"] = profile
        self.log_file.unlink(missing_ok=True)
        return state

    def run_sync(self, mode="--apply", answers=(), terminal=True, script="system-sync.sh", extra=()):
        command = ["/bin/bash", str(self.root / "scripts" / script), mode, *extra]
        if terminal:
            return run_terminal(command, self.environment, answers)
        return subprocess.run(command, env=self.environment, stdin=subprocess.DEVNULL,
                              stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True, timeout=25)

    def calls(self):
        return [json.loads(line) for line in self.log_file.read_text().splitlines()] if self.log_file.exists() else []

    def journal(self):
        journals = list((self.root / ".system-sync/history").glob("*/transaction.json"))
        self.assertTrue(journals)
        return json.loads(max(journals, key=lambda p:p.stat().st_mtime_ns).read_text())

    def test_noninteractive_apply_never_reaches_installer(self):
        for profile in selected_platforms(("arch", "debian", "fedora", "opensuse")):
            with self.subTest(profile=profile):
                self.configure(profile)
                result = self.run_sync(terminal=False)
                self.assertNotEqual(result.returncode, 0, result.stdout)
                self.assertIn("交互式终端", result.stdout)
                self.assertEqual(self.calls(), [])

    def test_linux_install_output_is_live_through_system_sync_for_all_profiles(self):
        for profile in selected_platforms(("arch", "debian", "fedora", "opensuse")):
            with self.subTest(profile=profile):
                self.configure(profile, live_progress=True)
                result = self.run_sync(answers=[("输入 yes 继续", "yes"),
                                               ("Fixture: native download", "download-seen"),
                                               ("Fixture: native install", "install-seen")])
                self.assertEqual(result.returncode, 0, result.stdout)
                self.assertEqual(self.journal()["phase"], "complete")
                self.assertIn("Fixture: native install", result.stdout)

    def test_wrapper_status_uses_one_preflight_and_one_package_snapshot(self):
        queries = self.root / "query-calls.jsonl"
        self.environment["FIXTURE_QUERY_LOG"] = str(queries)
        for profile in selected_platforms(("arch", "debian", "fedora", "opensuse")):
            with self.subTest(profile=profile):
                self.configure(profile)
                queries.unlink(missing_ok=True)
                result = self.run_sync("--status", terminal=False)
                self.assertEqual(result.returncode, 0, result.stdout)
                commands = [json.loads(line) for line in queries.read_text().splitlines()]
                self.assertEqual(commands.count(["--version"]), 1)
                snapshots = [command for command in commands if command[:3] == ["bootstrap", "packages", "status"]]
                self.assertEqual(snapshots, [["bootstrap", "packages", "status", "--json"]])
                self.assertIn("git", result.stdout)
                self.assertIn("new-tool", result.stdout)
                self.assertEqual(self.calls(), [])

    def test_readonly_wrappers_reject_old_mise_before_package_queries(self):
        queries = self.root / "query-calls.jsonl"
        self.environment["FIXTURE_QUERY_LOG"] = str(queries)
        for mode in ("--status", "--dry-run"):
            with self.subTest(mode=mode):
                self.configure(mise_version="2026.10.10 fixture")
                queries.unlink(missing_ok=True)
                result = self.run_sync(mode, terminal=False)
                self.assertNotEqual(result.returncode, 0, result.stdout)
                self.assertEqual([json.loads(line) for line in queries.read_text().splitlines()], [["--version"]])
                self.assertEqual(self.calls(), [])

    def test_cancel_before_install_is_audited(self):
        for profile in selected_platforms(("arch", "debian", "fedora", "opensuse")):
            old_phrase = "APPLY ZYPPER" if profile == "opensuse" else "APPLY 0"
            for response in ("", "no", "y", old_phrase):
                with self.subTest(profile=profile, response=response):
                    self.configure(profile)
                    result = self.run_sync(answers=[("输入 yes 继续", response)])
                    self.assertEqual(result.returncode, 2, result.stdout)
                    self.assertEqual(self.calls(), [])
                    self.assertEqual(self.journal()["phase"], "cancelled")

    def test_missing_packages_install_inside_audited_transaction(self):
        for profile in selected_platforms(("arch", "debian", "fedora", "opensuse")):
            with self.subTest(profile=profile):
                self.configure(profile)
                result = self.run_sync(answers=[("输入 yes 继续", "yes")])
                self.assertEqual(result.returncode, 0, result.stdout)
                self.assertEqual([call["kind"] for call in self.calls()], ["install"])
                self.assertEqual(self.calls()[0]["phase"], "installing")
                self.assertEqual(self.journal()["phase"], "complete")

    def test_partial_install_failure_keeps_audit_and_stops_before_marking(self):
        self.configure(install_failure=True)
        result = self.run_sync(answers=[("输入 yes 继续", "yes")])
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertEqual([call["kind"] for call in self.calls()], ["install"])
        self.assertEqual(self.journal()["phase"], "failed")
        self.assertEqual(self.journal()["last_phase"], "installing")

    def test_successful_install_with_missing_target_stops(self):
        self.configure(wrong_install_state=True)
        result = self.run_sync(answers=[("输入 yes 继续", "yes")])
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertIn("安装后仍有声明包", result.stdout)
        self.assertEqual([call["kind"] for call in self.calls()], ["install"])

    @for_platforms("arch", "debian", "fedora")
    def test_large_baseline_change_is_rejected_before_install(self):
        for profile in selected_platforms(("arch", "debian", "fedora")):
            with self.subTest(profile=profile):
                mandatory = {"arch":"base", "debian":"base-files", "fedora":"filesystem"}[profile]
                names = [mandatory, "git"] + [f"extra-{i}" for i in range(6)]
                self.configure(profile, installed=names, explicit=names)
                result = self.run_sync()
                self.assertNotEqual(result.returncode, 0, result.stdout)
                self.assertIn("大范围变更保护", result.stdout)
                self.assertEqual(self.calls(), [])

    def test_schema_and_manager_failures_stop_before_install(self):
        for profile in selected_platforms(("arch", "debian", "fedora", "opensuse")):
            for error in ("json_error", "unavailable", "query_failure"):
                with self.subTest(profile=profile, error=error):
                    self.configure(profile, **{error:True})
                    result = self.run_sync()
                    self.assertNotEqual(result.returncode, 0, result.stdout)
                    self.assertEqual(self.calls(), [])

    def test_absent_or_unknown_status_never_reaches_installer(self):
        for state in ("skipped", "unexpectedly_installed", "future-state"):
            with self.subTest(state=state):
                self.configure(override_state=state)
                result = self.run_sync()
                self.assertNotEqual(result.returncode, 0, result.stdout)
                self.assertEqual(self.calls(), [])

    @for_platforms("arch", "debian", "fedora")
    def test_protected_final_transaction_stops_removal(self):
        for profile in selected_platforms(("arch", "debian", "fedora")):
            with self.subTest(profile=profile):
                mandatory = {"arch":"base", "debian":"base-files", "fedora":"filesystem"}[profile]
                self.configure(profile, installed=[mandatory,"git","extra"], explicit=[mandatory,"git","extra"],
                               final_orphans=["extra","git"], transaction=["extra","git"])
                result = self.run_sync(answers=[("输入 yes 继续", "yes")])
                self.assertNotEqual(result.returncode, 0, result.stdout)
                self.assertNotIn("remove", [call["kind"] for call in self.calls()])
                self.assertIn("保护", result.stdout)

    @for_platforms("debian")
    def test_cancel_after_marking_preserves_packages_and_restoration_list(self):
        self.configure(installed=["base-files","git","extra"], explicit=["base-files","git","extra"], final_orphans=["extra"])
        result = self.run_sync(answers=[("输入 yes 继续", "yes"), ("即将移除 1 个包，输入 yes", "no")])
        self.assertEqual(result.returncode, 2, result.stdout)
        self.assertEqual([call["kind"] for call in self.calls()], ["install","mark"])
        self.assertIn("extra", json.loads(self.state_file.read_text())["installed"])
        self.assertEqual(self.journal()["phase"], "cancelled")
        demoted = list((self.root / ".system-sync/history").glob("*/demoted.txt"))
        self.assertIn("extra", demoted[-1].read_text())

    @for_platforms("arch", "debian", "fedora")
    def test_full_reason_convergence_and_dnf4(self):
        for profile, dnf4 in selected_platforms((("arch",False), ("debian",False), ("fedora",False), ("fedora",True)), key=lambda item: item[0]):
            with self.subTest(profile=profile, dnf4=dnf4):
                mandatory = {"arch":"base", "debian":"base-files", "fedora":"filesystem"}[profile]
                self.configure(profile, installed=[mandatory,"git","extra"], explicit=[mandatory,"git","extra"],
                               final_orphans=["extra"], dnf4=dnf4)
                result = self.run_sync(answers=[("输入 yes 继续", "yes"), ("即将移除 1 个包，输入 yes", "yes")])
                self.assertEqual(result.returncode, 0, result.stdout)
                self.assertEqual([call["kind"] for call in self.calls()], ["install","mark","remove"])
                self.assertNotIn("extra", json.loads(self.state_file.read_text())["installed"])

    def test_dry_run_never_mutates(self):
        for profile in selected_platforms(("arch", "debian", "fedora", "opensuse")):
            with self.subTest(profile=profile):
                self.configure(profile)
                result = self.run_sync("--dry-run", terminal=False)
                self.assertEqual(result.returncode, 0, result.stdout)
                self.assertEqual(self.calls(), [])
                self.assertFalse((self.root / ".system-sync/history").exists())

    def test_status_skips_install_and_cleanup_solvers_while_dry_run_includes_them(self):
        query_log = self.root / "all-queries.jsonl"
        self.environment["FIXTURE_ALL_QUERIES"] = str(query_log)
        for profile in selected_platforms(("arch", "debian", "fedora", "opensuse")):
            with self.subTest(profile=profile):
                self.configure(profile, initial_orphans=["extra"])
                query_log.unlink(missing_ok=True)
                result = self.run_sync("--status", terminal=False)
                self.assertEqual(result.returncode, 0, result.stdout)
                calls = [json.loads(line) for line in query_log.read_text().splitlines()]
                self.assertFalse(any("apply" in call or "autoremove" in call or "--unneeded" in call
                                     or "-Qdtq" in call or "--dry-run" in call for call in calls), calls)
                self.assertIn("package-dry-run", result.stdout)
                query_log.unlink()
                result = self.run_sync("--dry-run", terminal=False)
                self.assertEqual(result.returncode, 0, result.stdout)
                calls = [json.loads(line) for line in query_log.read_text().splitlines()]
                self.assertIn(["mise", "bootstrap", "packages", "apply", "--dry-run"], calls)
                self.assertTrue(any("autoremove" in call or "--unneeded" in call or "-Qdtq" in call
                                    for call in calls), calls)
                self.assertEqual(self.calls(), [])

    def test_missing_mandatory_root_stops_before_install(self):
        for profile in selected_platforms(("arch", "debian", "fedora", "opensuse")):
            with self.subTest(profile=profile):
                self.configure(profile, declared=["git", "new-tool"])
                result = self.run_sync()
                self.assertNotEqual(result.returncode, 0, result.stdout)
                self.assertEqual(self.calls(), [])

    def test_direct_platform_scripts_enforce_noninteractive_guard(self):
        for profile, script in selected_platforms((("arch","pacman-converge.sh"), ("debian","apt-converge.sh"),
                                ("fedora","dnf-converge.sh"), ("opensuse","zypper-converge.sh")), key=lambda item: item[0]):
            with self.subTest(profile=profile):
                self.configure(profile)
                result = self.run_sync(script=script, terminal=False)
                self.assertNotEqual(result.returncode, 0, result.stdout)
                self.assertIn("交互式终端", result.stdout)
                self.assertEqual(self.calls(), [])

    @for_platforms("opensuse")
    def test_zypper_native_install_then_protected_cleanup(self):
        self.configure("opensuse", installed=["filesystem","git","extra"], explicit=["filesystem","git","extra"])
        result = self.run_sync(answers=[("输入 yes 继续", "yes"), ("即将移除 1 个包，输入 yes", "yes")])
        self.assertEqual(result.returncode, 0, result.stdout)
        self.assertEqual([call["kind"] for call in self.calls()], ["install","remove"])
        self.assertEqual(self.calls()[0]["args"], ["mise","bootstrap","packages","apply","--manager","zypper","--raw"])
        self.assertEqual(self.calls()[1]["phase"], "removing")
        self.assertNotIn("extra", json.loads(self.state_file.read_text())["installed"])
        self.assertEqual(self.journal()["phase"], "complete")

    @for_platforms("opensuse")
    def test_zypper_revalidates_protected_transaction_after_install(self):
        self.configure("opensuse", installed=["filesystem","git","extra"], explicit=["filesystem","git","extra"],
                       final_transaction=["extra","git"])
        result = self.run_sync(answers=[("输入 yes 继续", "yes")])
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertIn("保护包", result.stdout)
        self.assertEqual([call["kind"] for call in self.calls()], ["install"])

    @for_platforms("opensuse")
    def test_zypper_large_or_malformed_plan_stops_before_install(self):
        extra = [f"extra-{i}" for i in range(26)]
        self.configure("opensuse", installed=["filesystem","git",*extra], explicit=["filesystem","git",*extra])
        result = self.run_sync()
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertIn("安全阈值", result.stdout)
        self.assertEqual(self.calls(), [])
        self.configure("opensuse", xml_error=True)
        result = self.run_sync()
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertIn("无法解析", result.stdout)
        self.assertEqual(self.calls(), [])

    def test_old_mise_is_rejected_before_mutation(self):
        self.configure(mise_version="2026.9.0 fixture")
        result = self.run_sync()
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertIn("2026.10.11", result.stdout)
        self.assertEqual(self.calls(), [])

    def test_zero_exit_without_completion_marker_is_rejected(self):
        self.configure()
        command = [str(self.root / "helper/scripts/run.sh"), "runtime",
                   "--", "/bin/sh", "-c", "exit 0"]
        result = run_terminal(command, self.environment)
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertIn("未记录事务完成", result.stdout)
        self.assertEqual(self.journal()["phase"], "failed")

    def test_shared_lock_blocks_all_mutating_entrypoints(self):
        self.configure()
        commands = (
            ["/bin/bash", str(self.root / "scripts/system-sync.sh"), "--apply"],
            [str(self.root / "helper/scripts/run.sh"), "package", "install", "new-tool"],
            ["/bin/bash", str(self.root / "scripts/homebrew-converge.sh"), "--upgrade"],
        )
        if scope() != "all":
            commands = commands[:2]
        with operation_lock(self.root):
            for command in commands:
                with self.subTest(command=command):
                    result = run_terminal(command, self.environment)
                    self.assertNotEqual(result.returncode, 0, result.stdout)
                    self.assertIn("另一条配置或包操作", result.stdout)
        self.assertEqual(self.calls(), [])
