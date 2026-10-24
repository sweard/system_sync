package onboard

import (
	"archive/tar"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/sweard/system_sync/helper/internal/configops"
	"github.com/sweard/system_sync/helper/internal/localconfig"
	"github.com/sweard/system_sync/helper/internal/taskexec"
)

type fake struct {
	t                                  *testing.T
	e                                  Engine
	installed, roots, foreign, orphans []string
	tools                              map[string]bool
	config                             map[string]any
	calls, mutations                   [][]string
	problem, extraRemoval, failQuery   string
	emptyFailure                       bool
	noChange                           bool
}

func fixture(t *testing.T, profile string) *fake {
	t.Helper()
	base := t.TempDir()
	base, err := filepath.EvalSymlinks(base)
	if err != nil {
		t.Fatal(err)
	}
	root, home := filepath.Join(base, "repo 中文"), filepath.Join(base, "home")
	if err := os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	f := &fake{t: t, installed: []string{"git", "local-tool", "orphan-lib"}, roots: []string{"git", "local-tool"}, foreign: []string{}, orphans: []string{}, tools: map[string]bool{}}
	f.e = Engine{Root: root, Home: home, Profile: profile, Environment: map[string]string{}, Runner: f, UID: 1000}
	dir := filepath.Join(root, "packages/linux", profile)
	name := "mise.toml"
	manifest := "[bootstrap.packages]\n\"" + f.e.manager() + ":git\" = { version = \"latest\" }\n\"" + f.e.manager() + ":missing-tool\" = \"latest\"\n\n[env]\nKEEP_ME = \"original-option\"\n"
	if profile == "macos" {
		dir, name = filepath.Join(root, "packages/macos"), "Brewfile"
		manifest = "# Existing options\nbrew \"git\", link: false\nbrew \"missing-tool\", link: false\n"
	}
	write(t, filepath.Join(dir, name), manifest)
	write(t, filepath.Join(root, "dotfiles/common/mise/config.toml"), "[tools]\nnode = \"1.2.3\"\n")
	f.config = map[string]any{"files": []any{map[string]any{"target": "~/.config/mise/config.toml", "source": filepath.Join(root, "dotfiles/common/mise/config.toml"), "mode": "symlink", "state": "missing", "origin": map[string]any{"config": filepath.Join(root, "mise.toml")}}}, "edits": []any{}}
	return f
}

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
}
func encoded(value any) string { data, _ := json.Marshal(value); return string(data) }

