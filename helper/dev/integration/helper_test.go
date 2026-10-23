//go:build integration

package integration

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

func init()                  { handlers["test_go_helper"] = helperCase }
func digest(b []byte) string { d := sha256.Sum256(b); return hex.EncodeToString(d[:]) }
func (f *fixture) binary() string {
	v := strings.Fields(read(f.t, filepath.Join(f.root, ".system-sync/helper/current")))
	return filepath.Join(f.root, ".system-sync/helper", v[0], v[1], "system-sync-helper")
}
func (f *fixture) deny() string {
	marker := filepath.Join(f.base, "unexpected-build-or-download")
	for _, n := range []string{"go", "python", "python3", "curl", "wget"} {
		f.shell(n, "echo called > "+quote(marker)+"\nexit 99")
	}
	f.env["PATH"] = f.bin + ":/usr/bin:/bin:/usr/sbin:/sbin"
	return marker
}
func (f *fixture) cloneBundle() string {
	v := strings.TrimSpace(read(f.t, filepath.Join(repo, "helper/VERSION")))
	p := filepath.Join(f.root, "helper/prebuilt", v)
	mkdir(f.t, p)
	targets := []string{"darwin_arm64", "darwin_amd64", "linux_arm64", "linux_amd64"}
	if os.Getenv("SYSTEM_SYNC_TEST_PROFILE") != "all" {
		targets = []string{runtime.GOOS + "_" + runtime.GOARCH}
	}
	for _, target := range targets {
		file := "system-sync-helper_" + v + "_" + target + ".tar.gz"
		copyTree(f.t, filepath.Join(repo, "helper/prebuilt", v, file), filepath.Join(p, file))
	}
	for _, n := range []string{"SHA256SUMS", "SOURCE_SHA256SUMS"} {
		copyTree(f.t, filepath.Join(repo, "helper/prebuilt", v, n), filepath.Join(p, n))
	}
	os.RemoveAll(filepath.Join(f.root, ".system-sync"))
	return p
}
func (f *fixture) archive(unsafe bool) (string, string) {
	t := f.t
	current := strings.Fields(read(t, filepath.Join(f.root, ".system-sync/helper/current")))
	installed := filepath.Join(f.root, ".system-sync/helper", current[0], current[1])
	dir := filepath.Join(f.root, "artifact")
	mkdir(t, dir)
	archive := filepath.Join(dir, "system-sync-helper_"+current[0]+"_"+runtime.GOOS+"_"+runtime.GOARCH+".tar.gz")
	fd, e := os.Create(archive)
	if e != nil {
		t.Fatal(e)
	}
	gz := gzip.NewWriter(fd)
	tw := tar.NewWriter(gz)
	for _, n := range []string{"system-sync-helper", "VERSION", "REVISION", "SHA256SUMS", "LICENSE-Go", "LICENSE-go-toml"} {
		p := filepath.Join(installed, n)
		if n == "LICENSE-Go" {
			p = filepath.Join(repo, "helper/licenses/Go-LICENSE.txt")
		}
		if n == "LICENSE-go-toml" {
			p = filepath.Join(repo, "helper/vendor/github.com/pelletier/go-toml/v2/LICENSE")
		}
		b := []byte(read(t, p))
		mode := int64(0644)
		if n == "system-sync-helper" {
			mode = 0755
		}
		if e := tw.WriteHeader(&tar.Header{Name: n, Size: int64(len(b)), Mode: mode, Typeflag: tar.TypeReg}); e != nil {
			t.Fatal(e)
		}
		tw.Write(b)
	}
	if unsafe {
		tw.WriteHeader(&tar.Header{Name: "../outside", Size: 4, Mode: 0644})
		tw.Write([]byte("evil"))
	}
	tw.Close()
	gz.Close()
	fd.Close()
	write(t, filepath.Join(dir, "SHA256SUMS"), digest([]byte(read(t, archive)))+"  "+filepath.Base(archive)+"\n")
	return dir, archive
}
func helperCase(f *fixture, name string) {
	if strings.Contains(name, "golden") || strings.Contains(name, "_match") || name == "test_empty_and_declared_tools_match_golden" {
		goldenCase(f, name)
		return
	}
	t := f.t
	f.helper()
	install := func(dir string) result {
		args := []string{filepath.Join(f.root, "helper/scripts/install.sh")}
		if dir != "" {
			args = append(args, dir)
		}
		return f.run(args...)
	}
	switch name {
	case "test_parser_stdin_contracts_and_no_partial_output_on_errors":
		for _, m := range []string{"apt", "dnf", "pacman", "aur", "zypper"} {
			b, _ := json.Marshal(map[string]any{m: map[string]any{"available": true, "packages": []any{map[string]any{"package": "git", "state": "installed"}}}})
			r := good(t, f.input(string(b), filepath.Join(f.root, "helper/scripts/run.sh"), "parse-status", "--manager", m, "-"))
			equal(t, r.out, m+"\tgit\tinstalled\n")
		}
		for _, text := range []string{`{}`, `[]`, `{"apt":{"available":false}}`, `{"apt":[]}`, `{"apt":[{"package":"git","state":"installed"},{"package":"git;id","state":"installed"}]}`} {
			r := f.input(text, filepath.Join(f.root, "helper/scripts/run.sh"), "parse-status", "--manager", "apt", "-")
			equal(t, r.code, 2)
			equal(t, r.out, "")
		}
		r := good(t, f.input(`<stream><to-remove><solvable name="extra"/></to-remove></stream>`, filepath.Join(f.root, "helper/scripts/run.sh"), "parse-zypper", "to-remove", "-"))
		equal(t, r.out, "extra\n")
		for _, text := range []string{`<broken>`, `<stream><to-remove><solvable name="git"/><solvable name="--help"/></to-remove></stream>`} {
			r := f.input(text, filepath.Join(f.root, "helper/scripts/run.sh"), "parse-zypper", "to-remove", "-")
			equal(t, r.code, 2)
			equal(t, r.out, "")
		}
	case "test_missing_corrupt_and_incompatible_binary_never_fall_back":
		marker := f.deny()
		binary := f.binary()
		write(t, binary, read(t, binary)+"modified")
		os.Chmod(binary, 0755)
		contains(t, bad(t, f.cli("--version")).err, "校验失败")
		write(t, filepath.Join(f.root, "helper/VERSION"), "v99.0.0\n")
		contains(t, bad(t, f.cli("--version")).err, "版本与仓库不符")
		f.copy("helper/VERSION")
		os.Remove(filepath.Join(f.root, ".system-sync/helper/current"))
		contains(t, bad(t, f.cli("--version")).err, "尚未准备")
		if exists(marker) {
			t.Fatal("unexpected fallback")
		}
	case "test_go_runtime_preflight_and_readonly_audit_need_no_interpreter":
		marker := f.deny()
		f.shell("uname", "printf 'Darwin\\n'")
		f.shell("brew", "exit 99")
		f.shell("mise", `case "$*" in
 '--version') printf '2026.10.15 fixture\n';;
 'ls --current --json') printf '{}\n';;
 'bootstrap dotfiles status --json') printf '{"files":[],"edits":[]}\n';;
 *) exit 91;; esac`)
		good(t, f.cli("runtime", "--check"))
		r := good(t, f.cli("audit", "--only", "tools", "--only", "config", "--json"))
		var v map[string]any
		json.Unmarshal([]byte(r.out), &v)
		equal(t, summary(v, "error"), float64(0))
		if exists(marker) {
			t.Fatal("runtime used interpreter")
		}
	case "test_write_workflows_and_shared_lock_need_no_python_or_go":
		writeWithoutRuntime(f)
	case "test_precompiled_archive_installs_offline_and_is_idempotent":
		dir, _ := f.archive(false)
		os.RemoveAll(filepath.Join(f.root, ".system-sync/helper"))
		good(t, install(dir))
		current := filepath.Join(f.root, ".system-sync/helper/current")
		first := read(t, current)
		contains(t, good(t, f.cli("--version")).out, "protocol=1")
		if !exists(filepath.Join(filepath.Dir(f.binary()), "LICENSE-Go")) {
			t.Fatal("license missing")
		}
		good(t, install(dir))
		equal(t, read(t, current), first)
	case "test_failed_archive_checks_preserve_active_binary":
		dir, p := f.archive(false)
		current := filepath.Join(f.root, ".system-sync/helper/current")
		before := read(t, current)
		write(t, p, read(t, p)+"corrupt")
		contains(t, bad(t, install(dir)).err, "SHA256")
		equal(t, read(t, current), before)
		dir, _ = f.archive(true)
		contains(t, bad(t, install(dir)).err, "路径")
		equal(t, read(t, current), before)
		if exists(filepath.Join(f.root, ".system-sync/outside")) {
			t.Fatal("unsafe member extracted")
		}
		good(t, f.cli("--version"))
	case "test_fresh_clone_bootstraps_offline_without_polluting_json":
		f.cloneBundle()
		marker := f.deny()
		f.shell("mise", `case "$*" in
 'ls --current --json') printf '{}\n';;
 'bootstrap dotfiles status --json') printf '{"files":[],"edits":[]}\n';;
 *) exit 91;; esac`)
		r := good(t, f.cli("audit", "--only", "tools", "--only", "config", "--json"))
		var v map[string]any
		json.Unmarshal([]byte(r.out), &v)
		equal(t, summary(v, "error"), float64(0))
		contains(t, r.err, "已安装")
		current := filepath.Join(f.root, ".system-sync/helper/current")
		before := stampedSnapshot(t, current)
		contains(t, good(t, f.cli("--version")).out, "protocol=1")
		excludes(t, good(t, f.cli("--version")).err, "已安装")
		equal(t, stampedSnapshot(t, current), before)
		if exists(marker) {
			t.Fatal("download attempted")
		}
	case "test_default_install_uses_repository_bundle_without_go":
		f.cloneBundle()
		marker := f.deny()
		good(t, install(""))
		contains(t, good(t, f.cli("--version")).out, "protocol=1")
		if exists(marker) {
			t.Fatal("unexpected compiler")
		}
	case "test_concurrent_first_launches_publish_one_installation":
		f.cloneBundle()
		marker := f.deny()
		var wg sync.WaitGroup
		results := make(chan result, 6)
		for i := 0; i < 6; i++ {
			wg.Add(1)
			go func() { defer wg.Done(); results <- f.cli("--version") }()
		}
		wg.Wait()
		close(results)
		for r := range results {
			good(t, r)
			if !strings.HasPrefix(r.out, "system-sync-helper v") {
				t.Fatal(r.out)
			}
		}
		entries, _ := os.ReadDir(filepath.Dir(f.binary()))
		names := []string{}
		for _, e := range entries {
			names = append(names, e.Name())
		}
		equal(t, setunion(names, nil), []string{"LICENSE-Go", "LICENSE-go-toml", "REVISION", "SHA256SUMS", "VERSION", "system-sync-helper"})
		if exists(filepath.Join(f.root, ".system-sync/helper/.install-lock")) || exists(marker) {
			t.Fatal("concurrent installation leaked lock/used compiler")
		}
	case "test_bundled_corruption_and_unsupported_platform_fail_without_fallback":
		bundle := f.cloneBundle()
		marker := f.deny()
		paths, _ := filepath.Glob(filepath.Join(bundle, "*.tar.gz"))
		for _, p := range paths {
			write(t, p, read(t, p)+"damaged")
		}
		r := bad(t, f.cli("--version"))
		contains(t, r.err, "SHA256")
		equal(t, r.out, "")
		if exists(filepath.Join(f.root, ".system-sync/helper/current")) {
			t.Fatal("corrupt bundle activated")
		}
		f.shell("uname", "printf 'unsupported\\n'")
		contains(t, bad(t, f.cli("--version")).err, "仅支持 macOS / Linux")
		if exists(marker) {
			t.Fatal("corrupt bundle fallback")
		}
	case "test_existing_corrupt_install_is_not_replaced_by_bundle":
		v := strings.TrimSpace(read(t, filepath.Join(f.root, "helper/VERSION")))
		copyTree(t, filepath.Join(repo, "helper/prebuilt", v), filepath.Join(f.root, "helper/prebuilt", v))
		binary := f.binary()
		write(t, binary, read(t, binary)+"damaged")
		os.Chmod(binary, 0755)
		contains(t, bad(t, f.cli("--version")).err, "校验失败")
	case "test_bundle_verification_rejects_source_drift_and_damaged_archives":
		bundle := f.cloneBundle()
		f.goConfig()
		f.copy("helper/cmd", "helper/internal", "helper/licenses", "helper/vendor", "helper/go.mod", "helper/go.sum", "helper/scripts/build.sh", "helper/scripts/verify.sh")
		marker := f.deny()
		os.Remove(filepath.Join(f.bin, "mise"))
		symlink(f, misePath(t), filepath.Join(f.bin, "mise"))
		verify := func() result {
			args := []string{filepath.Join(f.root, "helper/scripts/verify.sh")}
			if os.Getenv("SYSTEM_SYNC_TEST_PROFILE") != "all" {
				args = append(args, "--current-platform")
			}
			return f.run(args...)
		}
		contains(t, read(t, filepath.Join(bundle, "SOURCE_SHA256SUMS")), digest([]byte(goVersion(t)+"\n"))+"  ../dotfiles/common/mise/config.toml#tools.go\n")
		good(t, verify())
		config := filepath.Join(f.root, "dotfiles/common/mise/config.toml")
		write(t, config, "# comments\n[tools]\nnode=\"99.0.0\"\npython=\"99.0.0\"\n go = "+quoteTOML(goVersion(t))+" # fixed\n[env]\nEXAMPLE=\"unrelated\"\n_.file=\"missing-private.env\"\n")
		good(t, verify())
		before := read(t, config)
		write(t, config, "[tools]\ngo=\"9.8.7\"\n")
		contains(t, bad(t, verify()).err, "构建输入已变化")
		write(t, config, before)
		good(t, verify())
		write(t, config, "[env]\nsecret=\"private-parser-diagnostic\"\n[tools\n")
		r := bad(t, f.run("/bin/bash", "-e", "-o", "pipefail", "-c", `source "$1"; helper_source_manifest`, "manifest", filepath.Join(f.root, "helper/scripts/common.sh")))
		equal(t, r.out, "")
		contains(t, r.err, "tools.go")
		excludes(t, r.err, "private-parser-diagnostic")
		write(t, config, before)
		for _, rel := range []string{"helper/cmd/system-sync-helper/main.go", "helper/scripts/go-version.sh"} {
			p := filepath.Join(f.root, rel)
			before := read(t, p)
			write(t, p, before+"\n# modified\n")
			contains(t, bad(t, verify()).err, "构建输入已变化")
			write(t, p, before)
		}
		paths, _ := filepath.Glob(filepath.Join(bundle, "*.tar.gz"))
		write(t, paths[0], read(t, paths[0])+"damaged")
		contains(t, bad(t, verify()).err, "SHA256 校验失败")
		if exists(marker) {
			t.Fatal("verify used compiler/download")
		}
	case "test_all_four_bundles_have_consistent_metadata_and_binary_checksums":
		v := strings.TrimSpace(read(t, filepath.Join(repo, "helper/VERSION")))
		bundle := filepath.Join(repo, "helper/prebuilt", v)
		checks := strings.Split(strings.TrimSpace(read(t, filepath.Join(bundle, "SHA256SUMS"))), "\n")
		equal(t, len(checks), 4)
		targets := []string{"darwin_arm64", "darwin_amd64", "linux_arm64", "linux_amd64"}
		if os.Getenv("SYSTEM_SYNC_TEST_PROFILE") != "all" {
			targets = []string{runtime.GOOS + "_" + runtime.GOARCH}
		}
		for _, target := range targets {
			p := filepath.Join(bundle, "system-sync-helper_"+v+"_"+target+".tar.gz")
			b := []byte(read(t, p))
			contains(t, read(t, filepath.Join(bundle, "SHA256SUMS")), digest(b)+"  "+filepath.Base(p))
			files := readArchive(f, b)
			equal(t, len(files), 6)
			equal(t, strings.TrimSpace(string(files["VERSION"])), v)
			equal(t, strings.Fields(string(files["SHA256SUMS"])), []string{digest(files["system-sync-helper"]), "system-sync-helper"})
			equal(t, string(files["LICENSE-Go"]), read(t, filepath.Join(repo, "helper/licenses/Go-LICENSE.txt")))
			equal(t, string(files["LICENSE-go-toml"]), read(t, filepath.Join(repo, "helper/vendor/github.com/pelletier/go-toml/v2/LICENSE")))
		}
	default:
		t.Fatal("unported", name)
	}
}
func readArchive(f *fixture, b []byte) map[string][]byte {
	gz, e := gzip.NewReader(bytes.NewReader(b))
	if e != nil {
		f.t.Fatal(e)
	}
	defer gz.Close()
	r := tar.NewReader(gz)
	out := map[string][]byte{}
	for {
		h, e := r.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			f.t.Fatal(e)
		}
		if h.Typeflag != tar.TypeReg {
			f.t.Fatal("non-regular archive member", h.Name)
		}
		data, e := io.ReadAll(r)
		if e != nil {
			f.t.Fatal(e)
		}
		out[h.Name] = data
	}
	return out
}
func goldenCase(f *fixture, name string) {
	t := f.t
	var data struct {
		Cases map[string][]struct {
			Sections []string `json:"sections"`
			Strict   bool     `json:"strict"`
			ExitCode int      `json:"exit_code"`
			Report   any      `json:"report"`
			Text     string   `json:"text"`
		}
	}
	readJSON(t, filepath.Join(repo, "tests/fixtures/audit/reports.json"), &data)
	gold := data.Cases[name]
	i := 0
	newA := func() *auditFixture { return newAudit(newFixture(t)) }
	compare := func(a *auditFixture, sections []string, strict, text bool) string {
		if i >= len(gold) {
			t.Fatal("extra golden scenario")
		}
		expected := gold[i]
		i++
		equal(t, sections, expected.Sections)
		equal(t, strict, expected.Strict)
		r := a.invoke(sections, strict, false)
		equal(t, r.code, expected.ExitCode)
		normalized := strings.ReplaceAll(r.out, a.base, "<fixture>")
		var v any
		if e := json.Unmarshal([]byte(normalized), &v); e != nil {
			t.Fatal(e, r.all())
		}
		equal(t, v, expected.Report)
		excludes(t, r.all(), "private-query")
		excludes(t, r.out, "fixture-private-token")
		if text {
			r = a.invoke(sections, strict, true)
			equal(t, r.code, expected.ExitCode)
			equal(t, strings.ReplaceAll(r.out, a.base, "<fixture>"), expected.Text)
			excludes(t, r.out, "fixture-private-token")
			return r.out
		}
		return ""
	}
	all := []string{"tools", "packages", "path", "config"}
	switch name {
	case "test_all_candidate_details_and_healthy_omission_count_match_golden":
		a := newA()
		candidates, roots := []string{}, []string{}
		for j := 0; j < 12; j++ {
			candidates = append(candidates, fmt.Sprintf("fixture-local-%02d", j))
			roots = append(roots, fmt.Sprintf("fixture-root-%02d", j))
		}
		candidates[10] = "fixture-private-token"
		a.packages = packageStatus(packageRequired[0], map[string][]string{"Brewfile formula 根声明": roots, "Homebrew 可识别的未声明 requested formula": candidates})
		out := compare(a, []string{"packages"}, false, true)
		contains(t, out, "    - fixture-local-11")
		contains(t, out, "    - [REDACTED]")
		excludes(t, out, "    - fixture-root-11")
		contains(t, out, "另有 4 项已核对条目")
	case "test_empty_and_declared_tools_match_golden":
		compare(newA(), all, false, false)
		a := newA()
		a.tool("node", "node", false)
		compare(a, all, true, false)
	case "test_path_shadowing_alternatives_and_other_installers_match":
		a := newA()
		p := a.tool("node", "node", true)
		shadow := filepath.Join(a.home, ".local/bin/node")
		executable(t, shadow, "#!/bin/sh\nexit 99\n")
		executable(t, filepath.Join(a.home, ".nvm/versions/node/v0.1.0/bin/node"), "#!/bin/sh\nexit 99\n")
		a.env["PATH"] = filepath.Dir(shadow) + ":" + filepath.Dir(p) + ":/usr/bin:/bin"
		compare(a, all, false, false)
	case "test_linked_rust_shim_virtual_environment_and_caller_path_match":
		a := newA()
		setupRust(a)
		compare(a, all, false, false)
		a = newA()
		setupVirtual(a)
		compare(a, all, false, false)
		a.env["SYSTEM_SYNC_AUDIT_CALLER_PATH"] = a.home + "::relative:" + filepath.Join(a.home, "absent") + ":" + a.home
		compare(a, all, false, false)
	case "test_all_platform_reports_version_drift_and_cleanup_review_match":
		for _, required := range packageRequired {
			a := newA()
			a.packages = packageStatus(required, nil)
			compare(a, []string{"packages"}, false, false)
		}
		a := newA()
		a.packages = packageStatus(packageRequired[1], map[string][]string{"mise 最终生效声明": {"pacman:git [version_mismatch]"}, "声明但尚未以同名具体包安装": {"missing-tool"}, "当前显式安装包中未声明、但受保护而保留": {"kernel"}, "apply 时会从 explicit 降级为 dependency 的未声明包": {"old-tool"}})
		compare(a, []string{"packages"}, false, false)
		a = newA()
		a.packages += "\nHomebrew cleanup 精确预演\n  review details\n状态模式：bundle check 退出码为 1。\n"
		compare(a, []string{"packages"}, false, false)
	case "test_private_files_config_candidates_and_redaction_match":
		a := newA()
		for _, n := range []string{".gitconfig", ".config/mise/secrets.env", "private-application/token.json"} {
			write(t, filepath.Join(a.home, n), "private-file-value")
		}
		a.config = map[string]any{"files": []any{map[string]any{"target": "~/.zshrc", "state": "missing"}}, "edits": []any{map[string]any{"path": "~/.codex/config.toml", "state": "applied", "edit": map[string]any{"value": "private-edit-value"}}}, "orphaned": []any{map[string]any{"target": "~/.retired"}}}
		a.env["EXTRA_SECRET"] = "ok"
		a.tools = map[string]any{"fixture-private-token": []any{map[string]any{"version": "1.0.0", "installed": false}}}
		compare(a, all, false, false)
	case "test_incomplete_schemas_and_query_failure_match":
		for _, tools := range []any{[]any{}, map[string]any{"node": []any{}}, map[string]any{"node": []any{map[string]any{"installed": true}}}, map[string]any{"node": []any{map[string]any{"version": "1.0.0", "installed": true, "source": "wrong"}}}} {
			a := newA()
			a.tools = tools
			compare(a, []string{"tools", "path"}, false, false)
		}
		for _, s := range []any{"future_state", []any{}} {
			a := newA()
			a.config = map[string]any{"files": []any{map[string]any{"target": "~/.zshrc", "state": s}}, "edits": []any{}}
			compare(a, []string{"config"}, false, false)
		}
		a := newA()
		a.failures = [][]string{{"mise", "ls", "--current", "--json"}}
		compare(a, all, false, false)
	default:
		t.Fatal("unported golden", name)
	}
	equal(t, i, len(gold))
}
func writeWithoutRuntime(f *fixture) {
	t := f.t
	marker := f.deny()
	f.env["WRITE_TEST_BASE"] = f.base
	f.env["MISE_CONFIG_DIR"] = filepath.Join(f.home, ".config/mise")
	f.env["MISE_STATE_DIR"] = filepath.Join(f.home, ".local/state/mise")
	f.env["XDG_STATE_HOME"] = filepath.Join(f.home, ".local/state")
	f.mock("workflows", "uname", "mise", "brew")
	state, status := filepath.Join(f.base, "installed"), filepath.Join(f.base, "dotfiles-status.json")
	write(t, state, "git\nmise\n")
	write(t, status, `{"files":[],"edits":[]}`)
	p := filepath.Join(f.root, "packages/macos")
	manifest := filepath.Join(p, "Brewfile")
	write(t, manifest, "brew \"git\"\nbrew \"mise\"\n")
	write(t, filepath.Join(p, "protected-packages.txt"), "formula:git\nformula:mise\n")
	write(t, filepath.Join(p, "trusted-formulae.txt"), "# exact trust\n")
	before := read(t, manifest)
	good(t, f.cli("package", "install", "gitui", "--dry-run"))
	equal(t, read(t, manifest), before)
	if exists(filepath.Join(f.root, ".system-sync/history")) {
		t.Fatal("package preview wrote history")
	}
	release := lock(t, f.root)
	r := bad(t, f.input("yes\n", filepath.Join(f.root, "helper/scripts/run.sh"), "package", "install", "gitui"))
	contains(t, r.err, "另一条配置或包操作")
	equal(t, read(t, manifest), before)
	release()
	good(t, f.input("yes\n", filepath.Join(f.root, "helper/scripts/run.sh"), "package", "install", "gitui"))
	contains(t, read(t, manifest), `brew "gitui"`)
	extensions := filepath.Join(f.root, "extensions.toml")
	write(t, extensions, "[common]\n\"example.shared\"=\"1.2.3\"\n")
	extstate := filepath.Join(f.base, "extensions.json")
	write(t, extstate, `{"example.local":"4.5.6"}`)
	f.env["VSCODE_TEST_STATE"] = extstate
	f.env["VSCODE_TEST_LOG"] = filepath.Join(f.base, "extcalls")
	f.mock("vscode", "code")
	good(t, f.cli("vscode", "sync", "--manifest", extensions, "--code", filepath.Join(f.bin, "code")))
	s := jsonData(f, extstate)
	equal(t, s["example.local"], "4.5.6")
	equal(t, s["example.shared"], "1.2.3")
	private := filepath.Join(f.home, ".codex/config.toml")
	write(t, private, "private_canary=\"keep-local\"\n")
	good(t, f.cli("config-history", "init"))
	good(t, f.cli("config-history", "save", "-d", "isolated --named checkpoint"))
	if !exists(filepath.Join(f.base, "saved-by-mise")) {
		t.Fatal("history save not invoked")
	}
	source := filepath.Join(f.root, "private-source")
	write(t, source, "private-canary-content\n")
	os.Chmod(source, 0600)
	target := filepath.Join(f.home, "local-config")
	symlink(f, source, target)
	writeJSON(t, status, map[string]any{"files": []any{map[string]any{"mode": "symlink", "target": target, "origin": map[string]any{"config": filepath.Join(f.root, "mise.toml"), "source": source}}}, "edits": []any{}})
	r = good(t, f.cli("config-detach", "--dry-run"))
	if !islink(target) {
		t.Fatal("preview detached")
	}
	excludes(t, r.all(), "private-canary-content")
	good(t, f.cli("config-detach", "--yes"))
	if islink(target) {
		t.Fatal("detach did not materialize")
	}
	equal(t, read(t, target), read(t, source))
	info, _ := os.Stat(target)
	equal(t, info.Mode().Perm(), os.FileMode(0600))
	equal(t, read(t, private), "private_canary=\"keep-local\"\n")
	if exists(filepath.Join(f.home, ".config/mise/conf.d/system-sync-history.toml")) || exists(marker) {
		t.Fatal("write workflow needed interpreter or left enrollment")
	}
}
