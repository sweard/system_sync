package confighistory

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sweard/system_sync/helper/internal/localconfig"
)

type recordingRunner struct {
	command []string
	fail    bool
}

func (r *recordingRunner) Invoke(command []string, _ bool, _ time.Duration) (string, error) {
	r.command = command
	if r.fail {
		return "", fmt.Errorf("private-history-token")
	}
	return "", nil
}
func historyFixture(t *testing.T) (string, string, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("MISE_CONFIG_DIR", filepath.Join(home, ".config/mise"))
	t.Setenv("MISE_STATE_DIR", filepath.Join(home, "state"))
	target := filepath.Join(home, ".codex/config.toml")
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("private_canary=\"local\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	enrollment, history, _, err := localconfig.Locations(home)
	if err != nil {
		t.Fatal(err)
	}
	return home, enrollment, history
}
func TestManualLocalEnrollmentAndSaveDescription(t *testing.T) {
	home, enrollment, history := historyFixture(t)
	runner := &recordingRunner{}
	var out bytes.Buffer
	for i := 0; i < 2; i++ {
		if err := Run("init", "", home, runner, &out); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), "  - "+filepath.Join(home, ".codex/config.toml")+"\n") {
			t.Fatal("enrolled path missing", out.String())
		}
		out.Reset()
	}
	for path, mode := range map[string]os.FileMode{enrollment: 0600, history: 0700} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != mode {
			t.Fatalf("bad private mode for %s", path)
		}
	}
	description := "manual with spaces --private-canary"
	if err := Run("save", description, home, runner, &out); err != nil {
		t.Fatal(err)
	}
	wanted := []string{"mise", "dot", "save", "--description", description, "--", filepath.Join(home, ".codex/config.toml")}
	if !reflect.DeepEqual(runner.command, wanted) {
		t.Fatal(runner.command)
	}
	if strings.Contains(out.String(), "canary") {
		t.Fatal("description or private value leaked")
	}
	if !strings.Contains(out.String(), "  - "+wanted[6]+"\n") {
		t.Fatal("saved path missing", out.String())
	}
}

func TestSaveListsSkippedRegisteredFilesAndOnlySavesAvailableTargets(t *testing.T) {
	for _, allMissing := range []bool{false, true} {
		t.Run(fmt.Sprint(allMissing), func(t *testing.T) {
			home, _, _ := historyFixture(t)
			local := filepath.Join(home, ".zshrc.local")
			if err := os.WriteFile(local, []byte("private-shell-canary\n"), 0600); err != nil {
				t.Fatal(err)
			}
			runner := &recordingRunner{}
			if err := Run("init", "", home, runner, io.Discard); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(local); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(home, ".codex/config.toml")
			if allMissing {
				if err := os.Remove(target); err != nil {
					t.Fatal(err)
				}
			}
			var out bytes.Buffer
			err := Run("save", "manual", home, runner, &out)
			if (err != nil) != allMissing {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), "[!] 跳过") || !strings.Contains(out.String(), "  - "+local+"\n") {
				t.Fatal("missing skipped path", out.String())
			}
			if allMissing {
				if runner.command != nil || strings.Contains(out.String(), "已手动保存") ||
					!strings.Contains(out.String(), "  - "+target+"\n") {
					t.Fatal(runner.command, out.String())
				}
			} else if !reflect.DeepEqual(runner.command[6:], []string{target}) {
				t.Fatal(runner.command)
			}
			if strings.Contains(out.String(), "canary") {
				t.Fatal("private contents leaked")
			}
		})
	}
}
func TestCustomScopeMissingFilesLinksAndNativeFailureFailClosed(t *testing.T) {
	for _, which := range []string{"custom", "missing", "enrollment-link", "history-link", "native-failure"} {
		t.Run(which, func(t *testing.T) {
			home, enrollment, history := historyFixture(t)
			runner := &recordingRunner{}
			if err := Run("init", "", home, runner, io.Discard); err != nil {
				t.Fatal(err)
			}
			switch which {
			case "custom":
				_ = os.WriteFile(enrollment, []byte("[dotfiles]\n\"/unexpected\"={mode=\"track\"}\n"), 0600)
			case "missing":
				_ = os.Remove(filepath.Join(home, ".codex/config.toml"))
			case "enrollment-link":
				saved := enrollment + ".saved"
				_ = os.Rename(enrollment, saved)
				_ = os.Symlink(saved, enrollment)
			case "history-link":
				_ = os.Remove(history)
				_ = os.Symlink(t.TempDir(), history)
			case "native-failure":
				runner.fail = true
			}
			err := Run("save", "named", home, runner, io.Discard)
			if err == nil {
				t.Fatal("unsafe save accepted")
			}
			if strings.Contains(err.Error(), "private-history-token") {
				t.Fatal("native output leaked")
			}
			if which != "native-failure" && runner.command != nil {
				t.Fatal("unsafe scope reached mise save")
			}
		})
	}
}
