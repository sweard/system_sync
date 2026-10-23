#!/usr/bin/env python3
"""Time development checks; daily tasks do not depend on this Python runner."""

import argparse
import json
import math
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import time
import unittest

from check_scope import PROFILE_ENV, applies, current_profile, current_target, is_serial, scope


ROOT = Path(__file__).resolve().parents[1]
TESTS = ROOT / "tests"


def write_report(path, report):
    path.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.NamedTemporaryFile("w", encoding="utf-8", dir=path.parent, delete=False) as output:
        temporary = Path(output.name)
        try:
            json.dump(report, output, ensure_ascii=False, indent=2)
            output.write("\n")
        except BaseException:
            temporary.unlink(missing_ok=True)
            raise
    temporary.replace(path)


class TimedResult(unittest.TextTestResult):
    def __init__(self, *args, event_stream=None, **kwargs):
        super().__init__(*args, **kwargs)
        self.timings = []
        self.event_stream = event_stream

    def startTest(self, test):
        self.started = time.perf_counter()
        self.outcome = "passed"
        super().startTest(test)

    def addFailure(self, test, error):
        self.outcome = "failed"
        super().addFailure(test, error)

    def addError(self, test, error):
        self.outcome = "error"
        super().addError(test, error)

    def addSkip(self, test, reason):
        self.outcome = "skipped"
        super().addSkip(test, reason)

    def addSubTest(self, test, subtest, error):
        if error is not None:
            self.outcome = "failed"
        super().addSubTest(test, subtest, error)

    def addExpectedFailure(self, test, error):
        self.outcome = "expected_failure"
        super().addExpectedFailure(test, error)

    def addUnexpectedSuccess(self, test):
        self.outcome = "unexpected_success"
        super().addUnexpectedSuccess(test)

    def stopTest(self, test):
        record = {"id": test.id(), "seconds": time.perf_counter() - self.started,
                  "outcome": self.outcome}
        self.timings.append(record)
        if self.event_stream:
            self.event_stream.write(json.dumps(record) + "\n")
            self.event_stream.flush()
        super().stopTest(test)


def flatten(suite):
    for item in suite:
        if isinstance(item, unittest.TestSuite):
            yield from flatten(item)
        else:
            yield item


def discover_tests(directory, selected):
    loader = unittest.TestLoader()
    cases = list(flatten(loader.discover(str(directory))))
    if loader.errors:
        raise ValueError("集成测试导入失败：\n" + "\n".join(loader.errors))
    included = [case.id() for case in cases if applies(case, selected)]
    excluded = [case.id() for case in cases if not applies(case, selected)]
    if not included or len(set(included)) != len(included):
        raise ValueError("集成测试清单为空或包含重复用例")
    return included, excluded


def test_groups(cases, jobs, previous=()):
    groups = [[] for _ in range(min(jobs, len(cases)))]
    if len(groups) == 1:
        return [cases]
    weights = {item["id"]: item["seconds"] for item in previous
               if isinstance(item, dict) and isinstance(item.get("id"), str)
               and isinstance(item.get("seconds"), (int, float)) and math.isfinite(item["seconds"]) and item["seconds"] >= 0}
    loads = [0.0] * len(groups)
    # Whole test methods keep their setUp/cleanup and subtests in one process.
    for name in sorted(cases, key=lambda name: (-weights.get(name, 1.0), name)):
        index = min(range(len(groups)), key=lambda index: loads[index])
        groups[index].append(name)
        loads[index] += weights.get(name, 1.0)
    return [sorted(group) for group in groups if group]


def worker(spec_path):
    spec = json.loads(spec_path.read_text())
    os.environ[PROFILE_ENV] = spec["profile"]
    scope()  # Reject invalid scope before importing any tests.
    sys.path.insert(0, spec["directory"])
    suite = unittest.TestLoader().loadTestsFromNames(spec["tests"])
    result = None
    code = 1
    started = time.perf_counter()
    try:
        with Path(spec["events"]).open("w") as events:
            runner = unittest.TextTestRunner(verbosity=2,
                        resultclass=lambda *args, **kwargs: TimedResult(*args, event_stream=events, **kwargs))
            result = runner.run(suite)
        code = 0 if result.wasSuccessful() else 1
    except KeyboardInterrupt:
        code = 130
    finally:
        write_report(Path(spec["summary"]), {"exit_code": code, "seconds": time.perf_counter() - started,
                     "tests_run": result.testsRun if result else 0, "tests": result.timings if result else []})
    return code


