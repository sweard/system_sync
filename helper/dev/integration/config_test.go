//go:build integration

package integration

import (
	"archive/tar"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func init() {
	handlers["test_app_settings"] = appCase
	handlers["test_mise_config"] = misercCase
	handlers["test_config_preview"] = configCase
	handlers["test_config_history"] = historyCase
	handlers["test_config_detach"] = detachCase
	handlers["test_espanso"] = espansoCase
}
func jsonData(f *fixture, p string) map[string]any {
	var v map[string]any
	readJSON(f.t, p, &v)
	return v
}
func symlink(f *fixture, src, dst string) {
	mkdir(f.t, filepath.Dir(dst))
	if e := os.Symlink(src, dst); e != nil {
		f.t.Fatal(e)
	}
}
func islink(p string) bool     { i, e := os.Lstat(p); return e == nil && i.Mode()&os.ModeSymlink != 0 }
func resolved(p string) string { v, _ := filepath.EvalSymlinks(p); return v }
func (f *fixture) entry(file, key string) string {
	return good(f.t, f.mise("config", "get", "--file", filepath.Join(repo, file), "dotfiles."+quoteTOML(key))).out
}
func appCase(f *fixture, name string) {
	t := f.t
	f.copy("dotfiles/common/chatgpt", "dotfiles/common/vscode")
	user := ".config/Code/User"
	if runtime.GOOS == "darwin" {
		user = "Library/Application Support/Code/User"
	}
	settings := filepath.Join(f.home, user, "settings.json")
	config := filepath.Join(f.home, ".codex/config.toml")
	body := ""
	for _, k := range []string{"~/.codex/config.toml/shared", "~/.codex/AGENTS.md", "~/.codex/keybindings.json"} {
		body += "[dotfiles." + quoteTOML(k) + "]\n" + f.entry("mise.toml", k) + "\n"
	}
	body += "[dotfiles." + quoteTOML("~/"+user+"/settings.json/shared") + "]\n" + f.entry("mise."+nativePlatform()+".toml", "~/"+user+"/settings.json/shared")
	write(t, filepath.Join(f.root, "mise.toml"), body)
	switch name {
	case "test_merge_preserves_private_and_application_owned_keys":
		write(t, config, "# Local application state must survive\nmodel = \"old-model\"\n[features]\nhooks = false\nlocal_flag = true\n[desktop]\nselected-avatar-id = \"private-avatar\"\n[shell_environment_policy.set]\nPRIVATE_TOKEN = \"private-canary\"\n[projects.\"/private/example\"]\ntrust_level = \"trusted\"\n")
		private := map[string]any{"editor.fontSize": float64(1), "dart.flutterSdkPath": "/private/flutter", "git.path": "/private/git", "extension.private": map[string]any{"token": "private-canary", "url": "https://private.invalid"}}
		writeJSON(t, settings, private)
		auth := filepath.Join(f.home, ".codex/auth.json")
		write(t, auth, "{\"token\":\"private-auth-canary\"}\n")
		before := snapshot(t, f.home)
		good(t, f.mise("bootstrap", "dotfiles", "apply", "--dry-run"))
		equal(t, snapshot(t, f.home), before)
		good(t, f.mise("bootstrap", "dotfiles", "apply", "--yes"))
		public := filepath.Join(f.root, "dotfiles/common/chatgpt/config.toml")
		for _, k := range []string{"model", "model_reasoning_effort", "service_tier", "features.hooks", "features.js_repl", "desktop.appearanceLightChromeTheme.accent"} {
			equal(t, good(t, f.mise("config", "get", "--file", config, k)).out, good(t, f.mise("config", "get", "--file", public, k)).out)
		}
		for _, s := range []string{"# Local application state must survive", "local_flag = true", "selected-avatar-id = \"private-avatar\"", "PRIVATE_TOKEN = \"private-canary\"", "[projects.\"/private/example\"]", "trust_level = \"trusted\""} {
			contains(t, read(t, config), s)
		}
		shared := jsonData(f, filepath.Join(f.root, "dotfiles/common/vscode/settings.json"))
		for k, v := range shared {
			private[k] = v
		}
		equal(t, jsonData(f, settings), private)
		equal(t, read(t, auth), before[".codex/auth.json"].Content)
		for _, n := range []string{"AGENTS.md", "keybindings.json"} {
			p := filepath.Join(f.home, ".codex", n)
			if islink(p) {
				t.Fatal("copy mapping linked")
			}
			equal(t, read(t, p), read(t, filepath.Join(f.root, "dotfiles/common/chatgpt", n)))
		}
		write(t, config, strings.ReplaceAll(read(t, config), "private-canary", "local-new-canary"))
		s := jsonData(f, settings)
		s["extension.futurePrivateSetting"] = "local-new-canary"
		writeJSON(t, settings, s)
		before = snapshot(t, f.home)
		equal(t, good(t, f.mise("bootstrap", "dotfiles", "diff")).out, "")
		good(t, f.mise("bootstrap", "dotfiles", "apply", "--yes"))
		equal(t, snapshot(t, f.home), before)
	case "test_jsonc_is_rejected_without_overwriting_local_settings":
		write(t, config, "model = \"old-model\"\n")
		original := "{\n  // Local comment\n  \"extension.private\": \"private-canary\"\n}\n"
		write(t, settings, original)
		bad(t, f.mise("bootstrap", "dotfiles", "apply", "--yes"))
		equal(t, read(t, settings), original)
	default:
		t.Fatal("unported", name)
	}
}
func misercCase(f *fixture, name string) {
	t := f.t
	equal(t, name, "test_preview_apply_and_global_platform_discovery")
	config := filepath.Join(f.home, ".config/mise")
	f.env["MISE_CONFIG_DIR"] = config
	mkdir(t, config)
	entry := f.entry("mise.toml", "~/.config/mise/miserc.toml")
	write(t, filepath.Join(f.root, "mise.toml"), "[dotfiles.\"~/.config/mise/miserc.toml\"]\n"+entry)
	f.copy(".miserc.toml", "dotfiles/common/mise/miserc.toml")
	write(t, filepath.Join(config, "config.toml"), "# Global defaults\n")
	for _, p := range []string{"macos", "linux"} {
		write(t, filepath.Join(config, "config."+p+".toml"), "[env]\nRC_PLATFORM = "+quoteTOML(p)+"\n")
	}
	target := filepath.Join(config, "miserc.toml")
	source := filepath.Join(f.root, "dotfiles/common/mise/miserc.toml")
	good(t, f.mise("bootstrap", "dotfiles", "apply", "--dry-run"))
	if exists(target) {
		t.Fatal("preview deployed")
	}
	symlink(f, source, target)
	good(t, f.mise("bootstrap", "dotfiles", "apply", "--dry-run"))
	good(t, f.mise("bootstrap", "dotfiles", "apply", "--yes", "--force"))
	equal(t, read(t, target), read(t, source))
	good(t, f.mise("bootstrap", "dotfiles", "apply", "--dry-run"))
	outside := filepath.Join(f.base, "outside")
	mkdir(t, outside)
	old := f.root
	f.root = outside
	r := good(t, f.mise("config", "ls", "--json"))
	f.root = old
	contains(t, r.out, "config."+nativePlatform()+".toml")
	write(t, source, strings.ReplaceAll(read(t, source), "auto_env = true", "auto_env = false"))
	contains(t, good(t, f.mise("bootstrap", "dotfiles", "diff", target)).out, "+auto_env = false")
	good(t, f.mise("bootstrap", "dotfiles", "apply", "--yes"))
	equal(t, read(t, target), read(t, source))
	f.root = outside
	r = good(t, f.mise("config", "ls", "--json"))
	f.root = old
	excludes(t, r.out, "config."+nativePlatform()+".toml")
}
func configCase(f *fixture, name string) {
	t := f.t
	f.helper()
	f.copy("scripts/config-preview.sh")
	task := good(t, f.mise("config", "get", "--file", filepath.Join(repo, "mise.toml"), "tasks.config-dry-run")).out
	sync := good(t, f.mise("config", "get", "--file", filepath.Join(repo, "mise.toml"), "tasks.config-sync")).out
	header := "[settings.task]\nrun_auto_install = false\n[tasks.config-dry-run]\n" + task + "\n[tasks.config-sync]\n" + sync + "\n[dotfiles]\n"
	manifest := filepath.Join(f.root, "mise.toml")
	source, target := filepath.Join(f.root, "source"), filepath.Join(f.home, ".example")
	configure := func(mode, extra string) {
		write(t, manifest, header+"\"~/.example\" = {source = \"source\", mode = "+quoteTOML(mode)+"}\n"+extra)
	}
	write(t, source, "shared=true\n")
	write(t, target, "shared=true\n")
	configure("template", "")
	preview := func(args ...string) result {
		return f.mise(append([]string{"run", "--quiet", "config-dry-run"}, args...)...)
	}
	syncRun := func(args ...string) result { return f.mise(append([]string{"run", "config-sync"}, args...)...) }
	merge := func() {
		source, target = filepath.Join(f.root, "source.json"), filepath.Join(f.home, ".example.json")
		write(t, source, "{\"shared\": 2}\n")
		write(t, target, "{\"shared\": 1, \"private\": \"private-canary\"}\n")
		write(t, manifest, header+"\"~/.example.json/shared\" = {source = \"source.json\", merge = true}\n")
	}
	switch name {
	case "test_unchanged_template_explicitly_needs_no_sync":
		before := stampedSnapshot(t, target)
		r := good(t, preview())
		contains(t, r.out, "[+] 未发现需要应用的配置差异，无需同步。")
		excludes(t, r.all(), "render ")
		equal(t, stampedSnapshot(t, target), before)
	case "test_changed_template_is_rendered_without_deploying_or_running_hooks":
		write(t, source, "shared={{ vars.shared }}\n")
		write(t, manifest, read(t, manifest)+"\n[vars]\nshared = \"desired\"\n[bootstrap.hooks]\npre-dotfiles = \"touch pre-hook-ran\"\npost-dotfiles = \"touch post-hook-ran\"\n")
		before := stampedSnapshot(t, target)
		r := good(t, preview())
		contains(t, r.out, "-shared=true")
		contains(t, r.out, "+shared=desired")
		excludes(t, r.out, "无需同步")
		excludes(t, r.out, "(if changed)")
		equal(t, stampedSnapshot(t, target), before)
		if exists(filepath.Join(f.root, "pre-hook-ran")) || exists(filepath.Join(f.root, "post-hook-ran")) {
			t.Fatal("preview ran hooks")
		}
	case "test_permissions_and_link_structure_are_not_mistaken_for_no_change":
		for _, mode := range []string{"template", "symlink"} {
			configure(mode, "")
			os.Chmod(source, 0644)
			perm := os.FileMode(0644)
			expected := "desired symlink"
			if mode == "template" {
				perm = 0600
				expected = "permissions differ"
			}
			os.Chmod(target, perm)
			before := stampedSnapshot(t, target)
			r := good(t, preview())
			contains(t, r.out, expected)
			excludes(t, r.out, "无需同步")
			equal(t, stampedSnapshot(t, target), before)
		}
	case "test_missing_target_and_source_are_reported":
		configure("copy", "")
		os.Remove(target)
		contains(t, good(t, preview()).out, "+shared=true")
		if exists(target) {
			t.Fatal("preview created file")
		}
		os.Remove(source)
		r := good(t, preview())
		contains(t, r.out, "source missing")
		excludes(t, r.out, "无需同步")
	case "test_merge_preserves_private_keys_and_ignores_private_only_changes":
		merge()
		before := stampedSnapshot(t, target)
		r := good(t, preview())
		contains(t, r.out, `"shared": 2`)
		excludes(t, r.out, "无需同步")
		equal(t, stampedSnapshot(t, target), before)
		write(t, target, `{"shared":2,"private":"new-private-canary"}`)
		before = stampedSnapshot(t, target)
		contains(t, good(t, preview()).out, "无需同步")
		equal(t, stampedSnapshot(t, target), before)
	case "test_native_failure_is_not_reported_as_no_change":
		write(t, source, "{{ invalid template\n")
		before := stampedSnapshot(t, target)
		r := bad(t, preview())
		contains(t, r.err, "配置差异检查失败")
		excludes(t, r.all(), "无需同步")
		equal(t, stampedSnapshot(t, target), before)
	case "test_target_filter_and_help_are_forwarded":
		configure("template", "\"~/.other\" = {source = \"other\", mode = \"copy\"}\n")
		write(t, filepath.Join(f.root, "other"), "other changed\n")
		contains(t, good(t, preview(target)).out, "无需同步")
		contains(t, good(t, preview()).out, "~/.other")
		if exists(filepath.Join(f.home, ".other")) {
			t.Fatal("preview wrote filtered file")
		}
		r := good(t, f.run(filepath.Join(f.root, "scripts/config-preview.sh"), "--help"))
		contains(t, r.out, "Usage:")
		excludes(t, r.out, "检测到以下受管配置差异")
	case "test_sync_preserves_private_keys_and_backs_up_before_applying":
		merge()
		before := read(t, target)
		r := good(t, syncRun("--yes"))
		assertOrder(f, r.out, []string{"读取受管配置状态与差异", "复核并备份受管配置", "复核配置应用前的状态", "应用 1 个受管配置目标", "复查配置应用结果", "配置已部署并复查"})
		equal(t, jsonData(f, target), map[string]any{"shared": float64(2), "private": "private-canary"})
		dirs, _ := filepath.Glob(filepath.Join(f.root, ".system-sync/history/*-config-sync-*"))
		equal(t, len(dirs), 1)
		equal(t, tarMember(f, filepath.Join(dirs[0], "config.before.tar"), ".example.json"), before)
		equal(t, jsonData(f, filepath.Join(dirs[0], "transaction.json"))["phase"], "complete")
		contains(t, good(t, syncRun("--yes")).out, "无需同步")
		dirs, _ = filepath.Glob(filepath.Join(f.root, ".system-sync/history/*"))
		equal(t, len(dirs), 1)
	case "test_sync_preview_cancellation_and_shared_lock_do_not_modify_files":
		write(t, source, "changed=true\n")
		before := stampedSnapshot(t, target)
		good(t, syncRun("--dry-run"))
		equal(t, stampedSnapshot(t, target), before)
		for _, a := range []string{"y", "no"} {
			r := f.terminal([]string{filepath.Join(f.root, "helper/scripts/run.sh"), "config", "sync"}, [][2]string{{"输入 yes 确认", a}})
			equal(t, r.code, 2)
			equal(t, stampedSnapshot(t, target), before)
		}
		if exists(filepath.Join(f.root, ".system-sync/history")) {
			t.Fatal("cancel wrote history")
		}
		release := lock(t, f.root)
		bad(t, syncRun("--yes"))
		release()
		equal(t, stampedSnapshot(t, target), before)
	case "test_sync_only_applies_selected_owned_targets":
		configure("copy", "\"~/.other\" = {source = \"other\", mode = \"copy\"}\n")
		write(t, source, "changed=true\n")
		write(t, filepath.Join(f.root, "other"), "other=true\n")
		good(t, syncRun(target, "--yes"))
		equal(t, read(t, target), "changed=true\n")
		if exists(filepath.Join(f.home, ".other")) {
			t.Fatal("unselected file applied")
		}
	case "test_force_takeover_retains_the_original_file_in_backup":
		configure("symlink", "")
		before := read(t, target)
		good(t, syncRun("--force", "--yes"))
		if !islink(target) || resolved(target) != source {
			t.Fatal("force did not link")
		}
		dirs, _ := filepath.Glob(filepath.Join(f.root, ".system-sync/history/*-config-sync-*"))
		equal(t, len(dirs), 1)
		equal(t, tarMember(f, filepath.Join(dirs[0], "config.before.tar"), ".example"), before)
	case "test_symlink_each_keeps_unmanaged_files_and_backs_up_the_directory":
		os.Remove(source)
		os.Remove(target)
		write(t, filepath.Join(source, "shared"), "managed\n")
		write(t, filepath.Join(target, "private"), "local-only\n")
		configure("symlink-each", "")
		good(t, syncRun(target, "--yes"))
		if !islink(filepath.Join(target, "shared")) {
			t.Fatal("managed file not linked")
		}
		equal(t, read(t, filepath.Join(target, "private")), "local-only\n")
		dirs, _ := filepath.Glob(filepath.Join(f.root, ".system-sync/history/*-config-sync-*"))
		equal(t, len(dirs), 1)
		equal(t, tarMember(f, filepath.Join(dirs[0], "config.before.tar"), ".example/private"), "local-only\n")
	default:
		t.Fatal("unported", name)
	}
}
func assertOrder(f *fixture, text string, items []string) {
	prev := -1
	for _, s := range items {
		i := strings.Index(text, s)
		if i < 0 || i <= prev {
			f.t.Fatal("stage missing/out of order", s, text)
		}
		prev = i
	}
}
func tarMember(f *fixture, p, name string) string {
	fd, e := os.Open(p)
	if e != nil {
		f.t.Fatal(e)
	}
	defer fd.Close()
	r := tar.NewReader(fd)
	for {
		h, e := r.Next()
		if e != nil {
			f.t.Fatal("missing archive member", name, e)
		}
		if h.Name == name {
			b, e := io.ReadAll(r)
			if e != nil {
				f.t.Fatal(e)
			}
			return string(b)
		}
	}
}
func historyCase(f *fixture, name string) {
	t := f.t
	f.helper()
	target := filepath.Join(f.home, ".codex/config.toml")
	write(t, target, "shared = \"original\"\nprivate = \"private-canary\"\n")
	write(t, filepath.Join(f.root, "source.toml"), "shared = \"desired\"\n")
	write(t, filepath.Join(f.root, "mise.toml"), "[dotfiles]\n\"~/.codex/config.toml/shared\" = {source=\"source.toml\",merge=true}\n")
	config := filepath.Join(f.home, ".config/mise")
	f.env["MISE_CONFIG_DIR"] = config
	enrollment := filepath.Join(config, "conf.d/system-sync-history.toml")
	helper := func(action string) result { return f.cli("config-history", action) }
	checkpoints := func(local bool) []map[string]any {
		args := []string{"dot"}
		if local {
			args = append(args, "--local")
		}
		args = append(args, "history", "ls", "--json")
		r := good(t, f.mise(args...))
		var v []map[string]any
		if e := json.Unmarshal([]byte(r.out), &v); e != nil {
			t.Fatal(e)
		}
		return v
	}
	switch name {
	case "test_manual_save_and_rollback_keep_private_data_only_in_local_history":
		good(t, helper("init"))
		for _, v := range tomlData(f, enrollment)["dotfiles"].(map[string]any) {
			equal(t, v, map[string]any{"mode": "track-local", "autosave": false})
		}
		info, _ := os.Stat(enrollment)
		equal(t, info.Mode().Perm(), os.FileMode(0600))
		info, _ = os.Stat(filepath.Join(f.env["MISE_STATE_DIR"], "history-local"))
		equal(t, info.Mode().Perm(), os.FileMode(0700))
		good(t, helper("save"))
		before := checkpoints(true)[0]["id"]
		write(t, target, "shared = \"changed\"\nprivate = \"second-canary\"\n")
		good(t, f.mise("dot", "save"))
		equal(t, checkpoints(true)[0]["id"], before)
		id := fmtID(before)
		good(t, f.mise("dot", "--local", "rollback", target, "--to", id, "--dry-run"))
		contains(t, read(t, target), "second-canary")
		good(t, f.mise("dot", "--local", "rollback", target, "--to", id, "--yes"))
		contains(t, read(t, target), "private-canary")
		equal(t, len(checkpoints(false)), 0)
		good(t, f.mise("bootstrap", "dotfiles", "apply"))
		equal(t, tomlData(f, target), map[string]any{"shared": "desired", "private": "private-canary"})
		excludes(t, read(t, filepath.Join(f.root, "source.toml")), "canary")
	case "test_existing_custom_enrollment_and_shared_mode_are_not_overwritten":
		good(t, helper("init"))
		write(t, enrollment, "[dotfiles]\n\"/unexpected\" = {mode=\"track\"}\n")
		before := read(t, enrollment)
		bad(t, helper("init"))
		bad(t, helper("save"))
		equal(t, read(t, enrollment), before)
		if exists(filepath.Join(f.env["MISE_STATE_DIR"], "history")) {
			t.Fatal("shared history created")
		}
	case "test_registration_and_save_list_paths_and_report_missing_registered_files":
		local := filepath.Join(f.home, ".zshrc.local")
		write(t, local, "# shell-private-canary\n")
		r := good(t, helper("init"))
		contains(t, r.out, "  - "+target+"\n")
		contains(t, r.out, "  - "+local+"\n")
		excludes(t, r.out, "canary")
		os.Remove(local)
		r = good(t, helper("save"))
		contains(t, r.out, "[!] 跳过 1 个不可用的已登记配置：\n  - "+local)
		contains(t, r.out, "[+] 已手动保存 1 个配置到本机历史。\n  - "+target)
		equal(t, len(checkpoints(true)), 1)
		excludes(t, r.out, "canary")
		os.Remove(target)
		r = bad(t, helper("save"))
		contains(t, r.out, "[!] 跳过 2 个不可用的已登记配置")
		contains(t, r.out, target)
		contains(t, r.out, local)
		excludes(t, r.out, "已手动保存")
		equal(t, len(checkpoints(true)), 1)
	default:
		t.Fatal("unported", name)
	}
}
func fmtID(v any) string { b, _ := json.Marshal(v); return strings.Trim(string(b), "\"") }
func detachCase(f *fixture, name string) {
	t := f.t
	f.copy("mise.toml", "mise.macos.toml", "mise.linux.toml", ".miserc.toml", "dotfiles")
	f.helper()
	f.env["MISE_CONFIG_DIR"] = filepath.Join(f.home, ".config/mise")
	f.env["MISE_STATE_DIR"] = filepath.Join(f.home, ".local/state/mise")
	config := filepath.Join(f.home, ".config/mise/config.toml")
	codex := filepath.Join(f.home, ".codex/config.toml")
	user := ".config/Code/User/settings.json"
	espanso := filepath.Join(f.home, ".config/espanso")
	if runtime.GOOS == "darwin" {
		user = "Library/Application Support/Code/User/settings.json"
		espanso = filepath.Join(f.home, "Library/Application Support/espanso")
	}
	code := filepath.Join(f.home, user)
	for path, value := range map[string]string{codex: "private_fixture = \"private-canary\"\n", code: "{\"private.fixture\":\"private-canary\"}\n", filepath.Join(f.home, ".zshrc.local"): "# local-canary\n"} {
		write(t, path, value)
		os.Chmod(path, 0600)
	}
	private := filepath.Join(f.root, "private-fixture.yml")
	write(t, private, "private-canary: local-only\n")
	os.Chmod(private, 0600)
	write(t, filepath.Join(f.root, "mise.local.toml"), "[dotfiles]\n"+quoteTOML(filepath.Join(espanso, "match/private.yml"))+" = {source=\"private-fixture.yml\",mode=\"symlink\"}\n")
	good(t, f.mise("bootstrap", "dotfiles", "apply"))
	good(t, f.cli("config-history", "init"))
	enrollment := filepath.Join(f.home, ".config/mise/conf.d/system-sync-history.toml")
	backups := filepath.Join(f.home, ".local/state/system-sync/config-detach")
	original := read(t, filepath.Join(f.root, "dotfiles/common/mise/config.toml"))
	good(t, f.mise("bootstrap", "dotfiles", "status", "--json"))
	detach := func(args ...string) result {
		r := f.cli(append([]string{"config-detach"}, args...)...)
		excludes(t, r.all(), "private-canary")
		return r
	}
	switch name {
	case "test_preview_and_noninteractive_refusal_leave_configuration_untouched":
		before := snapshot(t, f.home)
		r := good(t, detach("--dry-run"))
		contains(t, r.out, "以上仅为预览")
		contains(t, r.out, "复制锁文件依赖目录")
		contains(t, r.out, "将 PATH 脚本引用改为本机位置")
		equal(t, snapshot(t, f.home), before)
		bad(t, detach())
		equal(t, snapshot(t, f.home), before)
		if exists(backups) || exists(filepath.Join(f.root, ".system-sync/operation.lock")) {
			t.Fatal("preview changed state")
		}
	case "test_interactive_detach_requires_yes_and_cancellation_leaves_no_backup":
		before := snapshot(t, f.home)
		args := []string{filepath.Join(f.root, "helper/scripts/run.sh"), "config-detach"}
		for _, a := range []string{"", "no", "y", "DETACH"} {
			r := f.terminal(args, [][2]string{{"输入 yes 确认", a}})
			equal(t, r.code, 2)
			equal(t, snapshot(t, f.home), before)
			if exists(backups) {
				t.Fatal("cancel created backups")
			}
		}
		r := good(t, f.terminal(args, [][2]string{{"输入 yes 确认", "yes"}}))
		contains(t, r.out, "配置已保留在本机位置")
		if islink(config) || exists(enrollment) || !exists(backups) {
			t.Fatal("detach missing")
		}
		excludes(t, r.all(), "private-canary")
	case "test_detach_preserves_current_values_private_files_and_works_without_repository":
		unmanaged := filepath.Join(espanso, "match/unmanaged.yml")
		write(t, unmanaged, "keep: user-owned\n")
		privateBefore := []string{read(t, codex), read(t, code), read(t, filepath.Join(f.home, ".zshrc.local"))}
		history := filepath.Join(f.home, ".local/state/mise/history-local/keep")
		write(t, history, "existing-checkpoint\n")
		agents := filepath.Join(f.home, ".codex/AGENTS.md")
		write(t, agents, read(t, agents)+"\nlocal collaboration rule\n")
		contains(t, good(t, detach("--yes")).out, "配置已保留在本机位置")
		if islink(filepath.Join(f.home, ".zshrc")) || islink(config) || exists(enrollment) {
			t.Fatal("links not materialized")
		}
		equal(t, []string{read(t, codex), read(t, code), read(t, filepath.Join(f.home, ".zshrc.local"))}, privateBefore)
		equal(t, read(t, unmanaged), "keep: user-owned\n")
		contains(t, read(t, agents), "local collaboration rule")
		target := filepath.Join(espanso, "match/private.yml")
		if islink(target) {
			t.Fatal("private link not detached")
		}
		info, _ := os.Stat(target)
		equal(t, info.Mode().Perm(), os.FileMode(0600))
		equal(t, read(t, target), read(t, private))
		equal(t, read(t, filepath.Join(f.root, "dotfiles/common/mise/config.toml")), original)
		equal(t, read(t, history), "existing-checkpoint\n")
		dirs, _ := filepath.Glob(filepath.Join(backups, "*"))
		equal(t, len(dirs), 1)
		equal(t, jsonData(f, filepath.Join(dirs[0], "transaction.json"))["phase"], "complete")
		for _, item := range snapshot(t, dirs[0]) {
			if item.Mode.IsRegular() {
				equal(t, item.Mode.Perm(), os.FileMode(0600))
			}
		}
		before := snapshot(t, backups)
		contains(t, good(t, detach("--yes")).out, "无需迁出")
		equal(t, snapshot(t, backups), before)
		old := f.root
		if e := os.Rename(old, filepath.Join(f.base, "archived project")); e != nil {
			t.Fatal(e)
		}
		f.root = f.home
		data := tomlData(f, config)
		script := data["env"].(map[string]any)["_"].(map[string]any)["source"].(map[string]any)["path"].(string)
		equal(t, script, filepath.Join(f.home, ".config/zsh/path.sh"))
		if !exists(script) {
			t.Fatal("detached PATH script missing")
		}
		r := good(t, f.mise("lock", "--global", "--sidecars", "--json"))
		excludes(t, r.out, old)
		var sidecars []map[string]any
		if e := json.Unmarshal([]byte(r.out), &sidecars); e != nil {
			t.Fatal(e)
		}
		graphs := 0
		for _, s := range sidecars {
			for _, g := range s["sidecars"].([]any) {
				graphs++
				equal(t, g.(map[string]any)["exists"], true)
			}
		}
		if graphs == 0 {
			t.Fatal("missing lock sidecars")
		}
		good(t, f.run("bash", "-c", `. "$HOME/.config/zsh/path.sh"`))
		if exists(filepath.Join(f.env["MISE_DATA_DIR"], "installs")) {
			t.Fatal("detaching installed tools")
		}
	case "test_conflicting_sidecar_aborts_before_replacing_any_target":
		write(t, filepath.Join(f.home, ".config/mise/.mise/locks/npm-yarn/1.22.22/unrelated"), "keep me")
		before := snapshot(t, f.home)
		bad(t, detach("--yes"))
		equal(t, snapshot(t, f.home), before)
		if exists(backups) {
			t.Fatal("conflict wrote backup")
		}
	case "test_custom_history_enrollment_and_broken_links_are_not_overwritten":
		write(t, enrollment, "[dotfiles]\n\"/unexpected\"={mode=\"track\"}\n")
		before := snapshot(t, f.home)
		bad(t, detach("--yes"))
		equal(t, snapshot(t, f.home), before)
		os.Remove(enrollment)
		os.Remove(private)
		before = snapshot(t, f.home)
		bad(t, detach("--yes"))
		equal(t, snapshot(t, f.home), before)
		if exists(backups) {
			t.Fatal("invalid target wrote backup")
		}
	case "test_shared_operation_lock_prevents_detach_during_package_changes":
		before := snapshot(t, f.home)
		release := lock(t, f.root)
		bad(t, detach("--yes"))
		release()
		equal(t, snapshot(t, f.home), before)
		if exists(backups) {
			t.Fatal("locked detach wrote backup")
		}
	case "test_user_replaced_external_config_links_are_preserved":
		external := filepath.Join(f.home, "own-config/config.toml")
		write(t, external, original)
		os.Remove(config)
		symlink(f, external, config)
		good(t, detach("--yes"))
		if !islink(config) || resolved(config) != external {
			t.Fatal("external config replaced")
		}
		equal(t, read(t, external), original)
	default:
		t.Fatal("unported", name)
	}
}
func espansoCase(f *fixture, name string) {
	t := f.t
	equal(t, name, "test_metadata_is_ignored_at_any_depth_and_local_content_is_preserved")
	platforms := []string{"macos", "linux"}
	if p := os.Getenv("SYSTEM_SYNC_TEST_PROFILE"); p != "all" && p != "" {
		platforms = []string{nativePlatform()}
	}
	for _, platform := range platforms {
		g := newFixture(t)
		g.helper()
		source := filepath.Join(g.root, "dotfiles/common/espanso")
		relative := ".config/espanso"
		if platform == "macos" {
			relative = "Library/Application Support/espanso"
		}
		target := filepath.Join(g.home, relative)
		public := filepath.Join(source, "config/default.yml")
		write(t, public, "show_icon: true\n")
		write(t, filepath.Join(source, "match/shared.yml"), "matches: []\n")
		write(t, filepath.Join(source, "mise.local.example.toml"), "# local example\n")
		private := filepath.Join(source, "private.local/match/private-snippets.yml")
		write(t, private, "matches: []\n# synthetic-private-canary\n")
		os.Chmod(private, 0600)
		privateTarget := filepath.Join(target, "match/private-snippets.yml")
		symlink(g, private, privateTarget)
		unmanaged := filepath.Join(target, "match/packages/local.yml")
		write(t, unmanaged, "# local-only-canary\n")
		for _, rel := range []string{".DS_Store", "match/.DS_Store"} {
			write(t, filepath.Join(source, rel), "source-finder-metadata")
			write(t, filepath.Join(target, rel), "local-finder-metadata")
		}
		entry := tomlData(g, filepath.Join(repo, "mise."+platform+".toml"))["dotfiles"].(map[string]any)["~/"+relative].(map[string]any)
		ex, _ := json.Marshal(entry["exclude"])
		write(t, filepath.Join(g.root, "mise.toml"), "[dotfiles]\n"+quoteTOML("~/"+relative)+" = {source="+quoteTOML(entry["source"].(string))+", mode="+quoteTOML(entry["mode"].(string))+", exclude="+string(ex)+"}\n"+quoteTOML(privateTarget)+" = {source="+quoteTOML(private)+",mode=\"symlink\"}\n")
		before := snapshot(t, target)
		metadataTime := map[string]int64{}
		for _, rel := range []string{".DS_Store", "match/.DS_Store"} {
			info, e := os.Stat(filepath.Join(target, rel))
			if e != nil {
				t.Fatal(e)
			}
			metadataTime[rel] = info.ModTime().UnixNano()
		}
		good(t, g.cli("config", "sync", "--yes"))
		if !islink(filepath.Join(target, "config/default.yml")) || resolved(filepath.Join(target, "config/default.yml")) != public {
			t.Fatal("public mapping missing")
		}
		if !islink(privateTarget) || resolved(privateTarget) != private {
			t.Fatal("private mapping changed")
		}
		equal(t, read(t, private), "matches: []\n# synthetic-private-canary\n")
		info, _ := os.Stat(private)
		equal(t, info.Mode().Perm(), os.FileMode(0600))
		equal(t, read(t, unmanaged), "# local-only-canary\n")
		if exists(filepath.Join(target, "private.local")) || exists(filepath.Join(target, "mise.local.example.toml")) {
			t.Fatal("private source deployed")
		}
		after := snapshot(t, target)
		for _, rel := range []string{".DS_Store", "match/.DS_Store"} {
			equal(t, after[rel], before[rel])
			info, e := os.Stat(filepath.Join(target, rel))
			if e != nil {
				t.Fatal(e)
			}
			equal(t, info.ModTime().UnixNano(), metadataTime[rel])
		}
		contains(t, good(t, g.cli("config", "diff")).out, "无需同步")
		write(t, filepath.Join(source, "match/.DS_Store"), "updated-source-metadata")
		write(t, filepath.Join(target, ".DS_Store"), "updated-local-metadata")
		contains(t, good(t, g.cli("config", "diff")).out, "无需同步")
		deployed := filepath.Join(target, "config/default.yml")
		os.Remove(deployed)
		write(t, deployed, "show_icon: false\n")
		excludes(t, good(t, g.cli("config", "diff")).out, "无需同步")
		equal(t, read(t, privateTarget), read(t, private))
	}
}
