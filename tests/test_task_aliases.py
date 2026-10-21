"""Resolve old task names through real mise without executing system commands."""

import json
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[1]
MISE = shutil.which("mise")


@unittest.skipUnless(MISE, "mise unavailable")
class TaskAliasTest(unittest.TestCase):
    def test_compatibility_aliases_forward_arguments_to_one_canonical_task(self):
        with tempfile.TemporaryDirectory(prefix="system-sync-task-aliases-") as temporary:
            base = Path(temporary).resolve()
            project, home = base / "project", base / "home"
            project.mkdir()
            home.mkdir()
            shutil.copy2(ROOT / "mise.toml", project / "mise.toml")
            environment = {
                "HOME": str(home), "PATH": f"{Path(MISE).parent}:/usr/bin:/bin",
                "MISE_CONFIG_DIR": str(base / "config"), "MISE_SYSTEM_CONFIG_DIR": str(base / "system"),
                "MISE_DATA_DIR": str(base / "data"), "MISE_STATE_DIR": str(base / "state"),
                "MISE_CACHE_DIR": str(base / "cache"), "MISE_CEILING_PATHS": str(base),
                "MISE_TRUSTED_CONFIG_PATHS": str(base), "MISE_OFFLINE": "1", "NO_COLOR": "1",
            }

            def run(*arguments):
                result = subprocess.run([MISE, *arguments], cwd=project, env=environment,
                                        capture_output=True, text=True, timeout=30)
                self.assertEqual(result.returncode, 0, result.stderr)
                return result.stdout if arguments[:2] == ("tasks", "ls") else result.stdout + result.stderr

            tasks = {task["name"].rsplit(":", 1)[-1]: task for task in json.loads(run("tasks", "ls", "--json"))}
            for old, canonical, arguments, command in (
                ("status", "package-status", (), "./scripts/system-sync.sh --status"),
                ("dry-run", "package-dry-run", (), "./scripts/system-sync.sh --dry-run"),
                ("vscode-dry-run", "vscode-status", ("--code", "/fixture/code"),
                 "./helper/scripts/run.sh vscode status --code /fixture/code"),
            ):
                with self.subTest(alias=old):
                    self.assertNotIn(old, tasks)
                    self.assertIn(old, tasks[canonical]["aliases"])
                    expected = run("run", "--dry-run", canonical, *arguments)
                    actual = run("run", "--dry-run", old, *arguments)
                    self.assertEqual(actual, expected)
                    self.assertIn(command, actual)
            self.assertFalse((project / ".system-sync").exists())


if __name__ == "__main__":
    unittest.main()
