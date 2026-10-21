package audit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sweard/system_sync/internal/query"
)

type fakeRunner struct {
	tools, packages, policy string
	config                  any
	paths                   map[string]string
	failures                map[string]bool
	calls                   [][]string
	mu                      sync.Mutex
	hook                    func([]string)
}

func packageStatus(required []string, entries map[string][]string) string {
	var output strings.Builder
	names := append([]string{}, required...)
	sort.Strings(names)
	for _, title := range names {
		items := entries[title]
		fmt.Fprintf(&output, "%s（%d）\n", title, len(items))
		if len(items) == 0 {
			output.WriteString("  （无）\n")
		}
		for _, item := range items {
			output.WriteString("  - " + item + "\n")
		}
	}
	return output.String()
}

func (r *fakeRunner) Run(command []string, environment map[string]string) (string, error) {
	if r.hook != nil {
		r.hook(command)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, append([]string{}, command...))
	key := strings.Join(command, " ")
	if r.failures[key] {
		return "", query.Error("模拟查询失败")
	}
	switch {
	case key == "mise ls --current --json":
		return r.tools, nil
	case key == "mise bootstrap dotfiles status --json":
		data, _ := json.Marshal(r.config)
		return string(data), nil
	case strings.HasPrefix(key, "mise which "):
		return r.paths[command[2]] + "\n", nil
	case command[len(command)-1] == "--status":
		return r.packages, nil
	case len(command) > 3 && command[1] == "--noprofile":
		if _, present := environment["PRIVATE_API_TOKEN"]; present {
			return "", query.Error("private variable forwarded to policy")
		}
		return r.policy, nil
	}
	return "", query.Error("unexpected fixture query")
}

func fixture(t *testing.T) (string, string, map[string]string, *fakeRunner) {
	t.Helper()
	base := t.TempDir()
	root, home := filepath.Join(base, "project"), filepath.Join(base, "home")
	os.MkdirAll(filepath.Join(root, "dotfiles/common/shell"), 0755)
	os.Mkdir(home, 0755)
	os.WriteFile(filepath.Join(root, "dotfiles/common/shell/path.sh"), []byte("# fixture\n"), 0644)
	env := map[string]string{"HOME": home, "PATH": "/usr/bin:/bin", "PRIVATE_API_TOKEN": "fixture-private-token"}
	runner := &fakeRunner{tools: "{}", packages: packageStatus(PackageRequired[0], nil), policy: "/usr/bin:/bin:/usr/sbin:/sbin", config: map[string]any{"files": []any{}, "edits": []any{}}, paths: map[string]string{}, failures: map[string]bool{}}
	return root, home, env, runner
}

func executableFixture(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 99\n"), 0755); err != nil {
		t.Fatal(err)
	}
	return path
}

func declareTool(t *testing.T, runner *fakeRunner, root, home, tool, command string, installed bool) string {
	t.Helper()
	dir := filepath.Join(home, ".local/share/mise/installs", tool, "1.0.0")
	path := executableFixture(t, filepath.Join(dir, "bin", command))
	data := map[string]any{}
	json.Unmarshal([]byte(runner.tools), &data)
	data[tool] = []map[string]any{{"version": "1.0.0", "installed": installed, "install_path": dir, "source": map[string]string{"path": filepath.Join(root, "mise.toml"), "type": "mise.toml"}}}
	encoded, _ := json.Marshal(data)
	runner.tools = string(encoded)
	runner.paths[command] = path
	return path
}

func hasCode(report Report, section, code string) bool {
	for _, item := range report.Sections[section] {
		if item["code"] == code {
			return true
		}
	}
	return false
}

func TestMissingToolsDoNotInstallAnything(t *testing.T) {
	root, home, env, runner := fixture(t)
	declareTool(t, runner, root, home, "node", "node", false)
	report := New(root, env, runner).Execute([]string{"tools"})
	if report.Summary["drift"] != 1 || !hasCode(report, "tools", "tool_missing") || len(runner.calls) != 1 || strings.Join(runner.calls[0], " ") != "mise ls --current --json" {
		t.Fatalf("%+v %v", report, runner.calls)
	}
}

func TestShadowedCommandsAndOtherInstallersRemainCandidates(t *testing.T) {
	root, home, env, runner := fixture(t)
	selected := declareTool(t, runner, root, home, "node", "node", true)
	shadow := executableFixture(t, filepath.Join(home, ".local/bin/node"))
	executableFixture(t, filepath.Join(home, ".nvm/versions/node/v0.1.0/bin/node"))
	env["PATH"] = filepath.Dir(shadow) + ":" + filepath.Dir(selected) + ":/usr/bin:/bin"
	report := New(root, env, runner).Execute([]string{"tools", "path"})
	if !hasCode(report, "path", "command_shadowed") || !hasCode(report, "tools", "external_installations") {
		t.Fatalf("%+v", report)
	}
}