func (f *fake) Run(command []string, capture bool) (string, error) {
	f.calls = append(f.calls, append([]string{}, command...))
	joined := strings.Join(command, " ")
	if f.failQuery != "" && strings.Contains(joined, f.failQuery) {
		return "", fmt.Errorf("private-query-diagnostic")
	}
	if !capture {
		f.mutations = append(f.mutations, append([]string{}, command...))
		if f.noChange {
			return "", nil
		}
		if command[0] == "mise" && has(command, "apply") {
			for _, value := range f.config["files"].([]any) {
				row := value.(map[string]any)
				if row["mode"] == "track-local" || row["mode"] == "track" {
					continue
				}
				row["state"] = "applied"
				target, _ := f.e.configuration().Target(row["target"].(string))
				source := row["source"].(string)
				if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
					return "", err
				}
				if err := os.Symlink(source, target); err != nil {
					return "", err
				}
			}
			return "", nil
		}
		if command[0] == "mise" && has(command, "install") {
			for _, tool := range command[3:] {
				if strings.HasPrefix(tool, "-") {
					continue
				}
				f.tools[shortTool(tool)] = true
			}
			return "", nil
		}
		if f.extraRemoval != "" {
			kept := []string{}
			for _, name := range f.installed {
				if name != f.extraRemoval {
					kept = append(kept, name)
				}
			}
			f.installed = kept
		}
		if !has(f.installed, "missing-tool") {
			f.installed = append(f.installed, "missing-tool")
			f.roots = append(f.roots, "missing-tool")
		}
		return "", nil
	}
	if strings.HasPrefix(joined, "brew bundle dump ") {
		path := ""
		for _, arg := range command {
			if strings.HasPrefix(arg, "--file=") {
				path = strings.TrimPrefix(arg, "--file=")
			}
		}
		text := ""
		for _, name := range f.roots {
			text += "brew \"" + name + "\"\n"
		}
		write(f.t, path, text)
		return "", nil
	}
	if strings.HasPrefix(joined, "brew list --formula") {
		return strings.Join(f.installed, "\n") + "\n", nil
	}
	if joined == "brew list --cask" {
		return "", nil
	}
	if joined == "pacman -Qq" {
		return strings.Join(f.installed, "\n") + "\n", nil
	}
	if has([]string{"pacman -Qqen", "pacman -Qqem", "pacman -Qdtq"}, joined) {
		values := f.roots
		if strings.HasSuffix(joined, "-Qqem") {
			values = f.foreign
		}
		if strings.HasSuffix(joined, "-Qdtq") {
			values = f.orphans
		}
		if len(values) == 0 && f.emptyFailure {
			return "", &taskexec.Failure{Code: 1, Output: ""}
		}
		return strings.Join(values, "\n"), nil
	}
	if command[0] == "dpkg-query" {
		text := ""
		for _, name := range f.installed {
			text += name + ":amd64\tinstalled\n"
		}
		return text, nil
	}
	if command[0] == "rpm" {
		text := ""
		for _, name := range f.installed {
			text += name + "\tx86_64\n"
		}
		return text, nil
	}
	if joined == "apt-mark showmanual" || strings.HasPrefix(joined, "dnf repoquery ") {
		return strings.Join(f.roots, "\n"), nil
	}
	if strings.HasPrefix(joined, "zypper --xmlout") {
		text := "<stream>"
		for _, name := range f.roots {
			text += "<solvable name=\"" + name + "\"/>"
		}
		return text + "</stream>", nil
	}
	if strings.HasPrefix(joined, "mise -C ") {
		state := []any{}
		data, err := localconfig.Read(filepath.Join(command[2], "mise.toml"))
		if err != nil {
			return "", err
		}
		for id := range localconfig.Table(localconfig.Table(data["bootstrap"])["packages"]) {
			manager, name, _ := strings.Cut(id, ":")
			if manager != f.e.manager() {
				continue
			}
			status := "missing"
			if has(f.installed, name) {
				status = "installed"
			}
			if f.problem != "" && name == "missing-tool" {
				status = f.problem
			}
			state = append(state, map[string]any{"package": name, "state": status})
		}
		return encoded(map[string]any{f.e.manager(): map[string]any{"available": true, "packages": state}}), nil
	}
	if joined == "mise bootstrap dotfiles status --json" {
		return encoded(f.config), nil
	}
	if strings.HasPrefix(joined, "mise bootstrap dotfiles diff") {
		return "--- target\n+++ source\n+managed-value\n", nil
	}
	if joined == "mise ls --json" {
		result := map[string]any{}
		for tool := range f.tools {
			name, version, _ := strings.Cut(tool, "@")
			result[name] = []any{map[string]any{"version": version, "installed": true}}
		}
		return encoded(result), nil
	}
	if strings.HasPrefix(joined, "apt-get --simulate") {
		return "Inst missing-tool (1.0)\n", nil
	}
	return "", fmt.Errorf("unexpected command: %v", command)
}

