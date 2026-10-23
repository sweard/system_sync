package checkrun

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
	"github.com/sweard/system_sync/helper/internal/syncstate"
)

const testGo = "1.2.3"

func prepWrite(t *testing.T, root, rel string, b []byte) {
	t.Helper()
	p := filepath.Join(root, rel)
	if e := os.MkdirAll(filepath.Dir(p), 0755); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(p, b, 0644); e != nil {
		t.Fatal(e)
	}
}
func prepManifest(goVersion string) []byte {
	return []byte(fmt.Sprintf("%x  %s\n%x  cmd/main.go\n", sha256.Sum256([]byte(goVersion+"\n")), goInput, sha256.Sum256([]byte("source"))))
}
func prepLock(t *testing.T, version string) []byte {
	t.Helper()
	platforms := map[string]any{}
	for _, p := range strings.Split(lockPlatforms, ",") {
		archive := strings.ReplaceAll(strings.ReplaceAll(p, "macos", "darwin"), "x64", "amd64")
		platforms["platforms."+p] = map[string]any{"url": "https://dl.google.com/go/go" + version + "." + archive + ".tar.gz", "checksum": "sha256:" + strings.Repeat("a", 64)}
	}
	platforms["version"], platforms["backend"], platforms["specifiers"] = version, "core:go", []string{version}
	b, e := toml.Marshal(map[string]any{"lockfile_version": 3, "tools": map[string]any{"go": []any{platforms}, "node": []any{map[string]any{"version": "24.0.0", "backend": "core:node"}}}})
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func prepFixture(t *testing.T, changed bool) string {
	t.Helper()
	root := t.TempDir()
	prepWrite(t, root, goConfig, []byte("[tools]\ngo=\""+testGo+"\"\n[env]\nPRIVATE=\"must-not-be-resolved\"\n"))
	prepWrite(t, root, goLock, prepLock(t, testGo))
	prepWrite(t, root, "helper/VERSION", []byte("v0.2.0\n"))
	prepWrite(t, root, "current-inputs", prepManifest(testGo))
	prepWrite(t, root, "helper/scripts/common.sh", []byte("helper_source_manifest() { cat '"+root+"/current-inputs'; }\n"))
	previous := testGo
	if changed {
		previous = "1.2.2"
	}
	prepWrite(t, root, "helper/prebuilt/v0.2.0/SOURCE_SHA256SUMS", prepManifest(previous))
	prepArchives(t, root, "previous")
	return root
}
func prepArchives(t *testing.T, root, value string) {
	t.Helper()
	var manifest strings.Builder
	for _, target := range bundleTargets {
		name := "system-sync-helper_v0.2.0_" + target + ".tar.gz"
		b := []byte(value + target)
		prepWrite(t, root, "helper/prebuilt/v0.2.0/"+name, b)
		fmt.Fprintf(&manifest, "%x  %s\n", sha256.Sum256(b), name)
	}
	prepWrite(t, root, "helper/prebuilt/v0.2.0/SHA256SUMS", []byte(manifest.String()))
}
func prepSnapshot(t *testing.T, root string) map[string]savedFile {
	t.Helper()
	result := map[string]savedFile{}
	for _, dir := range []string{"dotfiles", "helper/prebuilt"} {
		e := filepath.WalkDir(filepath.Join(root, dir), func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() {
				rel, _ := filepath.Rel(root, path)
				f, e := readRegular(root, rel)
				if e != nil {
					return e
				}
				result[rel] = f
			}
			return nil
		})
		if e != nil {
			t.Fatal(e)
		}
	}
	return result
}
func assertPrepSnapshot(t *testing.T, root string, before map[string]savedFile) {
	t.Helper()
	for rel, expected := range before {
		actual, e := readRegular(root, rel)
		if e != nil || actual.Mode != expected.Mode || !bytes.Equal(actual.Data, expected.Data) {
			t.Fatal("file not preserved", rel, e)
		}
	}
}
func fakePrepare(t *testing.T, root, fail string, calls *[]string) prepareExec {
	t.Helper()
	return func(ctx context.Context, dir string, args, env []string, out, errout io.Writer) int {
		name := filepath.Base(args[0])
		*calls = append(*calls, name)
		switch name {
		case "mise":
			if strings.Join(args, " ") != "mise lock --platform "+lockPlatforms+" go" || dir == root {
				t.Fatal("unbounded lock command", dir, args)
			}
			b, _ := os.ReadFile(filepath.Join(dir, "mise.toml"))
			if string(b) != "[tools]\ngo = \""+testGo+"\"\n" {
				t.Fatal("lock config contains other tools or private env", string(b))
			}
			for _, variable := range env {
				if strings.HasPrefix(variable, "MISE_GLOBAL_CONFIG_FILE=") || strings.HasPrefix(variable, "MISE_ENV=") || strings.HasPrefix(variable, "MISE_OVERRIDE_CONFIG_FILENAMES=") {
					t.Fatal("inherited mise configuration override", variable)
				}
			}
			b = prepLock(t, testGo)
			if fail == "other-tool" {
				b = bytes.ReplaceAll(b, []byte("24.0.0"), []byte("25.0.0"))
			}
			if fail == "incomplete-lock" {
				b = bytes.ReplaceAll(b, []byte("sha256:"), []byte("sha512:"))
			}
			prepWrite(t, dir, "mise.lock", b)
			if fail == "lock" {
				return 7
			}
			if fail == "concurrent-config" {
				prepWrite(t, root, goConfig, []byte("[tools]\ngo=\"9.9.9\"\n"))
			}
		case "build.sh":
			if dir != root || len(args) != 2 || args[1] != "--bundle" {
				t.Fatal(args)
			}
			prepArchives(t, root, "rebuilt")
			prepWrite(t, root, "helper/prebuilt/v0.2.0/SOURCE_SHA256SUMS", prepManifest(testGo))
			if fail == "build" {
				return 8
			}
			if fail == "missing-output" {
				os.Remove(filepath.Join(root, "helper/prebuilt/v0.2.0/SOURCE_SHA256SUMS"))
				return 8
			}
		case "verify.sh":
			if len(args) != 1 {
				t.Fatal("preparation must reverify all four targets", args)
			}
			if fail == "verify" {
				return 9
			}
		default:
			t.Fatal("unexpected prepare command", args)
		}
		return 0
	}
}