func TestRustShimsAndLinkedInstallationsAreRetained(t *testing.T) {
	root, home, env, runner := fixture(t)
	selected := declareTool(t, runner, root, home, "rust", "rustc", true)
	cargo := executableFixture(t, filepath.Join(home, ".cargo/bin/rustc"))
	os.Remove(selected)
	os.Symlink(cargo, selected)
	shim := executableFixture(t, filepath.Join(home, ".local/share/mise/shims/rustc"))
	env["PATH"] = filepath.Dir(shim) + ":" + filepath.Dir(cargo) + ":/usr/bin:/bin"
	runner.policy = filepath.Dir(cargo) + ":/usr/bin:/bin"
	report := New(root, env, runner).Execute([]string{"path"})
	if !hasCode(report, "path", "command_selected") || hasCode(report, "path", "command_shadowed") || hasCode(report, "path", "alternative_commands") {
		t.Fatalf("%+v", report)
	}
	root, home, env, runner = fixture(t)
	original := executableFixture(t, filepath.Join(home, ".nvm/versions/node/v1.0.0/bin/node"))
	dir := filepath.Join(home, ".local/share/mise/installs/node/1.0.0")
	os.MkdirAll(filepath.Dir(dir), 0755)
	os.Symlink(filepath.Dir(filepath.Dir(original)), dir)
	runner.tools = fmt.Sprintf(`{"node":[{"version":"1.0.0","installed":true,"install_path":%q}]}`, dir)
	report = New(root, env, runner).Execute([]string{"tools"})
	if !hasCode(report, "tools", "linked_install") || hasCode(report, "tools", "external_installations") {
		t.Fatalf("%+v", report)
	}
}

func TestVirtualEnvironmentAndUnsafeCallerPATH(t *testing.T) {
	root, home, env, runner := fixture(t)
	selected := declareTool(t, runner, root, home, "python", "python3", true)
	virtual := executableFixture(t, filepath.Join(root, ".venv/bin/python3"))
	env["VIRTUAL_ENV"] = filepath.Dir(filepath.Dir(virtual))
	env["PATH"] = filepath.Dir(virtual) + ":" + filepath.Dir(selected) + ":/usr/bin:/bin"
	report := New(root, env, runner).Execute([]string{"path"})
	if !hasCode(report, "path", "virtual_environment") || hasCode(report, "path", "command_shadowed") {
		t.Fatalf("%+v", report)
	}
	env["SYSTEM_SYNC_AUDIT_CALLER_PATH"] = home + "::relative:" + filepath.Join(home, "absent") + ":" + home
	report = New(root, env, runner).Execute([]string{"path"})
	for _, code := range []string{"unsafe_path", "missing_directory", "duplicate_path", "command_missing"} {
		if !hasCode(report, "path", code) {
			t.Fatalf("missing %s: %+v", code, report)
		}
	}
	if report.Context["path_source"] != "caller" {
		t.Fatal(report.Context)
	}
}

func TestPolicyFlutterAndHostPaths(t *testing.T) {
	root, home, env, runner := fixture(t)
	flutter := executableFixture(t, filepath.Join(home, "development/flutter/bin/flutter"))
	runner.policy = filepath.Dir(flutter) + ":/usr/bin:/bin"
	env["PATH"] = filepath.Join(home, ".codex/tmp/ephemeral/bin") + ":/pkg/env/global/bin:" + filepath.Join(home, "retired-tool/bin")
	report := New(root, env, runner).Execute([]string{"path"})
	if !hasCode(report, "path", "policy_path_missing") || !hasCode(report, "path", "retained_flutter") {
		t.Fatalf("%+v", report)
	}
	kinds := []any{}
	for _, item := range report.Sections["path"] {
		if item["code"] == "missing_directory" {
			kinds = append(kinds, item["kind"])
		}
	}
	if !reflect.DeepEqual(kinds, []any{"local", "local", "drift"}) {
		t.Fatal(kinds)
	}
}

