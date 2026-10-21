package syncstate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestLocksRejectConflictsAndForgedInheritedDescriptors(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SYSTEM_SYNC_LOCK_FD", "")
	first, err := OperationLock(root)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if second, err := OperationLock(root); err == nil {
		second.Close()
		t.Fatal("concurrent operation acquired lock")
	}
	unrelated, err := os.CreateTemp(t.TempDir(), "unrelated")
	if err != nil {
		t.Fatal(err)
	}
	defer unrelated.Close()
	t.Setenv("SYSTEM_SYNC_LOCK_FD", strconv.Itoa(int(unrelated.Fd())))
	if _, ok := InheritedLock(root); ok {
		t.Fatal("accepted wrong inode")
	}
	t.Setenv("SYSTEM_SYNC_LOCK_FD", strconv.Itoa(int(first.File.Fd())))
	if _, ok := InheritedLock(root); !ok {
		t.Fatal("rejected inherited lock")
	}
	second, err := OperationLock(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SYSTEM_SYNC_LOCK_FD", "")
	if third, err := OperationLock(root); err == nil {
		third.Close()
		t.Fatal("closing a borrowed duplicate released the caller's lock")
	}
}

func TestLockRejectsLinkedStateAndLockFile(t *testing.T) {
	t.Setenv("SYSTEM_SYNC_LOCK_FD", "")
	for _, linkedState := range []bool{true, false} {
		root := t.TempDir()
		target := t.TempDir()
		if linkedState {
			if err := os.Symlink(target, filepath.Join(root, ".system-sync")); err != nil {
				t.Fatal(err)
			}
		} else {
			os.Mkdir(filepath.Join(root, ".system-sync"), 0755)
			file := filepath.Join(target, "lock")
			os.WriteFile(file, nil, 0600)
			os.Symlink(file, filepath.Join(root, ".system-sync/operation.lock"))
		}
		if lock, err := OperationLock(root); err == nil {
			lock.Close()
			t.Fatal("accepted symlink")
		}
	}
}

func TestJournalKeepsPreviousPhaseAndWritesAtomically(t *testing.T) {
	directory := t.TempDir()
	if err := Journal(directory, "preflight", map[string]any{"profile": "debian"}); err != nil {
		t.Fatal(err)
	}
	if err := Journal(directory, "installing", nil); err != nil {
		t.Fatal(err)
	}
	if err := Journal(directory, "failed", map[string]any{"exit_code": 18}); err != nil {
		t.Fatal(err)
	}
	var data map[string]any
	content, _ := os.ReadFile(filepath.Join(directory, "transaction.json"))
	if err := json.Unmarshal(content, &data); err != nil {
		t.Fatal(err)
	}
	if data["phase"] != "failed" || data["last_phase"] != "installing" || data["profile"] != "debian" {
		t.Fatalf("%v", data)
	}
	staging, _ := filepath.Glob(filepath.Join(directory, ".journal-*"))
	if len(staging) != 0 {
		t.Fatal("left temporary journals")
	}
}

func TestPreflightRejectsOldMiseBeforeQueryingPlatform(t *testing.T) {
	bin := t.TempDir()
	log := filepath.Join(t.TempDir(), "queries")
	t.Setenv("PATH", bin)
	write := func(name, script string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+script), 0755); err != nil {
			t.Fatal(err)
		}
	}
	write("mise", "printf '2026.10.10 fixture\\n'\n")
	write("uname", fmt.Sprintf("printf touched > %q\n", log))
	if _, err := Preflight(t.TempDir()); err == nil {
		t.Fatal("accepted old mise")
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Fatal("queried native platform before rejecting old mise")
	}
}
