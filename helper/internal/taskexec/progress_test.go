package taskexec

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

type phaseOutput struct {
	mu     sync.Mutex
	buffer bytes.Buffer
	lines  chan string
}

func (w *phaseOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	n, err := w.buffer.Write(p)
	w.mu.Unlock()
	w.lines <- string(p)
	return n, err
}

func (w *phaseOutput) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buffer.String()
}

func TestProgressIsVisibleBeforeCompletionAndKeepsErrorsPrivate(t *testing.T) {
	for _, failure := range []error{nil, errors.New("private-command-diagnostic"), context.Canceled} {
		out := &phaseOutput{lines: make(chan string, 4)}
		ticks := make(chan time.Time)
		result := progress(out, "安全阶段名称", ticks, func() error {
			if line := <-out.lines; !strings.Contains(line, "[~] 安全阶段名称") {
				t.Fatal("phase was not visible before work started", line)
			}
			ticks <- time.Now()
			select {
			case line := <-out.lines:
				if !strings.Contains(line, "仍在执行") {
					t.Fatal("no ongoing status while work is blocked", line)
				}
			case <-time.After(time.Second):
				t.Fatal("progress did not arrive until after completion")
			}
			return failure
		})
		if result != failure || strings.Contains(out.String(), "private-command-diagnostic") {
			t.Fatal("error changed or private diagnostic leaked", result, out.String())
		}
		select {
		case ticks <- time.Now():
			t.Fatal("progress worker survived the operation")
		default:
		}
	}
}

func TestProgressCanBeDisabledForStructuredOutput(t *testing.T) {
	for _, out := range []io.Writer{nil, io.Discard} {
		called := 0
		failure := errors.New("query failed")
		if err := Progress(out, "must stay quiet", func() error { called++; return failure }); err != failure || called != 1 {
			t.Fatal("quiet mode changed operation behavior", err, called)
		}
	}
}
