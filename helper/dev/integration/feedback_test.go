//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/pelletier/go-toml/v2"
)

func init() { handlers["test_task_feedback"] = feedbackCase }
func mockFeedback(name string, args []string) int {
	switch {
	case name == "sleep":
		mockSave(os.Getenv("FEEDBACK_CHILD"), map[string]any{"pid": os.Getpid(), "parent": os.Getppid()})
		time.Sleep(30 * time.Second)
		return 0
	case name == "uname":
		printlnOut("Darwin")
		return 0
	case name == "mise" && len(args) > 3 && args[0] == "config":
		b, _ := os.ReadFile(args[3])
		var v map[string]any
		if toml.Unmarshal(b, &v) != nil {
			return 91
		}
		printlnOut(v["tools"].(map[string]any)["go"])
		return 0
	case name == "mise" && strings.Join(args, " ") == "--version":
		if os.Getenv("FEEDBACK_FAIL_VERSION") != "" {
			printlnErr("private-feedback-version")
			return 19
		}
		if os.Getenv("FEEDBACK_BAD_VERSION") != "" {
			printlnOut("private-feedback-invalid-version")
			return 0
		}
		printlnOut("2026.10.15")
		return 0
	case name == "go" && len(args) > 1 && args[0] == "env":
		printlnOut(map[string]string{"GOVERSION": os.Getenv("FEEDBACK_GO_VERSION"), "GOHOSTOS": "darwin", "GOHOSTARCH": "arm64"}[args[1]])
		return 0
	case name == "git":
		if len(args) > 1 && args[len(args)-2] == "--short=12" {
			printlnOut("0123456789ab")
		}
		return 0
	}
	os.WriteFile(os.Getenv("FEEDBACK_ENTERED"), nil, 0600)
	deadline := time.Now().Add(8 * time.Second)
	for !exists(os.Getenv("FEEDBACK_RELEASE")) {
		if time.Now().After(deadline) {
			return 96
		}
		time.Sleep(10 * time.Millisecond)
	}
	if os.Getenv("FEEDBACK_FAIL") != "" || name == "go" {
		printlnOut("private-feedback-output")
		printlnErr("private-feedback-diagnostic")
		if name == "go" {
			return 42
		}
		return 19
	}
	switch name {
	case "mise":
		if len(args) > 2 && strings.Join(args[:3], " ") == "bootstrap dotfiles status" {
			printlnOut(`{"files":[],"edits":[]}`)
		} else if len(args) > 4 && strings.Join(args[:5], " ") == "dot --local history ls --json" {
			printlnOut("[]")
		} else if len(args) > 0 && args[0] == "ls" {
			printlnOut("{}")
		} else {
			return 91
		}
	case "brew":
		if len(args) > 1 && args[0] == "list" {
			if args[1] == "--formula" {
				printlnOut("git")
			}
		} else if len(args) > 1 && args[0] == "bundle" && args[1] == "dump" {
			for _, arg := range args {
				if strings.HasPrefix(arg, "--file=") {
					os.WriteFile(strings.TrimPrefix(arg, "--file="), []byte("brew \"git\"\n"), 0600)
				}
			}
		} else {
			return 92
		}
	case "code":
		if strings.Join(args, " ") == "--list-extensions --show-versions" {
			printlnOut("example.shared@1.2.3")
		} else {
			return 93
		}
	default:
		return 93
	}
	return 0
}

type safeBuffer struct {
	sync.Mutex
	bytes.Buffer
}

func (w *safeBuffer) Write(p []byte) (int, error) {
	w.Lock()
	defer w.Unlock()
	return w.Buffer.Write(p)
}
func (w *safeBuffer) text() string { w.Lock(); defer w.Unlock(); return w.Buffer.String() }
func (f *fixture) blocked(args []string, label string) result {
	t := f.t
	release, entered := f.env["FEEDBACK_RELEASE"], f.env["FEEDBACK_ENTERED"]
	os.Remove(release)
	os.Remove(entered)
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, args[0], args[1:]...)
	c.Dir = f.root
	c.Env = f.environment()
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Cancel = func() error { return syscall.Kill(-c.Process.Pid, syscall.SIGKILL) }
	var stdout, stderr safeBuffer
	c.Stdout = &stdout
	c.Stderr = &stderr
	if e := startTracked(c); e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { done <- waitTracked(c) }()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	released := false
	var err error
