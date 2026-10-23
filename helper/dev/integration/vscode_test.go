//go:build integration

package integration

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

func init() { handlers["test_vscode_extensions"] = vscodeCase }
func tomlData(f *fixture, p string) map[string]any {
	var v map[string]any
	if e := toml.Unmarshal([]byte(read(f.t, p)), &v); e != nil {
		f.t.Fatal(e)
	}
	return v
}
func vscodeCase(f *fixture, name string) {
	t := f.t
	f.helper()
	manifest := filepath.Join(f.base, "extensions.toml")
	state := filepath.Join(f.base, "state.json")
	log := filepath.Join(f.base, "calls.jsonl")
	write(t, manifest, "[common]\n\"example.shared\" = \"1.2.3\"\n")
	write(t, state, `{"example.local":"4.5.6"}`)
	f.env["VSCODE_TEST_STATE"] = state
	f.env["VSCODE_TEST_LOG"] = log
	f.mock("vscode", "code")
	code := filepath.Join(f.bin, "code")
	cli := func(args ...string) result {
		return f.cli(append(append([]string{"vscode"}, args...), "--manifest", manifest, "--code", code)...)
	}
	capture := func(args ...string) result { return cli(append([]string{"status", "--capture"}, args...)...) }
	backupRoot := filepath.Join(f.root, ".system-sync/capture")
	stateData := func() map[string]any { var v map[string]any; readJSON(t, state, &v); return v }
	queryOnly := func() {
		text := read(t, log)
		excludes(t, text, "--install-extension")
		excludes(t, text, "--uninstall-extension")
		for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
			contains(t, line, "--list-extensions")
		}
	}
	switch name {
	case "test_capture_defaults_to_preview_and_lists_selected_and_excluded_versions":
		write(t, state, `{"zeta.local":"4.5.6","alpha.local":"1.0.0-beta.2","example.shared":"9.0.0"}`)
		before, installed := read(t, manifest), read(t, state)
		for _, args := range [][]string{nil, {"--dry-run"}, {"--only", "ALPHA.LOCAL,zeta.local", "--exclude", "zeta.local"}} {
			r := good(t, capture(args...))
			for _, s := range []string{"以上仅为预览", "alpha.local@1.0.0-beta.2", `+"alpha.local" = "1.0.0-beta.2"`, "到 [common]"} {
				contains(t, r.out, s)
			}
			if has(args, "--only") {
				excludes(t, r.out, `+"example.shared"`)
				contains(t, r.out, "[~] 跳过 2")
				excludes(t, r.out, `+"zeta.local"`)
			} else {
				contains(t, r.out, "example.shared: 1.2.3 -> 9.0.0 [common]")
				contains(t, r.out, `+"example.shared" = "9.0.0"`)
			}
			excludes(t, r.out, "[macos]")
			excludes(t, r.out, "[linux]")
			equal(t, read(t, manifest), before)
			equal(t, read(t, state), installed)
			if exists(backupRoot) {
				t.Fatal("preview created backup")
			}
		}
		queryOnly()
	case "test_capture_apply_appends_common_preserves_pins_and_is_idempotent":
		source := "# keep comment\n[common]\n\"example.shared\" = \"1.2.3\" # pinned\n\"example.missing\" = \"2.0.0\"\n"
		write(t, manifest, source)
		os.Chmod(manifest, 0640)
		write(t, state, `{"example.shared":"9.0.0","example.local":"4.5.6","excluded.local":"8.0.0"}`)
		installed := read(t, state)
		args := []string{"--apply", "--yes", "--only", "example.local,excluded.local", "--exclude", "excluded.local", "--exclude", "absent.local"}
		contains(t, good(t, capture(args...)).out, "[+] 已更新扩展清单")
		equal(t, tomlData(f, manifest)["common"], map[string]any{"example.shared": "1.2.3", "example.missing": "2.0.0", "example.local": "4.5.6"})
		contains(t, read(t, manifest), source)
		info, _ := os.Stat(manifest)
		equal(t, info.Mode().Perm(), os.FileMode(0640))
		paths, _ := filepath.Glob(filepath.Join(backupRoot, "*-vscode-*"))
		equal(t, len(paths), 1)
		equal(t, read(t, filepath.Join(paths[0], "extensions.toml.before")), source)
		equal(t, read(t, filepath.Join(paths[0], "extensions.toml.candidate")), read(t, manifest))
		info, _ = os.Stat(filepath.Join(paths[0], "extensions.toml.before"))
		equal(t, info.Mode().Perm(), os.FileMode(0600))
		info, _ = os.Stat(paths[0])
		equal(t, info.Mode().Perm(), os.FileMode(0700))
		before := snapshot(t, backupRoot)
		text := read(t, manifest)
		stamp := stampedSnapshot(t, manifest)
		contains(t, good(t, capture(args...)).out, "无需修改清单")
		equal(t, snapshot(t, backupRoot), before)
		equal(t, read(t, manifest), text)
		equal(t, stampedSnapshot(t, manifest), stamp)
		equal(t, read(t, state), installed)
		queryOnly()
	case "test_capture_explicit_platform_group_keeps_its_scope_on_later_default_capture":
		platform := nativePlatform()
		installed := read(t, state)
		contains(t, good(t, capture("--group", platform, "--apply", "--yes")).out, "到 ["+platform+"]")
		data := tomlData(f, manifest)
		equal(t, data["common"], map[string]any{"example.shared": "1.2.3"})
		equal(t, data[platform], map[string]any{"example.local": "4.5.6"})
		text := read(t, manifest)
		stamp := stampedSnapshot(t, manifest)
		contains(t, good(t, capture("--apply", "--yes")).out, "无需修改清单")
		equal(t, read(t, manifest), text)
		equal(t, stampedSnapshot(t, manifest), stamp)
		equal(t, read(t, state), installed)
		queryOnly()
	case "test_capture_exclusions_win_and_noop_does_not_create_backups":
		before := read(t, manifest)
		r := good(t, capture("--only", "example.local", "--exclude", "EXAMPLE.LOCAL", "--apply", "--yes"))
		contains(t, r.out, "[~] 跳过 1")
		contains(t, r.out, "无需修改清单")
		excludes(t, r.out, "[+] 已更新扩展清单")
		equal(t, read(t, manifest), before)
		if exists(backupRoot) {
			t.Fatal("noop backup")
		}
	case "test_capture_rejects_conflicting_flags_invalid_filters_and_wrong_platform":
		other := "macos"
		if nativePlatform() == "macos" {
			other = "linux"
		}
		before := read(t, manifest)
		for _, args := range [][]string{{"--apply", "--dry-run"}, {"--yes"}, {"--group", other}, {"--only", "missing.extension"}, {"--exclude", "example.*"}, {"--only", "example.local@4.5.6"}, {"--group", "future"}} {
			bad(t, capture(args...))
			equal(t, read(t, manifest), before)
		}
		for _, action := range []string{"status", "sync"} {
			equal(t, cli(action, "--exclude", "example.local").code, 2)
		}
		bad(t, cli("sync", "--capture"))
		if exists(backupRoot) {
			t.Fatal("invalid flags wrote backup")
		}
		if exists(log) {
			excludes(t, read(t, log), "--install-extension")
		}
	case "test_capture_refreshes_managed_versions_without_installing":
		source := "# keep\n[common]\n\"anthropic.claude-code\" = \"2.1.304\" # pinned\n\"example.missing\" = \"1.0.0\"\n"
		write(t, manifest, source)
		write(t, state, `{"anthropic.claude-code":"2.1.309"}`)
		installed := read(t, state)
		r := good(t, capture())
		contains(t, r.out, "待更新 1 个已纳管扩展版本")
		contains(t, r.out, "anthropic.claude-code: 2.1.304 -> 2.1.309 [common]")
		contains(t, r.out, `+"anthropic.claude-code" = "2.1.309" # pinned`)
		excludes(t, r.out, "待追加")
		equal(t, read(t, manifest), source)
		contains(t, good(t, capture("--exclude", "anthropic.claude-code", "--apply", "--yes")).out, "无需修改清单")
		if exists(backupRoot) {
			t.Fatal("excluded update backup")
		}
		contains(t, good(t, capture("--only", "anthropic.claude-code", "--apply", "--yes")).out, "已更新扩展清单")
		equal(t, read(t, manifest), strings.ReplaceAll(source, "2.1.304", "2.1.309"))
		paths, _ := filepath.Glob(filepath.Join(backupRoot, "*"))
		equal(t, len(paths), 1)
		equal(t, read(t, filepath.Join(paths[0], "extensions.toml.before")), source)
		before := snapshot(t, backupRoot)
		contains(t, good(t, capture("--apply", "--yes")).out, "无需修改清单")
		equal(t, snapshot(t, backupRoot), before)
		equal(t, read(t, state), installed)
		queryOnly()
	case "test_sync_warns_before_rolling_newer_extensions_back":
		write(t, manifest, "[common]\n\"anthropic.claude-code\" = \"2.1.304\"\n")
		write(t, state, `{"anthropic.claude-code":"2.1.309","example.local":"4.5.6"}`)
		before := read(t, state)
		for _, args := range [][]string{{"status"}, {"sync", "--dry-run"}} {
			r := good(t, cli(args...))
			contains(t, r.out, "2.1.309 -> 2.1.304（降级/回滚至清单固定版本）")
			contains(t, r.out, "mise run vscode-status --capture")
			equal(t, read(t, state), before)
		}
		r := good(t, cli("sync"))
		if strings.Index(r.out, "降级/回滚") >= strings.Index(r.out, "安装 VS Code 扩展") {
			t.Fatal("rollback warning too late")
		}
		equal(t, stateData(), map[string]any{"anthropic.claude-code": "2.1.304", "example.local": "4.5.6"})
	case "test_capture_noninteractive_apply_requires_explicit_yes_flag":
		before := read(t, manifest)
		r := bad(t, f.input("yes\n", filepath.Join(f.root, "helper/scripts/run.sh"), "vscode", "status", "--capture", "--apply", "--manifest", manifest, "--code", code))
		contains(t, r.err, "交互终端")
		equal(t, read(t, manifest), before)
		if exists(backupRoot) {
			t.Fatal("noninteractive backup")
		}
	case "test_capture_query_failure_and_invalid_manifest_do_not_write":
		before := read(t, manifest)
		write(t, filepath.Join(f.base, "bad-list"), "")
		contains(t, bad(t, capture("--apply", "--yes")).err, "无法识别")
		equal(t, read(t, manifest), before)
		os.Remove(log)
		write(t, manifest, "[common]\n\"example.shared\" = \"latest\"\n")
		bad(t, capture("--apply", "--yes"))
		if exists(log) || exists(backupRoot) {
			t.Fatal("invalid manifest queried or wrote backup")
		}
	case "test_capture_task_forwards_flags_with_real_mise_in_isolated_home":
		f.copy("mise.toml")
		manifest = filepath.Join(f.root, "dotfiles/common/vscode/extensions.toml")
		write(t, manifest, "[common]\n\"example.shared\" = \"1.2.3\"\n")
		for i, args := range [][]string{{"--exclude", "example.local"}, {"--apply", "--yes", "--only", "example.local"}} {
			good(t, f.mise(append([]string{"run", "vscode-status", "--capture", "--code", code}, args...)...))
			_, ok := tomlData(f, manifest)["common"].(map[string]any)["example.local"]
			equal(t, ok, i == 1)
		}
		write(t, state, `{"example.shared":"9.0.0","example.local":"4.5.6"}`)
		r := good(t, f.mise("run", "vscode-status", "--capture", "--code", code, "--only", "example.shared", "--apply", "--yes"))
		contains(t, r.out, "example.shared: 1.2.3 -> 9.0.0")
		equal(t, tomlData(f, manifest)["common"].(map[string]any)["example.shared"], "9.0.0")
		queryOnly()
	case "test_platform_selection_and_conflict_fail_before_cli":
		write(t, manifest, "[common]\n\"example.shared\"=\"1.2.3\"\n[macos]\n\"example.mac\"=\"2.0.0\"\n[linux]\n\"example.linux\"=\"3.0.0\"\n")
		platform, other := "linux", "mac"
		if nativePlatform() == "macos" {
			platform, other = "mac", "linux"
		}
		r := good(t, cli("status"))
		contains(t, r.out, "example.shared")
		contains(t, r.out, "example."+platform)
		excludes(t, r.out, "example."+other)
		os.Remove(log)
		write(t, manifest, read(t, manifest)+"\"--bad.flag\"=\"1.0.0\"\n")
		bad(t, cli("sync"))
		if exists(log) {
			t.Fatal("invalid id reached code")
		}
	case "test_status_and_preview_do_not_install_or_change_manifest":
		before, installed := read(t, manifest), read(t, state)
		for _, args := range [][]string{{"status"}, {"sync", "--dry-run"}} {
			r := good(t, cli(args...))
			for _, s := range []string{"[!] example.shared", "[~] 保留 1", "  - example.local@4.5.6\n"} {
				contains(t, r.out, s)
			}
		}
		equal(t, read(t, manifest), before)
		equal(t, read(t, state), installed)
		queryOnly()
	case "test_sync_verifies_versions_preserves_extra_and_isolates_both_directories":
		args := []string{"sync", "--user-data-dir", filepath.Join(f.base, "user data"), "--extensions-dir", filepath.Join(f.base, "extensions")}
		contains(t, good(t, cli(args...)).out, "  - example.local@4.5.6\n")
		equal(t, stateData(), map[string]any{"example.local": "4.5.6", "example.shared": "1.2.3"})
		contains(t, good(t, cli(args...)).out, "无需同步")
		count := 0
		for _, line := range strings.Split(strings.TrimSpace(read(t, log)), "\n") {
			contains(t, line, "--user-data-dir")
			contains(t, line, "--extensions-dir")
			if strings.Contains(line, "--install-extension") {
				count++
			}
		}
		equal(t, count, 1)
		excludes(t, read(t, log), "--uninstall-extension")
	case "test_extra_details_are_sorted_when_managed_extensions_match":
		s := map[string]any{"zeta.local": "4.5.6", "example.shared": "1.2.3", "alpha.local": "1.0.0-beta.2"}
		writeJSON(t, state, s)
		expected := "[~] 保留 2 个清单外扩展：\n  - alpha.local@1.0.0-beta.2\n  - zeta.local@4.5.6\n"
		for _, args := range [][]string{{"status"}, {"sync", "--dry-run"}, {"sync"}} {
			r := good(t, cli(args...))
			contains(t, r.out, expected)
			contains(t, r.out, "无需同步")
			equal(t, stateData(), s)
		}
		equal(t, good(t, cli("status")).out, expected+"[+] 1 个公共扩展均符合清单，无需同步。\n")
		queryOnly()
		write(t, state, `{"example.shared":"1.2.3"}`)
		excludes(t, good(t, cli("status")).out, "清单外扩展")
	case "test_unknown_output_and_partial_isolation_fail_closed":
		write(t, filepath.Join(f.base, "bad-list"), "")
		bad(t, cli("sync"))
		excludes(t, read(t, log), "--install-extension")
		os.Remove(log)
		bad(t, cli("sync", "--extensions-dir", filepath.Join(f.base, "ext")))
		if exists(log) {
			t.Fatal("partial isolation queried code")
		}
	case "test_failed_and_ineffective_installations_are_not_reported_as_success":
		for _, marker := range []string{"fail-install", "wrong-version"} {
			path := filepath.Join(f.base, marker)
			write(t, path, "")
			excludes(t, bad(t, cli("sync")).out, "[+] 已同步")
			os.Remove(path)
		}
	case "test_sync_reports_installation_before_completion_without_private_output":
		write(t, filepath.Join(f.base, "wait-install"), "")
		r := f.live([]string{filepath.Join(f.root, "helper/scripts/run.sh"), "vscode", "sync", "--manifest", manifest, "--code", code}, "安装 VS Code 扩展 1/1：example.shared@1.2.3", func() { write(t, filepath.Join(f.base, "release-install"), "") }, 10*time.Second)
		good(t, r)
		contains(t, r.out, "复查 VS Code 扩展安装版本")
		contains(t, r.out, "已同步 1 个公共扩展")
		excludes(t, r.all(), "private-install-token")
	default:
		t.Fatal("unported", name)
	}
}
