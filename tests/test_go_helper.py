"""Compare the Go CLI to the reference and validate offline binary activation."""

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
import test_audit as reference
import test_parsers as parsers


ROOT = Path(__file__).resolve().parents[1]
FAKE_MISE = '''import json, os, sys
data = json.loads(open(os.environ["GO_HELPER_FIXTURE"]).read())
args = sys.argv[1:]
if args in data["failures"]:
    print("private-query-output")
    print("private-query-diagnostic", file=sys.stderr)
    sys.exit(17)
if args == ["ls", "--current", "--json"]: print(json.dumps(data["tools"]))
elif args[:1] == ["which"]: print(data["paths"][args[-1]])
elif args == ["bootstrap", "dotfiles", "status", "--json"]: print(json.dumps(data["config"]))
else: sys.exit(91)
'''


class GoAuditParityTest(unittest.TestCase):
    def fixture(self):
        fixture = reference.AuditTest("test_missing_tools_are_reported_without_installing")
        fixture.setUp()
        self.addCleanup(fixture.doCleanups)
        prepare_helper(fixture.root)
        return fixture

    def compare(self, fixture, selected=reference.AUDIT.SECTIONS, strict=False):
        commands = fixture.root / "query-bin"
        commands.mkdir(exist_ok=True)
        state = fixture.root / "query-fixture.json"
        state.write_text(json.dumps({"tools": fixture.runner.tools, "paths": fixture.runner.paths,
                                     "config": fixture.runner.config,
                                     "failures": [list(call[1:]) for call in fixture.runner.failures]}))
        (commands / "mise.py").write_text(FAKE_MISE)
        mise = commands / "mise"
        mise.write_text(f'#!/bin/sh\nexec {shlex.quote(sys.executable)} -I -B {shlex.quote(str(commands / "mise.py"))} "$@"\n')
        mise.chmod(0o755)
        packages = fixture.root / "packages-output.txt"
        packages.write_text(fixture.runner.packages)
        status = fixture.root / "scripts/system-sync.sh"
        status.write_text('#!/bin/sh\nexec /bin/cat ' + shlex.quote(str(packages)) + '\n')
        status.chmod(0o755)
        (fixture.root / "dotfiles/common/shell/path.sh").write_text(
            '_system_sync_user_path() { PATH=' + shlex.quote(fixture.runner.policy) + '; export PATH; }\n')
        environment = dict(fixture.environment, GO_HELPER_FIXTURE=str(state),
                           PATH=str(commands) + ":" + fixture.environment["PATH"])
        expected = reference.AUDIT.Audit(fixture.root, environment).execute(selected)
        args = [str(fixture.root / "scripts/system-sync-helper.sh"), "audit", "--json"]
        for section in selected:
            args += ["--only", section]
        if strict:
            args += ["--strict"]
        result = subprocess.run(args, cwd=fixture.base, env=environment,
                                capture_output=True, text=True, timeout=30)
        self.assertEqual(result.returncode, reference.AUDIT.exit_code(expected, strict), result.stderr)
        self.assertEqual(json.loads(result.stdout), expected)
        self.assertNotIn("private-query", result.stdout + result.stderr)
        self.assertNotIn("fixture-private-token", result.stdout)

    def test_empty_and_declared_tools_match_reference(self):
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
        for required in reference.AUDIT.PACKAGE_REQUIRED:
            fixture = self.fixture()
            fixture.runner.packages = reference.package_status(required)
            self.compare(fixture, ("packages",))
        fixture = self.fixture()
        fixture.runner.packages = reference.package_status(reference.AUDIT.PACKAGE_REQUIRED[1],
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
        shutil.copy2(ROOT / "scripts/helper-install.sh", self.root / "scripts/helper-install.sh")
        self.environment = dict(os.environ, MISE_AUTO_INSTALL="false", MISE_TASK_RUN_AUTO_INSTALL="false")

    def run_helper(self, *args, text="", success=True):
        result = subprocess.run([str(self.root / "scripts/system-sync-helper.sh"), *args],
                                cwd=self.root.parent, env=self.environment, input=text,
                                capture_output=True, text=True, timeout=15)
        self.assertEqual(result.returncode == 0, success, result.stdout + result.stderr)
        return result

    def test_parser_stdin_contracts_and_no_partial_output_on_errors(self):
        for manager in ("apt", "dnf", "pacman", "aur", "zypper"):
            data = {manager: {"available": True, "packages": [{"package": "git", "state": "installed"}]}}
            result = self.run_helper("parse-status", "--manager", manager, "-", text=json.dumps(data))
            expected = parsers.MISE.parse_status(data, (manager,))
            self.assertEqual(result.stdout, "".join("\t".join(row) + "\n" for row in expected))
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
        (self.root / "helper-version.txt").write_text("v99.0.0\n")
        self.assertIn("版本与仓库不符", self.run_helper("--version", success=False).stderr)
        (self.root / "helper-version.txt").write_text((ROOT / "helper-version.txt").read_text())
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
            output.add(ROOT / "licenses/Go-LICENSE.txt", arcname="LICENSE-Go")
            if bad_name:
                info = tarfile.TarInfo("../outside")
                info.size = 4
                output.addfile(info, io.BytesIO(b"evil"))
        digest = hashlib.sha256(archive.read_bytes()).hexdigest()
        (directory / "SHA256SUMS").write_text(f"{digest}  {archive.name}\n")
        return directory, archive

    def install(self, directory=None, success=True):
        command = [str(self.root / "scripts/helper-install.sh")]
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
        version = (self.root / "helper-version.txt").read_text().strip()
        target = self.root / "prebuilt" / version
        shutil.copytree(ROOT / "prebuilt" / version, target)
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
        processes = [subprocess.Popen([str(self.root / "scripts/system-sync-helper.sh"), "--version"],
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
                         {"system-sync-helper", "VERSION", "REVISION", "SHA256SUMS", "LICENSE-Go"})
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
        version = (self.root / "helper-version.txt").read_text().strip()
        shutil.copytree(ROOT / "prebuilt" / version, self.root / "prebuilt" / version)
        self.binary.write_bytes(self.binary.read_bytes() + b"damaged")
        self.assertIn("校验失败", self.run_helper("--version", success=False).stderr)

    def test_bundle_verification_rejects_source_drift_and_damaged_archives(self):
        bundle = self.clone_bundle()
        for name in ("cmd", "internal", "licenses"):
            shutil.copytree(ROOT / name, self.root / name)
        for name in ("go.mod",):
            shutil.copy2(ROOT / name, self.root / name)
        if (ROOT / "go.sum").is_file():
            shutil.copy2(ROOT / "go.sum", self.root / "go.sum")
        for name in ("helper-build.sh", "helper-verify.sh"):
            shutil.copy2(ROOT / "scripts" / name, self.root / "scripts" / name)
        _, marker = self.deny_build_and_download()

        def verify(success=True):
            result = subprocess.run([str(self.root / "scripts/helper-verify.sh")], cwd=self.root.parent,
                                    env=self.environment, capture_output=True, text=True, timeout=15)
            self.assertEqual(result.returncode == 0, success, result.stdout + result.stderr)
            return result

        verify()
        source = self.root / "cmd/system-sync-helper/main.go"
        before = source.read_bytes()
        source.write_bytes(before + b"\n// modified\n")
        self.assertIn("构建输入已变化", verify(False).stderr)
        source.write_bytes(before)
        archive = next(bundle.glob("*.tar.gz"))
        archive.write_bytes(archive.read_bytes() + b"damaged")
        self.assertIn("SHA256 校验失败", verify(False).stderr)
        self.assertFalse(marker.exists())

    def test_all_four_bundles_have_consistent_metadata_and_binary_checksums(self):
        version = (ROOT / "helper-version.txt").read_text().strip()
        bundle = ROOT / "prebuilt" / version
        checksums = dict(line.split()[::-1] for line in (bundle / "SHA256SUMS").read_text().splitlines())
        self.assertEqual(len(checksums), 4)
        for target in ("darwin_arm64", "darwin_amd64", "linux_arm64", "linux_amd64"):
            archive = bundle / f"system-sync-helper_{version}_{target}.tar.gz"
            self.assertEqual(hashlib.sha256(archive.read_bytes()).hexdigest(), checksums[archive.name])
            with tarfile.open(archive) as contents:
                self.assertEqual(set(contents.getnames()),
                                 {"system-sync-helper", "VERSION", "REVISION", "SHA256SUMS", "LICENSE-Go"})
                self.assertTrue(all(member.isfile() for member in contents.getmembers()))
                self.assertEqual(contents.extractfile("VERSION").read().decode().strip(), version)
                expected = contents.extractfile("SHA256SUMS").read().decode().split()
                self.assertEqual(expected[1:], ["system-sync-helper"])
                self.assertEqual(hashlib.sha256(contents.extractfile("system-sync-helper").read()).hexdigest(), expected[0])
                self.assertEqual(contents.extractfile("LICENSE-Go").read(), (ROOT / "licenses/Go-LICENSE.txt").read_bytes())
