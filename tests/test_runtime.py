"""The supported Python floor must be checked before querying or changing packages."""

import importlib.util
from pathlib import Path
import unittest
from unittest.mock import patch


ROOT = Path(__file__).resolve().parents[1]
SPEC = importlib.util.spec_from_file_location("runtime_preflight", ROOT / "tests/reference/sync_runtime.py")
RUNTIME = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(RUNTIME)


class RuntimePreflightTest(unittest.TestCase):
    def test_old_python_is_rejected_before_native_commands(self):
        with patch.object(RUNTIME.sys, "version_info", (3, 10, 0)), patch.object(RUNTIME.shutil, "which") as query:
            with self.assertRaisesRegex(RUNTIME.RuntimeFailure, "Python 3.11"):
                RUNTIME.preflight(ROOT)
            query.assert_not_called()
