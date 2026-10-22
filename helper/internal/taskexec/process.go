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
	"path/filepath"
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
	Limit   time.Duration
}

func (e *Failure) Error() string {
	message := fmt.Sprintf("命令%s：%s", Redacted(e), Quote(e.Command))
	if !e.Timeout && !errors.Is(e.Cause, context.Canceled) && strings.TrimSpace(e.Output) != "" {
		message += "\n" + strings.TrimSpace(e.Output)
	}
	return message
}
func (e *Failure) Unwrap() error { return e.Cause }

// Redacted retains the error identity and actionable failure category without
// exposing command arguments, captured output or native error text.
func Redacted(err error) error { return diagnostic{err} }

type diagnostic struct{ error }

func (e diagnostic) Unwrap() error { return e.error }
func (e diagnostic) Error() string {
	var failure *Failure
	if errors.As(e.error, &failure) && failure.Timeout {
		if failure.Limit <= 0 {
			return "超时，已停止命令及其子进程"
		}
		return fmt.Sprintf("超时（上限 %s），已停止命令及其子进程", failure.Limit)
	}
	if errors.Is(e.error, context.Canceled) {
		return "已取消"
	}
	if errors.Is(e.error, context.DeadlineExceeded) {
		return "超时"
	}
	if errors.Is(e.error, exec.ErrNotFound) || errors.Is(e.error, os.ErrNotExist) {
		return "无法启动：命令不存在或不在 PATH 中"
	}
	if errors.Is(e.error, os.ErrPermission) {
		return "无法启动：没有执行权限"
	}
	if failure != nil && failure.Code > 0 {
		return fmt.Sprintf("失败（退出码 %d）", failure.Code)
	}
	if failure != nil && failure.Code < 0 {
		return "被信号终止"
	}
	return "失败，请检查对应命令的运行环境"
}

type Runner struct {
	Context        context.Context
	Dir            string
	Env            []string
	Stdin          io.Reader
	Stdout, Stderr io.Writer
	QueryTimeout   time.Duration
	Progress       io.Writer
}

func (r Runner) Run(command []string, capture bool) (string, error) {
	limit := time.Duration(0)
	if capture {
		limit = r.QueryTimeout
		if limit <= 0 {
			limit = time.Minute
		}
	}
	var output string
	var err error
	operation := func() error {
		output, err = r.Invoke(command, capture, limit)
		return err
	}
	if capture && len(command) > 0 {
		err = Progress(r.Progress, "查询 "+filepath.Base(command[0])+" 状态", operation)
	} else {
		err = operation()
	}
	return output, err
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
		return "", &Failure{Command: command, Code: code, Output: stderr.String() + stdout.String(), Cause: cause, Timeout: errors.Is(ctx.Err(), context.DeadlineExceeded), Limit: timeout}
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
