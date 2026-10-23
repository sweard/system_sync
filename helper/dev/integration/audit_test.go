//go:build integration

package integration

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var packageRequired = [][]string{{"Brewfile formula 根声明", "Brewfile cask 声明", "Brewfile tap 声明", "尚未安装的 formula", "尚未安装的 cask", "尚未添加的 tap", "Homebrew 可识别的未声明 requested formula", "未声明的已安装 cask", "未声明的 tap", "受保护但未在 Brewfile 声明的项目"}, {"mise 最终生效声明", "声明但尚未以同名具体包安装", "当前显式安装包中未声明、但受保护而保留", "apply 时会从 dependency 提升为 explicit 的声明/保护包", "apply 时会从 explicit 降级为 dependency 的未声明包"}, {"mise 最终生效声明", "声明但尚未以同名具体包安装", "当前手工根包中未声明、但受保护而保留", "apply 时会提升为手工根包的声明/保护包", "apply 时会降级为依赖的未声明手工根包"}, {"声明的根包", "声明但尚未安装", "未声明但受保护的 user-installed 包", "拟直接移除的未声明 user-installed 包"}}

func packageStatus(required []string, entries map[string][]string) string {
	titles := append([]string{}, required...)
	sort.Strings(titles)
	a := []string{}
	for _, s := range titles {
		v := entries[s]
		row := fmt.Sprintf("%s（%d）\n", s, len(v))
		if len(v) == 0 {
			row += "  （无）\n"
		} else {
			for _, n := range v {
				row += "  - " + n + "\n"
			}
		}
		a = append(a, row)
	}
	return strings.Join(a, "\n")
}

type auditFixture struct {
	*fixture
	tools            any
	paths            map[string]any
	config           any
	packages, policy string
	failures         [][]string
	system           map[string]string
	log              string
}

