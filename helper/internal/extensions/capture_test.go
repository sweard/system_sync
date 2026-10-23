package extensions

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sweard/system_sync/helper/internal/localconfig"
	"github.com/sweard/system_sync/helper/internal/syncstate"
)

func captureFixture(t *testing.T, source string) (string, CaptureOptions, *fakeCLI) {
	t.Helper()
	root := t.TempDir()
	options := CaptureOptions{Options: Options{Manifest: filepath.Join(root, "extensions.toml"), Code: filepath.Join(root, "code")}}
	if err := os.WriteFile(options.Manifest, []byte(source), 0640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(options.Code, []byte("placeholder"), 0700); err != nil {
		t.Fatal(err)
	}
	return root, options, &fakeCLI{current: map[string]string{"example.shared": "9.0.0", "zeta.local": "4.5.6", "alpha.local": "1.0.0-beta.2"}}
}

func assertCaptureQueriesOnly(t *testing.T, fake *fakeCLI) {
	t.Helper()
	for _, command := range fake.calls {
		if command[len(command)-2] != "--list-extensions" || command[len(command)-1] != "--show-versions" {
			t.Fatalf("capture changed installed extensions: %v", command)
		}
	}
}

func TestCapturePreviewSelectsExtrasAndPreservesUnselectedVersions(t *testing.T) {
	source := "# public preferences\n[common]\n\"example.shared\" = \"1.2.3\" # keep pinned\n\"example.missing\" = \"2.0.0\"\n"
	root, options, fake := captureFixture(t, source)
	options.Only = []string{"ALPHA.LOCAL,zeta.local", "alpha.local"}
	options.Exclude = []string{" zeta.local ", "uninstalled.local"}
	plan, err := NewCapture(options, fake)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Group != "common" || len(plan.added) != 1 || plan.added[0].name != "alpha.local" || len(plan.excluded) != 2 || len(plan.updated) != 0 {
		t.Fatalf("incorrect selection: %+v", plan)
	}
	if !strings.HasPrefix(plan.After, source) || !strings.HasSuffix(plan.After, "\"alpha.local\" = \"1.0.0-beta.2\"\n") {
		t.Fatal(plan.After)
	}
	var output bytes.Buffer
	plan.Render(&output)
	for _, detail := range []string{"[!] 待追加 1", "alpha.local@1.0.0-beta.2", "[~] 跳过 2", "zeta.local@4.5.6", "example.shared@9.0.0", "+\"alpha.local\" = \"1.0.0-beta.2\""} {
		if !strings.Contains(output.String(), detail) {
			t.Fatal(output.String())
		}
	}
	before, _ := os.ReadFile(options.Manifest)
	if string(before) != source || len(fake.calls) != 1 {
		t.Fatal("preview modified manifest or performed unnecessary queries")
	}
	if _, err := os.Stat(filepath.Join(root, ".system-sync")); !os.IsNotExist(err) {
		t.Fatal("preview created local state")
	}
	assertCaptureQueriesOnly(t, fake)
}

func TestCaptureDefaultCommonAndExplicitPlatformHaveCorrectScope(t *testing.T) {
	for _, group := range []string{"", "common", localconfig.Platform()} {
		t.Run("group="+group, func(t *testing.T) {
			_, options, fake := captureFixture(t, "[common]\n\"example.shared\" = \"1.2.3\"\n")
			fake.current["example.shared"] = "1.2.3"
			options.Group = group
			plan, err := NewCapture(options, fake)
			if err != nil {
				t.Fatal(err)
			}
			expected := group
			if expected == "" {
				expected = "common"
			}
			if plan.Group != expected {
				t.Fatalf("group %q resolved to %q", group, plan.Group)
			}
			data, err := localconfig.Decode([]byte(plan.After))
			if err != nil {
				t.Fatal(err)
			}
			for _, platform := range []string{"macos", "linux"} {
				wanted, err := declarations(data, platform, false)
				if err != nil {
					t.Fatal(err)
				}
				included := expected == "common" || expected == platform
				if (wanted["alpha.local"] == "1.0.0-beta.2") != included || (wanted["zeta.local"] == "4.5.6") != included || wanted["example.shared"] != "1.2.3" {
					t.Fatalf("group %q has wrong scope on %s: %v", group, platform, wanted)
				}
			}
			assertCaptureQueriesOnly(t, fake)
		})
	}
}

func TestCaptureCandidatePreservesCommentsAndPlatformTables(t *testing.T) {
	for _, group := range []string{"common", "macos", "linux"} {
		for _, source := range []string{"", "[common]\n", "# only comment", "[common]\n\"example.shared\" = \"1.2.3\" # pinned", "[common] # public\n\"example.shared\" = \"1.2.3\"\n\n# macOS\n[macos]\n\"example.mac\" = \"2.0.0\" # keep\n\n# Linux\n[linux]\n\"example.linux\" = \"3.0.0\"\n"} {
			t.Run(group+"/"+source, func(t *testing.T) {
				plan := CapturePlan{Before: source, Group: group, added: []extension{{"alpha.local", "1.0.0"}, {"zeta.local", "4.5.6"}}}
				after, err := plan.candidate()
				if err != nil {
					t.Fatal(err)
				}
				data, err := localconfig.Decode([]byte(after))
				if err != nil || localconfig.Table(data[group])["alpha.local"] != "1.0.0" || localconfig.Table(data[group])["zeta.local"] != "4.5.6" {
					t.Fatalf("invalid candidate: %s %v", after, err)
				}
				for _, line := range strings.Split(source, "\n") {
					if line != "" && !strings.Contains(after, line) {
						t.Fatalf("existing line lost: %q in %s", line, after)
					}
				}
				for _, section := range []string{"# macOS\n[macos]", "# Linux\n[linux]"} {
					if strings.Contains(source, section) && !strings.Contains(after, section) {
						t.Fatalf("section comment separated from its header: %s", after)
					}
				}
			})
		}
	}
}

func TestCaptureRejectsInvalidSelectionAndUnsafeLayouts(t *testing.T) {
	other := "linux"
	if localconfig.Platform() == "linux" {
		other = "macos"
	}
	for _, name := range []string{"unknown-group", "other-platform", "bad-only", "bad-exclude", "uninstalled-only", "common-conflict", "inline-table", "duplicate", "unknown-table"} {
		t.Run(name, func(t *testing.T) {
			root, options, fake := captureFixture(t, "[common]\n\"example.shared\" = \"1.2.3\"\n")
			switch name {
			case "unknown-group":
				options.Group = "future"
			case "other-platform":
				options.Group = other
			case "bad-only":
				options.Only = []string{"example.*"}
			case "bad-exclude":
				options.Exclude = []string{"alpha.local@1.0.0"}
			case "uninstalled-only":
				options.Only = []string{"unknown.extension"}
			case "common-conflict":
				options.Group = "common"
				_ = os.WriteFile(options.Manifest, []byte("[common]\n["+other+"]\n\"alpha.local\" = \"9.0.0\"\n"), 0640)
			case "inline-table":
				options.Group = "common"
				_ = os.WriteFile(options.Manifest, []byte("common = { \"example.shared\" = \"1.2.3\" }\n"), 0640)
			case "duplicate":
				_ = os.WriteFile(options.Manifest, []byte("[common]\n\"example.shared\" = \"1.2.3\"\n\"example.shared\" = \"2.0.0\"\n"), 0640)
			case "unknown-table":
				_ = os.WriteFile(options.Manifest, []byte("[future]\n\"example.shared\" = \"1.2.3\"\n"), 0640)
			}
			before, _ := os.ReadFile(options.Manifest)
			if _, err := NewCapture(options, fake); err == nil {
				t.Fatal("unsafe capture accepted")
			}
			after, _ := os.ReadFile(options.Manifest)
			if !bytes.Equal(before, after) {
				t.Fatal("failed capture modified manifest")
			}
			if _, err := os.Stat(filepath.Join(root, ".system-sync")); !os.IsNotExist(err) {
				t.Fatal("failed preview created local state")
			}
			assertCaptureQueriesOnly(t, fake)
		})
	}
}

func TestCaptureApplyBacksUpAtomicallyAndIsIdempotent(t *testing.T) {
	source := "# keep\n[common]\n\"example.shared\" = \"1.2.3\" # fixed\n\"example.missing\" = \"2.0.0\"\n"
	root, options, fake := captureFixture(t, source)
	options.Group = "common"
	plan, err := NewCapture(options, fake)
	if err != nil {
		t.Fatal(err)
	}
	backup, err := plan.Apply(context.Background(), root, fake)
	if err != nil {
		t.Fatal(err)
	}
	after, info, err := snapshot(options.Manifest)
	if err != nil || after != plan.After || info.Mode().Perm() != 0640 {
		t.Fatalf("incorrect write or permissions: %q %v", after, err)
	}
	if !strings.Contains(after, "\"example.shared\" = \"9.0.0\" # fixed") || len(plan.updated) != 1 {
		t.Fatal("managed version or comment not captured", after)
	}
	for name, expected := range map[string]string{"extensions.toml.before": source, "extensions.toml.candidate": after} {
		content, info, err := snapshot(filepath.Join(backup, name))
		if err != nil || content != expected || info.Mode().Perm() != 0600 {
			t.Fatalf("incorrect backup %s: %q %v", name, content, err)
		}
	}
	if info, err := os.Stat(backup); err != nil || info.Mode().Perm() != 0700 {
		t.Fatal("backup directory is not private")
	}
	if files, _ := filepath.Glob(filepath.Join(root, ".extensions-capture-*")); len(files) != 0 {
		t.Fatal("temporary files retained", files)
	}
	if len(fake.calls) != 2 || plan.added[0].name != "alpha.local" || plan.added[1].name != "zeta.local" {
		t.Fatal("capture was not sorted or installation was not rechecked")
	}
	second, err := NewCapture(options, fake)
	if err != nil || second.HasChanges() {
		t.Fatalf("capture is not idempotent: %v", err)
	}
	if directory, err := second.Apply(context.Background(), root, fake); err != nil || directory != "" {
		t.Fatal("no-op capture created backup", directory, err)
	}
	if dirs, _ := os.ReadDir(filepath.Dir(backup)); len(dirs) != 1 {
		t.Fatal("no-op created another capture")
	}
	assertCaptureQueriesOnly(t, fake)
}

func TestCaptureApplyRejectsConcurrentManifestChanges(t *testing.T) {
	for _, change := range []string{"content", "permissions", "replacement", "symlink"} {
		t.Run(change, func(t *testing.T) {
			root, options, fake := captureFixture(t, "[common]\n")
			plan, err := NewCapture(options, fake)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "content":
				_ = os.WriteFile(options.Manifest, []byte("# concurrent change\n[common]\n"), 0640)
			case "permissions":
				_ = os.Chmod(options.Manifest, 0600)
			case "replacement":
				_ = os.WriteFile(options.Manifest+".other", []byte(plan.Before), 0640)
				_ = os.Rename(options.Manifest+".other", options.Manifest)
			case "symlink":
				_ = os.WriteFile(options.Manifest+".other", []byte(plan.Before), 0640)
				_ = os.Remove(options.Manifest)
				_ = os.Symlink(options.Manifest+".other", options.Manifest)
			}
			before, _ := os.ReadFile(options.Manifest)
			if _, err := plan.Apply(context.Background(), root, fake); err == nil || !strings.Contains(err.Error(), "清单发生变化") {
				t.Fatal("concurrent edit not detected", err)
			}
			after, _ := os.ReadFile(options.Manifest)
			if !bytes.Equal(before, after) {
				t.Fatal("concurrent edit overwritten")
			}
		})
	}
}

