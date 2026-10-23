"""Validate Go output against frozen contracts and offline binary activation."""

import hashlib
import io
import json
import os
from pathlib import Path
import shlex
import shutil
import subprocess
import sys
import tarfile
import tempfile
import unittest

from helper_fixture import prepare_helper
import audit_fixture as fixtures


ROOT = Path(__file__).resolve().parents[1]


class GoAuditGoldenTest(unittest.TestCase):
    def setUp(self):
        self.golden = json.loads((ROOT / "tests/fixtures/audit/reports.json").read_text())["cases"][self._testMethodName]
        self.golden_index = 0

    def tearDown(self):
        self.assertEqual(self.golden_index, len(self.golden), "a frozen scenario was not exercised")

    def fixture(self):
        fixture = fixtures.AuditFixture()
        fixture.setUp()
        self.addCleanup(fixture.doCleanups)
        return fixture

    def compare(self, fixture, selected=fixtures.SECTIONS, strict=False, text=False):
        expected = self.golden[self.golden_index]
        self.golden_index += 1
        self.assertEqual(list(selected), expected["sections"])
        self.assertEqual(strict, expected["strict"])
        result = fixture.invoke(selected, strict)
        self.assertEqual(result.returncode, expected["exit_code"], result.stderr)
        self.assertEqual(fixture.normalize(json.loads(result.stdout)), expected["report"])
        self.assertNotIn("private-query", result.stdout + result.stderr)
        self.assertNotIn("fixture-private-token", result.stdout)
        if text:
            result = fixture.invoke(selected, strict, text=True)
            self.assertEqual(result.returncode, expected["exit_code"], result.stderr)
            self.assertEqual(fixture.normalize(result.stdout), expected["text"])
            self.assertNotIn("fixture-private-token", result.stdout)
            return result.stdout

    def test_all_candidate_details_and_healthy_omission_count_match_golden(self):
        fixture = self.fixture()
        candidates = [f"fixture-local-{i:02}" for i in range(12)]
        candidates[10] = "fixture-private-token"
        fixture.runner.packages = fixtures.package_status(fixtures.PACKAGE_REQUIRED[0], {
            "Brewfile formula 根声明": [f"fixture-root-{i:02}" for i in range(12)],
            "Homebrew 可识别的未声明 requested formula": candidates,
        })
        output = self.compare(fixture, ("packages",), text=True)
        self.assertIn("    - fixture-local-11", output)
        self.assertIn("    - [REDACTED]", output)
        self.assertNotIn("    - fixture-root-11", output)
        self.assertIn("另有 4 项已核对条目", output)

    def test_empty_and_declared_tools_match_golden(self):
        self.compare(self.fixture())
        fixture = self.fixture()
        fixture.tool(installed=False)
        self.compare(fixture, strict=True)

    def test_path_shadowing_alternatives_and_other_installers_match(self):
        fixture = self.fixture()
        selected = fixture.tool()
        shadow = fixture.executable(fixture.home / ".local/bin/node")
        fixture.executable(fixture.home / ".nvm/versions/node/v0.1.0/bin/node")
        fixture.environment["PATH"] = f"{shadow.parent}:{selected.parent}:/usr/bin:/bin"
        self.compare(fixture)

    def test_linked_rust_shim_virtual_environment_and_caller_path_match(self):
        fixture = self.fixture()
        selected = fixture.tool("rust", "rustc")
        cargo = fixture.executable(fixture.home / ".cargo/bin/rustc")
        selected.unlink()
        selected.symlink_to(cargo)
        shim = fixture.executable(fixture.home / ".local/share/mise/shims/rustc")
        fixture.environment["PATH"] = f"{shim.parent}:{cargo.parent}:/usr/bin:/bin"
        fixture.runner.policy = f"{cargo.parent}:/usr/bin:/bin"
        self.compare(fixture)
        fixture = self.fixture()
        selected = fixture.tool("python", "python3")
        virtual = fixture.executable(fixture.root / ".venv/bin/python3")
        fixture.environment.update(VIRTUAL_ENV=str(virtual.parent.parent),
                                   PATH=f"{virtual.parent}:{selected.parent}:/usr/bin:/bin")
        self.compare(fixture)
        fixture.environment["SYSTEM_SYNC_AUDIT_CALLER_PATH"] = f"{fixture.home}::relative:{fixture.home / 'absent'}:{fixture.home}"
        self.compare(fixture)

    def test_all_platform_reports_version_drift_and_cleanup_review_match(self):
        for required in fixtures.PACKAGE_REQUIRED:
            fixture = self.fixture()
            fixture.runner.packages = fixtures.package_status(required)
            self.compare(fixture, ("packages",))
        fixture = self.fixture()
        fixture.runner.packages = fixtures.package_status(fixtures.PACKAGE_REQUIRED[1],
            {"mise 最终生效声明": ["pacman:git [version_mismatch]"],
             "声明但尚未以同名具体包安装": ["missing-tool"],
             "当前显式安装包中未声明、但受保护而保留": ["kernel"],
             "apply 时会从 explicit 降级为 dependency 的未声明包": ["old-tool"]})
        self.compare(fixture, ("packages",))
        fixture = self.fixture()
        fixture.runner.packages += "\nHomebrew cleanup 精确预演\n  review details\n状态模式：bundle check 退出码为 1。\n"
        self.compare(fixture, ("packages",))

    def test_private_files_config_candidates_and_redaction_match(self):
        fixture = self.fixture()
        for name in (".gitconfig", ".config/mise/secrets.env", "private-application/token.json"):
            path = fixture.home / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text("private-file-value")
        fixture.runner.config = {"files": [{"target": "~/.zshrc", "state": "missing"}],
                                 "edits": [{"path": "~/.codex/config.toml", "state": "applied",
                                            "edit": {"value": "private-edit-value"}}],
                                 "orphaned": [{"target": "~/.retired"}]}
        fixture.environment["EXTRA_SECRET"] = "ok"
        fixture.runner.tools = {"fixture-private-token": [{"version": "1.0.0", "installed": False}]}
        self.compare(fixture)

    def test_incomplete_schemas_and_query_failure_match(self):
        for tools in ([], {"node": []}, {"node": [{"installed": True}]},
                      {"node": [{"version": "1.0.0", "installed": True, "source": "wrong"}]}):
            fixture = self.fixture()
            fixture.runner.tools = tools
            self.compare(fixture, ("tools", "path"))
        for state in ("future_state", []):
            fixture = self.fixture()
            fixture.runner.config = {"files": [{"target": "~/.zshrc", "state": state}], "edits": []}
            self.compare(fixture, ("config",))
        fixture = self.fixture()
        fixture.runner.failures.add(("mise", "ls", "--current", "--json"))
        self.compare(fixture)


class GoHelperIntegrationTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="system-sync-go-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name).resolve() / "repo with spaces 中文"
        self.root.mkdir()
        self.binary = prepare_helper(self.root)
        self.environment = dict(os.environ, MISE_AUTO_INSTALL="false", MISE_TASK_RUN_AUTO_INSTALL="false")

    def run_helper(self, *args, text="", success=True):
        result = subprocess.run([str(self.root / "helper/scripts/run.sh"), *args],
                                cwd=self.root.parent, env=self.environment, input=text,
                                capture_output=True, text=True, timeout=15)
        self.assertEqual(result.returncode == 0, success, result.stdout + result.stderr)
        return result

    def test_parser_stdin_contracts_and_no_partial_output_on_errors(self):
        for manager in ("apt", "dnf", "pacman", "aur", "zypper"):
            data = {manager: {"available": True, "packages": [{"package": "git", "state": "installed"}]}}
            result = self.run_helper("parse-status", "--manager", manager, "-", text=json.dumps(data))
            self.assertEqual(result.stdout, f"{manager}\tgit\tinstalled\n")
        for data in ({}, [], {"apt": {"available": False}}, {"apt": []},
                     {"apt": [{"package": "git", "state": "installed"}, {"package": "git;id", "state": "installed"}]}):
            result = self.run_helper("parse-status", "--manager", "apt", "-", text=json.dumps(data), success=False)
            self.assertEqual(result.returncode, 2)
            self.assertEqual(result.stdout, "")
        result = self.run_helper("parse-zypper", "to-remove", "-", text='<stream><to-remove><solvable name="extra"/></to-remove></stream>')
        self.assertEqual(result.stdout, "extra\n")
        for text in ('<broken>', '<stream><to-remove><solvable name="git"/><solvable name="--help"/></to-remove></stream>'):
            result = self.run_helper("parse-zypper", "to-remove", "-", text=text, success=False)
            self.assertEqual(result.returncode, 2)
            self.assertEqual(result.stdout, "")

    def test_missing_corrupt_and_incompatible_binary_never_fall_back(self):
        marker = self.root / "unexpected"
        commands = self.root / "bin"
        commands.mkdir()
        for name in ("go", "python3", "curl", "wget"):
            command = commands / name
            command.write_text('#!/bin/sh\necho called > ' + shlex.quote(str(marker)) + '\nexit 99\n')
            command.chmod(0o755)
        self.environment["PATH"] = str(commands) + ":/usr/bin:/bin:/usr/sbin:/sbin"
        self.binary.write_bytes(self.binary.read_bytes() + b"modified")
        result = self.run_helper("--version", success=False)
        self.assertIn("校验失败", result.stderr)
        self.assertFalse(marker.exists())
        (self.root / "helper/VERSION").write_text("v99.0.0\n")
        self.assertIn("版本与仓库不符", self.run_helper("--version", success=False).stderr)
        (self.root / "helper/VERSION").write_text((ROOT / "helper/VERSION").read_text())
        (self.root / ".system-sync/helper/current").unlink()
        self.assertIn("尚未准备", self.run_helper("--version", success=False).stderr)
        self.assertFalse(marker.exists())

    def test_go_runtime_preflight_and_readonly_audit_need_no_interpreter(self):
        commands = self.root / "bin"
        commands.mkdir()
        marker = self.root / "unexpected-interpreter"
        scripts = {
            "go": 'echo called > ' + shlex.quote(str(marker)) + '\nexit 99\n',
            "python3": 'echo called > ' + shlex.quote(str(marker)) + '\nexit 99\n',
            "uname": 'printf "Darwin\\n"\n', "brew": 'exit 99\n',
            "mise": 'case "$*" in\n"--version") printf "2026.10.15 fixture\\n" ;;\n'
                    '"ls --current --json") printf "{}\\n" ;;\n'
                    '"bootstrap dotfiles status --json") printf \'{"files":[],"edits":[]}\\n\' ;;\n'
                    '*) exit 91 ;;\nesac\n',
        }
        for name, script in scripts.items():
            command = commands / name
            command.write_text("#!/bin/sh\n" + script)
            command.chmod(0o755)
        self.environment.update(PATH=str(commands) + ":/usr/bin:/bin:/usr/sbin:/sbin", HOME=str(self.root / "home"))
        Path(self.environment["HOME"]).mkdir()
        self.run_helper("runtime", "--check")
        result = self.run_helper("audit", "--only", "tools", "--only", "config", "--json")
        self.assertEqual(json.loads(result.stdout)["summary"]["error"], 0)
        self.assertFalse(marker.exists())

    def test_write_workflows_and_shared_lock_need_no_python_or_go(self):
        import fcntl
        commands, marker = self.deny_build_and_download()
        home = self.root.parent / "workflow-home"
        home.mkdir()
        self.environment.update(HOME=str(home), MISE_CONFIG_DIR=str(home / ".config/mise"),
                                MISE_STATE_DIR=str(home / ".local/state/mise"),
                                XDG_STATE_HOME=str(home / ".local/state"))
        state, status = self.root / "installed", self.root / "dotfiles-status.json"
        state.write_text("git\nmise\n")
        status.write_text('{"files":[],"edits":[]}')
        saved = home / "saved-by-mise"
        scripts = {
            "uname": 'printf "Darwin\\n"\n',
            "mise": 'case "$*" in\n"--version") printf "2026.10.15 fixture\\n" ;;\n'
                    '"bootstrap dotfiles status --json") /bin/cat ' + shlex.quote(str(status)) + ' ;;\n'
                    '"dot save --description "*) : > ' + shlex.quote(str(saved)) + ' ;;\n'
                    '*) exit 91 ;;\nesac\n',
            "brew": 'case "$1 $2" in\n'
                    '"info --json=v2") printf \'{"formulae":[{"full_name":"gitui","aliases":[],"oldnames":[]}]}\\n\' ;;\n'
                    '"list --formula") /bin/cat ' + shlex.quote(str(state)) + ' ;;\n'
                    '"install --formula")\n'
                    ' [ "$HOMEBREW_NO_AUTO_UPDATE/$HOMEBREW_NO_INSTALL_CLEANUP/$HOMEBREW_NO_AUTOREMOVE/$HOMEBREW_NO_INSTALL_UPGRADE" = "1/1/1/1" ] || exit 98\n'
                    ' printf "gitui\\n" >> ' + shlex.quote(str(state)) + ' ;;\n'
                    '*) exit 92 ;;\nesac\n',
        }
        for name, body in scripts.items():
            executable = commands / name
            executable.write_text("#!/bin/sh\n" + body)
            executable.chmod(0o755)
        package_dir = self.root / "packages/macos"
        package_dir.mkdir(parents=True)
        manifest = package_dir / "Brewfile"
        manifest.write_text('brew "git"\nbrew "mise"\n')
        (package_dir / "protected-packages.txt").write_text("formula:git\nformula:mise\n")
        (package_dir / "trusted-formulae.txt").write_text("# exact trust\n")
        before = manifest.read_bytes()
        self.run_helper("package", "install", "gitui", "--dry-run")
        self.assertEqual(manifest.read_bytes(), before)
        self.assertFalse((self.root / ".system-sync/history").exists())
        with (self.root / ".system-sync/operation.lock").open("w") as lock:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            result = self.run_helper("package", "install", "gitui", text="yes\n", success=False)
            self.assertIn("另一条配置或包操作", result.stderr)
            self.assertEqual(manifest.read_bytes(), before)
        self.run_helper("package", "install", "gitui", text="yes\n")
        self.assertIn('brew "gitui"', manifest.read_text())

        extension_manifest = self.root / "extensions.toml"
        extension_manifest.write_text('[common]\n"example.shared"="1.2.3"\n')
        extension_state = self.root / "extension-state"
        extension_state.write_text("example.local@4.5.6\n")
        code = commands / "code"
        code.write_text('#!/bin/sh\nif [ "$1" = --list-extensions ]; then /bin/cat ' +
                        shlex.quote(str(extension_state)) + '; else\n'
                        ' [ "$1" = --install-extension ] || exit 93\n'
                        ' printf "%s\\n" "$2" >> ' + shlex.quote(str(extension_state)) + '\nfi\n')
        code.chmod(0o755)
        self.run_helper("vscode", "sync", "--manifest", str(extension_manifest), "--code", str(code))
        self.assertIn("example.local@4.5.6", extension_state.read_text())
        self.assertIn("example.shared@1.2.3", extension_state.read_text())

        private = home / ".codex/config.toml"
        private.parent.mkdir()
        private.write_text('private_canary="keep-local"\n')
        self.run_helper("config-history", "init")
        self.run_helper("config-history", "save", "-d", "isolated --named checkpoint")
        self.assertTrue(saved.is_file())
        source = self.root / "private-source"
        source.write_text("private-canary-content\n")
        source.chmod(0o600)
        target = home / "local-config"
        target.symlink_to(source)
        status.write_text(json.dumps({"files": [{"mode": "symlink", "target": str(target),
                            "origin": {"config": str(self.root / "mise.toml"), "source": str(source)}}], "edits": []}))
        result = self.run_helper("config-detach", "--dry-run")
        self.assertTrue(target.is_symlink())
        self.assertNotIn("private-canary-content", result.stdout + result.stderr)
        self.run_helper("config-detach", "--yes")
        self.assertFalse(target.is_symlink())
        self.assertEqual(target.read_bytes(), source.read_bytes())
        self.assertEqual(target.stat().st_mode & 0o777, 0o600)
        self.assertEqual(private.read_text(), 'private_canary="keep-local"\n')
        self.assertFalse((home / ".config/mise/conf.d/system-sync-history.toml").exists())
        self.assertFalse(marker.exists())

    def archive(self, *, bad_name=False):
        source = ROOT / ".system-sync/helper/current"
        version, checksum = source.read_text().split()
        installed = source.parent / version / checksum
        info = subprocess.check_output([str(installed / "system-sync-helper"), "--version"], text=True)
        target = info.split("target=", 1)[1].split()[0].replace("/", "_")
        directory = self.root / "artifact"
        directory.mkdir(exist_ok=True)
        archive = directory / f"system-sync-helper_{version}_{target}.tar.gz"
        with tarfile.open(archive, "w:gz") as output:
            for name in ("system-sync-helper", "VERSION", "REVISION", "SHA256SUMS"):
                output.add(installed / name, arcname=name)
            output.add(ROOT / "helper/licenses/Go-LICENSE.txt", arcname="LICENSE-Go")
            output.add(ROOT / "helper/vendor/github.com/pelletier/go-toml/v2/LICENSE", arcname="LICENSE-go-toml")
            if bad_name:
                info = tarfile.TarInfo("../outside")
                info.size = 4
                output.addfile(info, io.BytesIO(b"evil"))
        digest = hashlib.sha256(archive.read_bytes()).hexdigest()
        (directory / "SHA256SUMS").write_text(f"{digest}  {archive.name}\n")
        return directory, archive

    def install(self, directory=None, success=True):
        command = [str(self.root / "helper/scripts/install.sh")]
        if directory is not None:
            command.append(str(directory))
        result = subprocess.run(command,
                                cwd=self.root.parent, env=self.environment, capture_output=True, text=True, timeout=15)
        self.assertEqual(result.returncode == 0, success, result.stdout + result.stderr)
        return result

    def test_precompiled_archive_installs_offline_and_is_idempotent(self):
        directory, _ = self.archive()
        shutil.rmtree(self.root / ".system-sync/helper")
        self.install(directory)
        first = (self.root / ".system-sync/helper/current").read_bytes()
        self.assertIn("protocol=1", self.run_helper("--version").stdout)
        version, checksum = first.decode().split()
        self.assertTrue((self.root / ".system-sync/helper" / version / checksum / "LICENSE-Go").is_file())
        self.install(directory)
        self.assertEqual((self.root / ".system-sync/helper/current").read_bytes(), first)

    def test_failed_archive_checks_preserve_active_binary(self):
        directory, archive = self.archive()
        current = self.root / ".system-sync/helper/current"
        before = current.read_bytes()
        archive.write_bytes(archive.read_bytes() + b"corrupt")
        self.assertIn("SHA256", self.install(directory, success=False).stderr)
        self.assertEqual(current.read_bytes(), before)
        directory, archive = self.archive(bad_name=True)
        self.assertIn("路径", self.install(directory, success=False).stderr)
        self.assertEqual(current.read_bytes(), before)
        self.assertFalse((self.root / ".system-sync/outside").exists())
        self.run_helper("--version")

    def clone_bundle(self):
        version = (self.root / "helper/VERSION").read_text().strip()
        target = self.root / "helper/prebuilt" / version
        shutil.copytree(ROOT / "helper/prebuilt" / version, target)
        shutil.rmtree(self.root / ".system-sync")
        return target

    def deny_build_and_download(self):
        commands = self.root / "denied-bin"
        commands.mkdir()
        marker = self.root / "unexpected-build-or-download"
        for name in ("go", "python", "python3", "curl", "wget"):
            command = commands / name
            command.write_text('#!/bin/sh\necho called > ' + shlex.quote(str(marker)) + '\nexit 99\n')
            command.chmod(0o755)
        self.environment.update(PATH=str(commands) + ":/usr/bin:/bin:/usr/sbin:/sbin", HOME=str(self.root / "home"))
        Path(self.environment["HOME"]).mkdir()
        return commands, marker

    def test_fresh_clone_bootstraps_offline_without_polluting_json(self):
        self.clone_bundle()
        commands, marker = self.deny_build_and_download()
        mise = commands / "mise"
        mise.write_text('#!/bin/sh\ncase "$*" in\n'
                        '"ls --current --json") printf "{}\\n" ;;\n'
                        '"bootstrap dotfiles status --json") printf \'{"files":[],"edits":[]}\\n\' ;;\n'
                        '*) exit 91 ;;\nesac\n')
        mise.chmod(0o755)
        result = self.run_helper("audit", "--only", "tools", "--only", "config", "--json")
        self.assertEqual(json.loads(result.stdout)["summary"]["error"], 0)
        self.assertIn("已安装", result.stderr)
        current = self.root / ".system-sync/helper/current"
        first = (current.read_bytes(), current.stat().st_mtime_ns)
        self.assertIn("protocol=1", self.run_helper("--version").stdout)
        self.assertNotIn("已安装", self.run_helper("--version").stderr)
        self.assertEqual((current.read_bytes(), current.stat().st_mtime_ns), first)
        self.assertFalse(marker.exists())

    def test_default_install_uses_repository_bundle_without_go(self):
        self.clone_bundle()
        _, marker = self.deny_build_and_download()
        self.install()
        self.assertIn("protocol=1", self.run_helper("--version").stdout)
        self.assertFalse(marker.exists())

    def test_concurrent_first_launches_publish_one_installation(self):
        self.clone_bundle()
        _, marker = self.deny_build_and_download()
        processes = [subprocess.Popen([str(self.root / "helper/scripts/run.sh"), "--version"],
                                      cwd=self.root.parent, env=self.environment,
                                      stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
                     for _ in range(6)]
        for process in processes:
            stdout, stderr = process.communicate(timeout=20)
            self.assertEqual(process.returncode, 0, stdout + stderr)
            self.assertTrue(stdout.startswith("system-sync-helper v"), stdout)
        version, checksum = (self.root / ".system-sync/helper/current").read_text().split()
        directory = self.root / ".system-sync/helper" / version / checksum
        self.assertEqual({p.name for p in directory.iterdir()},
                         {"system-sync-helper", "VERSION", "REVISION", "SHA256SUMS", "LICENSE-Go", "LICENSE-go-toml"})
        self.assertFalse((self.root / ".system-sync/helper/.install-lock").exists())
        self.assertFalse(marker.exists())

    def test_bundled_corruption_and_unsupported_platform_fail_without_fallback(self):
        bundle = self.clone_bundle()
        commands, marker = self.deny_build_and_download()
        for archive in bundle.glob("*.tar.gz"):
            archive.write_bytes(archive.read_bytes() + b"damaged")
        result = self.run_helper("--version", success=False)
        self.assertIn("SHA256", result.stderr)
        self.assertEqual(result.stdout, "")
        self.assertFalse((self.root / ".system-sync/helper/current").exists())
        uname = commands / "uname"
        uname.write_text('#!/bin/sh\nprintf "unsupported\\n"\n')
        uname.chmod(0o755)
        self.assertIn("仅支持 macOS / Linux", self.run_helper("--version", success=False).stderr)
        self.assertFalse(marker.exists())

    def test_existing_corrupt_install_is_not_replaced_by_bundle(self):
        version = (self.root / "helper/VERSION").read_text().strip()
        shutil.copytree(ROOT / "helper/prebuilt" / version, self.root / "helper/prebuilt" / version)
        self.binary.write_bytes(self.binary.read_bytes() + b"damaged")
        self.assertIn("校验失败", self.run_helper("--version", success=False).stderr)

    def test_bundle_verification_rejects_source_drift_and_damaged_archives(self):
        bundle = self.clone_bundle()
        for name in ("cmd", "internal", "licenses", "vendor"):
            shutil.copytree(ROOT / "helper" / name, self.root / "helper" / name)
        for name in ("go.mod", "go.sum"):
            shutil.copy2(ROOT / "helper" / name, self.root / "helper" / name)
        for name in ("build.sh", "verify.sh"):
            shutil.copy2(ROOT / "helper/scripts" / name, self.root / "helper/scripts" / name)
        _, marker = self.deny_build_and_download()

        def verify(success=True):
            result = subprocess.run([str(self.root / "helper/scripts/verify.sh")], cwd=self.root.parent,
                                    env=self.environment, capture_output=True, text=True, timeout=15)
            self.assertEqual(result.returncode == 0, success, result.stdout + result.stderr)
            return result

        verify()
        source = self.root / "helper/cmd/system-sync-helper/main.go"
        before = source.read_bytes()
        source.write_bytes(before + b"\n// modified\n")
        self.assertIn("构建输入已变化", verify(False).stderr)
        source.write_bytes(before)
        archive = next(bundle.glob("*.tar.gz"))
        archive.write_bytes(archive.read_bytes() + b"damaged")
        self.assertIn("SHA256 校验失败", verify(False).stderr)
        self.assertFalse(marker.exists())

    def test_all_four_bundles_have_consistent_metadata_and_binary_checksums(self):
        version = (ROOT / "helper/VERSION").read_text().strip()
        bundle = ROOT / "helper/prebuilt" / version
        checksums = dict(line.split()[::-1] for line in (bundle / "SHA256SUMS").read_text().splitlines())
        self.assertEqual(len(checksums), 4)
        for target in ("darwin_arm64", "darwin_amd64", "linux_arm64", "linux_amd64"):
            archive = bundle / f"system-sync-helper_{version}_{target}.tar.gz"
            self.assertEqual(hashlib.sha256(archive.read_bytes()).hexdigest(), checksums[archive.name])
            with tarfile.open(archive) as contents:
                self.assertEqual(set(contents.getnames()),
                                 {"system-sync-helper", "VERSION", "REVISION", "SHA256SUMS", "LICENSE-Go", "LICENSE-go-toml"})
                self.assertTrue(all(member.isfile() for member in contents.getmembers()))
                self.assertEqual(contents.extractfile("VERSION").read().decode().strip(), version)
                expected = contents.extractfile("SHA256SUMS").read().decode().split()
                self.assertEqual(expected[1:], ["system-sync-helper"])
                self.assertEqual(hashlib.sha256(contents.extractfile("system-sync-helper").read()).hexdigest(), expected[0])
                self.assertEqual(contents.extractfile("LICENSE-Go").read(), (ROOT / "helper/licenses/Go-LICENSE.txt").read_bytes())
                self.assertEqual(contents.extractfile("LICENSE-go-toml").read(), (ROOT / "helper/vendor/github.com/pelletier/go-toml/v2/LICENSE").read_bytes())
