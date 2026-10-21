// Package taskexec runs the public CLIs used by write workflows.
package taskexec

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

type Failure struct {
	Command []string
	Code    int
	Output  string
	Cause   error
	Timeout bool
}

func (e *Failure) Error() string {
	return fmt.Sprintf("命令失败（%d）：%s\n%s", e.Code, Quote(e.Command), strings.TrimSpace(e.Output))
}
func (e *Failure) Unwrap() error { return e.Cause }

type Runner struct {
	Context        context.Context
	Dir            string
	Env            []string
	Stdin          io.Reader
	Stdout, Stderr io.Writer
}

func (r Runner) Run(command []string, capture bool) (string, error) {
	return r.Invoke(command, capture, 0)
}
func (r Runner) Invoke(command []string, capture bool, timeout time.Duration) (string, error) {
	ctx := r.Context
	if ctx == nil {
		ctx = context.Background()
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	if len(command) == 0 {
		return "", fmt.Errorf("缺少命令")
	}
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Dir, cmd.Env = r.Dir, r.Env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = time.Second
	var stdout, stderr bytes.Buffer
	if capture {
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
	} else {
		cmd.Stdin, cmd.Stdout, cmd.Stderr = r.Stdin, r.Stdout, r.Stderr
	}
	err := cmd.Run()
	if err != nil {
		code := 1
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			code = exit.ExitCode()
		}
		cause := err
		if ctx.Err() != nil {
			cause = ctx.Err()
		}
		return "", &Failure{Command: command, Code: code, Output: stderr.String() + stdout.String(), Cause: cause, Timeout: errors.Is(ctx.Err(), context.DeadlineExceeded)}
	}
	return stdout.String(), nil
}

func Confirm(ctx context.Context, input io.Reader, output io.Writer, prompt string) (bool, error) {
	fmt.Fprint(output, prompt)
	type answer struct {
		line string
		err  error
	}
	done := make(chan answer, 1)
	go func() { line, err := bufio.NewReader(input).ReadString('\n'); done <- answer{line, err} }()
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	case result := <-done:
		if result.err != nil && result.err != io.EOF {
			return false, result.err
		}
		return strings.TrimSpace(result.line) == "yes", nil
	}
}

func Quote(command []string) string {
	parts := make([]string, len(command))
	for i, value := range command {
		if value != "" && !strings.ContainsAny(value, " \t\r\n'\"\\$`;&|<>(){}[]*?!~") {
			parts[i] = value
		} else {
			parts[i] = "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
		}
	}
	return strings.Join(parts, " ")
}