func TestConfigReportsPathsWithoutReadingPrivateValues(t *testing.T) {
	root, home, env, runner := fixture(t)
	for _, name := range []string{".config/mise/secrets.env", ".gitconfig", "private-application/token.json"} {
		p := filepath.Join(home, name)
		os.MkdirAll(filepath.Dir(p), 0755)
		os.WriteFile(p, []byte("private-canary"), 0000)
	}
	runner.config = map[string]any{"files": []any{map[string]any{"target": "~/.zshrc", "state": "missing"}}, "edits": []any{map[string]any{"path": "~/.codex/config.toml", "state": "applied", "edit": map[string]string{"value": "hidden-merge-value"}}}}
	report := New(root, env, runner).Execute([]string{"config"})
	encoded, _ := json.Marshal(report)
	if strings.Contains(string(encoded), "private-canary") || strings.Contains(string(encoded), "hidden-merge-value") || strings.Contains(string(encoded), "private-application") || report.Summary["drift"] != 1 || !hasCode(report, "config", "unmanaged_config") || !hasCode(report, "config", "private_local") {
		t.Fatalf("%s", encoded)
	}
}

func TestUnknownConfigAndToolStatesFailClosed(t *testing.T) {
	root, _, env, runner := fixture(t)
	for _, state := range []any{"missing", "differs", "source_missing", "would_remove", "future_state", []any{}} {
		runner.config = map[string]any{"files": []any{map[string]any{"target": "~/.zshrc", "state": state}}, "edits": []any{}}
		report := New(root, env, runner).Execute([]string{"config"})
		if report.Summary["drift"]+report.Summary["error"] != 1 {
			t.Fatalf("%+v", report)
		}
	}
	runner.tools = "[]"
	runner.config = map[string]any{"future": []any{}}
	report := New(root, env, runner).Execute([]string{"tools", "config"})
	if report.Summary["error"] != 2 || ExitCode(report, true) != 2 {
		t.Fatalf("%+v", report)
	}
}

func TestAllPlatformPackageReportsAndVersionDrift(t *testing.T) {
	root, _, env, runner := fixture(t)
	for _, required := range PackageRequired {
		runner.packages = packageStatus(required, nil)
		report := New(root, env, runner).Execute([]string{"packages"})
		if report.Summary["error"] != 0 || report.Summary["drift"] != 0 {
			t.Fatalf("%+v", report)
		}
	}
	entries := map[string][]string{"mise 最终生效声明": {"pacman:git [version_mismatch]"}, "声明但尚未以同名具体包安装": {"missing-tool"}, "当前显式安装包中未声明、但受保护而保留": {"kernel"}, "apply 时会从 explicit 降级为 dependency 的未声明包": {"old-tool"}}
	runner.packages = packageStatus(PackageRequired[1], entries)
	report := New(root, env, runner).Execute([]string{"packages"})
	if report.Summary["drift"] != 2 || report.Summary["candidate"] != 1 || report.Summary["local"] != 1 || !hasCode(report, "packages", "package_version") {
		t.Fatalf("%+v", report)
	}
	for _, output := range []string{"", "future format", "声明的根包（1）\n  （无）\n"} {
		runner.packages = output
		report = New(root, env, runner).Execute([]string{"packages"})
		if report.Summary["error"] != 1 {
			t.Fatalf("%+v", report)
		}
	}
}

func TestIndependentCollectorsOverlapAndShareFailedToolSnapshot(t *testing.T) {
	root, _, env, runner := fixture(t)
	arrived := make(chan struct{}, 3)
	release := make(chan struct{})
	var ready sync.Once
	runner.hook = func(command []string) {
		key := strings.Join(command, " ")
		if key == "mise ls --current --json" || key == "mise bootstrap dotfiles status --json" || strings.HasSuffix(key, "system-sync.sh --status") {
			arrived <- struct{}{}
			select {
			case <-release:
			case <-time.After(5 * time.Second):
				panic("independent collectors did not overlap")
			}
		}
	}
	go func() {
		for i := 0; i < 3; i++ {
			<-arrived
		}
		ready.Do(func() { close(release) })
	}()
	report := New(root, env, runner).Execute(Sections)
	if report.Summary["error"] != 0 {
		t.Fatalf("%+v", report)
	}
	count := 0
	for _, call := range runner.calls {
		if strings.Join(call, " ") == "mise ls --current --json" {
			count++
		}
	}
	if count != 1 {
		t.Fatal(count)
	}
	runner.hook = nil
	runner.calls = nil
	runner.failures["mise ls --current --json"] = true
	report = New(root, env, runner).Execute(Sections)
	if report.Summary["error"] != 2 || len(report.Sections["packages"]) == 0 {
		t.Fatalf("%+v", report)
	}
	count = 0
	for _, call := range runner.calls {
		if strings.Join(call, " ") == "mise ls --current --json" {
			count++
		}
	}
	if count != 1 {
		t.Fatal(count)
	}
	delete(runner.failures, "mise ls --current --json")
	if New(root, env, runner).Execute(Sections).Summary["error"] != 0 {
		t.Fatal("failure was cached across audits")
	}
}

