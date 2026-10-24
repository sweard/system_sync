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
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/pelletier/go-toml/v2"
	"github.com/sweard/system_sync/helper/dev/checkrun"
)

var repo = sourceRoot()

func sourceRoot() string {
	if p := os.Getenv("SYSTEM_SYNC_REPO_ROOT"); p != "" {
		return p
	}
	_, p, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(p), "../../.."))
}
func TestMain(m *testing.M) {
	if mode := os.Getenv("SYSTEM_SYNC_FIXTURE_MODE"); mode != "" {
		os.Exit(runMockWithLease(mode, os.Getenv("SYSTEM_SYNC_FIXTURE_COMMAND"), os.Args[1:]))
	}
	stop := watchInterrupts()
	code := m.Run()
	stop()
	cleanupChildren()
	os.Exit(code)
}

type coverageCase struct {
	ID        string   `json:"id"`
	Platforms []string `json:"platforms"`
	Serial    bool     `json:"serial"`
}

var handlers = map[string]func(*fixture, string){}

func TestIntegration(t *testing.T) {
	var catalog []coverageCase
	readJSON(t, filepath.Join(repo, "tests/coverage.json"), &catalog)
	profile := os.Getenv("SYSTEM_SYNC_TEST_PROFILE")
	if profile == "" {
		profile = "all"
	}
	selected := func(c coverageCase) bool {
		for _, p := range c.Platforms {
			if profile == "all" || p == "common" || p == profile || (p == "linux" && profile != "macos") {
				return true
			}
		}
		return false
	}
	var previous struct{ Tests []checkrun.Timing }
	if b, e := os.ReadFile(os.Getenv("SYSTEM_SYNC_TEST_WEIGHTS")); e == nil {
		_ = json.Unmarshal(b, &previous)
	}
	weights := map[string]float64{}
	for _, r := range previous.Tests {
		weights[r.ID] = r.Seconds
	}
	sort.SliceStable(catalog, func(i, j int) bool { return weights[catalog[i].ID] > weights[catalog[j].ID] })
	seen := map[string]bool{}
	for _, c := range catalog {
		if seen[c.ID] {
			t.Fatalf("duplicate legacy test: %s", c.ID)
		}
		seen[c.ID] = true
		if handlers[strings.Split(c.ID, ".")[0]] == nil {
			t.Fatalf("no Go replacement: %s", c.ID)
		}
	}
	if len(catalog) != 201 {
		t.Fatalf("coverage contract: want 201 legacy methods, got %d", len(catalog))
	}
	for _, serial := range []bool{false, true} {
		label := "parallel"
		if serial {
			label = "serial"
		}
		t.Run(label, func(t *testing.T) {
			for _, c := range catalog {
				c := c
				if c.Serial != serial || !selected(c) {
					continue
				}
				t.Run(c.ID, func(t *testing.T) {
					if !serial {
						t.Parallel()
					}
					f := newFixture(t)
					handlers[strings.Split(c.ID, ".")[0]](f, strings.Split(c.ID, ".")[2])
				})
			}
		})
	}
}

