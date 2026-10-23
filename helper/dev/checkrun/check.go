// Package checkrun coordinates development validation without a scripting runtime.
package checkrun

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

type Case struct {
	ID        string   `json:"id"`
	Platforms []string `json:"platforms"`
	Serial    bool     `json:"serial"`
}
type Timing struct {
	ID      string  `json:"id"`
	Seconds float64 `json:"seconds"`
	Outcome string  `json:"outcome"`
}
type Stage struct {
	Name     string  `json:"name"`
	Seconds  float64 `json:"seconds"`
	ExitCode int     `json:"exit_code"`
}
type Report struct {
	Schema        int      `json:"schema"`
	Scope         string   `json:"scope"`
	Profile       string   `json:"profile"`
	Target        string   `json:"target"`
	Jobs          int      `json:"jobs"`
	Stages        []Stage  `json:"stages"`
	Tests         []Timing `json:"tests"`
	TestsSelected int      `json:"tests_selected"`
	TestsRun      int      `json:"tests_run"`
	TestsExcluded []string `json:"tests_excluded"`
	TestsSerial   []string `json:"tests_serial"`
	Logs          string   `json:"logs"`
	Seconds       float64  `json:"seconds"`
	ExitCode      int      `json:"exit_code"`
}
type Config struct {
	Root, ReportPath string
	All              bool
	Jobs             int
	Out, Err         io.Writer
	Stages           []Command
}
type Command struct {
	Name        string
	Args        []string
	Integration bool
}

func Target(goos, arch string) (string, error) {
	if (goos != "darwin" && goos != "linux") || (arch != "arm64" && arch != "amd64") {
		return "", errors.New("检查仅支持 macOS/Linux 的 ARM64/x86_64")
	}
	return goos + "_" + arch, nil
}
func Profile(ctx context.Context, root, goos string) (string, error) {
	if goos == "darwin" {
		return "macos", nil
	}
	if goos != "linux" {
		return "", errors.New("检查仅支持 macOS/Linux")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", filepath.Join(root, "scripts/detect-linux-profile.sh"))
	cmd.Dir = root
	out, e := cmd.Output()
	p := strings.TrimSpace(string(out))
	if e != nil || !validLinux(p) {
		return "", errors.New("无法识别当前 Linux profile；请运行 scripts/detect-linux-profile.sh 核对")
	}
	return p, nil
}
func validLinux(p string) bool {
	return p == "arch" || p == "debian" || p == "fedora" || p == "opensuse"
}
func ValidateCases(cases []Case) error {
	seen := map[string]bool{}
	for _, c := range cases {
		if c.ID == "" || seen[c.ID] {
			return fmt.Errorf("空或重复测试标识：%s", c.ID)
		}
		seen[c.ID] = true
		if len(c.Platforms) == 0 {
			return fmt.Errorf("测试缺少平台标记：%s", c.ID)
		}
		for _, p := range c.Platforms {
			if p != "common" && p != "all" && p != "linux" && p != "macos" && !validLinux(p) {
				return fmt.Errorf("未知测试平台标记：%s", p)
			}
		}
	}
	return nil
}

func Matches(c Case, p string) bool {
	for _, n := range c.Platforms {
		if p == "all" || n == "common" || n == p || (n == "linux" && validLinux(p)) {
			return true
		}
	}
	return false
}
func Save(path string, v any) error {
	b, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(path), 0755); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".report-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if _, e = f.Write(append(b, '\n')); e != nil {
		f.Close()
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	return os.Rename(f.Name(), path)
}
func safeDir(p string) error {
	i, e := os.Lstat(p)
	if e != nil && !os.IsNotExist(e) {
		return e
	}
	if e == nil && i.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("检查结果目录不能是符号链接：%s", p)
	}
	return nil
}

type lockedWriter struct {
	mu *sync.Mutex
	w  io.Writer
}

