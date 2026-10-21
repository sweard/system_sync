"""Fixtures for the upstream status contracts that gate destructive operations."""

import importlib.util
from pathlib import Path
import unittest
import xml.etree.ElementTree as ET


ROOT = Path(__file__).resolve().parents[1]


def load(name):
    spec = importlib.util.spec_from_file_location(name.replace("-", "_"), ROOT / "tests/reference" / (name + ".py"))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


MISE = load("extract-mise-packages")
ZYPPER = load("parse-zypper-xml")


class StatusParserTest(unittest.TestCase):
    def test_current_managers_and_deterministic_deduplication(self):
        for manager in ("apt", "dnf", "pacman", "aur", "zypper"):
            with self.subTest(manager=manager):
                item = {"package":"git", "state":"installed"}
                data = {manager:{"available":True, "packages":[item,item]}}
                self.assertEqual(MISE.parse_status(data, (manager,)), [(manager,"git","installed")])

    def test_supported_legacy_shapes(self):
        item = {"manager":"apt", "name":"git", "state":"installed"}
        for data in ({"apt":[item]}, {"packages":[item]}, [item]):
            self.assertEqual(MISE.parse_status(data, ("apt",)), [("apt","git","installed")])

    def test_unavailable_empty_and_changed_schema_fail_closed(self):
        for data in ({}, [], {"apt":{"available":False,"reason":"not installed"}},
                     {"apt":{"available":True,"packages":[]}}, {"apt":{"available":True,"items":[]}},
                     {"apt":{"packages":{}}}, {"packages":["git"]}):
            with self.subTest(data=data), self.assertRaises(MISE.StatusFormatError):
                MISE.parse_status(data, ("apt",))

    def test_unsafe_package_names_are_rejected(self):
        for name in ("--help", "git;id", "../git", "git\nbase", "git*"):
            with self.subTest(name=name), self.assertRaises(MISE.StatusFormatError):
                MISE.parse_status({"apt":[{"package":name,"state":"installed"}]}, ("apt",))

    def test_unknown_skipped_and_absent_states_fail_closed(self):
        for state in ("unknown", "skipped", "absent", "unexpectedly_installed", "future-state", None, [], {}):
            with self.subTest(state=state), self.assertRaises(MISE.StatusFormatError):
                MISE.parse_status({"apt":[{"package":"git","state":state}]}, ("apt",))
        with self.assertRaises(MISE.StatusFormatError):
            MISE.parse_status({"apt":[{"package":"git","state":"installed","desired_state":"absent"}]}, ("apt",))


class ZypperParserTest(unittest.TestCase):
    def test_namespaces_and_non_package_query_items(self):
        root = ET.fromstring('<stream xmlns="fixture"><solvable name="git" kind="package"/><solvable name="desktop" kind="pattern"/></stream>')
        self.assertEqual(list(ZYPPER.query_solvables(root)), ["git"])

    def test_only_removals_are_extracted(self):
        root = ET.fromstring('<stream><to-install><solvable name="keep"/></to-install><to-remove><solvable><name>extra</name><kind>package</kind></solvable></to-remove></stream>')
        self.assertEqual(list(ZYPPER.transaction_removals(root)), ["extra"])

    def test_non_package_removal_and_unsafe_names_fail(self):
        for item in ('<solvable name="desktop" kind="pattern"/>', '<solvable name="--help"/>', '<solvable/>'):
            with self.subTest(item=item), self.assertRaises(ZYPPER.ZypperXmlError):
                list(ZYPPER.transaction_removals(ET.fromstring('<stream><to-remove>' + item + '</to-remove></stream>')))