func TestCaptureApplyRechecksInstalledVersionsAndFailsClosed(t *testing.T) {
	for _, change := range []string{"updated", "removed", "malformed"} {
		t.Run(change, func(t *testing.T) {
			root, options, fake := captureFixture(t, "[common]\n")
			plan, err := NewCapture(options, fake)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "updated":
				fake.current["alpha.local"] = "8.0.0"
			case "removed":
				delete(fake.current, "alpha.local")
			case "malformed":
				fake.output = "private-invalid-output\n"
			}
			if _, err := plan.Apply(context.Background(), root, fake); err == nil || strings.Contains(err.Error(), "private-invalid-output") {
				t.Fatal("unsafe or misleading result", err)
			}
			after, _ := os.ReadFile(options.Manifest)
			if string(after) != plan.Before {
				t.Fatal("manifest changed after invalid query")
			}
			assertCaptureQueriesOnly(t, fake)
		})
	}
}

func TestCaptureUsesSharedLockAndRejectsRedirectedBackupPaths(t *testing.T) {
	for _, kind := range []string{"lock", "symlink-state", "symlink-capture"} {
		t.Run(kind, func(t *testing.T) {
			root, options, fake := captureFixture(t, "[common]\n")
			plan, err := NewCapture(options, fake)
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "lock":
				lock, err := syncstate.OperationLock(root)
				if err != nil {
					t.Fatal(err)
				}
				defer lock.Close()
			case "symlink-state":
				_ = os.Symlink(t.TempDir(), filepath.Join(root, ".system-sync"))
			case "symlink-capture":
				_ = os.Mkdir(filepath.Join(root, ".system-sync"), 0700)
				_ = os.Symlink(t.TempDir(), filepath.Join(root, ".system-sync/capture"))
			}
			if _, err := plan.Apply(context.Background(), root, fake); err == nil {
				t.Fatal("unsafe write accepted")
			}
			after, _ := os.ReadFile(options.Manifest)
			if string(after) != plan.Before {
				t.Fatal("manifest changed despite lock or unsafe backup path")
			}
		})
	}
}

