//go:build integration

package integration

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func init() {
	handlers["test_converge"] = convergeCase
	handlers["test_parsers"] = parserCase
	handlers["test_runtime"] = runtimeCase
}
func (f *fixture) converge() {
	f.copy("scripts", "packages")
	f.helper()
	executable(f.t, filepath.Join(f.root, "scripts/detect-linux-profile.sh"), "#!/bin/sh\nprintf '%s\\n' \"$FIXTURE_PROFILE\"\n")
	f.env["FIXTURE_STATE"] = filepath.Join(f.base, "state.json")
	f.env["FIXTURE_LOG"] = filepath.Join(f.base, "calls.jsonl")
	f.mock("converge", "uname", "mise", "sudo", "dpkg-query", "apt-mark", "apt-get", "dnf", "pacman", "rpm", "zypper")
}
func mandatory(p string) string {
	return map[string]string{"arch": "base", "debian": "base-files", "fedora": "filesystem", "opensuse": "filesystem"}[p]
}
func (f *fixture) configure(p string, options map[string]any) {
	s := map[string]any{"profile": p, "installed": []string{mandatory(p), "git"}, "explicit": []string{mandatory(p), "git"}, "declared": []string{mandatory(p), "git", "new-tool"}}
	for k, v := range options {
		s[k] = v
	}
	writeJSON(f.t, f.env["FIXTURE_STATE"], s)
	f.env["FIXTURE_PROFILE"] = p
	os.Remove(f.env["FIXTURE_LOG"])
}
func (f *fixture) state() map[string]any {
	var s map[string]any
	readJSON(f.t, f.env["FIXTURE_STATE"], &s)
	return s
}
func (f *fixture) calls() []map[string]any {
	out := []map[string]any{}
	p := f.env["FIXTURE_LOG"]
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
func (f *fixture) kinds() []string {
	a := []string{}
	for _, c := range f.calls() {
		a = append(a, c["kind"].(string))
	}
	return a
}
func (f *fixture) journal() map[string]any {
	paths, _ := filepath.Glob(filepath.Join(f.root, ".system-sync/history/*/transaction.json"))
	if len(paths) == 0 {
		f.t.Fatal("missing transaction audit")
	}
	var j map[string]any
	readJSON(f.t, paths[len(paths)-1], &j)
	return j
}
func (f *fixture) sync(mode string, terminal bool, answers ...[2]string) result {
	args := []string{"/bin/bash", filepath.Join(f.root, "scripts/system-sync.sh"), mode}
	if terminal {
		return f.terminal(args, answers)
	}
	return f.run(args...)
}
func convergeCase(f *fixture, name string) {
	t := f.t
	f.converge()
	ps := profiles()
	if len(ps) == 0 {
		t.Fatal("Linux case selected on macOS-only scope")
	}
	chosen := ps[0]
	switch name {
	case "test_noninteractive_apply_never_reaches_installer", "test_direct_platform_scripts_enforce_noninteractive_guard":
		for _, p := range ps {
			f.configure(p, nil)
			scripts := []string{"system-sync.sh"}
			if strings.Contains(name, "direct_") {
				scripts = []string{map[string]string{"arch": "pacman-converge.sh", "debian": "apt-converge.sh", "fedora": "dnf-converge.sh", "opensuse": "zypper-converge.sh"}[p]}
			}
			for _, script := range scripts {
				r := bad(t, f.run("/bin/bash", filepath.Join(f.root, "scripts", script), "--apply"))
				contains(t, r.all(), "交互式终端")
				equal(t, f.kinds(), []string{})
			}
		}
	case "test_linux_install_output_is_live_through_system_sync_for_all_profiles":
		for _, p := range ps {
			f.configure(p, map[string]any{"live_progress": true})
			r := good(t, f.sync("--apply", true, [2]string{"输入 yes 继续", "yes"}, [2]string{"Fixture: native download", "download-seen"}, [2]string{"Fixture: native install", "install-seen"}))
			contains(t, r.all(), "Fixture: native install")
			equal(t, f.journal()["phase"], "complete")
		}
	case "test_wrapper_status_uses_one_preflight_and_one_package_snapshot", "test_readonly_wrappers_reject_old_mise_before_package_queries":
		q := filepath.Join(f.base, "queries.jsonl")
		f.env["FIXTURE_QUERY_LOG"] = q
		for _, p := range ps {
			modes := []string{"--status"}
			if strings.Contains(name, "reject_old") {
				modes = append(modes, "--dry-run")
			}
			for _, mode := range modes {
				opts := map[string]any{}
				if strings.Contains(name, "reject_old") {
					opts["mise_version"] = "2026.10.10 fixture"
				}
				f.configure(p, opts)
				os.Remove(q)
				r := f.sync(mode, false)
				lines := strings.Split(strings.TrimSpace(read(t, q)), "\n")
				versions := 0
				snapshots := []string{}
				for _, line := range lines {
					if line == `["--version"]` {
						versions++
					}
					if strings.Contains(line, "status") {
						snapshots = append(snapshots, line)
					}
				}
				equal(t, versions, 1)
				if len(opts) > 0 {
					bad(t, r)
					equal(t, len(lines), 1)
				} else {
					good(t, r)
					equal(t, snapshots, []string{`["bootstrap","packages","status","--json"]`})
					contains(t, r.all(), "git")
					contains(t, r.all(), "new-tool")
				}
				equal(t, f.kinds(), []string{})
			}
		}
	case "test_cancel_before_install_is_audited":
		for _, p := range ps {
			old := "APPLY 0"
			if p == "opensuse" {
				old = "APPLY ZYPPER"
			}
			for _, response := range []string{"", "no", "y", old} {
				f.configure(p, nil)
				r := f.sync("--apply", true, [2]string{"输入 yes 继续", response})
				equal(t, r.code, 2)
				equal(t, f.kinds(), []string{})
				equal(t, f.journal()["phase"], "cancelled")
			}
		}
	case "test_missing_packages_install_inside_audited_transaction":
		for _, p := range ps {
			f.configure(p, nil)
			good(t, f.sync("--apply", true, [2]string{"输入 yes 继续", "yes"}))
			equal(t, f.kinds(), []string{"install"})
			equal(t, f.calls()[0]["phase"], "installing")
			equal(t, f.journal()["phase"], "complete")
		}
	case "test_partial_install_failure_keeps_audit_and_stops_before_marking", "test_successful_install_with_missing_target_stops":
		key := "install_failure"
		if strings.Contains(name, "missing_target") {
			key = "wrong_install_state"
		}
		f.configure(chosen, map[string]any{key: true})
		r := bad(t, f.sync("--apply", true, [2]string{"输入 yes 继续", "yes"}))
		equal(t, f.kinds(), []string{"install"})
		if key == "install_failure" {
			equal(t, f.journal()["phase"], "failed")
			equal(t, f.journal()["last_phase"], "installing")
		} else {
			contains(t, r.all(), "安装后仍有声明包")
		}
	case "test_large_baseline_change_is_rejected_before_install":
		for _, p := range ps {
			if p == "opensuse" {
				continue
			}
			names := []string{mandatory(p), "git"}
			for i := 0; i < 6; i++ {
				names = append(names, fmt.Sprintf("extra-%d", i))
			}
			f.configure(p, map[string]any{"installed": names, "explicit": names})
			r := bad(t, f.sync("--apply", true))
			contains(t, r.all(), "大范围变更保护")
			equal(t, f.kinds(), []string{})
		}
	case "test_schema_and_manager_failures_stop_before_install":
		for _, p := range ps {
			for _, key := range []string{"json_error", "unavailable", "query_failure"} {
				f.configure(p, map[string]any{key: true})
				bad(t, f.sync("--apply", true))
				equal(t, f.kinds(), []string{})
			}
		}
	case "test_absent_or_unknown_status_never_reaches_installer":
		for _, s := range []string{"skipped", "unexpectedly_installed", "future-state"} {
			f.configure(chosen, map[string]any{"override_state": s})
			bad(t, f.sync("--apply", true))
			equal(t, f.kinds(), []string{})
		}
	case "test_protected_final_transaction_stops_removal":
		for _, p := range ps {
			if p == "opensuse" {
				continue
			}
			a := []string{mandatory(p), "git", "extra"}
			f.configure(p, map[string]any{"installed": a, "explicit": a, "final_orphans": []string{"extra", "git"}, "transaction": []string{"extra", "git"}})
			r := bad(t, f.sync("--apply", true, [2]string{"输入 yes 继续", "yes"}))
			contains(t, r.all(), "保护")
			if has(f.kinds(), "remove") {
				t.Fatal("protected removal reached native manager")
			}
		}
	case "test_cancel_after_marking_preserves_packages_and_restoration_list":
		f.configure("debian", map[string]any{"installed": []string{"base-files", "git", "extra"}, "explicit": []string{"base-files", "git", "extra"}, "final_orphans": []string{"extra"}})
		r := f.sync("--apply", true, [2]string{"输入 yes 继续", "yes"}, [2]string{"即将移除 1 个包，输入 yes", "no"})
		equal(t, r.code, 2)
		equal(t, f.kinds(), []string{"install", "mark"})
		if !has(list(f.state()["installed"]), "extra") {
			t.Fatal("cancel removed package")
		}
		equal(t, f.journal()["phase"], "cancelled")
		paths, _ := filepath.Glob(filepath.Join(f.root, ".system-sync/history/*/demoted.txt"))
		contains(t, read(t, paths[len(paths)-1]), "extra")
	case "test_full_reason_convergence_and_dnf4":
		for _, p := range ps {
			if p == "opensuse" {
				continue
			}
			variants := []bool{false}
			if p == "fedora" {
				variants = append(variants, true)
			}
			for _, v := range variants {
				a := []string{mandatory(p), "git", "extra"}
				f.configure(p, map[string]any{"installed": a, "explicit": a, "final_orphans": []string{"extra"}, "dnf4": v})
				good(t, f.sync("--apply", true, [2]string{"输入 yes 继续", "yes"}, [2]string{"即将移除 1 个包，输入 yes", "yes"}))
				equal(t, f.kinds(), []string{"install", "mark", "remove"})
				if has(list(f.state()["installed"]), "extra") {
					t.Fatal("extra retained")
				}
			}
		}
	case "test_dry_run_never_mutates":
		for _, p := range ps {
			f.configure(p, nil)
			good(t, f.sync("--dry-run", false))
			equal(t, f.kinds(), []string{})
			if exists(filepath.Join(f.root, ".system-sync/history")) {
				t.Fatal("preview created audit")
			}
		}
	case "test_status_skips_install_and_cleanup_solvers_while_dry_run_includes_them":
		q := filepath.Join(f.base, "all.jsonl")
		f.env["FIXTURE_ALL_QUERIES"] = q
		for _, p := range ps {
			f.configure(p, map[string]any{"initial_orphans": []string{"extra"}})
			os.Remove(q)
			r := good(t, f.sync("--status", false))
			contains(t, r.all(), "package-dry-run")
			text := read(t, q)
			for _, x := range []string{`"apply"`, `"autoremove"`, `"--unneeded"`, `"-Qdtq"`, `"--dry-run"`} {
				excludes(t, text, x)
			}
			os.Remove(q)
			good(t, f.sync("--dry-run", false))
			text = read(t, q)
			contains(t, text, `["mise","bootstrap","packages","apply","--dry-run"]`)
			if !strings.Contains(text, "autoremove") && !strings.Contains(text, "--unneeded") && !strings.Contains(text, "-Qdtq") {
				t.Fatal("preview omitted cleanup solver")
			}
			equal(t, f.kinds(), []string{})
		}
	case "test_missing_mandatory_root_stops_before_install":
		for _, p := range ps {
			f.configure(p, map[string]any{"declared": []string{"git", "new-tool"}})
			bad(t, f.sync("--apply", true))
			equal(t, f.kinds(), []string{})
		}
	case "test_zypper_native_install_then_protected_cleanup":
		a := []string{"filesystem", "git", "extra"}
		f.configure("opensuse", map[string]any{"installed": a, "explicit": a})
		good(t, f.sync("--apply", true, [2]string{"输入 yes 继续", "yes"}, [2]string{"即将移除 1 个包，输入 yes", "yes"}))
		equal(t, f.kinds(), []string{"install", "remove"})
		equal(t, list(f.calls()[0]["args"]), []string{"mise", "bootstrap", "packages", "apply", "--manager", "zypper", "--raw"})
		equal(t, f.calls()[1]["phase"], "removing")
		if has(list(f.state()["installed"]), "extra") {
			t.Fatal("extra retained")
		}
		equal(t, f.journal()["phase"], "complete")
	case "test_zypper_revalidates_protected_transaction_after_install":
		a := []string{"filesystem", "git", "extra"}
		f.configure("opensuse", map[string]any{"installed": a, "explicit": a, "final_transaction": []string{"extra", "git"}})
		r := bad(t, f.sync("--apply", true, [2]string{"输入 yes 继续", "yes"}))
		contains(t, r.all(), "保护包")
		equal(t, f.kinds(), []string{"install"})
	case "test_zypper_large_or_malformed_plan_stops_before_install":
		a := []string{"filesystem", "git"}
		for i := 0; i < 26; i++ {
			a = append(a, fmt.Sprintf("extra-%d", i))
		}
		f.configure("opensuse", map[string]any{"installed": a, "explicit": a})
		contains(t, bad(t, f.sync("--apply", true)).all(), "安全阈值")
		equal(t, f.kinds(), []string{})
		f.configure("opensuse", map[string]any{"xml_error": true})
		contains(t, bad(t, f.sync("--apply", true)).all(), "无法解析")
		equal(t, f.kinds(), []string{})
	case "test_old_mise_is_rejected_before_mutation":
		f.configure(chosen, map[string]any{"mise_version": "2026.9.0 fixture"})
		contains(t, bad(t, f.sync("--apply", true)).all(), "2026.10.11")
		equal(t, f.kinds(), []string{})
	case "test_zero_exit_without_completion_marker_is_rejected":
		f.configure(chosen, nil)
		r := bad(t, f.terminal([]string{filepath.Join(f.root, "helper/scripts/run.sh"), "runtime", "--", "/bin/sh", "-c", "exit 0"}, nil))
		contains(t, r.all(), "未记录事务完成")
		equal(t, f.journal()["phase"], "failed")
	case "test_shared_lock_blocks_all_mutating_entrypoints":
		f.configure(chosen, nil)
		release := lock(t, f.root)
		defer release()
		commands := [][]string{{"/bin/bash", filepath.Join(f.root, "scripts/system-sync.sh"), "--apply"}, {filepath.Join(f.root, "helper/scripts/run.sh"), "package", "install", "new-tool"}}
		if os.Getenv("SYSTEM_SYNC_TEST_PROFILE") == "all" {
			commands = append(commands, []string{"/bin/bash", filepath.Join(f.root, "scripts/homebrew-converge.sh"), "--upgrade"})
		}
		for _, c := range commands {
			contains(t, bad(t, f.terminal(c, nil)).all(), "另一条配置或包操作")
		}
		equal(t, f.kinds(), []string{})
	default:
		t.Fatal("unported case", name)
	}
}
func parserCase(f *fixture, name string) {
	t := f.t
	f.helper()
	status := func(manager string, v any, success bool) {
		b, _ := json.Marshal(v)
		r := f.input(string(b), filepath.Join(f.root, "helper/scripts/run.sh"), "parse-status", "--manager", manager, "-")
		if success {
			good(t, r)
			equal(t, strings.TrimSpace(r.out), manager+"\tgit\tinstalled")
		} else {
			equal(t, r.code, 2)
			equal(t, r.out, "")
		}
	}
	switch name {
	case "test_current_managers_and_deterministic_deduplication":
		for _, m := range []string{"apt", "dnf", "pacman", "aur", "zypper"} {
			item := map[string]any{"package": "git", "state": "installed"}
			status(m, map[string]any{m: map[string]any{"available": true, "packages": []any{item, item}}}, true)
		}
	case "test_supported_legacy_shapes":
		item := map[string]any{"manager": "apt", "name": "git", "state": "installed"}
		for _, v := range []any{map[string]any{"apt": []any{item}}, map[string]any{"packages": []any{item}}, []any{item}} {
			status("apt", v, true)
		}
	case "test_unavailable_empty_and_changed_schema_fail_closed":
		for _, v := range []any{map[string]any{}, []any{}, map[string]any{"apt": map[string]any{"available": false, "reason": "not installed"}}, map[string]any{"apt": map[string]any{"available": true, "packages": []any{}}}, map[string]any{"apt": map[string]any{"available": true, "items": []any{}}}, map[string]any{"apt": map[string]any{"packages": map[string]any{}}}, map[string]any{"packages": []any{"git"}}} {
			status("apt", v, false)
		}
	case "test_unsafe_package_names_are_rejected":
		for _, n := range []string{"--help", "git;id", "../git", "git\nbase", "git*"} {
			status("apt", map[string]any{"apt": []any{map[string]any{"package": n, "state": "installed"}}}, false)
		}
	case "test_unknown_skipped_and_absent_states_fail_closed":
		for _, s := range []any{"unknown", "skipped", "absent", "unexpectedly_installed", "future-state", nil, []any{}, map[string]any{}} {
			status("apt", map[string]any{"apt": []any{map[string]any{"package": "git", "state": s}}}, false)
		}
		status("apt", map[string]any{"apt": []any{map[string]any{"package": "git", "state": "installed", "desired_state": "absent"}}}, false)
	default:
		mode := "to-remove"
		texts := []string{}
		want := ""
		success := true
		switch name {
		case "test_namespaces_and_non_package_query_items":
			mode = "solvables"
			texts = []string{`<stream xmlns="fixture"><solvable name="git" kind="package"/><solvable name="desktop" kind="pattern"/></stream>`}
			want = "git"
		case "test_only_removals_are_extracted":
			texts = []string{`<stream><to-install><solvable name="keep"/></to-install><to-remove><solvable><name>extra</name><kind>package</kind></solvable></to-remove></stream>`}
			want = "extra"
		case "test_non_package_removal_and_unsafe_names_fail":
			success = false
			for _, s := range []string{`<solvable name="desktop" kind="pattern"/>`, `<solvable name="--help"/>`, `<solvable/>`} {
				texts = append(texts, `<stream><to-remove><solvable name="valid-before-error"/>`+s+`</to-remove></stream>`)
			}
		default:
			t.Fatal("unported", name)
		}
		for _, text := range texts {
			r := f.input(text, filepath.Join(f.root, "helper/scripts/run.sh"), "parse-zypper", mode, "-")
			if success {
				good(t, r)
				equal(t, strings.TrimSpace(r.out), want)
			} else {
				equal(t, r.code, 2)
				equal(t, r.out, "")
			}
		}
	}
}
func runtimeCase(f *fixture, name string) {
	t := f.t
	f.converge()
	f.configure("debian", nil)
	switch name {
	case "test_old_python_is_rejected_before_native_commands": // Obsolete Python-version guard replaced by proving there is no interpreter prerequisite.
		f.shell("python3", "exit 99")
		f.shell("python", "exit 99")
		good(t, f.cli("runtime", "--check"))
		equal(t, f.kinds(), []string{})
	case "test_supported_python_delegates_preflight_to_the_go_helper":
		good(t, f.cli("runtime", "--check"))
		equal(t, f.kinds(), []string{})
	case "test_go_preflight_failure_is_propagated":
		f.configure("debian", map[string]any{"mise_version": "2026.9.0"})
		r := bad(t, f.cli("runtime", "--check"))
		contains(t, r.err, "2026.10.11")
		equal(t, f.kinds(), []string{})
	default:
		t.Fatal("unported", name)
	}
}