type fixture struct {
	t                     *testing.T
	base, root, home, bin string
	env                   map[string]string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, base: base, root: filepath.Join(base, "repo with spaces 中文"), home: filepath.Join(base, "home"), bin: filepath.Join(base, "bin"), env: map[string]string{}}
	for _, p := range []string{f.root, f.home, f.bin} {
		mkdir(t, p)
	}
	for _, k := range []string{"PATH", "TMPDIR", "LANG", "LC_ALL", "LC_CTYPE", "GOCACHE", "GOMODCACHE", "GOTOOLCHAIN", "GOPROXY", "GOSUMDB", "GOTELEMETRY", "GOENV", "CC", "SDKROOT"} {
		if v, ok := os.LookupEnv(k); ok {
			f.env[k] = v
		}
	}
	leaseDir := filepath.Join(base, "fixture-processes")
	mkdir(t, leaseDir)
	registerLeaseDir(leaseDir)
	t.Cleanup(func() { cleanupLeaseDir(leaseDir) })
	f.env["SYSTEM_SYNC_FIXTURE_LEASES"] = leaseDir
	f.env["HOME"] = f.home
	f.env["PATH"] = f.bin + ":" + os.Getenv("PATH")
	f.env["NO_COLOR"] = "1"
	for _, part := range []string{"CONFIG", "SYSTEM_CONFIG", "DATA", "STATE", "CACHE"} {
		f.env["MISE_"+part+"_DIR"] = filepath.Join(base, "mise-"+strings.ToLower(part))
	}
	f.env["MISE_CEILING_PATHS"] = base
	f.env["MISE_TRUSTED_CONFIG_PATHS"] = base + ":" + repo
	f.env["MISE_OFFLINE"] = "1"
	f.env["MISE_AUTO_INSTALL"] = "false"
	f.env["MISE_TASK_RUN_AUTO_INSTALL"] = "false"
	return f
}
func mkdir(t *testing.T, p string) {
	t.Helper()
	if e := os.MkdirAll(p, 0755); e != nil {
		t.Fatal(e)
	}
}
func write(t *testing.T, p, s string) {
	t.Helper()
	mkdir(t, filepath.Dir(p))
	if e := os.WriteFile(p, []byte(s), 0644); e != nil {
		t.Fatal(e)
	}
}
func read(t *testing.T, p string) string {
	t.Helper()
	b, e := os.ReadFile(p)
	if e != nil {
		t.Fatal(e)
	}
	return string(b)
}
func writeJSON(t *testing.T, p string, v any) {
	t.Helper()
	b, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		t.Fatal(e)
	}
	write(t, p, string(b)+"\n")
}
func readJSON(t *testing.T, p string, v any) {
	t.Helper()
	if e := json.Unmarshal([]byte(read(t, p)), v); e != nil {
		t.Fatal(e)
	}
}
func equal(t *testing.T, a, b any) {
	t.Helper()
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("got %#v\nwant %#v", a, b)
	}
}
func contains(t *testing.T, s, sub string) {
	t.Helper()
	if !strings.Contains(s, sub) {
		t.Fatalf("missing %q in:\n%s", sub, s)
	}
}
func excludes(t *testing.T, s, sub string) {
	t.Helper()
	if strings.Contains(s, sub) {
		t.Fatalf("unexpected %q in:\n%s", sub, s)
	}
}
func exists(p string) bool  { _, e := os.Lstat(p); return e == nil }
func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
func executable(t *testing.T, p, s string) {
	write(t, p, s)
	if e := os.Chmod(p, 0755); e != nil {
		t.Fatal(e)
	}
}
func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	i, e := os.Lstat(src)
	if e != nil {
		t.Fatal(e)
	}
	if i.Mode()&os.ModeSymlink != 0 {
		target, e := os.Readlink(src)
		if e != nil {
			t.Fatal(e)
		}
		mkdir(t, filepath.Dir(dst))
		if e = os.Symlink(target, dst); e != nil {
			t.Fatal(e)
		}
		return
	}
	if i.IsDir() {
		mkdir(t, dst)
		list, e := os.ReadDir(src)
		if e != nil {
			t.Fatal(e)
		}
		for _, x := range list {
			if strings.Contains(x.Name(), ".local") || x.Name() == "__pycache__" || x.Name() == "private.local" {
				continue
			}
			copyTree(t, filepath.Join(src, x.Name()), filepath.Join(dst, x.Name()))
		}
		return
	}
	write(t, dst, read(t, src))
	if e := os.Chmod(dst, i.Mode().Perm()); e != nil {
		t.Fatal(e)
	}
}
func (f *fixture) copy(paths ...string) {
	for _, p := range paths {
		copyTree(f.t, filepath.Join(repo, p), filepath.Join(f.root, p))
	}
}
func (f *fixture) helper() {
	current := strings.Fields(read(f.t, filepath.Join(repo, ".system-sync/helper/current")))
	if len(current) != 2 {
		f.t.Fatal("build helper first")
	}
	p := filepath.Join(".system-sync/helper", current[0], current[1])
	copyTree(f.t, filepath.Join(repo, p), filepath.Join(f.root, p))
	copyTree(f.t, filepath.Join(repo, ".system-sync/helper/current"), filepath.Join(f.root, ".system-sync/helper/current"))
	for _, s := range []string{"common.sh", "go-version.sh", "install.sh", "run.sh"} {
		copyTree(f.t, filepath.Join(repo, "helper/scripts", s), filepath.Join(f.root, "helper/scripts", s))
	}
	f.copy("helper/VERSION", "scripts/task-output.sh")
}
func (f *fixture) mock(mode string, names ...string) {
	exe, e := os.Executable()
	if e != nil {
		f.t.Fatal(e)
	}
	for _, name := range names {
		executable(f.t, filepath.Join(f.bin, name), "#!/bin/sh\nexport SYSTEM_SYNC_FIXTURE_MODE="+quote(mode)+"\nexport SYSTEM_SYNC_FIXTURE_COMMAND="+quote(name)+"\nexec "+quote(exe)+" \"$@\"\n")
	}
}
func (f *fixture) shell(name, body string) {
	executable(f.t, filepath.Join(f.bin, name), "#!/bin/sh\n"+body+"\n")
}
func (f *fixture) environment() []string {
	out := []string{}
	for k, v := range f.env {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}

type result struct {
	code     int
	out, err string
}

func (r result) all() string                 { return r.out + r.err }
func (f *fixture) run(args ...string) result { return f.input("", args...) }
func (f *fixture) input(input string, args ...string) result {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, args[0], args[1:]...)
	c.Dir = f.root
	c.Env = f.environment()
	c.Stdin = strings.NewReader(input)
	// Pipe-based commands must not inherit the caller's controlling terminal:
	// interactive zsh can otherwise stop waiting for foreground ownership.
	// Setsid also makes the child PID its process-group ID for cancellation.
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	c.Cancel = func() error { return syscall.Kill(-c.Process.Pid, syscall.SIGKILL) }
	var a, b bytes.Buffer
	c.Stdout = &a
	c.Stderr = &b
	e := runTracked(c)
	if ctx.Err() != nil {
		f.t.Fatalf("command timed out: %v\n%s%s", args, a.String(), b.String())
	}
	code := 0
	if e != nil {
		if x, ok := e.(*exec.ExitError); ok {
			code = x.ExitCode()
		} else {
			f.t.Fatal(e)
		}
	}
	return result{code, a.String(), b.String()}
}
func good(t *testing.T, r result) result {
	t.Helper()
	if r.code != 0 {
		t.Fatalf("exit %d:\n%s", r.code, r.all())
	}
	return r
}
func bad(t *testing.T, r result) result {
	t.Helper()
	if r.code == 0 {
		t.Fatalf("expected failure:\n%s", r.all())
	}
	return r
}
func (f *fixture) cli(args ...string) result {
	return f.run(append([]string{filepath.Join(f.root, "helper/scripts/run.sh")}, args...)...)
}
func (f *fixture) mise(args ...string) result {
	return f.run(append([]string{misePath(f.t)}, args...)...)
}
func misePath(t *testing.T) string {
	t.Helper()
	p, e := exec.LookPath("mise")
	if e != nil {
		t.Fatal("mise required")
	}
	return p
}
func goVersion(t *testing.T) string {
	var v struct{ Tools map[string]any }
	if e := toml.Unmarshal([]byte(read(t, filepath.Join(repo, "dotfiles/common/mise/config.toml"))), &v); e != nil {
		t.Fatal(e)
	}
	return v.Tools["go"].(string)
}
func (f *fixture) goConfig() {
	write(f.t, filepath.Join(f.root, "dotfiles/common/mise/config.toml"), "[tools]\ngo = "+quoteTOML(goVersion(f.t))+"\n")
}
func quoteTOML(s string) string { b, _ := json.Marshal(s); return string(b) }

