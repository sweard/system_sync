"""Fixtures for the upstream status contracts that gate destructive operations."""

import json
from pathlib import Path
import subprocess
import tempfile
import unittest

from helper_fixture import prepare_helper


class ParserFixture(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory(prefix="system-sync-parser-")
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name) / "repo with spaces 中文"
        prepare_helper(self.root)

    def invoke(self, arguments, text, success=True):
        result = subprocess.run([str(self.root / "helper/scripts/run.sh"), *arguments], input=text,
                                capture_output=True, text=True, timeout=10)
        self.assertEqual(result.returncode, 0 if success else 2, result.stdout + result.stderr)
        if not success:
            self.assertEqual(result.stdout, "", "invalid input produced partial package names")
        return result.stdout.splitlines()

    def status(self, data, manager="apt", success=True):
        lines = self.invoke(["parse-status", "--manager", manager, "-"], json.dumps(data), success)
        return [tuple(line.split("\t")) for line in lines]

    def zypper(self, mode, text, success=True):
        return self.invoke(["parse-zypper", mode, "-"], text, success)


class StatusParserTest(ParserFixture):
    def test_current_managers_and_deterministic_deduplication(self):
        for manager in ("apt", "dnf", "pacman", "aur", "zypper"):
            with self.subTest(manager=manager):
                item = {"package":"git", "state":"installed"}
                data = {manager:{"available":True, "packages":[item,item]}}
                self.assertEqual(self.status(data, manager), [(manager,"git","installed")])

    def test_supported_legacy_shapes(self):
        item = {"manager":"apt", "name":"git", "state":"installed"}
        for data in ({"apt":[item]}, {"packages":[item]}, [item]):
            self.assertEqual(self.status(data), [("apt","git","installed")])

    def test_unavailable_empty_and_changed_schema_fail_closed(self):
        for data in ({}, [], {"apt":{"available":False,"reason":"not installed"}},
                     {"apt":{"available":True,"packages":[]}}, {"apt":{"available":True,"items":[]}},
                     {"apt":{"packages":{}}}, {"packages":["git"]}):
            with self.subTest(data=data):
                self.status(data, success=False)

    def test_unsafe_package_names_are_rejected(self):
        for name in ("--help", "git;id", "../git", "git\nbase", "git*"):
            with self.subTest(name=name):
                self.status({"apt":[{"package":name,"state":"installed"}]}, success=False)

    def test_unknown_skipped_and_absent_states_fail_closed(self):
        for state in ("unknown", "skipped", "absent", "unexpectedly_installed", "future-state", None, [], {}):
            with self.subTest(state=state):
                self.status({"apt":[{"package":"git","state":state}]}, success=False)
        self.status({"apt":[{"package":"git","state":"installed","desired_state":"absent"}]}, success=False)


class ZypperParserTest(ParserFixture):
    def test_namespaces_and_non_package_query_items(self):
        text = '<stream xmlns="fixture"><solvable name="git" kind="package"/><solvable name="desktop" kind="pattern"/></stream>'
        self.assertEqual(self.zypper("solvables", text), ["git"])

    def test_only_removals_are_extracted(self):
        text = '<stream><to-install><solvable name="keep"/></to-install><to-remove><solvable><name>extra</name><kind>package</kind></solvable></to-remove></stream>'
        self.assertEqual(self.zypper("to-remove", text), ["extra"])

    def test_non_package_removal_and_unsafe_names_fail(self):
        for item in ('<solvable name="desktop" kind="pattern"/>', '<solvable name="--help"/>', '<solvable/>'):
            with self.subTest(item=item):
                self.zypper("to-remove", '<stream><to-remove><solvable name="valid-before-error"/>' + item + '</to-remove></stream>', success=False)