func (w *lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.w.Write(p)
}
func Execute(ctx context.Context, root string, args []string, env []string, out, errout io.Writer) int {
	if len(args) == 0 {
		return 1
	}
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = root
	cmd.Env = env
	cmd.Stdout = out
	cmd.Stderr = errout
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	finished := make(chan struct{})
	defer close(finished)
	cmd.Cancel = func() error {
		e := syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		go func(pid int) {
			select {
			case <-finished:
				return
			case <-time.After(2 * time.Second):
				_ = syscall.Kill(-pid, syscall.SIGKILL)
			}
		}(cmd.Process.Pid)
		return e
	}
	cmd.WaitDelay = 3 * time.Second
	e := cmd.Run()
	if ctx.Err() != nil {
		return 130
	}
	if e == nil {
		return 0
	}
	if x, ok := e.(*exec.ExitError); ok {
		return x.ExitCode()
	}
	fmt.Fprintf(errout, "[x] 无法执行 %s：%v\n", args[0], e)
	return 1
}
func environment(extra map[string]string) []string {
	env := []string{}
	for _, s := range os.Environ() {
		k, _, _ := strings.Cut(s, "=")
		if _, ok := extra[k]; !ok {
			env = append(env, s)
		}
	}
	for k, v := range extra {
		env = append(env, k+"="+v)
	}
	return env
}

type goEvent struct {
	Action, Test, Output string
	Elapsed              float64
}