func TestCaptureCanceledApplyDoesNotWrite(t *testing.T) {
	root, options, fake := captureFixture(t, "[common]\n")
	plan, err := NewCapture(options, fake)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := plan.Apply(ctx, root, fake); err != context.Canceled {
		t.Fatal("cancelled operation was not stopped", err)
	}
	after, _ := os.ReadFile(options.Manifest)
	if string(after) != plan.Before {
		t.Fatal("cancelled operation changed manifest")
	}
}

func TestCaptureRejectsLinkedManifestBeforeInvokingCLI(t *testing.T) {
	_, options, fake := captureFixture(t, "[common]\n")
	_ = os.Rename(options.Manifest, options.Manifest+".real")
	_ = os.Symlink(options.Manifest+".real", options.Manifest)
	if _, err := NewCapture(options, fake); err == nil || len(fake.calls) != 0 {
		t.Fatal("linked manifest was read or CLI was invoked", err)
	}
}

func TestCaptureUpdatesManagedVersionsInOriginalGroupsAndPreservesOtherContent(t *testing.T) {
	platform, other := localconfig.Platform(), "linux"
	if platform == "linux" {
		other = "macos"
	}
	for _, group := range []string{"common", platform} {
		t.Run(group, func(t *testing.T) {
			source := "# public\r\n[\"common\"] # keep header\r\n'example.shared'  = '1.2.3' # pinned\r\n\"example.missing\" = \"2.0.0\"\r\n[" + platform + "]\r\n\"example.shared\" = \"1.2.3\" # same scope\r\n\"example.platform\" = \"2.1.304\"\r\n[" + other + "]\r\n\"example.other\" = \"3.0.0\" # leave alone\r\n"
			_, options, fake := captureFixture(t, source)
			options.Group = group
			fake.current = map[string]string{"example.shared": "9.0.0", "example.platform": "2.1.309"}
			plan, err := NewCapture(options, fake)
			if err != nil {
				t.Fatal(err)
			}
			expected := strings.ReplaceAll(strings.ReplaceAll(source, "1.2.3", "9.0.0"), "2.1.304", "2.1.309")
			if len(plan.added) != 0 || len(plan.updated) != 2 || plan.After != expected {
				t.Fatalf("version capture changed scope or layout: %+v\n%s", plan, plan.After)
			}
			var out bytes.Buffer
			plan.Render(&out)
			for _, detail := range []string{"待更新 2 个已纳管扩展版本", "清单 -> 本机", "example.platform: 2.1.304 -> 2.1.309 [" + platform + "]", "-\"example.platform\" = \"2.1.304\"", "+\"example.platform\" = \"2.1.309\""} {
				if !strings.Contains(out.String(), detail) {
					t.Fatal(out.String())
				}
			}
			assertCaptureQueriesOnly(t, fake)
		})
	}
}

