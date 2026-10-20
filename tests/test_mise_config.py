"""Exercise miserc deployment with the real CLI in an isolated HOME."""

import json
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[1]
MISE = shutil.which("mise")


@unittest.skipUnless(MISE and sys.platform in ("darwin", "linux"), "mise or supported OS unavailable")
class MisercDeploymentTest(unittest.TestCase):
    def test_preview_apply_and_global_platform_discovery(self):
        with tempfile.TemporaryDirectory(prefix="system-sync-miserc-test-") as temporary:
            base = Path(temporary)
            home, project, outside = base / "home", base / "project", base / "outside"
            config = home / ".config/mise"
            for directory in (config, project, outside):
                directory.mkdir(parents=True)
            environment = {
                "HOME": str(home),
                "PATH": f"{Path(MISE).parent}:/usr/bin:/bin:/usr/sbin:/sbin",
                "MISE_CONFIG_DIR": str(config),
                "MISE_SYSTEM_CONFIG_DIR": str(base / "system"),
                "MISE_DATA_DIR": str(base / "data"),
                "MISE_STATE_DIR": str(base / "state"),
                "MISE_CACHE_DIR": str(base / "cache"),
                "MISE_CEILING_PATHS": str(base),
                "MISE_TRUSTED_CONFIG_PATHS": f"{base}:{ROOT}",
            }

            def run(*arguments, cwd=project):
                result = subprocess.run(
                    [MISE, *arguments], cwd=cwd, env=environment,
                    text=True, capture_output=True,
                )
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertNotIn("WARN", result.stderr)
                return result.stdout

            entry = run("config", "get", "--file", str(ROOT / "mise.toml"),
                        'dotfiles."~/.config/mise/miserc.toml"')
            (project / "mise.toml").write_text(
                '[dotfiles."~/.config/mise/miserc.toml"]\n' + entry
            )
            shutil.copy2(ROOT / ".miserc.toml", project / ".miserc.toml")
            source = project / "dotfiles/common/mise/miserc.toml"
            source.parent.mkdir(parents=True)
            shutil.copy2(ROOT / "dotfiles/common/mise/miserc.toml", source)
            (config / "config.toml").write_text("# Global defaults\n")
            for name in ("macos", "linux"):
                (config / f"config.{name}.toml").write_text(f'[env]\nRC_PLATFORM = "{name}"\n')
            target = config / "miserc.toml"

            run("bootstrap", "dotfiles", "apply", "--dry-run")
            self.assertFalse(target.exists(), "preview deployed a file")

            # Also cover migration from the existing symlink on this machine.
            target.symlink_to(source)
            run("bootstrap", "dotfiles", "apply", "--dry-run")
            run("bootstrap", "dotfiles", "apply", "--yes", "--force")
            self.assertEqual(target.read_bytes(), source.read_bytes())
            run("bootstrap", "dotfiles", "apply", "--dry-run")

            platform = "macos" if sys.platform == "darwin" else "linux"

            def loaded_outside_project():
                return {Path(item["path"]).name for item in
                        json.loads(run("config", "ls", "--json", cwd=outside))}

            self.assertIn(f"config.{platform}.toml", loaded_outside_project())

            # A later source edit must deploy and change discovery on the next invocation.
            source.write_text(source.read_text().replace("auto_env = true", "auto_env = false"))
            diff = run("bootstrap", "dotfiles", "diff", str(target))
            self.assertIn("+auto_env = false", diff)
            run("bootstrap", "dotfiles", "apply", "--yes")
            self.assertEqual(target.read_bytes(), source.read_bytes())
            self.assertNotIn(f"config.{platform}.toml", loaded_outside_project())


if __name__ == "__main__":
    unittest.main()