func TestCaptureAcrossFiveProfilesPreservesSourceOptions(t *testing.T) {
	for _, profile := range []string{"macos", "arch", "debian", "fedora", "opensuse"} {
		t.Run(profile, func(t *testing.T) {
			f := fixture(t, profile)
			c, err := f.e.Capture()
			if err != nil {
				t.Fatal(err)
			}
			if len(c.Missing) != 1 || c.Missing[0].Name != "missing-tool" {
				t.Fatalf("missing: %#v", c.Missing)
			}
			if !strings.Contains(c.Candidate.Diff, "local-tool") || !strings.Contains(c.Candidate.Content, "git") {
				t.Fatal(c.Candidate)
			}
			if profile == "macos" {
				if !strings.Contains(c.Candidate.Content, "link: false") {
					t.Fatal("lost brew option")
				}
			} else if !strings.Contains(c.Candidate.Content, `{ version = "latest" }`) || !strings.Contains(c.Candidate.Content, `KEEP_ME = "original-option"`) {
				t.Fatal("lost TOML text")
			}
			for path, before := range c.Sources {
				data, _ := os.ReadFile(path)
				if string(data) != before {
					t.Fatal("capture modified a source")
				}
			}
			if len(f.mutations) != 0 {
				t.Fatal(f.mutations)
			}
			if _, err := os.Stat(filepath.Join(f.e.Root, ".system-sync")); !os.IsNotExist(err) {
				t.Fatal("preview created persisted output")
			}
		})
	}
}

func TestForeignAndExistingOrphansAreRetainedAndNotGuessed(t *testing.T) {
	f := fixture(t, "arch")
	f.foreign = []string{"wsl-integration"}
	f.orphans = []string{"orphan-lib"}
	f.installed = append(f.installed, "wsl-integration")
	f.emptyFailure = true
	c, err := f.e.Capture()
	if err != nil {
		t.Fatal(err)
	}
	if !has(c.Foreign, "wsl-integration") || !has(c.Orphans, "orphan-lib") || len(c.Pending) != 1 {
		t.Fatalf("%+v", c)
	}
	if strings.Contains(c.Candidate.Content, "wsl-integration") {
		t.Fatal("foreign converted into repository declaration")
	}
	f.foreign = nil
	f.orphans = nil
	if _, err = f.e.Capture(); err != nil {
		t.Fatalf("valid empty-exit-1 query rejected: %v", err)
	}
	f.failQuery = "-Qqem"
	if _, err = f.e.Capture(); err == nil {
		t.Fatal("database query failure treated as empty")
	}
}

func TestCaptureSaveUsesRestrictedLocalFilesAndRejectsSymlinks(t *testing.T) {
	f := fixture(t, "arch")
	c, err := f.e.Capture()
	if err != nil {
		t.Fatal(err)
	}
	dir, err := c.Save(f.e.Root, "capture")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{dir, filepath.Join(dir, "capture.json"), filepath.Join(dir, "mise.toml.candidate"), filepath.Join(dir, "sources.before.json")} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0077 != 0 {
			t.Fatalf("private output permissions: %s %v", path, info.Mode())
		}
	}
	second := fixture(t, "arch")
	if err := os.Symlink(t.TempDir(), filepath.Join(second.e.Root, ".system-sync")); err != nil {
		t.Fatal(err)
	}
	if _, err = c.Save(second.e.Root, "capture"); err == nil {
		t.Fatal("linked output accepted")
	}
}