def stop_processes(processes):
    for signum in (signal.SIGINT, signal.SIGTERM, signal.SIGKILL):
        live = [process for process in processes if process.poll() is None]
        if not live:
            return
        for process in live:
            try:
                os.killpg(process.pid, signum)
            except ProcessLookupError:
                pass
        for process in live:
            try:
                process.wait(timeout=2)
            except subprocess.TimeoutExpired:
                pass


def finished_events(path):
    if not path.is_file():
        return 0
    return path.read_bytes().count(b"\n")


def integration_tests(report, selected, jobs, report_path, previous=(), directory=TESTS):
    included, excluded = discover_tests(directory, selected)
    report.update(tests_selected=len(included), tests_excluded=excluded, tests_run=0)
    cases = flatten(unittest.TestLoader().loadTestsFromNames(included))
    serial_names = [test.id() for test in cases if is_serial(test)] if jobs > 1 else []
    report["tests_serial"] = serial_names
    parallel_names = [name for name in included if name not in serial_names]
    groups = test_groups(parallel_names, jobs, previous) if parallel_names else []
    logs = Path(tempfile.mkdtemp(prefix=report_path.stem + "-", dir=report_path.parent))
    report["logs"] = str(logs)
    mode = f"{len(groups)} 个并行进程" if jobs > 1 else "单进程串行"
    print(f"[~] 集成测试：选择 {len(included)} 项，其他平台 {len(excluded)} 项；{mode}，{len(serial_names)} 项时序测试随后串行", flush=True)
    processes = []
    started = last_feedback = time.perf_counter()
    code = 0

    def launch(names):
        number = len(processes) + 1
        stem = logs / f"worker-{number:02}"
        spec = {"profile": selected, "directory": str(directory), "tests": names,
                "events": str(stem.with_suffix(".jsonl")), "summary": str(stem.with_suffix(".json"))}
        write_report(stem.with_suffix(".spec.json"), spec)
        with stem.with_suffix(".log").open("w") as output:
            process = subprocess.Popen([sys.executable, "-B", str(Path(__file__).resolve()), "--worker",
                                        str(stem.with_suffix(".spec.json"))], cwd=ROOT,
                                       env=dict(os.environ, PYTHONDONTWRITEBYTECODE="1"),
                                       stdout=output, stderr=subprocess.STDOUT, start_new_session=True)
        processes.append({"process": process, "stem": stem, "tests": names, "done": False})

    try:
        for names in groups:
            launch(names)
        while True:
            if all(item["done"] for item in processes):
                if serial_names:
                    print(f"[~] 串行检查 {len(serial_names)} 项时序敏感测试", flush=True)
                    launch(serial_names)
                    serial_names = []
                else:
                    break
            for item in processes:
                rc = item["process"].poll()
                if item["done"] or rc is None:
                    continue
                item["done"] = True
                try:
                    summary = json.loads(item["stem"].with_suffix(".json").read_text())
                    records = summary["tests"]
                    actual = [record["id"] for record in records]
                    complete = len(actual) == len(item["tests"]) and set(actual) == set(item["tests"])
                    success = rc == 0 and summary["exit_code"] == 0 and complete
                    report["tests"].extend(records)
                    report["tests_run"] += summary["tests_run"]
                except (OSError, ValueError, KeyError, TypeError):
                    success = False
                    summary = {"seconds": time.perf_counter() - started}
                print(f"[{'+' if success else 'x'}] {item['stem'].name}：{summary['seconds']:.2f}s", flush=True)
                if not success:
                    code = 1
                    print("[x] 集成测试失败或进程结果不完整；原始输出：", file=sys.stderr)
                    print(item["stem"].with_suffix(".log").read_text(errors="replace"), file=sys.stderr)
            now = time.perf_counter()
            if now - last_feedback >= 10:
                completed = sum(finished_events(item["stem"].with_suffix(".jsonl")) for item in processes)
                print(f"[~] 集成测试已完成 {completed}/{len(included)} 项，已耗时 {now - started:.1f}s", flush=True)
                last_feedback = now
            if not all(item["done"] for item in processes):
                time.sleep(0.05)
    finally:
        stop_processes([item["process"] for item in processes])
    report["tests"].sort(key=lambda record: record["id"])
    print(f"[{'+' if code == 0 else 'x'}] 集成测试完成：{report['tests_run']} 项；详细日志：{logs}", flush=True)
    return code


