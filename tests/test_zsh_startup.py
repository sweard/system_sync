from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[1]
ZSH = shutil.which("zsh")


@unittest.skipUnless(ZSH, "zsh is not installed")
class ZshStartupTest(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.home = Path(self.directory.name)
        self.bin = self.home / "bin"
        self.bin.mkdir()
        self.calls = self.home / "calls"
        self.env = {
            "HOME": str(self.home),
            "ZDOTDIR": str(self.home),
            "PATH": f"{self.bin}:/usr/bin:/bin:{self.bin}",
            "TERM": "dumb",
            "ZSH_TEST_CALLS": str(self.calls),
        }
        for name in ("zshenv", "zshrc"):
            (self.home / ("." + name)).symlink_to(ROOT / "dotfiles/common/zsh" / name)
        mise_config = self.home / ".config/zsh"
        mise_config.mkdir(parents=True)
        (mise_config / "path.sh").symlink_to(ROOT / "dotfiles/common/shell/path.sh")

    def write(self, relative, content, executable=False):
        target = self.home / relative
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(content)
        if executable:
            target.chmod(0o755)
        return target

    def shell(self, flags, script):
        result = subprocess.run(
            # Keep the runner's /etc/zshrc (including Debian compinit prompts)
            # outside this test of the repository's own startup files.
            [ZSH, "-d", *flags, script], env=self.env, text=True,
            stdout=subprocess.PIPE, stderr=subprocess.PIPE,
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stderr, "")
        return result.stdout.splitlines()

    def test_noninteractive_startup_is_quiet_without_optional_tools(self):
        for name in ("git", "brew", "mise"):
            self.write(
                "bin/" + name,
                '#!/bin/sh\nprintf "%s\\n" "$0" >> "$ZSH_TEST_CALLS"\nexit 99\n',
                executable=True,
            )
        lines = self.shell(["-c"], 'print -r -- ready; print -rl -- "${path[@]}"')
        self.assertEqual(lines[0], "ready")
        self.assertEqual(len(lines[1:]), len(set(lines[1:])))
        self.assertFalse(self.calls.exists(), "noninteractive startup invoked a manager")

    def test_interactive_startup_keeps_local_settings_and_user_git(self):
        self.write(".local/share/zinit/zinit.git/zinit.zsh", "zinit() { :; }\n")
        self.write(".config/zsh/platform.zsh", "typeset -g ZSH_TEST_PLATFORM=loaded\n")
        self.write(".p10k.zsh", "typeset -g ZSH_TEST_THEME=loaded\n")
        self.write(".zshrc.local", "export ZSH_TEST_PRIVATE='local value'\n")
        self.write(".openclaw/completions/openclaw.zsh", "typeset -g ZSH_TEST_COMPLETION=loaded\n")
        user_git = self.write(".git-ai/bin/git", "#!/bin/sh\nexit 0\n", executable=True)
        self.write("bin/git", "#!/bin/sh\nexit 0\n", executable=True)
        self.write("bin/brew", "#!/bin/sh\nexit 0\n", executable=True)
        self.write(
            "bin/mise",
            '#!/bin/sh\nprintf "%s\\n" "$*" >> "$ZSH_TEST_CALLS"\n'
            'test "$*" = "activate zsh" || exit 99\n'
            'printf "%s\\n" "export ZSH_TEST_MISE=active"\n',
            executable=True,
        )
        lines = self.shell(
            ["-i", "-c"],
            'print -r -- "$ZSH_TEST_PLATFORM|$ZSH_TEST_THEME|$ZSH_TEST_PRIVATE|'
            '$ZSH_TEST_COMPLETION|$ZSH_TEST_MISE|$(command -v git)|'
            '${+functions[nvm]}|${+functions[pyenv]}"',
        )
        self.assertEqual(lines, [f"loaded|loaded|local value|loaded|active|{user_git}|0|0"])
        self.assertEqual(self.calls.read_text().splitlines(), ["activate zsh"])

    def test_login_noninteractive_loads_environment_without_prompt_hooks(self):
        (self.home / ".zprofile").symlink_to(ROOT / "dotfiles/linux/zsh/zprofile")
        self.write(
            "bin/mise",
            '#!/bin/sh\nprintf "%s\\n" "$*" >> "$ZSH_TEST_CALLS"\n'
            'test "$*" = "env -s zsh" || exit 99\n'
            'printf "%s\\n" "export ZSH_TEST_MISE=environment"\n',
            executable=True,
        )
        lines = self.shell(["-l", "-c"], 'print -r -- "$ZSH_TEST_MISE|${+functions[mise]}"')
        self.assertEqual(lines, ["environment|0"])
        self.assertEqual(self.calls.read_text().splitlines(), ["env -s zsh"])


if __name__ == "__main__":
    unittest.main()
