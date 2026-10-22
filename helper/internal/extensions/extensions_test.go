package extensions

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeCLI struct {
	current        map[string]string
	calls          [][]string
	fail, noChange bool
	output         string
	limits         []time.Duration
}

func (f *fakeCLI) Invoke(command []string, _ bool, limit time.Duration) (string, error) {
	f.calls = append(f.calls, command)
	f.limits = append(f.limits, limit)
	for i, arg := range command {
		if arg == "--install-extension" {
			if f.fail {
				return "", fmt.Errorf("private-token")
			}
			name, v, _ := strings.Cut(command[i+1], "@")
			if !f.noChange {
				f.current[name] = v
			}
			return "", nil
		}
	}
	if f.output != "" {
		return f.output, nil
	}
	result := ""
	for name, v := range f.current {
		result += name + "@" + v + "\n"
	}
	return result, nil
}
func TestDeclarationsValidateAllPlatformsAndConflicts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "extensions.toml")
	valid := "[common]\n\"example.shared\"=\"1.0.0\"\n[macos]\n\"example.mac\"=\"2.0.0\"\n[linux]\n\"example.linux\"=\"3.0.0\"\n"
	if err := os.WriteFile(path, []byte(valid), 0600); err != nil {
		t.Fatal(err)
	}
	for _, platform := range []string{"macos", "linux"} {
		wanted, err := Declarations(path, platform)
		name := "example.mac"
		if platform == "linux" {
			name = "example.linux"
		}
		if err != nil || len(wanted) != 2 || wanted["example.shared"] != "1.0.0" || wanted[name] == "" {
			t.Fatalf("platform %s: %v %v", platform, wanted, err)
		}
	}
	for _, text := range []string{`[common]
"example.shared"="1.0.0"
[linux]
"--bad.flag"="1.0.0"`, `[common]
"example.shared"="1.0.0"
[macos]
"example.shared"="2.0.0"`, `[future]
"example.shared"="1.0.0"`, `[common]
"example.shared"=true`} {
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Declarations(path, "macos"); err == nil {
			t.Fatal("unsafe manifest accepted")
		}
	}
}
func TestInstalledRejectsDuplicatesAndMalformedOutput(t *testing.T) {
	for _, output := range []string{"unknown\n", "example.shared@1.0.0\nEXAMPLE.SHARED@1.1.0\n", "example.shared@latest\n"} {
		if _, err := Installed(&fakeCLI{output: output}, []string{"code"}); err == nil {
			t.Fatalf("accepted %q", output)
		}
	}
}
func TestExtensionFailuresAreRedactedAndExtrasRetained(t *testing.T) {
	for _, failure := range []string{"failed", "ineffective", "preview", "success"} {
		t.Run(failure, func(t *testing.T) {
			dir := t.TempDir()
			manifest, code := filepath.Join(dir, "extensions.toml"), filepath.Join(dir, "code")
			_ = os.WriteFile(manifest, []byte("[common]\n\"example.shared\"=\"1.2.3\"\n"), 0600)
			_ = os.WriteFile(code, []byte("placeholder"), 0700)
			fake := &fakeCLI{current: map[string]string{"example.local": "4.5.6"}, fail: failure == "failed", noChange: failure == "ineffective"}
			err := Run(Options{Action: "sync", DryRun: failure == "preview", Manifest: manifest, Code: code}, fake, io.Discard)
			if (err != nil) != (failure == "failed" || failure == "ineffective") {
				t.Fatalf("unexpected result %v", err)
			}
			if err != nil && strings.Contains(err.Error(), "private-token") {
				t.Fatal("native diagnostic leaked")
			}
			if failure == "failed" && (!strings.Contains(err.Error(), "example.shared@1.2.3") || !strings.Contains(err.Error(), "已安装项保留")) {
				t.Fatal("failed install did not identify the extension or retained work", err)
			}
			for index, command := range fake.calls {
				limit := time.Minute
				if strings.Contains(strings.Join(command, " "), "--install-extension") {
					limit = time.Hour
				}
				if fake.limits[index] != limit {
					t.Fatal("status query and installation used the wrong time limit", command, fake.limits[index])
				}
			}
			if fake.current["example.local"] != "4.5.6" {
				t.Fatal("extra extension removed")
			}
			if failure == "preview" && len(fake.calls) != 1 {
				t.Fatal("preview installed extension")
			}
		})
	}
}

func TestExtraExtensionDetailsAreSortedAndPreservedForEveryAction(t *testing.T) {
	for _, action := range []string{"status", "preview", "sync"} {
		for _, installed := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/installed=%v", action, installed), func(t *testing.T) {
				dir := t.TempDir()
				manifest, code := filepath.Join(dir, "extensions.toml"), filepath.Join(dir, "code")
				if err := os.WriteFile(manifest, []byte("[common]\n\"example.shared\"=\"1.2.3\"\n"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(code, []byte("placeholder"), 0700); err != nil {
					t.Fatal(err)
				}
				fake := &fakeCLI{current: map[string]string{"zeta.local": "4.5.6", "alpha.local": "1.0.0-beta.2"}}
				if installed {
					fake.current["example.shared"] = "1.2.3"
				}
				options := Options{Action: action, Manifest: manifest, Code: code}
				if action == "preview" {
					options.Action, options.DryRun = "sync", true
				}
				var out bytes.Buffer
				if err := Run(options, fake, &out); err != nil {
					t.Fatal(err)
				}
				expected := "[~] 保留 2 个清单外扩展：\n  - alpha.local@1.0.0-beta.2\n  - zeta.local@4.5.6\n"
				if !strings.Contains(out.String(), expected) {
					t.Fatal(out.String())
				}
				if fake.current["alpha.local"] != "1.0.0-beta.2" || fake.current["zeta.local"] != "4.5.6" {
					t.Fatal("extra extensions changed")
				}
				for _, command := range fake.calls {
					if strings.Contains(strings.Join(command, " "), "--uninstall-extension") {
						t.Fatal("extra extension removal requested")
					}
				}
				if (action != "sync" || installed) && len(fake.calls) != 1 {
					t.Fatal("read-only or unchanged run performed extra operations", fake.calls)
				}
			})
		}
	}
}

func TestNoExtraExtensionsHaveNoRetentionBlock(t *testing.T) {
	dir := t.TempDir()
	manifest, code := filepath.Join(dir, "extensions.toml"), filepath.Join(dir, "code")
	if err := os.WriteFile(manifest, []byte("[common]\n\"example.shared\"=\"1.2.3\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(code, []byte("placeholder"), 0700); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Run(Options{Action: "status", Manifest: manifest, Code: code},
		&fakeCLI{current: map[string]string{"example.shared": "1.2.3"}}, &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "清单外扩展") || !strings.Contains(out.String(), "无需同步") {
		t.Fatal(out.String())
	}
}
