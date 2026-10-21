package packageedit

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type fakeRunner struct {
	installed        map[string]bool
	calls, mutations [][]string
	fail, noChange   bool
	extraRemoval     string
	canonical        map[string]string
	mutate           func()
}

func (f *fakeRunner) Run(command []string, capture bool) (string, error) {
	f.calls = append(f.calls, append([]string{}, command...))
	name := command[len(command)-1]
	if !capture {
		f.mutations = append(f.mutations, command)
		if f.fail {
			return "", fmt.Errorf("fake native failure")
		}
		if f.mutate != nil {
			f.mutate()
		}
		if !f.noChange {
			if contains(command, "--cask") {
				name = short(name)
			}
			if contains(command, "uninstall") || contains(command, "remove") || contains(command, "-R") {
				delete(f.installed, name)
			} else {
				f.installed[name] = true
			}
		}
		return "", nil
	}
	if command[0] == "brew" && command[1] == "info" {
		if mapped := f.canonical[name]; mapped != "" {
			name = mapped
		}
		key := "formulae"
		if contains(command, "--cask") {
			key = "casks"
		}
		data, _ := json.Marshal(map[string]any{key: []any{map[string]any{"full_name": name, "full_token": name, "aliases": []any{}, "oldnames": []any{}, "old_tokens": []any{}}}})
		return string(data), nil
	}
	if command[0] == "brew" && command[1] == "list" || command[0] == "pacman" && command[1] == "-Qq" || command[0] == "rpm" && command[1] == "-qa" {
		output := ""
		for name := range f.installed {
			output += name + "\n"
		}
		return output, nil
	}
	if command[0] == "dpkg-query" {
		output := ""
		for name := range f.installed {
			output += name + ":amd64\tinstalled\n"
		}
		return output, nil
	}
	if command[0] == "apt-cache" {
		return "Package: " + name + "\nArchitecture: amd64\n", nil
	}
	if command[0] == "apt-get" && command[1] == "--simulate" {
		output := "Remv " + name + " [1.0]\n"
		if f.extraRemoval != "" {
			output += "Remv " + f.extraRemoval + " [1.0]\n"
		}
		return output, nil
	}
	if command[0] == "mise" {
		return "", nil
	}
	if command[0] == "dnf" && command[1] == "repoquery" {
		return name + "\n", nil
	}
	if command[0] == "zypper" && command[1] == "--xmlout" {
		return `<stream><solvable name="` + name + `"/></stream>`, nil
	}
	if contains(command, "-Si") || command[0] == "rpm" && command[1] == "-e" {
		return "", nil
	}
	return "", fmt.Errorf("unexpected native command: %v", command)
}
func contains(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}

type packageFixture struct {
	root, brew, trust string
	runner            *fakeRunner
}

