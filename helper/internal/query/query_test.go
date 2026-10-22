package query

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestQueriesDoNotExposePrivateDiagnosticsOrForwardStdin(t *testing.T) {
	runner := ProcessRunner{Root: t.TempDir(), Environment: map[string]string{"PATH": "/usr/bin:/bin"}}
	output, err := runner.Run([]string{"/bin/sh", "-c", "printf private-stdout; printf private-stderr >&2; exit 7"}, nil)
	if err == nil || output != "" || strings.Contains(err.Error(), "private") {
		t.Fatalf("%q %v", output, err)
	}
	output, err = runner.Run([]string{"/bin/sh", "-c", "if read answer; then exit 9; fi; printf closed"}, nil)
	if err != nil || output != "closed" {
		t.Fatalf("stdin was not closed: %q %v", output, err)
	}
}

func TestTimeoutStopsDescendants(t *testing.T) {
	root := t.TempDir()
	sentinel := filepath.Join(root, "late-write")
	runner := ProcessRunner{Root: root, Environment: map[string]string{"PATH": "/usr/bin:/bin"}, Timeout: 50 * time.Millisecond}
	_, err := runner.Run([]string{"/bin/sh", "-c", `(sleep 0.2; echo private > "$1") & wait`, "fixture", sentinel}, nil)
	if err == nil || strings.Contains(err.Error(), "private") || !strings.Contains(err.Error(), "50ms") || strings.Contains(err.Error(), "60 秒") {
		t.Fatalf("%v", err)
	}
	time.Sleep(250 * time.Millisecond)
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatal("descendant wrote after timeout")
	}
}

func TestQueryUsesTheProvidedSearchPath(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "query-bin")
	os.Mkdir(bin, 0755)
	os.WriteFile(filepath.Join(bin, "fixture-command"), []byte("#!/bin/sh\nprintf selected"), 0755)
	runner := ProcessRunner{Root: root, Environment: map[string]string{"PATH": bin}}
	output, err := runner.Run([]string{"fixture-command"}, nil)
	if err != nil || output != "selected" {
		t.Fatalf("%q %v", output, err)
	}
}