loop:
	for {
		select {
		case err = <-done:
			break loop
		case <-ticker.C:
			if !released && strings.Contains(stderr.text(), label) && exists(entered) {
				released = true
				os.WriteFile(release, nil, 0600)
			}
		}
	}
	os.WriteFile(release, nil, 0600)
	if ctx.Err() != nil || !released {
		t.Fatalf("no feedback while command blocked: %v\n%s%s", args, stdout.text(), stderr.text())
	}
	code := 0
	if err != nil {
		if x, ok := err.(*exec.ExitError); ok {
			code = x.ExitCode()
		} else {
			t.Fatal(err)
		}
	}
	return result{code, stdout.text(), stderr.text()}
}
func feedbackCase(f *fixture, name string) {
	t := f.t
	f.helper()
	f.goConfig()
	f.mock("feedback", "mise", "uname", "brew", "code", "go", "git")
	f.env["PATH"] = f.bin + ":/usr/bin:/bin:/usr/sbin:/sbin"
	f.env["FEEDBACK_RELEASE"] = filepath.Join(f.base, "release")
	f.env["FEEDBACK_ENTERED"] = filepath.Join(f.base, "entered")
	f.env["FEEDBACK_GO_VERSION"] = "go" + goVersion(t)
	f.env["MISE_CONFIG_DIR"] = filepath.Join(f.home, ".config/mise")
	f.env["MISE_STATE_DIR"] = filepath.Join(f.home, "state")
	f.env["XDG_STATE_HOME"] = filepath.Join(f.home, "state")
	write(t, filepath.Join(f.root, "packages/macos/Brewfile"), "brew \"git\"\n")
	write(t, filepath.Join(f.root, "packages/macos/protected-packages.txt"), "formula:git\n")
	write(t, filepath.Join(f.root, "dotfiles/common/vscode/extensions.toml"), "[common]\n\"example.shared\"=\"1.2.3\"\n")
	write(t, filepath.Join(f.root, "mise.toml"), "[dotfiles]\n")
	cases := []struct {
		args  []string
		label string
	}{{[]string{"config", "status", "--json"}, "查询受管配置状态"}, {[]string{"config-history", "list", "--json"}, "查询本机配置历史"}, {[]string{"config", "diff"}, "读取受管配置状态与差异"}, {[]string{"config-detach", "--dry-run"}, "读取配置迁出状态"}, {[]string{"vscode", "status", "--code", filepath.Join(f.bin, "code")}, "查询 VS Code 已安装扩展"}, {[]string{"vscode", "status", "--capture", "--code", filepath.Join(f.bin, "code")}, "采集 VS Code 扩展与本机版本"}, {[]string{"package-capture"}, "采集本机软件清单与来源"}, {[]string{"machine-init", "--only", "packages"}, "读取机器接入计划"}, {[]string{"audit", "--only", "tools"}, "只读审计：tools"}}
	switch name {
	case "test_readonly_tasks_report_progress_before_native_queries_finish":
		for _, c := range cases {
			r := good(t, f.blocked(append([]string{filepath.Join(f.root, "helper/scripts/run.sh")}, c.args...), c.label))
			excludes(t, r.out, "[~] "+c.label)
			if has(c.args, "--json") && !json.Valid([]byte(r.out)) {
				t.Fatal("invalid JSON", r.out)
			}
		}
		if exists(filepath.Join(f.root, ".system-sync/history")) || exists(filepath.Join(f.root, ".system-sync/capture")) {
			t.Fatal("readonly task wrote history")
		}
	case "test_query_errors_keep_failure_category_and_hide_native_diagnostics":
		f.env["FEEDBACK_FAIL"] = "1"
		write(t, f.env["FEEDBACK_RELEASE"], "")
		for _, c := range cases {
			r := bad(t, f.cli(c.args...))
			contains(t, r.all(), "19")
			excludes(t, r.all(), "private-feedback")
		}
	case "test_machine_readable_previews_stay_parseable_and_quiet":
		write(t, f.env["FEEDBACK_RELEASE"], "")
		for _, args := range [][]string{{"audit", "--only", "tools", "--json"}, {"package-capture", "--json"}, {"machine-init", "--only", "packages", "--json"}} {
			r := good(t, f.cli(args...))
			if !json.Valid([]byte(r.out)) {
				t.Fatal("invalid JSON", r.out)
			}
			excludes(t, r.err, "[~]")
		}
	case "test_failed_version_query_is_not_reported_as_an_outdated_version":
		for flag, message := range map[string]string{"FEEDBACK_FAIL_VERSION": "退出码 19", "FEEDBACK_BAD_VERSION": "版本输出无法识别"} {
			f.env[flag] = "1"
			r := bad(t, f.cli("package-capture", "--json"))
			contains(t, r.err, message)
			excludes(t, r.err, "请先升级")
			excludes(t, r.all(), "private-feedback")
			delete(f.env, flag)
		}
	case "test_native_help_failure_returns_nonzero_and_actionable_diagnostic":
		f.env["FEEDBACK_FAIL"] = "1"
		write(t, f.env["FEEDBACK_RELEASE"], "")
		r := bad(t, f.cli("config", "diff", "--help"))
		contains(t, r.err, "退出码 19")
		contains(t, r.err, "mise bootstrap dotfiles diff --help")
		excludes(t, r.all(), "private-feedback")
	case "test_silent_build_reports_target_before_completion_and_preserves_exit_code":
		f.copy("helper/scripts/build.sh")
		r := f.blocked([]string{filepath.Join(f.root, "helper/scripts/build.sh")}, "构建 helper：darwin_arm64")
		equal(t, r.code, 42)
		contains(t, r.err, "darwin_arm64，退出码 42")
		if exists(filepath.Join(f.root, "helper/prebuilt")) {
			t.Fatal("failed build published artifacts")
		}
	case "test_shell_heartbeat_does_not_pollute_captured_data_or_leave_open_pipes":
		tick := filepath.Join(f.base, "tick")
		f.shell("sleep", "if [ \"$1\" = 10 ] && [ ! -e "+quote(tick)+" ]; then touch "+quote(tick)+"; exit 0; fi\nexec /bin/sleep 30")
		script := filepath.Join(f.base, "operation.sh")
		write(t, script, "#!/bin/bash\nset -Eeuo pipefail\nsource "+quote(filepath.Join(f.root, "scripts/task-output.sh"))+"\nsystem_sync_capture 'fixture phase' \"$1\" \"$2\" /bin/sh -c 'printf private-out; printf private-err >&2; touch \"$FEEDBACK_ENTERED\"; while [ ! -e \"$FEEDBACK_RELEASE\" ]; do /bin/sleep 0.01; done; exit 7'\n")
		output, errors := filepath.Join(f.base, "stdout-data"), filepath.Join(f.base, "stderr-data")
		r := f.blocked([]string{"/bin/bash", script, output, errors}, "仍在执行")
		equal(t, r.code, 7)
		equal(t, r.out, "")
		equal(t, read(t, output), "private-out")
		equal(t, read(t, errors), "private-err")
		excludes(t, r.err, "private-")
	case "test_fast_shell_steps_finish_in_strict_mode_without_waiting_for_a_tick":
		start := time.Now()
		r := good(t, f.run("/bin/bash", "-c", `set -Eeuo pipefail; source "$1"; value="$(system_sync_step fast printf native-data)"; printf '%s' "$value"`, "fixture", filepath.Join(f.root, "scripts/task-output.sh")))
		if time.Since(start) > 3*time.Second {
			t.Fatal("fast step waited for ticker")
		}
		equal(t, r.out, "native-data")
		contains(t, r.err, "[~] fast")
		excludes(t, r.err, "unbound variable")
	case "test_ticker_stops_sleep_when_interrupted_immediately_after_launch":
		tickerBoundary(f)
	default:
		t.Fatal("unported", name)
	}
}
func tickerBoundary(f *fixture) {
	t := f.t
	child, paused, resume := filepath.Join(f.base, "sleep-process.json"), filepath.Join(f.base, "ticker-paused"), filepath.Join(f.base, "ticker-resume")
	f.env["FEEDBACK_CHILD"] = child
	f.env["FEEDBACK_TICKER_PAUSED"] = paused
	f.env["FEEDBACK_TICKER_RESUME"] = resume
	f.mock("feedback", "sleep")
	script := `set -Eeuo pipefail
set -T
trap 'if [[ "$BASH_SUBSHELL" == 2 && ! -e "$FEEDBACK_TICKER_PAUSED" ]] && [[ -n "$(jobs -pr)" ]]; then
  : > "$FEEDBACK_TICKER_PAUSED"
  while [[ ! -e "$FEEDBACK_TICKER_RESUME" ]]; do /bin/sleep 0.01; done
fi' DEBUG
source "$1"
system_sync_step fixture /bin/sh -c 'touch "$FEEDBACK_ENTERED"; while [ ! -e "$FEEDBACK_RELEASE" ]; do /bin/sleep 0.01; done; printf native-data'`
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, "/bin/bash", "-c", script, "fixture", filepath.Join(f.root, "scripts/task-output.sh"))
	c.Dir = f.root
	c.Env = f.environment()
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr
	if e := startTracked(c); e != nil {
		t.Fatal(e)
	}
	defer func() {
		os.WriteFile(resume, nil, 0600)
		os.WriteFile(f.env["FEEDBACK_RELEASE"], nil, 0600)
		_ = syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
	}()
	var data struct{ PID, Parent int }
	for {
		if exists(child) && exists(paused) && exists(f.env["FEEDBACK_ENTERED"]) {
			b, _ := os.ReadFile(child)
			if json.Unmarshal(b, &data) == nil {
				break
			}
		}
		if ctx.Err() != nil {
			waitTracked(c)
			t.Fatal("ticker did not reach interruption boundary")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if e := syscall.Kill(data.Parent, syscall.SIGTERM); e != nil {
		t.Fatal(e)
	}
	os.WriteFile(f.env["FEEDBACK_RELEASE"], nil, 0600)
	if e := waitTracked(c); e != nil {
		t.Fatal(e, stderr.String())
	}
	equal(t, stdout.String(), "native-data")
	contains(t, stderr.String(), "[~] fixture")
}
