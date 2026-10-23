"""The supported Python floor must be checked before querying or changing packages."""

from contextlib import redirect_stderr
import io
import subprocess
import unittest
from unittest.mock import patch

import check_environment


class RuntimePreflightTest(unittest.TestCase):
    def test_old_python_is_rejected_before_native_commands(self):
        diagnostic = io.StringIO()
        with patch.object(check_environment.sys, "version_info", (3, 10, 0)), patch.object(check_environment.subprocess, "run") as query, redirect_stderr(diagnostic):
            self.assertEqual(check_environment.main(), 1)
            query.assert_not_called()
        self.assertIn("Python 3.11+", diagnostic.getvalue())

    def test_supported_python_delegates_preflight_to_the_go_helper(self):
        with patch.object(check_environment.sys, "version_info", (3, 11, 0)), patch.object(check_environment.subprocess, "run", return_value=subprocess.CompletedProcess([], 0)) as query:
            self.assertEqual(check_environment.main(), 0)
        query.assert_called_once_with([str(check_environment.ROOT / "helper/scripts/run.sh"), "runtime", "--check"], cwd=check_environment.ROOT)

    def test_go_preflight_failure_is_propagated(self):
        with patch.object(check_environment.subprocess, "run", return_value=subprocess.CompletedProcess([], 17)):
            self.assertEqual(check_environment.main(), 17)