func newAudit(f *fixture) *auditFixture {
	f.root = filepath.Join(f.base, "project")
	mkdir(f.t, f.root)
	f.helper()
	a := &auditFixture{fixture: f, tools: map[string]any{}, paths: map[string]any{}, config: map[string]any{"files": []any{}, "edits": []any{}}, packages: packageStatus(packageRequired[0], nil), policy: "/usr/bin:/bin:/usr/sbin:/sbin", failures: [][]string{}, system: map[string]string{}, log: filepath.Join(f.base, "queries.jsonl")}
	for _, s := range []string{"/usr/bin", "/bin", "/usr/sbin", "/sbin"} {
		p := filepath.Join(f.base, "system", strings.ReplaceAll(strings.Trim(s, "/"), "/", "-"))
		mkdir(f.t, p)
		a.system[s] = p
	}
	f.env = map[string]string{"HOME": f.home, "PATH": "/usr/bin:/bin", "PRIVATE_API_TOKEN": "fixture-private-token"}
	return a
}
func (a *auditFixture) tool(name, command string, installed bool) string {
	dir := filepath.Join(a.home, ".local/share/mise/installs", name, "1.0.0")
	p := filepath.Join(dir, "bin", command)
	executable(a.t, p, "#!/bin/sh\nexit 99\n")
	a.tools.(map[string]any)[name] = []any{map[string]any{"version": "1.0.0", "installed": installed, "install_path": dir, "source": map[string]any{"type": "mise.toml", "path": filepath.Join(a.root, "mise.toml")}}}
	a.paths[command] = p
	return p
}
func (a *auditFixture) invoke(sections []string, strict, text bool) result {
	f := a.fixture
	t := f.t
	isolated := func(p string) string {
		parts := strings.Split(p, ":")
		for i, s := range parts {
			if v, ok := a.system[s]; ok {
				parts[i] = v
			}
		}
		return strings.Join(parts, ":")
	}
	f.bin = filepath.Join(f.root, "query-bin")
	mkdir(t, f.bin)
	state := filepath.Join(f.bin, "state.json")
	writeJSON(t, state, map[string]any{"tools": a.tools, "paths": a.paths, "config": a.config, "packages": a.packages, "policy": isolated(a.policy), "log": a.log, "status_script": filepath.Join(f.root, "scripts/system-sync.sh"), "failures": a.failures})
	f.env["AUDIT_TEST_STATE"] = state
	f.mock("audit", "mise", "packages", "policy")
	copyTree(t, filepath.Join(f.bin, "packages"), filepath.Join(f.root, "scripts/system-sync.sh"))
	policy := filepath.Join(f.bin, "policy")
	write(t, filepath.Join(f.root, "dotfiles/common/shell/path.sh"), "_system_sync_user_path() { PATH=\"$(AUDIT_TEST_STATE="+quote(state)+" "+quote(policy)+")\"; export PATH; }\n")
	old := f.env["PATH"]
	f.env["PATH"] = f.bin + ":" + isolated(old)
	caller, hasCaller := f.env["SYSTEM_SYNC_AUDIT_CALLER_PATH"]
	if hasCaller {
		f.env["SYSTEM_SYNC_AUDIT_CALLER_PATH"] = isolated(caller)
	}
	current := strings.Fields(read(t, filepath.Join(f.root, ".system-sync/helper/current")))
	binary := filepath.Join(f.root, ".system-sync/helper", current[0], current[1], "system-sync-helper")
	args := []string{binary, "--root", f.root, "audit"}
	if text {
		args = append(args, "--color", "never")
	} else {
		args = append(args, "--json")
	}
	for _, s := range sections {
		args = append(args, "--only", s)
	}
	if strict {
		args = append(args, "--strict")
	}
	r := f.run(args...)
	f.env["PATH"] = old
	if hasCaller {
		f.env["SYSTEM_SYNC_AUDIT_CALLER_PATH"] = caller
	}
	if r.code < 0 || r.code > 2 {
		t.Fatal(r.all())
	}
	return r
}
func (a *auditFixture) report(sections ...string) map[string]any {
	r := a.invoke(sections, false, false)
	var v map[string]any
	if e := json.Unmarshal([]byte(r.out), &v); e != nil {
		a.t.Fatal(e, r.all())
	}
	return v
}
func codes(report map[string]any, section string) []string {
	out := []string{}
	for _, v := range report["sections"].(map[string]any)[section].([]any) {
		out = append(out, v.(map[string]any)["code"].(string))
	}
	return out
}
func summary(report map[string]any, key string) float64 {
	return report["summary"].(map[string]any)[key].(float64)
}
func init() { handlers["test_audit"] = auditCase }
func auditCase(f *fixture, name string) {
	t := f.t
	if name == "test_native_task_does_not_install_and_preserves_caller_path" {
		auditTask(f)
		return
	}
	a := newAudit(f)
	switch name {
	case "test_missing_tools_are_reported_without_installing":
		a.tool("node", "node", false)
		r := a.report("tools")
		equal(t, summary(r, "drift"), float64(1))
		if !has(codes(r, "tools"), "tool_missing") {
			t.Fatal(r)
		}
		rows := read(t, a.log)
		equal(t, strings.Count(rows, "\n"), 1)
		contains(t, rows, `"command":["mise","ls","--current","--json"]`)
	case "test_shadowed_commands_and_other_installers_are_candidates":
		selected := a.tool("node", "node", true)
		shadow := filepath.Join(f.home, ".local/bin/node")
		executable(t, shadow, "#!/bin/sh\nexit 99\n")
		executable(t, filepath.Join(f.home, ".nvm/versions/node/v0.1.0/bin/node"), "#!/bin/sh\nexit 99\n")
		f.env["PATH"] = filepath.Dir(shadow) + ":" + filepath.Dir(selected) + ":/usr/bin:/bin"
		r := a.report("tools", "path")
		if !has(codes(r, "path"), "command_shadowed") || !has(codes(r, "tools"), "external_installations") || summary(r, "candidate") <= 0 {
			t.Fatal(r)
		}
	case "test_shims_and_rustup_backend_are_not_duplicate_management":
		setupRust(a)
		r := a.report("path")
		if !has(codes(r, "path"), "command_selected") || has(codes(r, "path"), "command_shadowed") || has(codes(r, "path"), "alternative_commands") {
			t.Fatal(r)
		}
	case "test_linked_external_installation_is_retained":
		original := filepath.Join(f.home, ".nvm/versions/node/v1.0.0/bin/node")
		executable(t, original, "#!/bin/sh\nexit 99\n")
		dir := filepath.Join(f.home, ".local/share/mise/installs/node/1.0.0")
		symlink(f, filepath.Dir(filepath.Dir(original)), dir)
		a.tools = map[string]any{"node": []any{map[string]any{"version": "1.0.0", "installed": true, "install_path": dir}}}
		r := a.report("tools")
		if !has(codes(r, "tools"), "linked_install") || has(codes(r, "tools"), "external_installations") {
			t.Fatal(r)
		}
	case "test_virtual_environment_is_an_intentional_python_override":
		setupVirtual(a)
		r := a.report("path")
		if !has(codes(r, "path"), "virtual_environment") || has(codes(r, "path"), "command_shadowed") {
			t.Fatal(r)
		}
	case "test_path_checks_caller_environment_and_filters_private_variables":
		selected := a.tool("node", "node", true)
		f.env["PATH"] = filepath.Dir(selected) + ":/usr/bin:/bin"
		f.env["SYSTEM_SYNC_AUDIT_CALLER_PATH"] = f.home + "::relative:" + filepath.Join(f.home, "absent") + ":" + f.home
		r := a.report("path")
		equal(t, r["context"].(map[string]any)["path_source"], "caller")
		for _, s := range []string{"unsafe_path", "missing_directory", "duplicate_path", "command_missing"} {
			if !has(codes(r, "path"), s) {
				t.Fatal("missing code", s, r)
			}
		}
		for _, row := range readRows(f, a.log) {
			if has(list(row["command"]), "--noprofile") {
				if _, ok := row["environment"].(map[string]any)["PRIVATE_API_TOKEN"]; ok {
					t.Fatal("private env forwarded")
				}
			}
		}
	case "test_expected_paths_and_preserved_flutter_are_reported":
		p := filepath.Join(f.home, "development/flutter/bin/flutter")
		executable(t, p, "#!/bin/sh\nexit 99\n")
		a.policy = filepath.Dir(p) + ":/usr/bin:/bin"
		r := a.report("path")
		for _, s := range []string{"policy_path_missing", "retained_flutter"} {
			if !has(codes(r, "path"), s) {
				t.Fatal(r)
			}
		}
	case "test_host_injected_paths_are_kept_separate_from_unmanaged_paths":
		f.env["PATH"] = strings.Join([]string{filepath.Join(f.home, ".codex/tmp/ephemeral/bin"), "/pkg/env/global/bin", filepath.Join(f.home, "retired-tool/bin")}, ":")
		r := a.report("path")
		kinds := []string{}
		for _, v := range r["sections"].(map[string]any)["path"].([]any) {
			item := v.(map[string]any)
			if item["code"] == "missing_directory" {
				kinds = append(kinds, item["kind"].(string))
			}
		}
		equal(t, kinds, []string{"local", "local", "drift"})
	case "test_configs_are_compared_only_on_owned_states_and_whitelisted_paths":
		for p, v := range map[string]string{".config/mise/secrets.env": "private-content-that-must-never-be-read", "private-application/token.json": "other-private-content", ".gitconfig": "git-private-value"} {
			write(t, filepath.Join(f.home, p), v)
		}
		a.config = map[string]any{"files": []any{map[string]any{"target": "~/.zshrc", "state": "missing"}}, "edits": []any{map[string]any{"path": "~/.codex/config.toml", "state": "applied", "edit": map[string]any{"value": "hidden-merge-value"}}}}
		r := a.report("config")
		equal(t, summary(r, "drift"), float64(1))
		for _, s := range []string{"unmanaged_config", "private_local"} {
			if !has(codes(r, "config"), s) {
				t.Fatal(r)
			}
		}
		b, _ := json.Marshal(r)
		output := string(b) + a.invoke([]string{"config"}, false, true).out
		for _, s := range []string{"private-content-that-must-never-be-read", "other-private-content", "git-private-value", "hidden-merge-value", "private-application"} {
			excludes(t, output, s)
		}
		equal(t, read(t, filepath.Join(f.home, ".config/mise/secrets.env")), "private-content-that-must-never-be-read")
	case "test_all_dotfile_drift_states_and_invalid_state_are_handled":
		for _, s := range []any{"missing", "differs", "source_missing", "would_remove", "future_state", []any{}} {
			a.config = map[string]any{"files": []any{map[string]any{"target": "~/.zshrc", "state": s}}, "edits": []any{}}
			r := a.report("config")
			equal(t, summary(r, "drift")+summary(r, "error"), float64(1))
		}
	case "test_package_status_all_platform_contracts_are_supported":
		for _, required := range packageRequired {
			a.packages = packageStatus(required, nil)
			r := a.report("packages")
			equal(t, summary(r, "error"), float64(0))
			equal(t, summary(r, "drift"), float64(0))
			rows := readRows(f, a.log)
			equal(t, list(rows[len(rows)-1]["command"]), []string{filepath.Join(f.root, "scripts/system-sync.sh"), "--status"})
		}
	case "test_packages_distinguish_missing_candidates_and_protected_roots":
		a.packages = packageStatus(packageRequired[1], map[string][]string{"声明但尚未以同名具体包安装": {"missing-tool"}, "当前显式安装包中未声明、但受保护而保留": {"kernel"}, "apply 时会从 explicit 降级为 dependency 的未声明包": {"old-tool"}})
		r := a.report("packages")
		for _, s := range []string{"drift", "candidate", "local"} {
			equal(t, summary(r, s), float64(1))
		}
	case "test_package_version_drift_and_broken_reports_do_not_look_clean":
		a.packages = packageStatus(packageRequired[1], map[string][]string{"mise 最终生效声明": {"pacman:git [version_mismatch]"}})
		if !has(codes(a.report("packages"), "packages"), "package_version") {
			t.Fatal("version drift hidden")
		}
		for _, s := range []string{"", "future format", "声明的根包（1）\n  （无）\n"} {
			a.packages = s
			equal(t, summary(a.report("packages"), "error"), float64(1))
		}
	case "test_collector_failure_does_not_hide_other_sections":
		a.failures = [][]string{{"mise", "ls", "--current", "--json"}}
		r := a.report("tools", "packages", "config")
		equal(t, summary(r, "error"), float64(1))
		if len(r["sections"].(map[string]any)["packages"].([]any)) == 0 {
			t.Fatal("other sections hidden")
		}
		equal(t, a.invoke([]string{"tools", "packages", "config"}, false, false).code, 2)
	case "test_unknown_tool_and_config_schemas_are_incomplete":
		a.tools = []any{}
		a.config = map[string]any{"future": []any{}}
		r := a.report("tools", "config")
		equal(t, summary(r, "error"), float64(2))
		equal(t, a.invoke([]string{"tools", "config"}, false, false).code, 2)
	case "test_redaction_and_exit_codes_preserve_machine_readable_schema":
		f.env["EXTRA_SECRET"] = "ok"
		equal(t, a.invoke([]string{"tools"}, false, false).code, 0)
		a.tool("fixture-private-token", "fixture-command", false)
		r := a.invoke([]string{"tools"}, false, false)
		equal(t, r.code, 0)
		excludes(t, r.out, "fixture-private-token")
		var v map[string]any
		json.Unmarshal([]byte(r.out), &v)
		equal(t, v["sections"].(map[string]any)["tools"].([]any)[0].(map[string]any)["kind"], "drift")
		equal(t, a.invoke([]string{"tools"}, true, false).code, 1)
	case "test_failed_tool_snapshot_is_shared_but_retried_on_the_next_audit":
		a.failures = [][]string{{"mise", "ls", "--current", "--json"}}
		r := a.report("tools", "packages", "path", "config")
		equal(t, summary(r, "error"), float64(2))
		count := func() int {
			n := 0
			for _, r := range readRows(f, a.log) {
				if strings.Join(list(r["command"]), " ") == "mise ls --current --json" {
					n++
				}
			}
			return n
		}
		equal(t, count(), 1)
		a.failures = [][]string{}
		equal(t, summary(a.report("tools", "packages", "path", "config"), "error"), float64(0))
		equal(t, count(), 2)
	default:
		t.Fatal("unported", name)
	}
}
func readRows(f *fixture, p string) []map[string]any {
	out := []map[string]any{}
	if !exists(p) {
		return out
	}
	for _, s := range strings.Split(strings.TrimSpace(read(f.t, p)), "\n") {
		var v map[string]any
		if e := json.Unmarshal([]byte(s), &v); e != nil {
			f.t.Fatal(e)
		}
		out = append(out, v)
	}
	return out
}
func setupRust(a *auditFixture) {
	p := a.tool("rust", "rustc", true)
	cargo := filepath.Join(a.home, ".cargo/bin/rustc")
	executable(a.t, cargo, "#!/bin/sh\nexit 99\n")
	os.Remove(p)
	symlink(a.fixture, cargo, p)
	shim := filepath.Join(a.home, ".local/share/mise/shims/rustc")
	executable(a.t, shim, "#!/bin/sh\nexit 99\n")
	a.env["PATH"] = filepath.Dir(shim) + ":" + filepath.Dir(cargo) + ":/usr/bin:/bin"
	a.policy = filepath.Dir(cargo) + ":/usr/bin:/bin"
}
func setupVirtual(a *auditFixture) {
	p := a.tool("python", "python3", true)
	v := filepath.Join(a.root, ".venv/bin/python3")
	executable(a.t, v, "#!/bin/sh\nexit 99\n")
	a.env["VIRTUAL_ENV"] = filepath.Dir(filepath.Dir(v))
	a.env["PATH"] = filepath.Dir(v) + ":" + filepath.Dir(p) + ":/usr/bin:/bin"
}
func auditTask(f *fixture) {
	t := f.t
	f.helper()
	f.copy("dotfiles/common/shell/path.sh")
	caller := filepath.Join(f.base, "caller bin")
	mkdir(t, caller)
	f.env["PATH"] = caller + ":" + filepath.Dir(misePath(t)) + ":/usr/bin:/bin:/usr/sbin:/sbin"
	tool := filepath.Join(f.base, "task-python/bin/python3")
	executable(t, tool, "#!/bin/sh\nexit 99\n")
	task := good(t, f.mise("config", "get", "--file", filepath.Join(repo, "mise.toml"), "tasks.audit")).out
	write(t, filepath.Join(f.root, "mise.toml"), "[settings.task]\nrun_auto_install=false\n[tools]\nnode=\"0.0.0\"\npython="+quoteTOML("path:"+filepath.Dir(filepath.Dir(tool)))+"\n[tasks.audit]\n"+task)
	args := []string{"run", "--quiet", "audit", "--only", "tools", "--only", "path", "--json"}
	r := good(t, f.mise(args...))
	var report map[string]any
	if e := json.Unmarshal([]byte(r.out), &report); e != nil {
		t.Fatal(e)
	}
	equal(t, summary(report, "error"), float64(0))
	if !has(codes(report, "tools"), "tool_missing") {
		t.Fatal(report)
	}
	contains(t, r.out, caller)
	equal(t, report["context"].(map[string]any)["path_source"], "caller")
	equal(t, report["context"].(map[string]any)["path_entries"], float64(len(strings.Split(f.env["PATH"], ":"))))
	if exists(filepath.Join(f.env["MISE_DATA_DIR"], "installs/node/0.0.0")) {
		t.Fatal("audit installed node")
	}
	var forced map[string]any
	json.Unmarshal([]byte(good(t, f.mise(append(args, "--color", "always")...)).out), &forced)
	equal(t, forced, report)
	for _, mode := range []string{"auto", "always", "never"} {
		f.env["TERM"] = "dumb"
		f.env["NO_COLOR"] = "1"
		r := good(t, f.mise("run", "--quiet", "audit", "--only", "tools", "--color", mode))
		contains(t, r.out, "[!] 漂移 · node")
		contains(t, r.out, "[+] 已核对 · python")
		equal(t, strings.Contains(r.out, "\x1b["), mode == "always")
	}
	for _, variant := range []string{"color", "NO_COLOR", "TERM"} {
		delete(f.env, "NO_COLOR")
		f.env["TERM"] = "xterm-256color"
		if variant == "NO_COLOR" {
			f.env["NO_COLOR"] = "1"
		}
		if variant == "TERM" {
			f.env["TERM"] = "dumb"
		}
		r := good(t, f.ttyOutput([]string{misePath(t), "run", "audit", "--only", "tools"}))
		contains(t, r.out, "[!] 漂移 · node")
		equal(t, strings.Contains(r.out, "\x1b[31m"), variant == "color")
	}
	r = bad(t, f.mise("run", "--quiet", "audit", "--only", "tools", "--strict", "--json"))
	json.Unmarshal([]byte(r.out), &forced)
	equal(t, summary(forced, "error"), float64(0))
}