func TestInstallOnlyAcrossFiveProfilesAndRepeatedApply(t *testing.T) {
	for _, profile := range []string{"macos", "arch", "debian", "fedora", "opensuse"} {
		t.Run(profile, func(t *testing.T) {
			f := fixture(t, profile)
			plan, err := f.e.Plan([]string{"packages"})
			if err != nil {
				t.Fatal(err)
			}
			before := append([]string{}, f.installed...)
			if _, err = f.e.Apply(context.Background(), plan, io.Discard); err != nil {
				t.Fatal(err)
			}
			if len(f.mutations) != 1 {
				t.Fatal(f.mutations)
			}
			for _, name := range before {
				if !has(f.installed, name) {
					t.Fatal("removed " + name)
				}
			}
			for _, command := range f.mutations {
				for _, forbidden := range []string{"cleanup", "autoremove", "remove", "uninstall", "--asdeps", "-R", "-Rs", "--allowerasing", "--force-resolution"} {
					if has(command, forbidden) {
						t.Fatal(command)
					}
				}
			}
			if profile == "debian" && !has(f.mutations[0], "--no-remove") {
				t.Fatal("missing native removal guard")
			}
			if profile == "macos" {
				path := strings.TrimPrefix(f.mutations[0][len(f.mutations[0])-1], "--file=")
				if !has(f.mutations[0], "--verbose") {
					t.Fatal("Homebrew installation output is hidden", f.mutations[0])
				}
				data, _ := os.ReadFile(path)
				if strings.Contains(string(data), `brew "git"`) || !strings.Contains(string(data), `brew "missing-tool", link: false`) {
					t.Fatal(string(data))
				}
			}
			again, err := f.e.Plan([]string{"packages"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = f.e.Apply(context.Background(), again, io.Discard); err != nil {
				t.Fatal(err)
			}
			if len(f.mutations) != 1 {
				t.Fatal("repeated apply reinstalled existing packages")
			}
		})
	}
}

func TestUnexpectedRemovalAndMissingInstallRemainNeedsReview(t *testing.T) {
	for _, which := range []string{"removal", "no-change"} {
		t.Run(which, func(t *testing.T) {
			f := fixture(t, "arch")
			plan, err := f.e.Plan([]string{"packages"})
			if err != nil {
				t.Fatal(err)
			}
			if which == "removal" {
				f.extraRemoval = "local-tool"
			} else {
				f.noChange = true
			}
			dir, err := f.e.Apply(context.Background(), plan, io.Discard)
			if err == nil {
				t.Fatal("invalid native result passed")
			}
			data, _ := os.ReadFile(filepath.Join(dir, "transaction.json"))
			if !strings.Contains(string(data), `"phase": "needs-review"`) {
				t.Fatal(string(data))
			}
		})
	}
}

func TestChangedManifestOrInventoryCannotUseOldPlan(t *testing.T) {
	for _, what := range []string{"manifest", "inventory"} {
		t.Run(what, func(t *testing.T) {
			f := fixture(t, "arch")
			p, err := f.e.Plan([]string{"packages"})
			if err != nil {
				t.Fatal(err)
			}
			if what == "manifest" {
				for path := range p.Packages.Sources {
					write(t, path, "[bootstrap.packages]\n\"pacman:other\"=\"latest\"\n")
					break
				}
			} else {
				f.installed = append(f.installed, "concurrent-install")
			}
			if _, err = f.e.Apply(context.Background(), p, io.Discard); err == nil {
				t.Fatal("stale plan applied")
			}
			if len(f.mutations) != 0 {
				t.Fatal(f.mutations)
			}
		})
	}
}

func TestConfigAndToolsAreStagedBackedUpAndIdempotent(t *testing.T) {
	f := fixture(t, "arch")
	p, err := f.e.Plan(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Pending) != 0 {
		t.Fatal(p.Pending)
	}
	dir, err := f.e.Apply(context.Background(), p, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.mutations) != 3 || f.mutations[0][0] != "sudo" || !has(f.mutations[1], "apply") || !has(f.mutations[2], "node@1.2.3") {
		t.Fatal(f.mutations)
	}
	if !has(f.mutations[2], "--raw") {
		t.Fatal("mise tool installation output is buffered", f.mutations[2])
	}
	if _, err = os.Stat(filepath.Join(dir, "config.before.tar")); err != nil {
		t.Fatal(err)
	}
	again, err := f.e.Plan(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.e.Apply(context.Background(), again, io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(f.mutations) != 3 {
		t.Fatal("repeated init mutated host")
	}
}

func TestConfigConflictsDoNotForceOrPartiallyInstall(t *testing.T) {
	f := fixture(t, "arch")
	target := filepath.Join(f.e.Home, ".config/mise/config.toml")
	write(t, target, "private-local-value")
	p, err := f.e.Plan(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Pending) == 0 {
		t.Fatal("ordinary-file conflict not reported")
	}
	if _, err = f.e.Apply(context.Background(), p, io.Discard); err == nil {
		t.Fatal("conflicted plan applied")
	}
	if len(f.mutations) != 0 {
		t.Fatal(f.mutations)
	}
	data, _ := os.ReadFile(target)
	if string(data) != "private-local-value" {
		t.Fatal("private file replaced")
	}
}

func TestLocalHistoryTrackingDoesNotConflictWithOwnedMapping(t *testing.T) {
	f := fixture(t, "arch")
	f.config["files"] = append(f.config["files"].([]any), map[string]any{"target": "~/.config/mise/config.toml", "mode": "track-local", "state": "tracked", "origin": map[string]any{"config": filepath.Join(f.e.Home, ".config/mise/conf.d/system-sync-history.toml")}})
	p, err := f.e.Plan([]string{"config"})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Pending) != 0 || len(p.ConfigTargets) != 1 {
		t.Fatal(p.Pending, p.ConfigTargets)
	}
	if _, err = f.e.Apply(context.Background(), p, io.Discard); err != nil {
		t.Fatal(err)
	}
}

func TestPrivateConfigurationBackupHasOriginalContents(t *testing.T) {
	f := fixture(t, "arch")
	target := filepath.Join(f.e.Home, ".private-app.toml")
	write(t, target, "token = 'private-fixture-secret'\n")
	dir := t.TempDir()
	if err := configops.BackupTargets(f.e.Home, dir, []string{target}); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(filepath.Join(dir, "config.before.tar"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	r := tar.NewReader(file)
	header, err := r.Next()
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if header.Name != ".private-app.toml" || !strings.Contains(string(data), "private-fixture-secret") {
		t.Fatal("backup incomplete")
	}
}

func TestLegacyToolSourceBlocksOnlyToolsStage(t *testing.T) {
	f := fixture(t, "arch")
	path := filepath.Join(f.e.Home, ".nvm/versions/node/v1/bin/node")
	write(t, path, "#!/bin/sh\n")
	if err := os.Chmod(path, 0700); err != nil {
		t.Fatal(err)
	}
	p, err := f.e.Plan(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Pending) == 0 {
		t.Fatal("legacy installation not identified")
	}
	packages, err := f.e.Plan([]string{"packages"})
	if err != nil {
		t.Fatal(err)
	}
	if len(packages.Pending) != 0 {
		t.Fatal(packages.Pending)
	}
	if _, err = f.e.Apply(context.Background(), packages, io.Discard); err != nil {
		t.Fatal(err)
	}
}

func TestUnsafeStatesFailedQueriesAndCancellationDoNotInstall(t *testing.T) {
	for _, state := range []string{"absent", "skipped", "future", "version_mismatch"} {
		t.Run(state, func(t *testing.T) {
			f := fixture(t, "arch")
			f.problem = state
			p, err := f.e.Plan([]string{"packages"})
			if err == nil {
				_, err = f.e.Apply(context.Background(), p, io.Discard)
			}
			if err == nil || len(f.mutations) != 0 {
				t.Fatalf("unsafe state accepted: %v %v", err, f.mutations)
			}
		})
	}
	f := fixture(t, "arch")
	f.failQuery = "-Qqem"
	if _, err := f.e.Capture(); err == nil || strings.Contains(err.Error(), "private-query-diagnostic") {
		t.Fatal("query failure exposed output or accepted incomplete capture")
	}
	f = fixture(t, "arch")
	p, err := f.e.Plan([]string{"packages"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = f.e.Apply(ctx, p, io.Discard); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if len(f.mutations) != 0 {
		t.Fatal(f.mutations)
	}
}

func TestMissingPinnedOrSpecialPackageIsNeverInstalledAsLatest(t *testing.T) {
	for _, value := range []string{`"1.2.3"`, `{ version = "latest", force = true }`} {
		t.Run(value, func(t *testing.T) {
			f := fixture(t, "arch")
			path := filepath.Join(f.e.Root, "packages/linux/arch/mise.toml")
			write(t, path, "[bootstrap.packages]\n\"pacman:git\" = \"latest\"\n\"pacman:missing-tool\" = "+value+"\n")
			p, err := f.e.Plan([]string{"packages"})
			if err != nil {
				t.Fatal(err)
			}
			if len(p.Pending) == 0 {
				t.Fatal("unsupported package options accepted")
			}
			if _, err = f.e.Apply(context.Background(), p, io.Discard); err == nil {
				t.Fatal("installed pinned package as latest")
			}
			if len(f.mutations) != 0 {
				t.Fatal(f.mutations)
			}
			if !strings.Contains(p.Packages.Candidate.Content, value) {
				t.Fatal("candidate lost original package options")
			}
		})
	}
}

func TestStageSelectionAndWSLDetection(t *testing.T) {
	if stages, err := Stages([]string{"tools,packages", "packages"}); err != nil || !reflect.DeepEqual(stages, []string{"packages", "tools"}) {
		t.Fatal(stages, err)
	}
	if _, err := Stages([]string{"unknown"}); err == nil {
		t.Fatal("unknown stage accepted")
	}
	f := fixture(t, "arch")
	f.e.Environment["WSL_DISTRO_NAME"] = "archlinux"
	p, err := f.e.Plan([]string{"packages"})
	if err != nil || !p.WSL {
		t.Fatal(p, err)
	}
}

func TestChangedToolSourcesOrInstalledVersionsRequireFreshPreview(t *testing.T) {
	for _, what := range []string{"source", "installed"} {
		t.Run(what, func(t *testing.T) {
			f := fixture(t, "arch")
			target := filepath.Join(f.e.Home, ".config/mise/config.toml")
			if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
				t.Fatal(err)
			}
			source := filepath.Join(f.e.Root, "dotfiles/common/mise/config.toml")
			if err := os.Symlink(source, target); err != nil {
				t.Fatal(err)
			}
			p, err := f.e.Plan([]string{"tools"})
			if err != nil {
				t.Fatal(err)
			}
			if len(p.Pending) > 0 {
				t.Fatal(p.Pending)
			}
			if what == "source" {
				write(t, source, "[tools]\nnode=\"9.9.9\"\n")
			} else {
				f.tools["node@0.1.0"] = true
			}
			if _, err = f.e.Apply(context.Background(), p, io.Discard); err == nil {
				t.Fatal("stale tools plan applied")
			}
			if len(f.mutations) > 0 {
				t.Fatal(f.mutations)
			}
		})
	}
}

func TestExternalOverlappingEditAndUnknownModeBlockConfiguration(t *testing.T) {
	for _, what := range []string{"overlap", "mode"} {
		t.Run(what, func(t *testing.T) {
			f := fixture(t, "arch")
			if what == "overlap" {
				f.config["edits"] = []any{map[string]any{"path": "~/.config/mise/config.toml", "state": "differs", "origin": map[string]any{"config": filepath.Join(f.e.Home, ".config/mise/config.local.toml")}}}
			} else {
				f.config["files"].([]any)[0].(map[string]any)["mode"] = "future-mode"
			}
			p, err := f.e.Plan([]string{"config"})
			if err != nil {
				t.Fatal(err)
			}
			if len(p.Pending) == 0 {
				t.Fatal("unsafe configuration accepted")
			}
			if _, err = f.e.Apply(context.Background(), p, io.Discard); err == nil {
				t.Fatal("unsafe config applied")
			}
			if len(f.mutations) != 0 {
				t.Fatal(f.mutations)
			}
		})
	}
}
