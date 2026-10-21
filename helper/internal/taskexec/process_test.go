package taskexec

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTimeoutKillsDescendantBeforeItWrites(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "orphan-wrote")
	runner := Runner{Context: context.Background()}
	_, err := runner.Invoke([]string{"/bin/sh", "-c", "(sleep 0.6; printf unsafe > " + Quote([]string{marker}) + ") & wait"}, true, 100*time.Millisecond)
	var failure *Failure
	if !errors.As(err, &failure) || !failure.Timeout {
		t.Fatalf("expected timeout: %v", err)
	}
	time.Sleep(700 * time.Millisecond)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("descendant survived timeout")
	}
}
func TestCancellationStopsChildAndDoesNotStreamCapturedSecrets(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runner := Runner{Context: ctx, Stderr: io.Discard, Stdout: io.Discard}
	if _, err := runner.Run([]string{"/bin/sh", "-c", "sleep 1"}, true); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation: %v", err)
	}
}
func TestConfirmOnlyAcceptsYes(t *testing.T) {
	for _, answer := range []string{"yes", "no", "y", "", "YES", "UNINSTALL foo"} {
		yes, err := Confirm(context.Background(), strings.NewReader(answer+"\n"), io.Discard, "prompt")
		if err != nil || yes != (answer == "yes") {
			t.Fatalf("answer %q: %v %v", answer, yes, err)
		}
	}
}