func TestCaptureManagedSelectionAcceptsInstalledVersionsInEitherDirection(t *testing.T) {
	platform, other := localconfig.Platform(), "linux"
	if platform == "linux" {
		other = "macos"
	}
	for _, installed := range []string{"2.1.309", "2.1.300"} {
		for _, excluded := range []bool{false, true} {
			t.Run(installed+"/excluded="+fmt.Sprint(excluded), func(t *testing.T) {
				source := "[" + platform + "]\n\"anthropic.claude-code\" = \"2.1.304\" # pinned\n[" + other + "]\n\"anthropic.claude-code\" = \"5.0.0\" # other platform\n"
				root, options, fake := captureFixture(t, source)
				options.Only = []string{"ANTHROPIC.CLAUDE-CODE"}
				fake.current = map[string]string{"anthropic.claude-code": installed}
				if excluded {
					options.Exclude = []string{"anthropic.claude-code"}
				}
				plan, err := NewCapture(options, fake)
				if err != nil || plan.HasChanges() == excluded {
					t.Fatal("incorrect managed selection", plan, err)
				}
				if _, err := plan.Apply(context.Background(), root, fake); err != nil {
					t.Fatal(err)
				}
				expected := source
				if !excluded {
					expected = strings.Replace(source, "2.1.304", installed, 1)
				}
				after, _ := os.ReadFile(options.Manifest)
				if string(after) != expected || fake.current["anthropic.claude-code"] != installed {
					t.Fatal("capture changed scope or installed extension", string(after), fake.current)
				}
				second, err := NewCapture(options, fake)
				if err != nil || second.HasChanges() {
					t.Fatal("managed capture is not idempotent", err)
				}
				assertCaptureQueriesOnly(t, fake)
			})
		}
	}
}