func TestCommandBatchesDeduplicateOverlapAndStopAfterFailure(t *testing.T) {
	root, home, env, runner := fixture(t)
	ordered := []string{}
	for _, pair := range [][2]string{{"node", "node"}, {"python", "python3"}, {"ruby", "ruby"}, {"java", "java"}, {"uv", "uv"}, {"go", "go"}} {
		path := declareTool(t, runner, root, home, pair[0], pair[1], true)
		ordered = append(ordered, fmt.Sprintf(`%q:[{"version":"1.0.0","installed":true,"install_path":%q}]`, pair[0], filepath.Dir(filepath.Dir(path))))
		env["PATH"] = filepath.Dir(path) + ":" + env["PATH"]
	}
	runner.tools = "{" + strings.Join(ordered, ",") + "}"
	runner.failures["mise which python3"] = true
	arrived := make(chan struct{}, 4)
	release := make(chan struct{})
	runner.hook = func(command []string) {
		if len(command) > 1 && command[0] == "mise" && command[1] == "which" {
			arrived <- struct{}{}
			select {
			case <-release:
			case <-time.After(5 * time.Second):
				panic("command batch did not overlap")
			}
		}
	}
	go func() {
		for i := 0; i < 4; i++ {
			<-arrived
		}
		close(release)
	}()
	report := New(root, env, runner).Execute([]string{"path", "config"})
	if report.Summary["error"] != 1 {
		t.Fatalf("%+v", report)
	}
	commands := []string{}
	selected := []string{}
	for _, call := range runner.calls {
		if len(call) > 1 && call[1] == "which" {
			commands = append(commands, call[2])
		}
	}
	sort.Strings(commands)
	if !reflect.DeepEqual(commands, []string{"java", "node", "python3", "ruby"}) {
		t.Fatal(commands)
	}
	for _, item := range report.Sections["path"] {
		if item["code"] == "command_selected" {
			selected = append(selected, item["item"].(string))
		}
	}
	if !reflect.DeepEqual(selected, []string{"node"}) {
		t.Fatal(selected)
	}
	runner.hook = nil
	runner.calls = nil
	runner.failures = map[string]bool{}
	runner.tools = `{"node":[{"version":"1.0.0","installed":true},{"version":"2.0.0","installed":true}]}`
	report = New(root, env, runner).Execute([]string{"path"})
	count := 0
	for _, call := range runner.calls {
		if len(call) > 1 && call[1] == "which" {
			count++
		}
	}
	if count != 1 || report.Summary["error"] != 0 {
		t.Fatalf("%d %+v", count, report)
	}
}

func TestRedactionColorsAndExitCodes(t *testing.T) {
	report := Report{1, map[string]any{"root": "prefix fixture-private-token suffix", "path_source": "caller"}, map[string]int{"ok": 1, "drift": 1, "candidate": 1, "local": 0, "error": 0}, map[string][]Finding{"tools": {{"kind": "drift", "code": "tool_missing", "item": "fixture-private-token", "message": "missing"}, {"kind": "candidate", "code": "external", "item": "other", "message": "review"}}}}
	env := map[string]string{"PRIVATE_API_TOKEN": "fixture-private-token", "EXTRA_SECRET": "ok"}
	redactReport(&report, env)
	data, _ := json.Marshal(report)
	if strings.Contains(string(data), "fixture-private-token") || report.Sections["tools"][0]["kind"] != "drift" {
		t.Fatal(string(data))
	}
	if ExitCode(report, false) != 0 || ExitCode(report, true) != 1 {
		t.Fatal("exit code")
	}
	report.Summary["error"] = 1
	if ExitCode(report, true) != 2 {
		t.Fatal("incomplete audit must take precedence")
	}
	if !strings.Contains(Render(report, true), "\x1b[31m") || strings.Contains(Render(report, false), "\x1b[") {
		t.Fatal("color rendering")
	}
	if UseColor("auto", map[string]string{"NO_COLOR": ""}, true) || UseColor("auto", map[string]string{"TERM": "dumb"}, true) || UseColor("auto", nil, false) || !UseColor("always", env, false) {
		t.Fatal("color policy")
	}
}