func newFixture(t *testing.T) *packageFixture {
	t.Helper()
	root := t.TempDir()
	directory := filepath.Join(root, "packages/macos")
	if err := os.MkdirAll(directory, 0755); err != nil {
		t.Fatal(err)
	}
	brew := filepath.Join(directory, "Brewfile")
	trust := filepath.Join(directory, "trusted-casks.txt")
	for path, content := range map[string]string{brew: "# Test baseline\ntap \"abue-ammar/tinycast\"\nbrew \"git\"\nbrew \"mise\"\nbrew \"ruby\", link: false\ncask \"firefox\"\ncask \"google-chrome\"\ncask \"abue-ammar/tinycast/tinycast\", trusted: true\n", filepath.Join(directory, "protected-packages.txt"): "formula:git\nformula:mise\n", trust: "# Test cask trust\nabue-ammar/tinycast/tinycast\n", filepath.Join(directory, "trusted-formulae.txt"): "# Formula trust\n"} {
		if err := os.WriteFile(path, []byte(content), 0640); err != nil {
			t.Fatal(err)
		}
	}
	for profile, manager := range Managers {
		directory := filepath.Join(root, "packages/linux", profile)
		if err := os.MkdirAll(directory, 0755); err != nil {
			t.Fatal(err)
		}
		text := "# Linux roots\n[bootstrap.packages]\n"
		for _, name := range []string{"base", "base-files", "filesystem", "git", "ripgrep", "zsh"} {
			text += `"` + manager + ":" + name + `" = "latest"` + "\n"
		}
		if err := os.WriteFile(filepath.Join(directory, "mise.toml"), []byte(text), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, "protected-packages.txt"), []byte("git\nzsh\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return &packageFixture{root, brew, trust, &fakeRunner{installed: map[string]bool{"git": true, "mise": true, "base": true, "base-files": true, "filesystem": true}, canonical: map[string]string{}}}
}
func (f *packageFixture) command(t *testing.T, profile string, options Options) *Command {
	t.Helper()
	p, err := New(f.root, profile, options, f.runner)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func execute(p *Command, answer bool) error {
	return p.Execute(context.Background(), func() (bool, error) { return answer, nil }, io.Discard)
}
func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
func expectError(t *testing.T, err error, part string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), part) {
		t.Fatalf("expected %q, got %v", part, err)
	}
}
func TestTargetedFormulaCaskAliasesAndOptions(t *testing.T) {
	for _, which := range []string{"formula", "cask", "alias", "options"} {
		t.Run(which, func(t *testing.T) {
			f := newFixture(t)
			options := Options{Action: "install", Package: "gitui"}
			wanted := `brew "gitui"`
			original := read(t, f.brew)
			switch which {
			case "cask":
				options.Cask, options.Package = true, "zed"
				wanted = `cask "zed"`
			case "alias":
				options.Package = "rg"
				f.runner.canonical["rg"] = "ripgrep"
				wanted = `brew "ripgrep"`
			case "options":
				options.Package = "ruby"
				wanted = `brew "ruby", link: false`
			}
			f.runner.installed["automatically-installed-dependency"] = true
			if err := execute(f.command(t, "macos", options), true); err != nil {
				t.Fatal(err)
			}
			after := read(t, f.brew)
			if !strings.Contains(after, wanted) || strings.Contains(after, "automatically-installed-dependency") {
				t.Fatal(after)
			}
			if which == "options" && (after != original || !contains(f.runner.mutations[0], "--skip-link")) {
				t.Fatal("existing options changed")
			}
			if len(f.runner.mutations) != 1 {
				t.Fatal(f.runner.mutations)
			}
			info, err := os.Stat(f.brew)
			if err != nil || info.Mode().Perm() != 0640 {
				t.Fatal("manifest permissions changed")
			}
		})
	}
}
func TestPreviewCancellationAndAbsentUninstall(t *testing.T) {
	for _, mode := range []string{"preview", "cancel", "absent", "uninstall"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			before := read(t, f.brew)
			options := Options{Action: "uninstall", Package: "firefox", Cask: true, DryRun: mode == "preview"}
			if mode == "uninstall" {
				f.runner.installed["firefox"], f.runner.installed["google-chrome"] = true, true
			}
			if err := execute(f.command(t, "macos", options), mode != "cancel"); err != nil {
				t.Fatal(err)
			}
			if mode == "preview" || mode == "cancel" {
				if read(t, f.brew) != before || len(f.runner.mutations) > 0 {
					t.Fatal("preview/cancel mutated")
				}
				if _, err := os.Stat(filepath.Join(f.root, ".system-sync")); !os.IsNotExist(err) {
					t.Fatal("preview/cancel wrote audit")
				}
			} else {
				after := read(t, f.brew)
				if strings.Contains(after, `cask "firefox"`) || !strings.Contains(after, `cask "google-chrome"`) {
					t.Fatal(after)
				}
				if mode == "absent" && len(f.runner.mutations) != 0 {
					t.Fatal("absent uninstall ran native command")
				}
			}
		})
	}
}
func TestFailuresNeverCommitPlannedManifestAndAreJournaled(t *testing.T) {
	for _, failure := range []string{"install-failed", "uninstall-failed", "ineffective", "concurrent-edit", "extra-removal", "confirmation-change"} {
		t.Run(failure, func(t *testing.T) {
			f := newFixture(t)
			before := read(t, f.brew)
			options := Options{Action: "install", Package: "gitui"}
			part := ""
			switch failure {
			case "install-failed":
				f.runner.fail = true
				part = "fake native failure"
			case "uninstall-failed":
				f.runner.fail = true
				f.runner.installed["firefox"] = true
				options = Options{Action: "uninstall", Package: "firefox", Cask: true}
				part = "fake native failure"
			case "ineffective":
				f.runner.noChange = true
				part = "安装状态未达到目标"
			case "concurrent-edit":
				f.runner.mutate = func() { _ = os.WriteFile(f.brew, []byte(before+"# concurrent change\n"), 0640) }
				part = "拒绝覆盖"
			case "extra-removal":
				f.runner.mutate = func() { delete(f.runner.installed, "git") }
				part = "未计划的其他包移除"
			case "confirmation-change":
				part = "确认期间安装状态已改变"
			}
			p := f.command(t, "macos", options)
			err := p.Execute(context.Background(), func() (bool, error) {
				if failure == "confirmation-change" {
					f.runner.installed["other"] = true
				}
				return true, nil
			}, io.Discard)
			expectError(t, err, part)
			if failure == "concurrent-edit" {
				if read(t, f.brew) != before+"# concurrent change\n" {
					t.Fatal("concurrent edit lost")
				}
			} else if read(t, f.brew) != before {
				t.Fatal("failed native operation edited manifest")
			}
			if p.Audit != "" {
				var record map[string]any
				if err := json.Unmarshal([]byte(read(t, filepath.Join(p.Audit, "transaction.json"))), &record); err != nil {
					t.Fatal(err)
				}
				if record["phase"] != "needs-review" {
					t.Fatal(record)
				}
			}
		})
	}
}
func TestThirdPartyTrustSourceConflictsAndUninstall(t *testing.T) {
	for _, mode := range []string{"untrusted", "trusted", "preview", "conflict", "remove"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			options := Options{Action: "install", Package: "fixture/apps/demo", Cask: true, Trust: mode != "untrusted", DryRun: mode == "preview"}
			if mode == "conflict" {
				options.Package = "other/apps/tinycast"
			}
			if mode == "remove" {
				options = Options{Action: "uninstall", Package: "tinycast", Cask: true}
				f.runner.canonical["tinycast"] = "abue-ammar/tinycast/tinycast"
				f.runner.installed["tinycast"] = true
			}
			err := execute(f.command(t, "macos", options), true)
			switch mode {
			case "untrusted":
				expectError(t, err, "--trust")
			case "conflict":
				expectError(t, err, "另一个 tap")
			default:
				if err != nil {
					t.Fatal(err)
				}
			}
			if mode == "trusted" {
				if !strings.Contains(read(t, f.brew), `tap "fixture/apps"`) || !strings.Contains(read(t, f.brew), `cask "fixture/apps/demo", trusted: true`) || !strings.Contains(read(t, f.trust), "fixture/apps/demo\n") {
					t.Fatal("exact trust missing")
				}
			}
			if mode == "preview" {
				if !reflect.DeepEqual(f.runner.calls, [][]string{{"brew", "list", "--cask"}}) {
					t.Fatal("preview loaded untrusted Ruby", f.runner.calls)
				}
			}
			if mode == "remove" {
				if strings.Contains(read(t, f.trust), "abue-ammar/tinycast/tinycast") || !strings.Contains(read(t, f.brew), `tap "abue-ammar/tinycast"`) {
					t.Fatal("tap/trust cleanup wrong")
				}
			}
		})
	}
}
func TestPartialMultiFileWriteRollsBack(t *testing.T) {
	f := newFixture(t)
	before, trust := read(t, f.brew), read(t, f.trust)
	p := f.command(t, "macos", Options{Action: "install", Package: "fixture/apps/demo", Cask: true, Trust: true})
	p.AtomicWrite = func(path, content string) error {
		if path == f.brew {
			return fmt.Errorf("injected disk failure")
		}
		return atomicWrite(path, content)
	}
	expectError(t, execute(p, true), "injected disk failure")
	if read(t, f.brew) != before || read(t, f.trust) != trust {
		t.Fatal("partial edits not rolled back")
	}
}