func TestGoPreparationNoChangeIsReadOnly(t *testing.T) {
	root := prepFixture(t, false)
	before := prepSnapshot(t, root)
	var out bytes.Buffer
	var calls []string
	if e := prepareGo(context.Background(), root, false, strings.NewReader("yes\n"), &out, &out, false, fakePrepare(t, root, "", &calls)); e != nil {
		t.Fatal(e)
	}
	if len(calls) != 0 || !strings.Contains(out.String(), "无需准备") {
		t.Fatal(out.String(), calls)
	}
	assertPrepSnapshot(t, root, before)
	if _, e := os.Stat(filepath.Join(root, ".system-sync")); !os.IsNotExist(e) {
		t.Fatal("no-op created preparation state")
	}
}
func TestGoPreparationNoninteractiveAndExactYes(t *testing.T) {
	for _, answer := range []string{"", "y\n", "YES\n", "yes \n", " yes\n", "yes", "no\n", "yes\n"} {
		t.Run(fmt.Sprintf("%q", answer), func(t *testing.T) {
			root := prepFixture(t, true)
			before := prepSnapshot(t, root)
			var calls []string
			var out bytes.Buffer
			interactive := answer != "yes\n" // even piped yes must fail closed
			e := prepareGo(context.Background(), root, false, strings.NewReader(answer), &out, &out, interactive, fakePrepare(t, root, "", &calls))
			if e == nil || len(calls) != 0 {
				t.Fatal("unconfirmed write", answer, e, calls)
			}
			assertPrepSnapshot(t, root, before)
			if _, e := os.Stat(filepath.Join(root, ".system-sync")); !os.IsNotExist(e) {
				t.Fatal("cancellation created backup or operation lock")
			}
		})
	}
}
func TestGoPreparationDetectsLockMetadataAndVersionDrift(t *testing.T) {
	for _, mutation := range []string{"version", "url", "checksum", "missing-platform", "binding"} {
		t.Run(mutation, func(t *testing.T) {
			root := prepFixture(t, false)
			b := prepLock(t, testGo)
			switch mutation {
			case "version":
				b = prepLock(t, "1.2.2")
			case "url":
				b = bytes.Replace(b, []byte("https://dl.google.com"), []byte("https://wrong.invalid"), 1)
			case "checksum":
				b = bytes.Replace(b, []byte("sha256:"), []byte("invalid:"), 1)
			case "missing-platform":
				b = bytes.ReplaceAll(b, []byte("platforms.macos-arm64"), []byte("platforms.unknown"))
			case "binding":
				b = bytes.ReplaceAll(b, []byte("specifiers = ['1.2.3']"), []byte("specifiers = ['latest']"))
			}
			prepWrite(t, root, goLock, b)
			plan, e := inspectGo(context.Background(), root, false)
			if e != nil || len(plan.Reasons) != 1 || !strings.Contains(plan.Reasons[0], "下载锁") {
				t.Fatal(plan.Reasons, e, string(b))
			}
		})
	}
}
func TestGoPreparationRejectsUnrelatedDriftAndDamage(t *testing.T) {
	for _, mutation := range []string{"source", "manifest", "archive", "config", "lock", "symlink"} {
		t.Run(mutation, func(t *testing.T) {
			root := prepFixture(t, true)
			switch mutation {
			case "source":
				prepWrite(t, root, "current-inputs", append(prepManifest(testGo), []byte(fmt.Sprintf("%s  extra.go\n", strings.Repeat("a", 64)))...))
			case "manifest":
				prepWrite(t, root, "helper/prebuilt/v0.2.0/SOURCE_SHA256SUMS", []byte("broken\n"))
			case "archive":
				prepWrite(t, root, "helper/prebuilt/v0.2.0/system-sync-helper_v0.2.0_linux_amd64.tar.gz", []byte("damaged"))
			case "config":
				prepWrite(t, root, goConfig, []byte("[tools]\ngo='latest'\n"))
			case "lock":
				prepWrite(t, root, goLock, []byte("private-parser-diagnostic='secret'\n[invalid"))
			case "symlink":
				path := filepath.Join(root, goLock)
				os.Rename(path, path+".local")
				os.Symlink(path+".local", path)
			}
			var calls []string
			e := prepareGo(context.Background(), root, false, strings.NewReader("yes\n"), io.Discard, io.Discard, true, fakePrepare(t, root, "", &calls))
			if e == nil || len(calls) != 0 || strings.Contains(e.Error(), "secret") {
				t.Fatal("unsafe drift accepted", e, calls)
			}
		})
	}
}
func TestGoPreparationKeepsCurrentArchiveScope(t *testing.T) {
	root := prepFixture(t, false)
	foreign := "linux_amd64"
	current, _ := Target(runtime.GOOS, runtime.GOARCH)
	if foreign == current {
		foreign = "darwin_arm64"
	}
	prepWrite(t, root, "helper/prebuilt/v0.2.0/system-sync-helper_v0.2.0_"+foreign+".tar.gz", []byte("damage"))
	if _, e := inspectGo(context.Background(), root, false); e != nil {
		t.Fatal("current scope checked foreign archive", e)
	}
	if _, e := inspectGo(context.Background(), root, true); e == nil {
		t.Fatal("all scope missed foreign damage")
	}
}
func TestGoPreparationSuccessAndRollback(t *testing.T) {
	for _, failure := range []string{"", "lock", "incomplete-lock", "other-tool", "build", "missing-output", "verify"} {
		t.Run("failure="+failure, func(t *testing.T) {
			root := prepFixture(t, true)
			prepWrite(t, root, goLock, prepLock(t, "1.2.2"))
			if e := os.Chmod(filepath.Join(root, goLock), 0600); e != nil {
				t.Fatal(e)
			}
			before := prepSnapshot(t, root)
			var calls []string
			var out bytes.Buffer
			e := prepareGo(context.Background(), root, false, strings.NewReader("yes\n"), &out, &out, true, fakePrepare(t, root, failure, &calls))
			if failure != "" {
				if e == nil || !strings.Contains(e.Error(), "检查已停止") {
					t.Fatal(e, out.String())
				}
				assertPrepSnapshot(t, root, before)
			} else {
				if e != nil || strings.Join(calls, ",") != "mise,build.sh,verify.sh" {
					t.Fatal(e, calls, out.String())
				}
				p, e := inspectGo(context.Background(), root, true)
				if e != nil || len(p.Reasons) != 0 {
					t.Fatal(e, p.Reasons)
				}
			}
			backups, _ := filepath.Glob(filepath.Join(root, ".system-sync/check/go-prepare-*"))
			if len(backups) != 1 {
				t.Fatal("missing transaction backup", backups)
			}
			b, _ := os.ReadFile(filepath.Join(backups[0], "transaction.json"))
			phase := "prepared"
			if failure != "" {
				phase = "rolled_back"
			}
			if !strings.Contains(string(b), phase) {
				t.Fatal(string(b))
			}
			l, e := syncstate.OperationLock(root)
			if e != nil {
				t.Fatal("preparation did not release shared lock", e)
			}
			l.Close()
		})
	}
}
func TestGoPreparationRechecksConcurrencyAndSharedLock(t *testing.T) {
	for _, race := range []string{"lock", "prompt", "resolve"} {
		t.Run(race, func(t *testing.T) {
			root := prepFixture(t, true)
			var calls []string
			input := io.Reader(strings.NewReader("yes\n"))
			failure := ""
			if race == "lock" {
				l, e := syncstate.OperationLock(root)
				if e != nil {
					t.Fatal(e)
				}
				defer l.Close()
			} else if race == "prompt" {
				input = &onRead{do: func() { prepWrite(t, root, goConfig, []byte("[tools]\ngo='9.9.9'\n")) }}
			} else {
				failure = "concurrent-config"
			}
			e := prepareGo(context.Background(), root, false, input, io.Discard, io.Discard, true, fakePrepare(t, root, failure, &calls))
			if e == nil || strings.Contains(strings.Join(calls, ","), "build.sh") {
				t.Fatal("concurrent changes were overwritten", e, calls)
			}
			if race != "lock" {
				b, _ := os.ReadFile(filepath.Join(root, goConfig))
				if !bytes.Contains(b, []byte("9.9.9")) {
					t.Fatal("overwrote concurrent user edit")
				}
			}
		})
	}
}

