"""Use the real mise CLI against synthetic app settings, never the host's files."""

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
class AppSettingsTest(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory(prefix="system-sync-app-settings-")
        self.addCleanup(self.directory.cleanup)
        self.base = Path(self.directory.name)
        self.home, self.project = self.base / "home", self.base / "project"
        self.home.mkdir()
        self.project.mkdir()
        self.env = {
            "HOME": str(self.home),
            "PATH": f"{Path(MISE).parent}:/usr/bin:/bin:/usr/sbin:/sbin",
            "MISE_CONFIG_DIR": str(self.base / "mise-config"),
            "MISE_SYSTEM_CONFIG_DIR": str(self.base / "system"),
            "MISE_DATA_DIR": str(self.base / "data"),
            "MISE_STATE_DIR": str(self.base / "state"),
            "MISE_CACHE_DIR": str(self.base / "cache"),
            "MISE_CEILING_PATHS": str(self.base),
            "MISE_TRUSTED_CONFIG_PATHS": f"{self.base}:{ROOT}",
        }
        for directory in ("chatgpt", "vscode"):
            shutil.copytree(ROOT / "dotfiles/common" / directory,
                            self.project / "dotfiles/common" / directory)
        platform = "macos" if sys.platform == "darwin" else "linux"
        user_directory = "Library/Application Support/Code/User" if platform == "macos" else ".config/Code/User"
        self.settings = self.home / user_directory / "settings.json"
        self.settings.parent.mkdir(parents=True)
        self.config = self.home / ".codex/config.toml"
        self.config.parent.mkdir()
        entries = []
        for file, key in (
            ("mise.toml", "~/.codex/config.toml/shared"),
            ("mise.toml", "~/.codex/AGENTS.md"),
            ("mise.toml", "~/.codex/keybindings.json"),
            (f"mise.{platform}.toml", f"~/{user_directory}/settings.json/shared"),
        ):
            entry = self.run_mise("config", "get", "--file", str(ROOT / file),
                                  "dotfiles." + json.dumps(key)).stdout
            entries.append("[dotfiles." + json.dumps(key) + "]\n" + entry)
        (self.project / "mise.toml").write_text("\n".join(entries))

    def run_mise(self, *arguments, success=True):
        result = subprocess.run([MISE, *arguments], cwd=self.project, env=self.env,
                                text=True, capture_output=True)
        if success:
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertNotIn("WARN", result.stderr)
        return result

    def test_merge_preserves_private_and_application_owned_keys(self):
        self.config.write_text(
            '# Local application state must survive\nmodel = "old-model"\n'
            '[features]\nhooks = false\nlocal_flag = true\n'
            '[desktop]\nselected-avatar-id = "private-avatar"\n'
            '[shell_environment_policy.set]\nPRIVATE_TOKEN = "private-canary"\n'
            '[projects."/private/example"]\ntrust_level = "trusted"\n'
        )
        private_settings = {
            "editor.fontSize": 1,
            "dart.flutterSdkPath": "/private/flutter",
            "git.path": "/private/git",
            "extension.private": {"token": "private-canary", "url": "https://private.invalid"},
        }
        self.settings.write_text(json.dumps(private_settings, indent=4) + "\n")
        auth = self.config.parent / "auth.json"
        auth.write_text('{"token":"private-auth-canary"}\n')
        original = {path: path.read_bytes() for path in (self.config, self.settings, auth)}
        self.run_mise("bootstrap", "dotfiles", "apply", "--dry-run")
        for path, content in original.items():
            self.assertEqual(path.read_bytes(), content, "preview changed an app file")

        self.run_mise("bootstrap", "dotfiles", "apply", "--yes")
        public = self.project / "dotfiles/common/chatgpt/config.toml"
        for key in ("model", "model_reasoning_effort", "service_tier", "features.hooks",
                    "features.js_repl", "desktop.appearanceLightChromeTheme.accent"):
            self.assertEqual(
                self.run_mise("config", "get", "--file", str(self.config), key).stdout,
                self.run_mise("config", "get", "--file", str(public), key).stdout,
            )
        for local in ('# Local application state must survive', 'local_flag = true',
                      'selected-avatar-id = "private-avatar"', 'PRIVATE_TOKEN = "private-canary"',
                      '[projects."/private/example"]', 'trust_level = "trusted"'):
            self.assertIn(local, self.config.read_text())
        shared_settings = json.loads((self.project / "dotfiles/common/vscode/settings.json").read_text())
        self.assertEqual(json.loads(self.settings.read_text()), {**private_settings, **shared_settings})
        self.assertEqual(auth.read_bytes(), original[auth])
        for name in ("AGENTS.md", "keybindings.json"):
            target = self.config.parent / name
            self.assertFalse(target.is_symlink())
            self.assertEqual(target.read_bytes(), (public.parent / name).read_bytes())

        # New local state is neither drift nor a reason to rewrite the file.
        self.config.write_text(self.config.read_text().replace("private-canary", "local-new-canary"))
        settings = json.loads(self.settings.read_text())
        settings["extension.futurePrivateSetting"] = "local-new-canary"
        self.settings.write_text(json.dumps(settings, indent=4) + "\n")
        applied = {path: path.read_bytes() for path in (self.config, self.settings, auth)}
        self.assertEqual(self.run_mise("bootstrap", "dotfiles", "diff").stdout, "")
        self.run_mise("bootstrap", "dotfiles", "apply", "--yes")
        for path, content in applied.items():
            self.assertEqual(path.read_bytes(), content)

    def test_jsonc_is_rejected_without_overwriting_local_settings(self):
        self.config.write_text('model = "old-model"\n')
        original = '{\n  // Local comment\n  "extension.private": "private-canary"\n}\n'
        self.settings.write_text(original)
        result = self.run_mise("bootstrap", "dotfiles", "apply", "--yes", success=False)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.settings.read_text(), original)


if __name__ == "__main__":
    unittest.main()