func TestCancellationDuringMultiFileCommitRollsBack(t *testing.T) {
	f := newFixture(t)
	before, trusted := read(t, f.brew), read(t, f.trust)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := f.command(t, "macos", Options{Action: "install", Package: "fixture/apps/demo", Cask: true, Trust: true})
	p.AtomicWrite = func(path, content string) error {
		err := atomicWrite(path, content)
		if path == f.trust {
			cancel()
		}
		return err
	}
	err := p.Execute(ctx, func() (bool, error) { return true, nil }, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("expected cancellation: %v", err)
	}
	if read(t, f.brew) != before || read(t, f.trust) != trusted {
		t.Fatal("cancelled partial writes not rolled back")
	}
}

type runnerFunc func([]string, bool) (string, error)

func (f runnerFunc) Run(command []string, capture bool) (string, error) { return f(command, capture) }
func TestOfficialMiseParsesGeneratedManifestsAndParserFailureStopsMutation(t *testing.T) {
	mise, err := exec.LookPath("mise")
	if err != nil {
		t.Skip("mise unavailable")
	}
	f := newFixture(t)
	for _, part := range []string{"CONFIG", "SYSTEM_CONFIG", "DATA", "STATE", "CACHE"} {
		t.Setenv("MISE_"+part+"_DIR", filepath.Join(t.TempDir(), part))
	}
	t.Setenv("MISE_TRUSTED_CONFIG_PATHS", f.root)
	for profile := range Managers {
		p := f.command(t, profile, Options{Action: "install", Package: "gitui", DryRun: true})
		p.Runner = runnerFunc(func(command []string, capture bool) (string, error) {
			if command[0] == "mise" {
				output, err := exec.Command(mise, command[1:]...).CombinedOutput()
				return string(output), err
			}
			return f.runner.Run(command, capture)
		})
		if err := execute(p, true); err != nil {
			t.Fatalf("%s: %v", profile, err)
		}
	}
	p := f.command(t, "debian", Options{Action: "install", Package: "gitui"})
	p.Runner = runnerFunc(func(command []string, capture bool) (string, error) {
		if command[0] == "mise" {
			return "", fmt.Errorf("mise rejects invalid TOML")
		}
		return f.runner.Run(command, capture)
	})
	expectError(t, execute(p, true), "mise rejects")
	if len(f.runner.mutations) != 0 {
		t.Fatal("parser failure reached native mutation")
	}
}
func TestLinuxProfilesCommandsProtectAndEditOnlyRequestedRoot(t *testing.T) {
	for profile, manager := range Managers {
		t.Run(profile, func(t *testing.T) {
			f := newFixture(t)
			path := filepath.Join(f.root, "packages/linux", profile, "mise.toml")
			originalBrew := read(t, f.brew)
			if err := execute(f.command(t, profile, Options{Action: "install", Package: "gitui"}), true); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(read(t, path), `"`+manager+`:gitui" = "latest"`) || read(t, f.brew) != originalBrew {
				t.Fatal("wrong profile edited")
			}
			f.runner.installed["ripgrep"] = true
			if err := execute(f.command(t, profile, Options{Action: "uninstall", Package: "ripgrep"}), true); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(read(t, path), "ripgrep") || !strings.Contains(read(t, path), "git") {
				t.Fatal("wrong declaration removed")
			}
			command := f.runner.mutations[len(f.runner.mutations)-1]
			for _, bad := range []string{"-Rns", "--clean-deps", "autoremove", "--force", "--nodeps"} {
				if contains(command, bad) {
					t.Fatal("unsafe command", command)
				}
			}
			protected := map[string]string{"arch": "base", "debian": "apt", "fedora": "kernel-core", "opensuse": "zypper"}[profile]
			expectError(t, execute(f.command(t, profile, Options{Action: "uninstall", Package: protected}), true), "受保护包")
		})
	}
}
func TestAURAndReverseDependencyGuards(t *testing.T) {
	f := newFixture(t)
	p := f.command(t, "arch", Options{Action: "install", Package: "sample-aur-bin", AUR: true})
	p.UID = 501
	p.LookPath = func(string) (string, error) { return "/fake/paru", nil }
	if err := execute(p, true); err != nil {
		t.Fatal(err)
	}
	if f.runner.mutations[0][0] != "/fake/paru" {
		t.Fatal(f.runner.mutations)
	}
	if err := execute(f.command(t, "arch", Options{Action: "uninstall", Package: "sample-aur-bin"}), true); err != nil {
		t.Fatal(err)
	}
	p = f.command(t, "arch", Options{Action: "install", Package: "other-aur", AUR: true})
	p.UID = 0
	p.LookPath = func(string) (string, error) { return "/fake/paru", nil }
	_, err := p.NativeCommand()
	expectError(t, err, "普通用户")
	f = newFixture(t)
	f.runner.installed["ripgrep"] = true
	f.runner.extraRemoval = "git"
	expectError(t, execute(f.command(t, "debian", Options{Action: "uninstall", Package: "ripgrep"}), true), "连带移除其他包")
	if len(f.runner.mutations) != 0 {
		t.Fatal("reverse dependencies removed")
	}
}
func TestUnsafeDeclarationsNamesAndSymlinksFailBeforeMutation(t *testing.T) {
	for _, name := range []string{"https://example.org/app", "../package", "app*", "app;id", "app=1.0", "--help"} {
		f := newFixture(t)
		if _, err := New(f.root, "macos", Options{Action: "install", Package: name}, f.runner); err == nil {
			t.Fatal("selector accepted", name)
		}
	}
	for _, mode := range []string{"dynamic", "empty", "symlink", "pin", "multiline", "protected-alias"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			profile := "macos"
			options := Options{Action: "install", Package: "gitui"}
			part := ""
			switch mode {
			case "dynamic":
				_ = os.WriteFile(f.brew, []byte(read(t, f.brew)+"if OS.mac?\nend\n"), 0640)
				part = "不是支持的静态声明"
			case "empty":
				_ = os.WriteFile(f.brew, []byte("cask \"firefox\"\n"), 0640)
				options = Options{Action: "uninstall", Package: "firefox", Cask: true}
				part = "空的软件包清单"
			case "symlink":
				saved := f.brew + ".saved"
				_ = os.Rename(f.brew, saved)
				_ = os.Symlink(saved, f.brew)
				part = "普通的本地清单"
			case "pin", "multiline":
				profile = "debian"
				path := filepath.Join(f.root, "packages/linux/debian/mise.toml")
				source := read(t, path)
				if mode == "pin" {
					source = strings.Replace(source, `"apt:ripgrep" = "latest"`, `"apt:ripgrep" = "1.0"`, 1)
					options.Package = "ripgrep"
					part = "版本固定"
				} else {
					source += "\"apt:example\" = {\nversion = \"latest\"\n}\n"
					part = "无法安全编辑"
				}
				_ = os.WriteFile(path, []byte(source), 0644)
			case "protected-alias":
				f.runner.canonical["git-alias"] = "git"
				options = Options{Action: "uninstall", Package: "git-alias"}
				part = "受保护包"
			}
			expectError(t, execute(f.command(t, profile, options), true), part)
			if len(f.runner.mutations) > 0 {
				t.Fatal("unsafe input reached native writer")
			}
		})
	}
}