def run_command(command):
    process = subprocess.Popen(command, cwd=ROOT, start_new_session=True)
    started = time.perf_counter()
    try:
        while True:
            try:
                return process.wait(timeout=10)
            except subprocess.TimeoutExpired:
                print(f"[~] 当前检查阶段仍在执行，已耗时 {time.perf_counter() - started:.1f}s", flush=True)
    except BaseException:
        stop_processes([process])
        raise


def main(argv=None):
    parser = argparse.ArgumentParser(description="执行开发检查并记录阶段与集成测试耗时")
    parser.add_argument("--all-platforms", action="store_true", help="运行完整跨平台验收")
    parser.add_argument("--jobs", type=int, default=min(2, os.cpu_count() or 1), help="集成测试进程数，默认最多 2；1 为串行")
    parser.add_argument("--report", type=Path, default=Path(".system-sync/check/latest.json"), help="JSON 耗时报告路径")
    parser.add_argument("--worker", type=Path, help=argparse.SUPPRESS)
    args = parser.parse_args(argv)
    if sys.version_info < (3, 11):
        parser.error("检查需要 Python 3.11+")
    if args.worker:
        return worker(args.worker)
    if args.jobs < 1:
        parser.error("--jobs 必须大于零")
    for path in (ROOT / ".system-sync", ROOT / ".system-sync/check"):
        if path.is_symlink():
            parser.error(f"检查结果目录不能是符号链接：{path}")
    report_path = args.report if args.report.is_absolute() else ROOT / args.report
    report_path.parent.mkdir(parents=True, exist_ok=True)
    try:
        previous = json.loads(report_path.read_text()).get("tests", [])
    except (OSError, ValueError, AttributeError):
        previous = []
    if not isinstance(previous, list):
        previous = []
    try:
        selected = "all" if args.all_platforms else current_profile()
        target = current_target()
    except ValueError as error:
        parser.error(str(error))
    report = {"schema": 1, "scope": "all" if args.all_platforms else "current",
              "profile": selected, "target": target, "jobs": args.jobs, "stages": [], "tests": []}
    verify = [str(ROOT / "helper/scripts/verify.sh")]
    if not args.all_platforms:
        verify.append("--current-platform")
    stages = [
        ("预编译包与构建输入", verify),
        ("本机 helper 构建与启用", [str(ROOT / "helper/scripts/build.sh")]),
        ("开发环境预检", [sys.executable, "-B", str(TESTS / "check_environment.py")]),
        ("mise task 定义", ["mise", "tasks", "validate"]),
        ("Go 测试与 race", [str(ROOT / "helper/scripts/test.sh")]),
        ("Python 集成测试", None),
    ]
    started = time.perf_counter()
    code = 0
    try:
        for name, command in stages:
            print(f"[~] 开始检查：{name}", flush=True)
            stage_started = time.perf_counter()
            try:
                code = run_command(command) if command else integration_tests(report, selected, args.jobs, report_path, previous)
            except (Exception, KeyboardInterrupt) as error:
                code = 130 if isinstance(error, KeyboardInterrupt) else 1
                print(f"[x] {name}中止：{error or '收到中断信号'}", file=sys.stderr)
            duration = time.perf_counter() - stage_started
            report["stages"].append({"name": name, "seconds": duration, "exit_code": code})
            print(f"[{'+' if code == 0 else 'x'}] {name}：{duration:.2f}s（退出码 {code}）", flush=True)
            if code:
                break
    except (OSError, KeyboardInterrupt) as error:
        code = 130 if isinstance(error, KeyboardInterrupt) else 1
        print(f"[x] 检查中止：{error or '收到中断信号'}", file=sys.stderr)
    finally:
        report["seconds"] = time.perf_counter() - started
        report["exit_code"] = code
        write_report(report_path, report)
        print("\n[~] 检查阶段耗时", flush=True)
        for stage in report["stages"]:
            print(f"  {stage['seconds']:8.2f}s  {stage['name']}", flush=True)
        print("[~] 最慢的 10 项集成测试", flush=True)
        for case in sorted(report["tests"], key=lambda item: item["seconds"], reverse=True)[:10]:
            print(f"  {case['seconds']:8.2f}s  {case['id']}", flush=True)
        print(f"[{'+' if code == 0 else 'x'}] 总耗时 {report['seconds']:.2f}s；报告：{report_path}", flush=True)
    return code


if __name__ == "__main__":
    raise SystemExit(main())
