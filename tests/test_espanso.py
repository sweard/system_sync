"""Exercise the real Espanso mappings with synthetic metadata and private files."""

import json
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import tomllib
import unittest

from helper_fixture import prepare_helper


ROOT = Path(__file__).resolve().parents[1]
MISE = shutil.which("mise")


@unittest.skipUnless(MISE and sys.platform in ("darwin", "linux"), "mise or supported OS unavailable")
class EspansoMappingTest(unittest.TestCase):
    def test_metadata_is_ignored_at_any_depth_and_local_content_is_preserved(self):
        for platform, relative in (
            ("macos", "Library/Application Support/espanso"),
            ("linux", ".config/espanso"),
        ):
            with self.subTest(platform=platform), tempfile.TemporaryDirectory(prefix="system-sync-espanso-") as temporary:
                base = Path(temporary).resolve()
                home, project = base / "home", base / "project"
                home.mkdir()
                project.mkdir()
                prepare_helper(project)
                source = project / "dotfiles/common/espanso"
                target = home / relative
                for directory in (source / "config", source / "match", source / "private.local/match",
                                  target / "match/packages"):
                    directory.mkdir(parents=True)
                public = source / "config/default.yml"
                public.write_text("show_icon: true\n")
                (source / "match/shared.yml").write_text("matches: []\n")
                (source / "mise.local.example.toml").write_text("# local example\n")
                private = source / "private.local/match/private-snippets.yml"
                private.write_text("matches: []\n# synthetic-private-canary\n")
                private.chmod(0o600)
                private_target = target / "match/private-snippets.yml"
                private_target.symlink_to(private)
                unmanaged = target / "match/packages/local.yml"
                unmanaged.write_text("# local-only-canary\n")
                metadata = []
                for relative_metadata in (".DS_Store", "match/.DS_Store"):
                    (source / relative_metadata).write_bytes(b"source-finder-metadata")
                    path = target / relative_metadata
                    path.write_bytes(b"local-finder-metadata")
                    metadata.append((path, path.read_bytes(), path.stat().st_mtime_ns))
                with (ROOT / f"mise.{platform}.toml").open("rb") as file:
                    entry = tomllib.load(file)["dotfiles"]["~/" + relative]
                (project / "mise.toml").write_text(
                    "[dotfiles]\n" + json.dumps("~/" + relative) + " = {source = "
                    + json.dumps(entry["source"]) + ", mode = " + json.dumps(entry["mode"])
                    + ", exclude = " + json.dumps(entry["exclude"]) + "}\n"
                    + json.dumps(str(private_target)) + " = {source = "
                    + json.dumps(str(private)) + ', mode = "symlink"}\n')
                environment = {
                    "HOME": str(home), "PATH": f"{Path(MISE).parent}:/usr/bin:/bin:/usr/sbin:/sbin",
                    "MISE_CONFIG_DIR": str(base / "config"), "MISE_SYSTEM_CONFIG_DIR": str(base / "system"),
                    "MISE_DATA_DIR": str(base / "data"), "MISE_STATE_DIR": str(base / "state"),
                    "MISE_CACHE_DIR": str(base / "cache"), "MISE_CEILING_PATHS": str(base),
                    "MISE_TRUSTED_CONFIG_PATHS": str(base), "MISE_OFFLINE": "1", "NO_COLOR": "1",
                }

                def helper(*arguments):
                    result = subprocess.run([str(project / "helper/scripts/run.sh"), "config", *arguments],
                                            cwd=project, env=environment, text=True, capture_output=True, timeout=30)
                    self.assertEqual(result.returncode, 0, result.stderr)
                    return result.stdout

                helper("sync", "--yes")
                self.assertTrue((target / "config/default.yml").is_symlink())
                self.assertEqual((target / "config/default.yml").resolve(), public)
                self.assertTrue(private_target.is_symlink())
                self.assertEqual(private_target.resolve(), private)
                self.assertEqual(private.read_text(), "matches: []\n# synthetic-private-canary\n")
                self.assertEqual(private.stat().st_mode & 0o777, 0o600)
                self.assertEqual(unmanaged.read_text(), "# local-only-canary\n")
                self.assertFalse((target / "private.local").exists())
                self.assertFalse((target / "mise.local.example.toml").exists())
                for path, content, mtime in metadata:
                    self.assertFalse(path.is_symlink())
                    self.assertEqual(path.read_bytes(), content)
                    self.assertEqual(path.stat().st_mtime_ns, mtime)
                self.assertIn("无需同步", helper("diff"))
                # Finder can rewrite metadata in either tree without causing drift.
                (source / "match/.DS_Store").write_bytes(b"updated-source-metadata")
                (target / ".DS_Store").write_bytes(b"updated-local-metadata")
                self.assertIn("无需同步", helper("diff"))
                # A real configuration conflict must still be visible.
                deployed = target / "config/default.yml"
                deployed.unlink()
                deployed.write_text("show_icon: false\n")
                self.assertNotIn("无需同步", helper("diff"))
                self.assertEqual(private_target.read_bytes(), private.read_bytes())


if __name__ == "__main__":
    unittest.main()