type onRead struct{ do func() }

func (r *onRead) Read(p []byte) (int, error) {
	r.do()
	return copy(p, "yes\n"), nil
}
func TestGoPreparationCancellationRollsBack(t *testing.T) {
	root := prepFixture(t, true)
	before := prepSnapshot(t, root)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls []string
	fake := fakePrepare(t, root, "", &calls)
	run := func(ctx context.Context, dir string, args, env []string, out, errout io.Writer) int {
		if filepath.Base(args[0]) == "build.sh" {
			prepArchives(t, root, "interrupted")
			cancel()
			return 130
		}
		return fake(ctx, dir, args, env, out, errout)
	}
	e := prepareGo(ctx, root, false, strings.NewReader("yes\n"), io.Discard, io.Discard, true, run)
	if e == nil || !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatal(e)
	}
	assertPrepSnapshot(t, root, before)
}

func TestGoPreparationRollbackDoesNotFollowNewSymlink(t *testing.T) {
	root := prepFixture(t, true)
	before := prepSnapshot(t, root)
	outside := filepath.Join(t.TempDir(), "keep")
	if e := os.WriteFile(outside, []byte("outside"), 0600); e != nil {
		t.Fatal(e)
	}
	blocked := "helper/prebuilt/v0.2.0/SOURCE_SHA256SUMS"
	var calls []string
	fake := fakePrepare(t, root, "", &calls)
	run := func(ctx context.Context, dir string, args, env []string, out, errout io.Writer) int {
		if filepath.Base(args[0]) == "build.sh" {
			prepArchives(t, root, "partial")
			if e := os.Remove(filepath.Join(root, blocked)); e != nil {
				t.Fatal(e)
			}
			if e := os.Symlink(outside, filepath.Join(root, blocked)); e != nil {
				t.Fatal(e)
			}
			return 8
		}
		return fake(ctx, dir, args, env, out, errout)
	}
	e := prepareGo(context.Background(), root, false, strings.NewReader("yes\n"), io.Discard, io.Discard, true, run)
	if e == nil || !strings.Contains(e.Error(), "回滚不完整") || !strings.Contains(e.Error(), "/before") {
		t.Fatal("missing recovery diagnostic", e)
	}
	b, _ := os.ReadFile(outside)
	if string(b) != "outside" {
		t.Fatal("rollback followed a symlink")
	}
	delete(before, blocked)
	assertPrepSnapshot(t, root, before)
	backups, _ := filepath.Glob(filepath.Join(root, ".system-sync/check/go-prepare-*"))
	if len(backups) != 1 {
		t.Fatal(backups)
	}
	b, _ = os.ReadFile(filepath.Join(backups[0], "transaction.json"))
	if !strings.Contains(string(b), "rollback_failed") {
		t.Fatal(string(b))
	}
}

