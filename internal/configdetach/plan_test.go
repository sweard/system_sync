package configdetach

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func fixture(t *testing.T) (*Plan, string) {
	t.Helper()
	base := t.TempDir()
	root, home := filepath.Join(base, "repo 中文"), filepath.Join(base, "home")
	for _, path := range []string{root, home} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	p, err := New(root, home)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a", "b"} {
		source := filepath.Join(p.Root, name)
		if err := os.WriteFile(source, []byte(name+"-private-content"), 0600); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(p.Home, name)
		if err := os.Symlink(source, target); err != nil {
			t.Fatal(err)
		}
		if err := p.links(target); err != nil {
			t.Fatal(err)
		}
	}
	return p, filepath.Join(p.Home, "state/backups")
}
func transaction(t *testing.T, backup string) map[string]any {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(backup, "*/transaction.json"))
	if err != nil || len(paths) != 1 {
		t.Fatalf("backups: %v %v", paths, err)
	}
	data, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if err = json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	return record
}
func TestFailedReplacementRestoresLinksAndPreservesPrivateBackups(t *testing.T) {
	p, backup := fixture(t)
	before := map[string]*Node{}
	for _, name := range []string{"a", "b"} {
		node, err := snapshot(filepath.Join(p.Home, name), "", false, nil)
		if err != nil {
			t.Fatal(err)
		}
		before[name] = node
	}
	failed := false
	p.rename = func(source, target string) error {
		if target == filepath.Join(p.Home, "b") && !failed {
			failed = true
			return errors.New("injected disk failure")
		}
		return os.Rename(source, target)
	}
	if _, err := p.Apply(context.Background(), backup, io.Discard); err == nil {
		t.Fatal("expected failure")
	}
	for name, node := range before {
		after, err := snapshot(filepath.Join(p.Home, name), "", false, nil)
		if err != nil || !reflect.DeepEqual(after, node) {
			t.Fatalf("%s was not restored: %v", name, err)
		}
	}
	if transaction(t, backup)["phase"] != "rolled_back" {
		t.Fatal("missing rollback journal")
	}
	if err := filepath.WalkDir(backup, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() && info.Mode().Perm() != 0600 {
			t.Errorf("backup not private: %s %o", path, info.Mode().Perm())
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	staging, _ := filepath.Glob(filepath.Join(p.Home, ".system-sync-*"))
	if len(staging) != 0 {
		t.Fatal(staging)
	}
}
func TestRollbackKeepsConcurrentDeletion(t *testing.T) {
	p, backup := fixture(t)
	failed := false
	p.rename = func(source, target string) error {
		if target == filepath.Join(p.Home, "b") && !failed {
			failed = true
			if err := os.Remove(filepath.Join(p.Home, "a")); err != nil {
				t.Fatal(err)
			}
			return errors.New("injected concurrent deletion")
		}
		return os.Rename(source, target)
	}
	if _, err := p.Apply(context.Background(), backup, io.Discard); err == nil {
		t.Fatal("expected failure")
	}
	if _, err := os.Lstat(filepath.Join(p.Home, "a")); !os.IsNotExist(err) {
		t.Fatal("concurrent deletion overwritten")
	}
	record := transaction(t, backup)
	if record["phase"] != "recovery_required" || !reflect.DeepEqual(record["conflicts"], []any{filepath.Join(p.Home, "a")}) {
		t.Fatal(record)
	}
	if !isLink(filepath.Join(p.Home, "b")) {
		t.Fatal("second target not restored")
	}
}
func isLink(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode()&os.ModeSymlink != 0
}
func TestSourceDriftLinkedParentsAndOutsideHomeFailBeforeWrites(t *testing.T) {
	p, backup := fixture(t)
	if err := os.WriteFile(filepath.Join(p.Root, "a"), []byte("new source"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Apply(context.Background(), backup, io.Discard); err == nil {
		t.Fatal("source drift ignored")
	}
	if _, err := os.Stat(backup); !os.IsNotExist(err) {
		t.Fatal("backup created before verification")
	}
	if _, err := p.target(filepath.Join(p.Home, "../outside")); err == nil {
		t.Fatal("outside HOME accepted")
	}
	external := t.TempDir()
	parent := filepath.Join(p.Home, "alias")
	if err := os.Symlink(external, parent); err != nil {
		t.Fatal(err)
	}
	if err := p.checkParents(filepath.Join(parent, "config")); err == nil {
		t.Fatal("linked parent accepted")
	}
}
func TestDirectoryMaterializationAndFailedDirectoryReplacement(t *testing.T) {
	for _, failure := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "rollback"}[failure], func(t *testing.T) {
			p, backup := fixture(t)
			p.Changes = nil
			source := filepath.Join(p.Root, "config")
			if err := os.Mkdir(source, 0755); err != nil {
				t.Fatal(err)
			}
			private := filepath.Join(source, "private")
			if err := os.WriteFile(private, []byte("private-directory-data"), 0600); err != nil {
				t.Fatal(err)
			}
			external := filepath.Join(p.Home, "user-file")
			if err := os.WriteFile(external, []byte("unmanaged"), 0644); err != nil {
				t.Fatal(err)
			}
			relative, err := filepath.Rel(source, external)
			if err != nil {
				t.Fatal(err)
			}
			for name, target := range map[string]string{"internal": private, "external": relative} {
				if err := os.Symlink(target, filepath.Join(source, name)); err != nil {
					t.Fatal(err)
				}
			}
			target := filepath.Join(p.Home, "config")
			if err := os.Symlink(source, target); err != nil {
				t.Fatal(err)
			}
			if err := p.links(target); err != nil {
				t.Fatal(err)
			}
			failed := false
			if failure {
				p.rename = func(staged, destination string) error {
					if destination == target && !failed {
						failed = true
						return errors.New("injected directory rename failure")
					}
					return os.Rename(staged, destination)
				}
			}
			_, err = p.Apply(context.Background(), backup, io.Discard)
			if failure {
				if err == nil || !isLink(target) || transaction(t, backup)["phase"] != "rolled_back" {
					t.Fatalf("directory not restored: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if isLink(target) || isLink(filepath.Join(target, "internal")) {
				t.Fatal("repo link retained")
			}
			if err = os.Rename(p.Root, p.Root+"-moved"); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"private", "internal"} {
				data, err := os.ReadFile(filepath.Join(target, name))
				if err != nil || string(data) != "private-directory-data" {
					t.Fatalf("%s: %v", name, err)
				}
				info, err := os.Stat(filepath.Join(target, name))
				if err != nil || info.Mode().Perm() != 0600 {
					t.Fatal("private permissions changed")
				}
			}
			resolved, err := filepath.EvalSymlinks(filepath.Join(target, "external"))
			if err != nil || resolved != external {
				t.Fatal("external link changed")
			}
		})
	}
}
func TestCancellationRollsBackChanges(t *testing.T) {
	p, backup := fixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	renamed := false
	p.rename = func(source, target string) error {
		err := os.Rename(source, target)
		if err == nil && !renamed {
			renamed = true
			cancel()
		}
		return err
	}
	if _, err := p.Apply(ctx, backup, io.Discard); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation: %v", err)
	}
	for _, name := range []string{"a", "b"} {
		if !isLink(filepath.Join(p.Home, name)) {
			t.Fatal("interrupted transaction left detached file")
		}
	}
	if transaction(t, backup)["phase"] != "rolled_back" {
		t.Fatal("interrupted transaction not journaled")
	}
}
func TestDirectoryCyclesAndUnsafeStateFailClosed(t *testing.T) {
	p, _ := fixture(t)
	cycle := filepath.Join(p.Root, "cycle")
	if err := os.Mkdir(cycle, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(cycle, filepath.Join(cycle, "again")); err != nil {
		t.Fatal(err)
	}
	if _, err := snapshot(cycle, p.Root, true, nil); err == nil {
		t.Fatal("directory cycle accepted")
	}
	for _, data := range []map[string]any{{}, {"files": []any{map[string]any{}}, "edits": []any{}}, {"files": []any{map[string]any{"origin": map[string]any{"config": filepath.Join(p.Root, "mise.toml")}, "mode": "future", "target": filepath.Join(p.Home, "future")}}, "edits": []any{}}} {
		if err := p.Collect(data, filepath.Join(p.Home, "absent")); err == nil {
			t.Fatal("unsafe state accepted")
		}
	}
}
