//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/sweard/system_sync/helper/dev/checkrun"
)

func init() { handlers["test_check_runner"] = runnerCase }
func runnerCase(f *fixture, name string) {
	t := f.t
	var catalog []checkrun.Case
	readJSON(t, filepath.Join(repo, "tests/coverage.json"), &catalog)
	switch name {
	case "test_current_scope_filters_os_distro_and_architecture_and_all_keeps_everything", "test_method_metadata_can_narrow_a_platform_specific_class":
		for _, p := range []string{"macos", "arch", "debian", "fedora", "opensuse"} {
			for _, q := range []string{"macos", "arch", "debian", "fedora", "opensuse"} {
				equal(t, checkrun.Matches(checkrun.Case{Platforms: []string{q}}, p), p == q)
			}
			equal(t, checkrun.Matches(checkrun.Case{Platforms: []string{"linux"}}, p), p != "macos")
			equal(t, checkrun.Matches(checkrun.Case{Platforms: []string{"common"}}, p), true)
			equal(t, checkrun.Matches(checkrun.Case{Platforms: []string{"all"}}, p), false)
		}
		equal(t, checkrun.Matches(checkrun.Case{Platforms: []string{"opensuse"}}, "arch"), false)
		equal(t, checkrun.Matches(checkrun.Case{Platforms: []string{"opensuse"}}, "all"), true)
		if e := checkrun.ValidateCases([]checkrun.Case{{ID: "bad", Platforms: []string{"typo-profile"}}}); e == nil {
			t.Fatal("unknown platform tag accepted")
		}
	case "test_host_detection_uses_the_executing_os_architecture_and_linux_detector":
		for _, pair := range [][2]string{{"darwin", "arm64"}, {"darwin", "amd64"}, {"linux", "arm64"}, {"linux", "amd64"}} {
			target, e := checkrun.Target(pair[0], pair[1])
			if e != nil {
				t.Fatal(e)
			}
			equal(t, target, pair[0]+"_"+pair[1])
		}
		if _, e := checkrun.Target("windows", "amd64"); e == nil {
			t.Fatal("unsupported target accepted")
		}
		p, e := checkrun.Profile(context.Background(), f.root, "darwin")
		if e != nil {
			t.Fatal(e)
		}
		equal(t, p, "macos")
		for _, p := range []string{"arch", "debian", "fedora", "opensuse"} {
			executable(t, filepath.Join(f.root, "scripts/detect-linux-profile.sh"), "#!/bin/sh\nprintf '%s\\n' "+quote(p)+"\n")
			actual, e := checkrun.Profile(context.Background(), f.root, "linux")
			if e != nil {
				t.Fatal(e)
			}
			equal(t, actual, p)
		}
		executable(t, filepath.Join(f.root, "scripts/detect-linux-profile.sh"), "#!/bin/sh\necho private-detail >&2\nexit 1\n")
		if _, e := checkrun.Profile(context.Background(), f.root, "linux"); e == nil || strings.Contains(e.Error(), "private-detail") {
			t.Fatal("bad detector diagnostics", e)
		}
	case "test_real_catalog_preserves_full_coverage_and_excludes_foreign_package_workflows":
		equal(t, len(catalog), 201)
		seen := map[string]bool{}
		for _, c := range catalog {
			if seen[c.ID] {
				t.Fatal("duplicate legacy method")
			}
			seen[c.ID] = true
			equal(t, checkrun.Matches(c, "all"), true)
		}
		for _, p := range []string{"macos", "arch", "debian", "fedora", "opensuse"} {
			included, excluded := 0, 0
			for _, c := range catalog {
				if checkrun.Matches(c, p) {
					included++
					if p == "macos" && strings.HasPrefix(c.ID, "test_converge.") {
						t.Fatal("foreign package test selected")
					}
					if p != "macos" && strings.HasPrefix(c.ID, "test_homebrew_preview.") {
						t.Fatal("foreign Homebrew test selected")
					}
				} else {
					excluded++
				}
				if strings.HasSuffix(c.ID, "test_zypper_native_install_then_protected_cleanup") {
					equal(t, checkrun.Matches(c, p), p == "opensuse")
				}
			}
			equal(t, included+excluded, len(catalog))
		}
	case "test_report_is_complete_json_and_replaces_a_previous_result":
		p := filepath.Join(f.base, "timing/report.json")
		if e := checkrun.Save(p, map[string]any{"exit_code": 1}); e != nil {
			t.Fatal(e)
		}
		v := map[string]any{"exit_code": float64(0), "tests": []any{"完整记录"}}
		if e := checkrun.Save(p, v); e != nil {
			t.Fatal(e)
		}
		equal(t, jsonData(f, p), v)
		entries, _ := os.ReadDir(filepath.Dir(p))
		equal(t, len(entries), 1)
	case "test_timing_records_cleanup_errors_skips_and_subtest_failures":
		events := ""
		for _, row := range []struct{ id, action string }{{"pass", "pass"}, {"subtest-failure", "fail"}, {"cleanup-error", "fail"}, {"skip", "skip"}} {
			b, _ := json.Marshal(map[string]any{"Action": row.action, "Test": "TestIntegration/parallel/" + row.id, "Elapsed": 0.01})
			events += string(b) + "\n"
		}
		expected := map[string]bool{"pass": true, "subtest-failure": true, "cleanup-error": true, "skip": true}
		records, e := checkrun.Collect(strings.NewReader(events), expected, io.Discard)
		if e == nil {
			t.Fatal("failure hidden")
		}
		equal(t, len(records), 4)
		outcomes := map[string]string{}
		for _, r := range records {
			outcomes[r.ID] = r.Outcome
			if r.Seconds < 0 {
				t.Fatal("negative duration")
			}
		}
		equal(t, outcomes, map[string]string{"pass": "passed", "subtest-failure": "failed", "cleanup-error": "failed", "skip": "skipped"})
	case "test_stage_failure_and_exception_are_recorded_and_stop_later_checks":
		f.copy("tests/coverage.json", "scripts/detect-linux-profile.sh")
		for _, command := range [][]string{{"/bin/sh", "-c", "exit 7"}, {filepath.Join(f.base, "missing-executable")}} {
			var out bytes.Buffer
			p := filepath.Join(f.base, "report.json")
			code := checkrun.Run(context.Background(), checkrun.Config{Root: f.root, ReportPath: p, All: true, Jobs: 1, Out: &out, Err: &out, Stages: []checkrun.Command{{Name: "failed", Args: command}, {Name: "must-not-run", Args: []string{"/bin/sh", "-c", "touch " + quote(filepath.Join(f.base, "should-not-exist"))}}}})
			if code == 0 {
				t.Fatal("stage failure hidden")
			}
			v := jsonData(f, p)
			equal(t, v["exit_code"], float64(code))
			equal(t, len(v["stages"].([]any)), 1)
			if exists(filepath.Join(f.base, "should-not-exist")) {
				t.Fatal("later stage ran")
			}
		}
		p := filepath.Join(f.base, "current.json")
		code := checkrun.Run(context.Background(), checkrun.Config{Root: f.root, ReportPath: p, Jobs: 1, Out: io.Discard, Err: io.Discard, Stages: []checkrun.Command{{Name: "stop", Args: []string{"/bin/sh", "-c", "exit 7"}}}})
		equal(t, code, 7)
		equal(t, jsonData(f, p)["target"], runtime.GOOS+"_"+runtime.GOARCH)
		checkGoPreparationCLI(f)
	case "test_previous_durations_balance_groups_without_dropping_or_duplicating_cases":
		cases := []checkrun.Case{{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"}}
		previous := []checkrun.Timing{{ID: "a", Seconds: 8}, {ID: "b", Seconds: 4}, {ID: "c", Seconds: 3}, {ID: "d", Seconds: 1}}
		groups := checkrun.Groups(cases, 2, previous)
		equal(t, len(groups), 2)
		seen := map[string]bool{}
		weights := map[string]float64{"a": 8, "b": 4, "c": 3, "d": 1}
		for _, g := range groups {
			sum := 0.0
			for _, c := range g {
				if seen[c.ID] {
					t.Fatal("duplicate case")
				}
				seen[c.ID] = true
				sum += weights[c.ID]
			}
			equal(t, sum, float64(8))
		}
		equal(t, len(seen), 4)
		equal(t, checkrun.Groups(cases, 1, previous), [][]checkrun.Case{cases})
	case "test_zero_exit_without_worker_results_is_a_failure":
		records, e := checkrun.Collect(strings.NewReader(""), map[string]bool{"must-run": true}, io.Discard)
		if e == nil || len(records) != 0 {
			t.Fatal("incomplete zero-exit result accepted")
		}
		syntheticRunner(f, `func TestIntegration(t *testing.T){os.Exit(0)}`, []string{"must-run"}, false)
	case "test_failure_and_subtest_failure_keep_nonzero_exit_and_complete_coverage":
		syntheticRunner(f, `func TestIntegration(t *testing.T){t.Run("parallel",func(t *testing.T){t.Run("fail",func(t *testing.T){t.Run("child",func(t *testing.T){t.Fatal("synthetic failure")})});t.Run("pass",func(t *testing.T){})})}`, []string{"fail", "pass"}, false)
	case "test_independent_processes_overlap_and_every_case_has_a_duration":
		syntheticRunner(f, `func TestIntegration(t *testing.T){t.Run("parallel",func(t *testing.T){for _,id:=range []string{"one","two","three","four"}{id:=id;t.Run(id,func(t *testing.T){t.Parallel();path:=filepath.Join(os.Getenv("CASE_ROOT"),id);os.WriteFile(path,nil,0600);deadline:=time.Now().Add(5*time.Second);for{paths,_:=filepath.Glob(filepath.Join(os.Getenv("CASE_ROOT"),"*"));if len(paths)>=2{break};if time.Now().After(deadline){t.Fatal("workers did not overlap")};time.Sleep(time.Millisecond)}})}})}`, []string{"one", "two", "three", "four"}, true)
	case "test_timing_sensitive_cases_run_after_parallel_workers_finish":
		syntheticRunner(f, `func TestIntegration(t *testing.T){root:=os.Getenv("CASE_ROOT");t.Run("parallel",func(t *testing.T){t.Run("payload",func(t *testing.T){t.Parallel();os.WriteFile(filepath.Join(root,"active"),nil,0600);time.Sleep(20*time.Millisecond);os.Remove(filepath.Join(root,"active"));os.WriteFile(filepath.Join(root,"done"),nil,0600)})});t.Run("serial",func(t *testing.T){t.Run("exclusive",func(t *testing.T){if _,e:=os.Stat(filepath.Join(root,"done"));e!=nil{t.Fatal(e)};if _,e:=os.Stat(filepath.Join(root,"active"));e==nil{t.Fatal("parallel still active")}})})}`, []string{"payload", "exclusive"}, true)
	case "test_interruption_reaps_workers":
		pidfile := filepath.Join(f.base, "child.pid")
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan int, 1)
		go func() {
			done <- checkrun.Execute(ctx, f.root, []string{"/bin/sh", "-c", "echo $$ > " + quote(pidfile) + "; exec /bin/sleep 30"}, f.environment(), io.Discard, io.Discard)
		}()
		deadline := time.Now().Add(5 * time.Second)
		for !exists(pidfile) {
			if time.Now().After(deadline) {
				cancel()
				t.Fatal("child not started")
			}
			time.Sleep(time.Millisecond)
		}
		pid := 0
		fmt.Sscanf(read(t, pidfile), "%d", &pid)
		cancel()
		equal(t, <-done, 130)
		if e := syscall.Kill(pid, 0); e != syscall.ESRCH {
			t.Fatal("interrupted worker alive", pid, e)
		}
		// Native commands can have their own process groups. A live fixture
		// lease must be reaped; an unlocked stale PID must never be signalled.
		leaseDir := f.env["SYSTEM_SYNC_FIXTURE_LEASES"]
		write(t, filepath.Join(leaseDir, strconv.Itoa(os.Getpid())), "stale lease")
		child := filepath.Join(f.base, "leased-child.json")
		f.env["FEEDBACK_CHILD"] = child
		f.mock("feedback", "sleep")
		command := exec.Command(filepath.Join(f.bin, "sleep"))
		command.Env = f.environment()
		command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if e := startTracked(command); e != nil {
			t.Fatal(e)
		}
		defer command.Process.Kill()
		deadline = time.Now().Add(5 * time.Second)
		for !exists(child) {
			if time.Now().After(deadline) {
				t.Fatal("leased fixture never started")
			}
			time.Sleep(time.Millisecond)
		}
		cleanupLeaseDir(leaseDir)
		if e := waitTracked(command); e == nil {
			t.Fatal("leased fixture was not stopped")
		}
		if e := syscall.Kill(command.Process.Pid, 0); e != syscall.ESRCH {
			t.Fatal("leased fixture still alive", e)
		}
	case "test_current_flag_checks_each_executing_target_without_requiring_foreign_archives":
		bundleScope(f)
	default:
		t.Fatal("unported", name)
	}
}
func syntheticRunner(f *fixture, source string, ids []string, success bool) {
	t := f.t
	root := filepath.Join(f.base, "synthetic")
	mkdir(t, root)
	goPath := filepath.Join(root, "helper")
	write(t, filepath.Join(goPath, "go.mod"), "module synthetic\ngo 1.23\n")
	imports := `package synthetic
import("testing";"os";"path/filepath";"time")
var _=os.Getpid;var _=filepath.Join;var _=time.Second
`
	write(t, filepath.Join(goPath, "dev/integration/synthetic_test.go"), imports+source)
	cases := []checkrun.Case{}
	for _, id := range ids {
		cases = append(cases, checkrun.Case{ID: id, Platforms: []string{"common"}})
	}
	writeJSON(t, filepath.Join(root, "tests/coverage.json"), cases)
	markers := filepath.Join(root, "markers")
	mkdir(t, markers)
	command := []string{"env", "CASE_ROOT=" + markers, "go", "test", "-mod=vendor", "-json", "-count=1", "-parallel=2", "./dev/integration"}
	code := checkrun.Run(context.Background(), checkrun.Config{Root: root, All: true, Jobs: 2, Out: io.Discard, Err: io.Discard, Stages: []checkrun.Command{{Name: "synthetic integration", Args: command, Integration: true}}})
	equal(t, code == 0, success)
	report := jsonData(f, filepath.Join(root, ".system-sync/check/latest.json"))
	records := report["tests"].([]any)
	if len(ids) == 1 && ids[0] == "must-run" {
		equal(t, len(records), 0)
	} else {
		equal(t, len(records), len(ids))
		for _, r := range records {
			if r.(map[string]any)["seconds"].(float64) < 0 {
				t.Fatal("missing timing")
			}
		}
	}
}
func bundleScope(f *fixture) {
	t := f.t
	f.copy("helper/scripts/verify.sh")
	bundle := filepath.Join(f.root, "helper/prebuilt/v0.0.0")
	mkdir(t, bundle)
	targets := []string{"darwin_arm64", "darwin_amd64", "linux_arm64", "linux_amd64"}
	payload := "synthetic archive content"
	checks := ""
	for _, target := range targets {
		checks += digest([]byte(payload)) + "  system-sync-helper_v0.0.0_" + target + ".tar.gz\n"
	}
	write(t, filepath.Join(bundle, "SHA256SUMS"), checks)
	for _, target := range targets {
		files, _ := filepath.Glob(filepath.Join(bundle, "*.tar.gz"))
		for _, p := range files {
			os.Remove(p)
		}
		archive := filepath.Join(bundle, "system-sync-helper_v0.0.0_"+target+".tar.gz")
		write(t, archive, payload)
		write(t, filepath.Join(bundle, "SOURCE_SHA256SUMS"), "source-record\n")
		common := "HELPER_DIR=" + quote(filepath.Join(f.root, "helper")) + "\nhelper_error() { printf '%s\\n' \"$*\" >&2; exit 1; }\nhelper_version() { printf 'v0.0.0\\n'; }\nhelper_target() { printf '%s\\n' " + quote(target) + "; }\nhelper_source_manifest() { printf 'source-record\\n'; }\nhelper_checksum() { shasum -a 256 \"$1\" | awk '{ print $1 }'; }\nsystem_sync_step() { shift; \"$@\"; }\n"
		write(t, filepath.Join(f.root, "helper/scripts/common.sh"), common)
		verify := func(args ...string) result {
			return f.run(append([]string{"/bin/bash", filepath.Join(f.root, "helper/scripts/verify.sh")}, args...)...)
		}
		contains(t, good(t, verify("--current-platform")).out, target)
		bad(t, verify())
		write(t, archive, payload+"corrupt")
		contains(t, bad(t, verify("--current-platform")).err, "SHA256 校验失败")
		write(t, archive, payload)
		write(t, filepath.Join(bundle, "SOURCE_SHA256SUMS"), "stale-source-record\n")
		contains(t, bad(t, verify("--current-platform")).err, "构建输入已变化")
	}
}