func Collect(r io.Reader, expected map[string]bool, log io.Writer) ([]Timing, error) {
	return collect(r, expected, log, nil)
}
func collect(r io.Reader, expected map[string]bool, log io.Writer, progress func(int)) ([]Timing, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 4096), 4<<20)
	records := map[string]Timing{}
	for scanner.Scan() {
		line := scanner.Bytes()
		_, _ = log.Write(append(append([]byte{}, line...), '\n'))
		var e goEvent
		if x := json.Unmarshal(line, &e); x != nil {
			return nil, fmt.Errorf("无法解析 Go 测试事件：%w", x)
		}
		parts := strings.Split(e.Test, "/")
		if len(parts) != 3 || parts[0] != "TestIntegration" {
			continue
		}
		id := parts[2]
		if !expected[id] {
			return nil, fmt.Errorf("非预期测试结果：%s", id)
		}
		if e.Action != "pass" && e.Action != "fail" && e.Action != "skip" {
			continue
		}
		if _, ok := records[id]; ok {
			return nil, fmt.Errorf("重复测试结果：%s", id)
		}
		if e.Elapsed < 0 {
			return nil, fmt.Errorf("测试耗时非法：%s", id)
		}
		outcome := map[string]string{"pass": "passed", "fail": "failed", "skip": "skipped"}[e.Action]
		records[id] = Timing{id, e.Elapsed, outcome}
		if progress != nil {
			progress(len(records))
		}
	}
	out := []Timing{}
	for _, r := range records {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if e := scanner.Err(); e != nil {
		return out, e
	}
	if len(out) != len(expected) {
		return out, fmt.Errorf("测试结果不完整：%d/%d", len(out), len(expected))
	}
	for _, r := range out {
		if r.Outcome != "passed" {
			return out, fmt.Errorf("测试未通过：%s（%s）", r.ID, r.Outcome)
		}
	}
	return out, nil
}
func Run(ctx context.Context, c Config) int {
	if c.Out == nil {
		c.Out = os.Stdout
	}
	if c.Err == nil {
		c.Err = os.Stderr
	}
	var outputLock sync.Mutex
	out := &lockedWriter{mu: &outputLock, w: c.Out}
	errout := &lockedWriter{mu: &outputLock, w: c.Err}
	if c.Jobs < 1 {
		fmt.Fprintln(errout, "[x] --jobs 必须大于零")
		return 2
	}
	root, e := filepath.Abs(c.Root)
	if e != nil {
		return 2
	}
	for _, p := range []string{filepath.Join(root, ".system-sync"), filepath.Join(root, ".system-sync/check")} {
		if e := safeDir(p); e != nil {
			fmt.Fprintln(errout, e)
			return 2
		}
	}
	path := c.ReportPath
	if path == "" {
		path = ".system-sync/check/latest.json"
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	if e := os.MkdirAll(filepath.Dir(path), 0755); e != nil {
		fmt.Fprintln(errout, e)
		return 2
	}
	target, e := Target(runtime.GOOS, runtime.GOARCH)
	if e != nil {
		fmt.Fprintln(errout, e)
		return 2
	}
	profile := "all"
	scope := "all"
	if !c.All {
		profile, e = Profile(ctx, root, runtime.GOOS)
		scope = "current"
	}
	if e != nil {
		fmt.Fprintln(errout, e)
		return 2
	}
	report := Report{Schema: 2, Scope: scope, Profile: profile, Target: target, Jobs: c.Jobs, Tests: []Timing{}, Stages: []Stage{}, TestsExcluded: []string{}, TestsSerial: []string{}}
	var catalog []Case
	b, e := os.ReadFile(filepath.Join(root, "tests/coverage.json"))
	if e == nil {
		e = json.Unmarshal(b, &catalog)
	}
	if e != nil {
		fmt.Fprintln(errout, e)
		return 2
	}
	if e := ValidateCases(catalog); e != nil {
		fmt.Fprintln(errout, e)
		return 2
	}
	expected := map[string]bool{}
	for _, test := range catalog {
		if Matches(test, profile) {
			if expected[test.ID] {
				fmt.Fprintln(errout, "重复测试标识", test.ID)
				return 2
			}
			expected[test.ID] = true
			if test.Serial {
				report.TestsSerial = append(report.TestsSerial, test.ID)
			}
		} else {
			report.TestsExcluded = append(report.TestsExcluded, test.ID)
		}
	}
	report.TestsSelected = len(expected)
	if len(expected) == 0 {
		fmt.Fprintln(errout, "测试清单为空")
		return 2
	}
	logs, e := os.MkdirTemp(filepath.Dir(path), strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))+"-")
	if e != nil {
		fmt.Fprintln(errout, e)
		return 2
	}
	report.Logs = logs
	previous := filepath.Join(logs, "previous.json")
	if b, e := os.ReadFile(path); e == nil {
		_ = os.WriteFile(previous, b, 0600)
	}
	verify := []string{filepath.Join(root, "helper/scripts/verify.sh")}
	if !c.All {
		verify = append(verify, "--current-platform")
	}
	stages := c.Stages
	if stages == nil {
		stages = []Command{{"预编译包与构建输入", verify, false}, {"本机 helper 构建与启用", []string{filepath.Join(root, "helper/scripts/build.sh")}, false}, {"开发环境预检", []string{filepath.Join(root, "helper/scripts/run.sh"), "runtime", "--check"}, false}, {"mise task 定义", []string{"mise", "tasks", "validate"}, false}, {"Go 测试与 race", []string{filepath.Join(root, "helper/scripts/test.sh")}, false}, {"Go 集成测试", []string{"go", "test", "-mod=vendor", "-json", "-count=1", "-tags=integration", fmt.Sprintf("-parallel=%d", c.Jobs), "-timeout=10m", "./dev/integration"}, true}}
	}
	started := time.Now()
	code := 0
	for i, stage := range stages {
		if ctx.Err() != nil {
			code = 130
			break
		}
		fmt.Fprintf(out, "[~] 开始检查：%s\n", stage.Name)
		begin := time.Now()
		stop := make(chan struct{})
		var completed atomic.Int64
		go func() {
			ticker := time.NewTicker(10 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-stop:
					return
				case <-ticker.C:
					if stage.Integration {
						fmt.Fprintf(out, "[~] 集成测试已完成 %d/%d 项，已耗时 %.1fs\n", completed.Load(), len(expected), time.Since(begin).Seconds())
					} else {
						fmt.Fprintf(out, "[~] %s仍在执行，已耗时 %.1fs\n", stage.Name, time.Since(begin).Seconds())
					}
				}
			}
		}()
		log, e := os.Create(filepath.Join(logs, fmt.Sprintf("stage-%02d.log", i+1)))
		if e != nil {
			close(stop)
			fmt.Fprintln(errout, e)
			code = 1
			break
		}
		if stage.Integration {
			fmt.Fprintf(out, "[~] 集成测试：选择 %d 项，其他平台 %d 项；最多 %d 并发，时序测试随后串行\n", len(expected), len(report.TestsExcluded), c.Jobs)
			pr, pw := io.Pipe()
			done := make(chan int, 1)
			env := environment(map[string]string{"SYSTEM_SYNC_REPO_ROOT": root, "SYSTEM_SYNC_TEST_PROFILE": profile, "SYSTEM_SYNC_TEST_WEIGHTS": previous, "GOTOOLCHAIN": "local", "GOPROXY": "off", "GOSUMDB": "off", "GOTELEMETRY": "off", "CGO_ENABLED": "0"})
			go func() {
				done <- Execute(ctx, filepath.Join(root, "helper"), stage.Args, env, pw, io.MultiWriter(errout, log))
				pw.Close()
			}()
			records, err := collect(pr, expected, log, func(n int) { completed.Store(int64(n)) })
			pr.Close()
			code = <-done
			report.Tests = records
			report.TestsRun = len(records)
			if err != nil {
				fmt.Fprintln(errout, "[x]", err)
				if code == 0 {
					code = 1
				}
			}
			if code != 0 {
				fmt.Fprintf(errout, "[x] 集成测试失败；日志：%s\n", log.Name())
				if b, e := os.ReadFile(log.Name()); e == nil {
					fmt.Fprintln(errout, string(b))
				}
			}
		} else {
			code = Execute(ctx, root, stage.Args, os.Environ(), io.MultiWriter(out, log), io.MultiWriter(errout, log))
		}
		log.Close()
		close(stop)
		elapsed := time.Since(begin).Seconds()
		report.Stages = append(report.Stages, Stage{stage.Name, elapsed, code})
		mark := "+"
		if code != 0 {
			mark = "x"
		}
		fmt.Fprintf(out, "[%s] %s：%.2fs（退出码 %d）\n", mark, stage.Name, elapsed, code)
		if code != 0 {
			break
		}
	}
	report.Seconds = time.Since(started).Seconds()
	report.ExitCode = code
	if e := Save(path, report); e != nil {
		fmt.Fprintln(errout, "[x] 无法保存检查报告：", e)
		if code == 0 {
			code = 1
		}
	}
	fmt.Fprintln(out, "\n[~] 检查阶段耗时")
	for _, s := range report.Stages {
		fmt.Fprintf(out, "  %8.2fs  %s\n", s.Seconds, s.Name)
	}
	slow := append([]Timing{}, report.Tests...)
	sort.Slice(slow, func(i, j int) bool { return slow[i].Seconds > slow[j].Seconds })
	fmt.Fprintln(out, "[~] 最慢的 10 项集成测试")
	for i, r := range slow {
		if i >= 10 {
			break
		}
		fmt.Fprintf(out, "  %8.2fs  %s\n", r.Seconds, r.ID)
	}
	fmt.Fprintf(out, "[~] 总耗时 %.2fs；报告：%s\n", report.Seconds, path)
	return code
}

// Groups preserves complete cases while balancing measured costs for parallel work.
func Groups(cases []Case, jobs int, previous []Timing) [][]Case {
	if len(cases) == 0 || jobs < 1 {
		return nil
	}
	if jobs == 1 {
		return [][]Case{append([]Case{}, cases...)}
	}
	jobs = min(jobs, len(cases))
	groups := make([][]Case, jobs)
	loads := make([]float64, jobs)
	weights := map[string]float64{}
	for _, v := range previous {
		if v.Seconds >= 0 && v.Seconds < 1e12 {
			weights[v.ID] = v.Seconds
		}
	}
	ordered := append([]Case{}, cases...)
	weight := func(c Case) float64 {
		if v, ok := weights[c.ID]; ok {
			return v
		}
		return 1
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		a, b := weight(ordered[i]), weight(ordered[j])
		if a == b {
			return ordered[i].ID < ordered[j].ID
		}
		return a > b
	})
	for _, c := range ordered {
		smallest := 0
		for j := 1; j < jobs; j++ {
			if loads[j] < loads[smallest] {
				smallest = j
			}
		}
		groups[smallest] = append(groups[smallest], c)
		loads[smallest] += weight(c)
	}
	return groups
}
