"""Check shared PATH policy and real mise environment resolution in an isolated HOME."""

import json
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import tomllib
import unittest


ROOT = Path(__file__).resolve().parents[1]
MISE = shutil.which("mise")
BASH = shutil.which("bash")
ZSH = shutil.which("zsh")
PATH_SOURCE = ROOT / "dotfiles/common/shell/path.sh"


@unittest.skipUnless(BASH, "bash is not installed")
class SharedPathTest(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory(prefix="system-sync-path-test-")
        self.addCleanup(self.directory.cleanup)
        self.home = Path(self.directory.name)
        self.env = {"HOME": str(self.home), "PATH": "/usr/bin:/bin"}

    def source(self, platform, extra="", shell=BASH):
        result = subprocess.run(
            [shell, "-c", 'OSTYPE="$1"; . "$2"; _system_sync_user_path; ' + extra +
             'printf "%s\\n" "${ANDROID_HOME-}" "$PATH" "${LANG-}" "${LC_ALL-}"',
             "path-test", platform, str(PATH_SOURCE)],
            cwd=self.home, env=self.env, text=True, capture_output=True,
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stderr, "")
        return result.stdout.splitlines()

    def test_missing_directories_do_not_add_home_or_current_directory(self):
        android, path, lang, locale = self.source("linux-gnu")
        self.assertEqual((android, path, lang, locale), ("", "/usr/bin:/bin", "", ""))

    def test_legacy_flutter_directory_is_not_added_before_mise(self):
        (self.home / "development/flutter/bin").mkdir(parents=True)
        _, path, _, _ = self.source("linux-gnu")
        self.assertEqual(path, "/usr/bin:/bin")

    def test_platform_paths_spaces_and_shim_priority(self):
        for platform, sdk, toolbox in (
            ("darwin25.0", "Library/Android/sdk", "Library/Application Support/JetBrains/Toolbox/scripts"),
            ("linux-gnu", "Android/Sdk", ".local/share/JetBrains/Toolbox/scripts"),
        ):
            with self.subTest(platform=platform):
                relative_paths = [".local/share/mise/shims", ".local/bin", ".git-ai/bin",
                                  ".cargo/bin", toolbox,
                                  sdk + "/emulator", sdk + "/platform-tools", sdk + "/build-tools"]
                for relative in relative_paths:
                    (self.home / relative).mkdir(parents=True, exist_ok=True)
                android, path, _, _ = self.source(platform)
                self.assertEqual(android, str(self.home / sdk))
                self.assertEqual(path.split(":"), [str(self.home / p) for p in relative_paths] +
                                 ["/usr/bin", "/bin"])
                self.assertNotIn(str(self.home / sdk / "tools"), path.split(":"))

    def test_custom_sdk_and_cargo_locations_are_preserved(self):
        sdk, cargo, data = self.home / "custom SDK", self.home / "cargo", self.home / "data"
        for directory in (sdk / "platform-tools", cargo / "bin", data / "shims"):
            directory.mkdir(parents=True)
        self.env.update(ANDROID_HOME=str(sdk), CARGO_HOME=str(cargo), MISE_DATA_DIR=str(data))
        android, path, _, _ = self.source("linux-gnu")
        self.assertEqual(android, str(sdk))
        self.assertEqual(path.split(":"), [str(data / "shims"), str(cargo / "bin"),
                                         str(sdk / "platform-tools"), "/usr/bin", "/bin"])

    @unittest.skipUnless(ZSH, "zsh is not installed")
    def test_zsh_repeated_startup_keeps_path_unique(self):
        for relative in (".config/zsh", ".local/bin", ".git-ai/bin"):
            (self.home / relative).mkdir(parents=True)
        (self.home / ".config/zsh/path.sh").symlink_to(PATH_SOURCE)
        self.env["ZDOTDIR"] = str(self.home)
        (self.home / ".zshenv").symlink_to(ROOT / "dotfiles/common/zsh/zshenv")
        result = subprocess.run(
            [ZSH, "-c", 'source ~/.zshenv; source ~/.zshenv; print -rl -- "${path[@]}"'],
            cwd=self.home, env=self.env, text=True, capture_output=True,
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stderr, "")
        paths = result.stdout.splitlines()
        self.assertEqual(len(paths), len(set(paths)))
        self.assertLess(paths.index(str(self.home / ".git-ai/bin")), paths.index("/usr/bin"))


@unittest.skipUnless(MISE and sys.platform in ("darwin", "linux"), "mise or supported OS unavailable")
class MiseEnvironmentTest(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory(prefix="system-sync-env-test-")
        self.addCleanup(self.directory.cleanup)
        self.base = Path(self.directory.name).resolve()
        self.home = self.base / "home"
        self.config = self.home / ".config/mise"
        self.project, self.outside = self.base / "project", self.base / "outside"
        for directory in (self.config, self.project, self.outside):
            directory.mkdir(parents=True)
        self.env = {
            "HOME": str(self.home), "PATH": f"{Path(MISE).parent}:/usr/bin:/bin:/usr/sbin:/sbin",
            "MISE_CONFIG_DIR": str(self.config), "MISE_SYSTEM_CONFIG_DIR": str(self.base / "system"),
            "MISE_DATA_DIR": str(self.base / "data"), "MISE_STATE_DIR": str(self.base / "state"),
            "MISE_CACHE_DIR": str(self.base / "cache"), "MISE_CEILING_PATHS": str(self.base),
            "MISE_TRUSTED_CONFIG_PATHS": f"{self.base}:{ROOT}", "MISE_OFFLINE": "1",
        }
        source = ROOT / "dotfiles/common/mise/config.toml"
        self.definition = tomllib.loads(source.read_text())
        # Keep the real environment declarations without installing the machine's tools.
        env_source = source.read_text().split("\n[env]\n", 1)[1]
        python_home = self.home / "python"
        (python_home / "bin").mkdir(parents=True)
        # Match the deployed symlink and its canonical source-directory layout.
        config_source = self.base / "sources/common/mise/config.toml"
        config_source.parent.mkdir(parents=True)
        config_source.write_text(
            "redactions = " + json.dumps(self.definition["redactions"]) +
            '\n[tools]\npython = "path:' + str(python_home) + '"\n\n[env]\n' + env_source
        )
        (self.config / "config.toml").symlink_to(config_source)
        shell_source = config_source.parent.parent / "shell/path.sh"
        shell_source.parent.mkdir()
        shutil.copy2(PATH_SOURCE, shell_source)

    def mise(self, *arguments, cwd=None, environment=None):
        result = subprocess.run(
            [MISE, *arguments], cwd=cwd or self.outside, env=environment or self.env,
            text=True, capture_output=True,
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertNotIn("WARN", result.stderr)
        return result.stdout

    def resolved(self, **kwargs):
        return json.loads(self.mise("env", "--json", **kwargs))

    def test_optional_private_file_and_project_override(self):
        initial = self.resolved()
        self.assertNotIn("CASE_SUBMITTER", initial)
        self.assertEqual(initial["UV_PYTHON"], str(self.home / "python/bin/python3"))
        self.assertEqual(initial["UV_PYTHON_DOWNLOADS"], "never")
        secret = self.config / "secrets.env"
        secret.write_text("CASE_SUBMITTER='private fixture'\nDASHBOARD_HOST='https://private.invalid'\n")
        secret.chmod(0o600)
        self.assertEqual(self.resolved()["CASE_SUBMITTER"], "private fixture")
        command = 'printf "%s\\n" "$CASE_SUBMITTER" "$DASHBOARD_HOST"'
        (self.project / "mise.toml").write_text(
            "[tasks.private-output]\nrun = " + json.dumps(command) + "\n"
        )
        masked = self.mise("run", "--quiet", "private-output", cwd=self.project)
        self.assertTrue(masked.strip())
        self.assertNotIn("private fixture", masked)
        self.assertNotIn("https://private.invalid", masked)
        (self.project / "mise.toml").write_text('[env]\nCASE_SUBMITTER = "project fixture"\n')
        self.assertEqual(self.resolved(cwd=self.project)["CASE_SUBMITTER"], "project fixture")
        self.assertEqual(self.resolved()["CASE_SUBMITTER"], "private fixture")
        secret.unlink()
        self.assertNotIn("CASE_SUBMITTER", self.resolved())

    def test_exec_task_and_repeated_loading_keep_shared_path(self):
        for relative in (".local/bin", ".git-ai/bin", "development/flutter/bin"):
            (self.home / relative).mkdir(parents=True)
        shim_dir = self.base / "data/shims"
        shim_dir.mkdir(parents=True)
        # User command directories are inherited from zsh, before mise adds its tools.
        self.env["PATH"] = ":".join([str(shim_dir), str(self.home / ".local/bin"),
                                     str(self.home / ".git-ai/bin"), self.env["PATH"]])
        initial = self.resolved()
        inherited = dict(self.env, **initial)
        repeated = self.resolved(environment=inherited)
        self.assertEqual(initial["PATH"], repeated["PATH"])
        paths = initial["PATH"].split(":")
        self.assertEqual(len(paths), len(set(paths)))
        self.assertLess(paths.index(str(shim_dir)), paths.index(str(self.home / ".local/bin")))
        self.assertLess(paths.index(str(self.home / ".git-ai/bin")), paths.index("/usr/bin"))
        command = 'printf "%s\\n" "$UV_PYTHON_DOWNLOADS" "$PATH"'
        self.assertEqual(self.mise("exec", "--", "sh", "-c", command).splitlines(), ["never", initial["PATH"]])
        (self.project / "mise.toml").write_text(
            '[tasks.capture]\nrun = \'printf "%s\\n" "$UV_PYTHON_DOWNLOADS"\'\n'
        )
        self.assertEqual(self.mise("run", "--quiet", "capture", cwd=self.project).strip(), "never")

    def test_project_tool_selection_stays_ahead_of_unmanaged_commands(self):
        for directory, version in (("node-global", "global"), ("node-project", "project"),
                                   (".local", "unmanaged")):
            executable = self.home / directory / "bin/node"
            executable.parent.mkdir(parents=True)
            executable.write_text('#!/bin/sh\nprintf "%s\\n" "' + version + '"\n')
            executable.chmod(0o755)
        config = self.config / "config.toml"
        self.mise("link", "node@26.11.0", str(self.home / "node-global"))
        self.mise("link", "node@26.12.0", str(self.home / "node-project"))
        config.write_text(config.read_text().replace(
            "[tools]\n", '[tools]\nnode = "26.11.0"\n', 1
        ))
        (self.project / "mise.toml").write_text(
            '[tools]\nnode = "26.12.0"\n'
        )
        self.mise("reshim")
        self.assertTrue((self.base / "data/shims/node").exists())
        self.env["PATH"] = str(self.home / ".local/bin") + ":" + self.env["PATH"]
        for cwd, expected in ((self.outside, "global"), (self.project, "project"),
                              (self.outside, "global")):
            with self.subTest(expected=expected):
                self.assertEqual(self.mise("exec", "--", "node", cwd=cwd).strip(), expected)

    @unittest.skipUnless(ZSH, "zsh is not installed")
    def test_shell_hook_restores_project_environment_and_path(self):
        project_bin = self.project / "bin"
        project_bin.mkdir()
        sdk_name = "Library/Android/sdk" if sys.platform == "darwin" else "Android/Sdk"
        global_sdk = self.home / sdk_name
        project_sdk = self.project / "custom SDK"
        for sdk in (global_sdk, project_sdk):
            (sdk / "platform-tools").mkdir(parents=True)
        (self.config / "secrets.env").write_text("CASE_SUBMITTER='global fixture'\n")
        (self.project / "mise.toml").write_text(
            '[env]\nCASE_SUBMITTER = "project fixture"\n_.path = "bin"\n'
            'ANDROID_HOME = ' + json.dumps(str(project_sdk)) + '\n'
        )
        script = '''
eval "$("$1" activate zsh)"
printf '%s|%s|%s\n' "$CASE_SUBMITTER" "$ANDROID_HOME" "$PATH"
cd "$2"
eval "$("$1" hook-env -s zsh --force)"
printf '%s|%s|%s\n' "$CASE_SUBMITTER" "$ANDROID_HOME" "$PATH"
cd "$3"
eval "$("$1" hook-env -s zsh --force)"
printf '%s|%s|%s\n' "$CASE_SUBMITTER" "$ANDROID_HOME" "$PATH"
'''
        result = subprocess.run(
            [ZSH, "-f", "-c", script, "hook-test", MISE, str(self.project), str(self.outside)],
            cwd=self.outside, env=self.env, text=True, capture_output=True,
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stderr, "")
        before, inside, after = [line.split("|", 2) for line in result.stdout.splitlines()]
        self.assertEqual(before[0], "global fixture")
        self.assertEqual(before[1], str(global_sdk))
        self.assertEqual(inside[0], "project fixture")
        self.assertEqual(inside[1], str(project_sdk))
        self.assertIn(str(project_bin), inside[2].split(":"))
        self.assertIn(str(project_sdk / "platform-tools"), inside[2].split(":"))
        self.assertNotIn(str(global_sdk / "platform-tools"), inside[2].split(":"))
        self.assertEqual(after, before)


if __name__ == "__main__":
    unittest.main()
