package taskexec

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCapturedQueriesHaveDeadlineButNativeOperationsKeepStreaming(t *testing.T) {
	var status, output bytes.Buffer
	runner := Runner{QueryTimeout: 40 * time.Millisecond, Progress: &status, Stdout: &output}
	command := []string{"/bin/sh", "-c", "sleep 0.15; printf finished"}
	_, err := runner.Run(command, true)
	var failure *Failure
	if !errors.As(err, &failure) || !failure.Timeout || failure.Limit != runner.QueryTimeout || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("captured query was not bounded: %v", err)
	}
	if !strings.Contains(status.String(), "查询 sh 状态") || output.Len() != 0 {
		t.Fatal("captured query had no progress or exposed native output")
	}
	status.Reset()
	if _, err := runner.Run(command, false); err != nil || output.String() != "finished" || status.Len() != 0 {
		t.Fatalf("native operation inherited the query deadline or lost stdout: %q %v", output.String(), err)
	}
}

func TestRedactedFailuresDistinguishCausesWithoutLeakingPrivateData(t *testing.T) {
	private := filepath.Join(t.TempDir(), "private-feedback-command")
	if err := os.WriteFile(private, []byte("#!/bin/sh\nexit 0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cases := []struct {
		name, want string
		runner     Runner
		command    []string
		identity   error
	}{
		{"missing", "命令不存在或不在 PATH", Runner{}, []string{private + "-missing"}, os.ErrNotExist},
		{"permission", "没有执行权限", Runner{}, []string{private}, os.ErrPermission},
		{"exit", "退出码 17", Runner{}, []string{"/bin/sh", "-c", "printf private-feedback-out; printf private-feedback-err >&2; exit 17"}, nil},
		{"timeout", "上限 40ms", Runner{QueryTimeout: 40 * time.Millisecond}, []string{"/bin/sh", "-c", "printf private-feedback-out; sleep 1"}, context.DeadlineExceeded},
		{"cancel", "已取消", Runner{Context: ctx}, []string{"/bin/sh", "-c", "printf private-feedback-out"}, context.Canceled},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			_, err := test.runner.Run(test.command, true)
			if err == nil {
				t.Fatal("fixture unexpectedly succeeded")
			}
			safe := Redacted(err)
			if !strings.Contains(safe.Error(), test.want) || strings.Contains(safe.Error(), "private-feedback") {
				t.Fatalf("unsafe or ambiguous diagnostic: %v", safe)
			}
			if test.identity != nil && !errors.Is(safe, test.identity) {
				t.Fatalf("lost original failure identity: %v", safe)
			}
		})
	}
}

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