func TestCaptureManagedVersionRecheckRejectsChangedOrRemovedInstallation(t *testing.T) {
	for _, change := range []string{"changed", "removed"} {
		t.Run(change, func(t *testing.T) {
			root, options, fake := captureFixture(t, "[common]\n\"example.shared\" = \"1.2.3\"\n")
			options.Only = []string{"example.shared"}
			plan, err := NewCapture(options, fake)
			if err != nil || len(plan.added) != 0 || len(plan.updated) != 1 {
				t.Fatal("missing update-only plan", plan, err)
			}
			fake.current["example.shared"] = "8.0.0"
			if change == "removed" {
				delete(fake.current, "example.shared")
			}
			if _, err := plan.Apply(context.Background(), root, fake); err == nil || !strings.Contains(err.Error(), "安装或版本发生变化") {
				t.Fatal("managed version was not rechecked", err)
			}
			current, _ := os.ReadFile(options.Manifest)
			if string(current) != plan.Before {
				t.Fatal("version recheck failure modified manifest")
			}
			if _, err := os.Stat(filepath.Join(root, ".system-sync/capture")); !os.IsNotExist(err) {
				t.Fatal("version recheck failure created a backup")
			}
		})
	}
}

func TestCaptureManagedVersionsRejectOtherPlatformConflictsAndUnsupportedLayout(t *testing.T) {
	other := "linux"
	if localconfig.Platform() == "linux" {
		other = "macos"
	}
	for _, source := range []string{
		"[common]\n\"example.shared\" = \"1.2.3\"\n[" + other + "]\n\"example.shared\" = \"1.2.3\" # other platform\n",
		"common = { \"example.shared\" = \"1.2.3\" }\n",
	} {
		root, options, fake := captureFixture(t, source)
		options.Only = []string{"example.shared"}
		if _, err := NewCapture(options, fake); err == nil {
			t.Fatal("unsafe managed version capture accepted", source)
		}
		after, _ := os.ReadFile(options.Manifest)
		if string(after) != source {
			t.Fatal("failed preview modified manifest")
		}
		if _, err := os.Stat(filepath.Join(root, ".system-sync")); !os.IsNotExist(err) {
			t.Fatal("failed preview created state")
		}
		assertCaptureQueriesOnly(t, fake)
	}
}
