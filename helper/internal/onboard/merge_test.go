package onboard

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMergeAcrossFiveProfilesPreservesManifestAndBacksUp(t *testing.T) {
	for _, profile := range []string{"macos", "arch", "debian", "fedora", "opensuse"} {
		t.Run(profile, func(t *testing.T) {
			f := fixture(t, profile)
			c, err := f.e.Capture()
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(f.e.Root, c.Candidate.Path)
			before := c.Sources[path]
			if err := os.Chmod(path, 0640); err != nil {
				t.Fatal(err)
			}
			directory, err := f.e.Merge(context.Background(), c, io.Discard)
			if err != nil {
				t.Fatal(err)
			}
			after, err := readManifest(path)
			if err != nil || after != c.Candidate.Content || !strings.Contains(after, "missing-tool") || strings.Contains(after, "orphan-lib") {
				t.Fatalf("merge lost declarations or included dependencies: %s %v", after, err)
			}
			backup, err := os.ReadFile(filepath.Join(directory, filepath.Base(path)+".before"))
			if err != nil || string(backup) != before {
				t.Fatalf("missing original backup: %q %v", backup, err)
			}
			for _, file := range []string{directory, filepath.Join(directory, filepath.Base(path)+".before"), filepath.Join(directory, "transaction.json")} {
				info, err := os.Stat(file)
				if err != nil || info.Mode().Perm()&0077 != 0 {
					t.Fatalf("unrestricted backup: %s %v", file, err)
				}
			}
			data, err := os.ReadFile(filepath.Join(directory, "transaction.json"))
			var journal map[string]any
			if err != nil || json.Unmarshal(data, &journal) != nil || journal["phase"] != "complete" || journal["manifest"] != c.Candidate.Path {
				t.Fatalf("missing completed journal: %s %v", data, err)
			}
			info, err := os.Stat(path)
			if err != nil || info.Mode().Perm() != 0640 {
				t.Fatalf("manifest permissions changed: %v", err)
			}
			again, err := f.e.Capture()
			if err != nil || again.Candidate.Diff != "" {
				t.Fatalf("merge was not idempotent: %+v %v", again, err)
			}
			if dir, err := f.e.Merge(context.Background(), again, io.Discard); err != nil || dir != "" {
				t.Fatalf("no-op merge created a backup: %q %v", dir, err)
			}
			current, _ := os.Stat(path)
			dirs, _ := filepath.Glob(filepath.Join(f.e.Root, ".system-sync/capture/*"))
			if !current.ModTime().Equal(info.ModTime()) || len(dirs) != 1 || len(f.mutations) != 0 {
				t.Fatal("no-op rewrote the manifest, created a backup or mutated packages")
			}
		})
	}
}

func TestMergeSkipsForeignAndUntrustedHomebrewRoots(t *testing.T) {
	for _, profile := range []string{"arch", "macos"} {
		t.Run(profile, func(t *testing.T) {
			f := fixture(t, profile)
			unknown := "foreign-tool"
			if profile == "arch" {
				f.foreign = []string{unknown}
			} else {
				unknown = "vendor/tap/third-party"
				f.roots = append(f.roots, unknown)
			}
			f.installed = append(f.installed, unknown)
			c, err := f.e.Capture()
			if err != nil || len(c.Pending) == 0 {
				t.Fatalf("unknown source not reported: %v", err)
			}
			if _, err := f.e.Merge(context.Background(), c, io.Discard); err != nil {
				t.Fatal(err)
			}
			content, _ := readManifest(filepath.Join(f.e.Root, c.Candidate.Path))
			if strings.Contains(content, unknown) || !strings.Contains(content, "local-tool") || len(f.mutations) != 0 {
				t.Fatal("merge expanded trust, omitted known root or mutated packages")
			}
		})
	}
}

func TestMergeRefusesChangedInputsBeforeSavingOrWriting(t *testing.T) {
	for _, change := range []string{"manifest", "new-override", "installed", "roots", "foreign", "orphans", "query-failure", "cancel", "symlink"} {
		t.Run(change, func(t *testing.T) {
			f := fixture(t, "arch")
			c, err := f.e.Capture()
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(f.e.Root, c.Candidate.Path)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch change {
			case "manifest":
				write(t, path, c.Sources[path]+"# concurrent edit\n")
			case "new-override":
				write(t, filepath.Join(filepath.Dir(path), "mise.local.toml"), "[env]\nLOCAL=\"new\"\n")
			case "installed":
				f.installed = append(f.installed, "new-dependency")
			case "roots":
				f.roots = []string{"git"}
			case "foreign":
				f.foreign = []string{"orphan-lib"}
			case "orphans":
				f.orphans = []string{"orphan-lib"}
			case "query-failure":
				f.failQuery = "pacman -Qq"
			case "cancel":
				cancel()
			case "symlink":
				copy := filepath.Join(t.TempDir(), "manifest")
				write(t, copy, c.Sources[path])
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(copy, path); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := os.ReadFile(path)
			_, err = f.e.Merge(ctx, c, io.Discard)
			if err == nil || strings.Contains(err.Error(), "private-query-diagnostic") {
				t.Fatalf("changed capture accepted or private diagnostics leaked: %v", err)
			}
			if change == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation identity lost")
			}
			after, _ := os.ReadFile(path)
			if string(after) != string(before) || len(f.mutations) != 0 {
				t.Fatal("merge overwrote a concurrent change or mutated packages")
			}
			if _, err := os.Stat(filepath.Join(f.e.Root, ".system-sync/capture")); !os.IsNotExist(err) {
				t.Fatal("stale merge saved a capture")
			}
		})
	}
}

func TestMergeBackupAndWriteFailuresPreserveOriginal(t *testing.T) {
	for _, failure := range []string{"backup", "write", "directory-link"} {
		t.Run(failure, func(t *testing.T) {
			f := fixture(t, "arch")
			c, err := f.e.Capture()
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(f.e.Root, c.Candidate.Path)
			switch failure {
			case "backup":
				if err := os.Symlink(t.TempDir(), filepath.Join(f.e.Root, ".system-sync")); err != nil {
					t.Fatal(err)
				}
			case "write":
				if os.Geteuid() == 0 {
					t.Skip("root bypasses directory write permissions")
				}
				if err := os.Chmod(filepath.Dir(path), 0500); err != nil {
					t.Fatal(err)
				}
				defer os.Chmod(filepath.Dir(path), 0700)
			case "directory-link":
				real := filepath.Join(t.TempDir(), "arch")
				if err := os.Rename(filepath.Dir(path), real); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(real, filepath.Dir(path)); err != nil {
					t.Fatal(err)
				}
			}
			directory, err := f.e.Merge(context.Background(), c, io.Discard)
			if err == nil {
				t.Fatal("failed merge unexpectedly succeeded")
			}
			current, _ := os.ReadFile(path)
			if string(current) != c.Sources[path] || len(f.mutations) != 0 {
				t.Fatal("failed merge changed the manifest or packages")
			}
			if failure == "write" {
				backup, _ := os.ReadFile(filepath.Join(directory, "mise.toml.before"))
				data, _ := os.ReadFile(filepath.Join(directory, "transaction.json"))
				var journal map[string]any
				if string(backup) != c.Sources[path] || json.Unmarshal(data, &journal) != nil || journal["phase"] != "needs-review" {
					t.Fatal("write failure lost recovery data")
				}
			}
		})
	}
}
