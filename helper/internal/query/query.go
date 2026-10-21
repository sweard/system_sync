// Package query runs read-only status commands without forwarding private output.
package query

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type Error string

func (e Error) Error() string { return string(e) }

type Runner interface {
	Run(command []string, environment map[string]string) (string, error)
}

type ProcessRunner struct {
	Root        string
	Environment map[string]string
	Timeout     time.Duration
}

func Environment() map[string]string {
	result := map[string]string{}
	for _, item := range os.Environ() {
		name, value, _ := strings.Cut(item, "=")
		result[name] = value
	}
	return result
}

func EnvList(environment map[string]string) []string {
	result := make([]string, 0, len(environment))
	for name, value := range environment {
		result = append(result, name+"="+value)
	}
	return result
}

func (r ProcessRunner) Run(command []string, environment map[string]string) (string, error) {
	if len(command) == 0 {
		return "", Error("查询命令不可用；该项尚未核对")
	}
	timeout := r.Timeout
	if timeout == 0 {
		timeout = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if environment == nil {
		environment = r.Environment
	}
	program, err := findCommand(command[0], r.Root, environment)
	if err != nil {
		return "", Error("查询命令不可用；该项尚未核对")
	}
	cmd := exec.CommandContext(ctx, program, command[1:]...)
	cmd.Dir = r.Root
	cmd.Env = EnvList(environment)
	// A shell status command can have descendants. A timeout must stop the whole
	// query tree, and WaitDelay also bounds a pipe inherited by a descendant.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = time.Second
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", Error("查询超过 60 秒；该项尚未核对")
		}
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return "", Error(fmt.Sprintf("查询退出码为 %d；请单独运行对应状态命令检查", exit.ExitCode()))
		}
		return "", Error("查询命令不可用；该项尚未核对")
	}
	return strings.ToValidUTF8(stdout.String(), "�"), nil
}

func findCommand(name, root string, environment map[string]string) (string, error) {
	if strings.ContainsRune(name, filepath.Separator) {
		if filepath.IsAbs(name) {
			return name, nil
		}
		return filepath.Join(root, name), nil
	}
	path, present := environment["PATH"]
	if !present {
		path = "/bin:/usr/bin"
	}
	for _, directory := range strings.Split(path, string(os.PathListSeparator)) {
		if !filepath.IsAbs(directory) {
			directory = filepath.Join(root, directory)
		}
		candidate := filepath.Join(directory, name)
		info, err := os.Stat(candidate)
		if err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0 {
			return candidate, nil
		}
	}
	return "", exec.ErrNotFound
}
