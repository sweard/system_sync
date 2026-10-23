//go:build integration

package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var cleanTargets = []string{".system-sync/rebuild/home", ".system-sync/vscode-validation", ".system-sync/flutter-validation/project", ".system-sync/flutter-validation/pub-cache", ".system-sync/go/build", ".system-sync/dist"}
var cleanPreserved = []string{".system-sync/history/run/transaction.json", ".system-sync/tool-migration/original.bak", ".system-sync/env-path-takeover/original.sh", ".system-sync/app-config-takeover/original.json", ".system-sync/go/mod/cache/download/dependency.zip", ".system-sync/helper/current", ".system-sync/rebuild/result.json", ".system-sync/rebuild/install.log", ".system-sync/flutter-validation/result.json", ".system-sync/flutter-validation/analyze.log", "helper/prebuilt/v0.2.0/helper.tar.gz", "dotfiles/common/mise/config.toml", ".git/objects/keep"}

func init() { handlers["test_clean"] = cleanCase }
func cleanCase(f *fixture, name string) {
	t := f.t
	f.copy("scripts/clean.sh", "scripts/task-output.sh")
	for _, p := range cleanTargets {
		write(t, filepath.Join(f.root, p, "cached-file"), "reproducible data")
	}
	for _, p := range cleanPreserved {
		write(t, filepath.Join(f.root, p), "keep this data")
	}
	outside := filepath.Join(f.base, "outside")
	write(t, filepath.Join(outside, "keep"), "outside data")
	script := filepath.Join(f.root, "scripts/clean.sh")
	run := func(args ...string) result {
		old := f.root
		f.root = outside
		r := f.run(append([]string{script}, args...)...)
		f.root = old
		return r
	}
	preserved := func() {
		for _, p := range cleanPreserved {
			equal(t, read(t, filepath.Join(f.root, p)), "keep this data")
		}
		equal(t, read(t, filepath.Join(outside, "keep")), "outside data")
	}
	absent := func() {
		for _, p := range cleanTargets {
			if exists(filepath.Join(f.root, p)) {
				t.Fatal("cleanup target retained", p)
			}
		}
	}
	stub := func(mode string) string {
		counter := filepath.Join(f.base, "rm-attempts")
		os.Remove(counter)
		f.env["CLEAN_TEST_COUNTER"] = counter
		f.env["CLEAN_TEST_TARGET"] = filepath.Join(f.root, cleanTargets[0])
		f.env["CLEAN_TEST_RM_MODE"] = mode
		f.env["CLEAN_TEST_OUTSIDE"] = outside
		real, e := exec.LookPath("rm")
		if e != nil {
			t.Fatal(e)
		}
		f.env["CLEAN_TEST_REAL_RM"] = real
		f.mock("clean", "rm")
		return counter
	}
	switch name {
	case "test_default_and_explicit_preview_do_not_change_any_files":
		before := snapshot(t, f.root)
		for _, a := range [][]string{nil, {"--dry-run"}} {
			r := good(t, run(a...))
			for _, p := range cleanTargets {
				contains(t, r.out, p)
			}
			contains(t, r.out, "待清理 6 项")
			contains(t, r.out, "预计可回收")
			contains(t, r.out, "以上仅为预览")
			equal(t, snapshot(t, f.root), before)
		}
	case "test_apply_from_other_directory_removes_only_the_allowlist":
		good(t, run("--apply", "--yes"))
		absent()
		preserved()
		contains(t, good(t, run("--apply", "--yes")).out, "无需清理")
	case "test_missing_state_directory_is_a_noop_and_is_not_created":
		os.RemoveAll(filepath.Join(f.root, ".system-sync"))
		before := snapshot(t, f.root)
		contains(t, good(t, run("--apply", "--yes")).out, "无需清理")
		equal(t, snapshot(t, f.root), before)
		if exists(filepath.Join(f.root, ".system-sync")) {
			t.Fatal("noop created state")
		}
	case "test_noninteractive_apply_requires_an_explicit_yes_flag":
		before := snapshot(t, f.root)
		r := run("--apply")
		equal(t, r.code, 2)
		contains(t, r.err, "非交互环境未执行清理")
		equal(t, snapshot(t, f.root), before)
	case "test_interactive_confirmation_accepts_only_exact_lowercase_yes":
		before := snapshot(t, f.root)
		for _, a := range []string{"", "y", "YES", "yes ", "no"} {
			r := f.terminal([]string{script, "--apply"}, [][2]string{{"输入 yes 确认", a}})
			equal(t, r.code, 2)
			equal(t, snapshot(t, f.root), before)
		}
		good(t, f.terminal([]string{script, "--apply"}, [][2]string{{"输入 yes 确认", "yes"}}))
		absent()
		preserved()
	case "test_invalid_arguments_and_help_leave_files_untouched":
		before := snapshot(t, f.root)
		for _, a := range [][]string{{"--yes"}, {"--dry-run", "--yes"}, {"--apply", "--dry-run"}, {"--dry-run", "--apply"}, {"--all"}, {outside}} {
			equal(t, run(a...).code, 2)
			equal(t, snapshot(t, f.root), before)
		}
		contains(t, good(t, run("--help")).out, "用法：clean.sh")
		equal(t, snapshot(t, f.root), before)
	case "test_linked_state_parent_or_target_aborts_before_any_deletion":
		for _, rel := range []string{".system-sync", ".system-sync/rebuild", ".system-sync/go/build"} {
			p := filepath.Join(f.root, rel)
			backup := p + ".test-backup"
			if e := os.Rename(p, backup); e != nil {
				t.Fatal(e)
			}
			symlink(f, outside, p)
			before := snapshot(t, f.root)
			for _, a := range [][]string{{"--dry-run"}, {"--apply", "--yes"}} {
				r := run(a...)
				equal(t, r.code, 1)
				contains(t, r.err, "不能是符号链接")
				equal(t, snapshot(t, f.root), before)
				equal(t, read(t, filepath.Join(outside, "keep")), "outside data")
			}
			os.Remove(p)
			os.Rename(backup, p)
		}
	case "test_non_directory_target_aborts_before_any_deletion":
		p := filepath.Join(f.root, ".system-sync/go/build")
		os.RemoveAll(p)
		write(t, p, "unexpected user file")
		before := snapshot(t, f.root)
		r := run("--apply", "--yes")
		equal(t, r.code, 1)
		contains(t, r.err, "必须是目录")
		equal(t, snapshot(t, f.root), before)
	case "test_links_inside_a_cleanup_directory_do_not_remove_external_data":
		p := filepath.Join(f.root, ".system-sync/go/build/external")
		symlink(f, outside, p)
		good(t, run("--apply", "--yes"))
		if exists(p) {
			t.Fatal("external link retained")
		}
		preserved()
	case "test_transient_directory_metadata_is_retried_and_cleaned", "test_successful_rm_must_also_leave_the_target_absent":
		mode := "once"
		if strings.Contains(name, "successful_rm") {
			mode = "success-recreated"
		}
		counter := stub(mode)
		r := good(t, run("--apply", "--yes"))
		equal(t, read(t, counter), "2\n")
		contains(t, r.out, "重试清理（2/3）")
		excludes(t, r.err, "Directory not empty")
		absent()
		preserved()
	case "test_persistent_directory_writes_stop_after_three_attempts":
		counter := stub("always")
		r := run("--apply", "--yes")
		equal(t, r.code, 1)
		equal(t, read(t, counter), "3\n")
		contains(t, r.err, "Directory not empty")
		excludes(t, r.out, "清理完成")
		for _, p := range cleanTargets[1:] {
			if !exists(filepath.Join(f.root, p)) {
				t.Fatal("cleanup continued after failure")
			}
		}
		preserved()
	case "test_permission_and_mixed_errors_stop_without_retrying":
		for _, mode := range []string{"permission", "mixed"} {
			counter := stub(mode)
			before := snapshot(t, f.root)
			r := run("--apply", "--yes")
			equal(t, r.code, 1)
			equal(t, read(t, counter), "1\n")
			contains(t, r.err, "Permission denied")
			excludes(t, r.out, "重试清理")
			equal(t, snapshot(t, f.root), before)
		}
	case "test_retry_rejects_a_target_replaced_by_an_external_link":
		counter := stub("link")
		r := run("--apply", "--yes")
		equal(t, r.code, 1)
		equal(t, read(t, counter), "1\n")
		contains(t, r.err, "不能是符号链接")
		for _, p := range cleanTargets[1:] {
			if !exists(filepath.Join(f.root, p)) {
				t.Fatal("cleanup continued after link replacement")
			}
		}
		preserved()
	default:
		t.Fatal("unported", name)
	}
}