func TestGoPreparationRollbackPreservesConcurrentToolUpgrade(t *testing.T) {
	root := prepFixture(t, true)
	before := prepSnapshot(t, root)
	var calls []string
	fake := fakePrepare(t, root, "", &calls)
	run := func(ctx context.Context, dir string, args, env []string, out, errout io.Writer) int {
		if filepath.Base(args[0]) == "build.sh" {
			prepArchives(t, root, "partial")
			prepWrite(t, root, goLock, bytes.ReplaceAll(prepLock(t, testGo), []byte("24.0.0"), []byte("25.0.0")))
			return 8
		}
		return fake(ctx, dir, args, env, out, errout)
	}
	e := prepareGo(context.Background(), root, false, strings.NewReader("yes\n"), io.Discard, io.Discard, true, run)
	if e == nil || !strings.Contains(e.Error(), "被其他进程修改，未覆盖") {
		t.Fatal("concurrent tool upgrade not reported", e)
	}
	b, _ := os.ReadFile(filepath.Join(root, goLock))
	if !bytes.Contains(b, []byte("25.0.0")) {
		t.Fatal("rollback overwrote a concurrent tool upgrade")
	}
	delete(before, goLock)
	assertPrepSnapshot(t, root, before)
}

func TestGoLockResolutionUsesOnlyIsolatedConfig(t *testing.T) {
	if _, e := exec.LookPath("mise"); e != nil {
		t.Skip("mise is required by the check task")
	}
	_, source, _, _ := runtime.Caller(0)
	repo := filepath.Clean(filepath.Join(filepath.Dir(source), "../../.."))
	b, e := os.ReadFile(filepath.Join(repo, goConfig))
	if e != nil {
		t.Fatal(e)
	}
	var config struct{ Tools struct{ Go string } }
	if e := toml.Unmarshal(b, &config); e != nil {
		t.Fatal(e)
	}
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	stage := filepath.Join(root, "isolated")
	prepWrite(t, root, "mise.toml", []byte("[tools]\nnode='99.0.0'\n[env]\n_.source='must-not-run.sh'\n"))
	prepWrite(t, stage, "mise.toml", []byte("[tools]\ngo='"+config.Tools.Go+"'\n"))
	b, e = os.ReadFile(filepath.Join(repo, goLock))
	if e != nil {
		t.Fatal(e)
	}
	prepWrite(t, stage, "mise.lock", b)
	before, _ := parseLock(b)
	t.Setenv("MISE_GLOBAL_CONFIG_FILE", filepath.Join(root, "broken-global.toml"))
	prepWrite(t, root, "broken-global.toml", []byte("[invalid"))
	t.Setenv("MISE_OVERRIDE_CONFIG_FILENAMES", "invalid.toml")
	t.Setenv("MISE_ENV", "private")
	t.Setenv("MISE_OFFLINE", "1")
	var out bytes.Buffer
	code := Execute(context.Background(), stage, []string{"mise", "lock", "--platform", lockPlatforms, "go"}, lockEnvironment(stage), &out, &out)
	if code != 0 {
		t.Fatal("isolated offline lock resolution failed", code, out.String())
	}
	b, e = os.ReadFile(filepath.Join(stage, "mise.lock"))
	if e != nil {
		t.Fatal(e)
	}
	after, e := parseLock(b)
	if e != nil || !goLockReady(after, config.Tools.Go) {
		t.Fatal("resolved lock not usable", e, out.String())
	}
	delete(before["tools"].(map[string]any), "go")
	delete(after["tools"].(map[string]any), "go")
	if !reflect.DeepEqual(before, after) {
		t.Fatal("mise changed unrelated tool locks", out.String())
	}
}
