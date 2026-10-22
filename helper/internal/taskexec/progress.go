package taskexec

import (
	"fmt"
	"io"
	"time"
)

// Progress reports a trusted phase label while a silent operation runs. The
// operation must not write to out concurrently. Captured output and errors are
// never printed here: callers retain their existing parsing and redaction.
func Progress(out io.Writer, label string, operation func() error) error {
	if out == nil || out == io.Discard {
		return operation()
	}
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	return progress(out, label, ticker.C, operation)
}

func progress(out io.Writer, label string, ticks <-chan time.Time, operation func() error) error {
	fmt.Fprintln(out, "[~] "+label)
	done, stopped := make(chan struct{}), make(chan struct{})
	started := time.Now()
	go func() {
		defer close(stopped)
		for {
			select {
			case <-done:
				return
			case <-ticks:
				elapsed := int(time.Since(started).Seconds())
				fmt.Fprintf(out, "[~] %s（仍在执行，已等待 %d 秒）\n", label, elapsed)
			}
		}
	}()
	defer func() { close(done); <-stopped }()
	return operation()
}