type snapItem struct {
	Mode          os.FileMode
	Content, Link string
}

func snapshot(t *testing.T, p string) map[string]snapItem {
	t.Helper()
	out := map[string]snapItem{}
	if !exists(p) {
		return out
	}
	e := filepath.Walk(p, func(path string, i os.FileInfo, e error) error {
		if e != nil {
			return e
		}
		rel, _ := filepath.Rel(p, path)
		v := snapItem{Mode: i.Mode()}
		if i.Mode().IsRegular() {
			v.Content = read(t, path)
		} else if i.Mode()&os.ModeSymlink != 0 {
			v.Link, e = os.Readlink(path)
			if e != nil {
				return e
			}
		}
		out[rel] = v
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	return out
}
func lock(t *testing.T, root string) func() {
	t.Helper()
	p := filepath.Join(root, ".system-sync/operation.lock")
	mkdir(t, filepath.Dir(p))
	fd, e := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		t.Fatal(e)
	}
	if e = syscall.Flock(int(fd.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		t.Fatal(e)
	}
	return func() { syscall.Flock(int(fd.Fd()), syscall.LOCK_UN); fd.Close() }
}
func profiles() []string {
	p := os.Getenv("SYSTEM_SYNC_TEST_PROFILE")
	if p == "" || p == "all" {
		return []string{"arch", "debian", "fedora", "opensuse"}
	}
	if p == "macos" {
		return nil
	}
	return []string{p}
}
func nativePlatform() string {
	if runtime.GOOS == "darwin" {
		return "macos"
	}
	return "linux"
}
func (f *fixture) terminal(args []string, answers [][2]string) result {
	f.t.Helper()
	master, slave, e := openPTY()
	if e != nil {
		f.t.Fatal(e)
	}
	defer master.Close()
	defer slave.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, args[0], args[1:]...)
	c.Dir = f.root
	c.Env = f.environment()
	c.Stdin = slave
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Cancel = func() error { return syscall.Kill(-c.Process.Pid, syscall.SIGKILL) }
	w := &terminalWriter{input: master, answers: answers}
	c.Stdout = w
	c.Stderr = w
	e = startTracked(c)
	slave.Close()
	if e != nil {
		f.t.Fatal(e)
	}
	e = waitTracked(c)
	if ctx.Err() != nil {
		f.t.Fatalf("terminal timeout: %v\n%s", args, w.buf.String())
	}
	code := 0
	if e != nil {
		if x, ok := e.(*exec.ExitError); ok {
			code = x.ExitCode()
		} else {
			f.t.Fatal(e)
		}
	}
	if w.next != len(answers) {
		f.t.Fatalf("missing terminal answers %d/%d:\n%s", w.next, len(answers), w.buf.String())
	}
	return result{code, w.buf.String(), ""}
}

type terminalWriter struct {
	mu           sync.Mutex
	buf          bytes.Buffer
	input        io.Writer
	answers      [][2]string
	next, cursor int
}

func (w *terminalWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf.Write(p)
	for w.next < len(w.answers) {
		a := w.answers[w.next]
		i := strings.Index(w.buf.String()[w.cursor:], a[0])
		if i < 0 {
			break
		}
		w.cursor += i + len(a[0])
		fmt.Fprintln(w.input, a[1])
		w.next++
	}
	return len(p), nil
}

// live requires a status message before releasing a deliberately blocked child.
func (f *fixture) live(args []string, marker string, release func(), deadline time.Duration) result {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()
	c := exec.CommandContext(ctx, args[0], args[1:]...)
	c.Dir = f.root
	c.Env = f.environment()
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Cancel = func() error { return syscall.Kill(-c.Process.Pid, syscall.SIGKILL) }
	w := &liveWriter{marker: marker, release: release}
	c.Stdout = w
	c.Stderr = w
	e := runTracked(c)
	if ctx.Err() != nil {
		f.t.Fatalf("no timely feedback: %v\n%s", args, w.buf.String())
	}
	if !w.released {
		f.t.Fatalf("status did not arrive before completion: %s\n%s", marker, w.buf.String())
	}
	code := 0
	if e != nil {
		if x, ok := e.(*exec.ExitError); ok {
			code = x.ExitCode()
		} else {
			f.t.Fatal(e)
		}
	}
	return result{code, w.buf.String(), ""}
}

type liveWriter struct {
	mu       sync.Mutex
	buf      bytes.Buffer
	marker   string
	release  func()
	released bool
}

func (w *liveWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf.Write(p)
	if !w.released && strings.Contains(w.buf.String(), w.marker) {
		w.released = true
		w.release()
	}
	return len(p), nil
}

// ttyOutput is used only when stdout's terminal status is the behavior under test.
func (f *fixture) ttyOutput(args []string, answers ...[2]string) result {
	return f.ptyOutput(args, false, answers...)
}

func (f *fixture) controllingTerminal(args []string, answers ...[2]string) result {
	return f.ptyOutput(args, true, answers...)
}

func (f *fixture) ptyOutput(args []string, controlling bool, answers ...[2]string) result {
	t := f.t
	master, slave, e := openPTY()
	if e != nil {
		t.Fatal(e)
	}
	defer master.Close()
	defer slave.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, args[0], args[1:]...)
	c.Dir = f.root
	c.Env = f.environment()
	c.Stdin = slave
	c.Stdout = slave
	c.Stderr = slave
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if controlling {
		c.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	}
	c.Cancel = func() error { return syscall.Kill(-c.Process.Pid, syscall.SIGKILL) }
	if e := startTracked(c); e != nil {
		t.Fatal(e)
	}
	slave.Close()
	done := make(chan string, 1)
	w := &terminalWriter{input: master, answers: answers}
	go func() { _, _ = io.Copy(w, master); done <- w.buf.String() }()
	e = waitTracked(c)
	var output string
	select {
	case output = <-done:
	case <-time.After(time.Second):
		master.Close()
		output = <-done
	}
	if ctx.Err() != nil {
		t.Fatal("terminal output timed out", output)
	}
	if w.next != len(answers) {
		t.Fatalf("missing terminal answers %d/%d:\n%s", w.next, len(answers), output)
	}
	code := 0
	if e != nil {
		if x, ok := e.(*exec.ExitError); ok {
			code = x.ExitCode()
		} else {
			t.Fatal(e)
		}
	}
	return result{code, output, ""}
}

// Stamps are used where the old suite also required a no-op to preserve mtime.
type fileStamp struct {
	Tree     map[string]snapItem
	Modified int64
}

func stampedSnapshot(t *testing.T, path string) fileStamp {
	t.Helper()
	info, e := os.Stat(path)
	if e != nil {
		t.Fatal(e)
	}
	return fileStamp{snapshot(t, path), info.ModTime().UnixNano()}
}
