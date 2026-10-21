// Package syncstate owns the cross-process package lock and transaction journal.
// The lock inode and inherited descriptor protocol are compatible with the
// Python reference implementations retained for regression tests.
package syncstate

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/sweard/system_sync/helper/internal/terminal"
)

const MinMise = "2026.10.11"

func Preflight(root string) (string, error) {
	if _, err := exec.LookPath("mise"); err != nil {
		return "", fmt.Errorf("找不到 mise；需要 mise >= %s", MinMise)
	}
	output, err := exec.Command("mise", "--version").Output()
	match := regexp.MustCompile(`^(?:mise\s+)?([0-9]+)\.([0-9]+)\.([0-9]+)\b`).FindStringSubmatch(string(output))
	valid := err == nil && len(match) == 4
	if valid {
		required := []int{2026, 10, 11}
		for i, wanted := range required {
			got, _ := strconv.Atoi(match[i+1])
			if got > wanted {
				break
			}
			if got < wanted {
				valid = false
				break
			}
		}
	}
	if !valid {
		return "", fmt.Errorf("需要 mise >= %s；请先升级 mise", MinMise)
	}
	output, err = exec.Command("uname", "-s").Output()
	if err != nil {
		return "", fmt.Errorf("无法识别当前平台")
	}
	profile := ""
	commands := []string{}
	switch strings.TrimSpace(string(output)) {
	case "Darwin":
		profile = "macos"
		commands = []string{"brew"}
	case "Linux":
		output, err = exec.Command("bash", filepath.Join(root, "scripts/detect-linux-profile.sh")).Output()
		if err != nil {
			return "", fmt.Errorf("无法识别 Linux profile")
		}
		profile = strings.TrimSpace(string(output))
		switch profile {
		case "arch":
			commands = []string{"pacman"}
		case "debian":
			commands = []string{"apt-get", "apt-mark", "dpkg-query"}
		case "fedora":
			commands = []string{"dnf", "rpm"}
		case "opensuse":
			commands = []string{"zypper", "rpm"}
		default:
			return "", fmt.Errorf("未知的 Linux profile：%s", profile)
		}
	default:
		return "", fmt.Errorf("不支持的平台")
	}
	missing := []string{}
	for _, name := range append([]string{"bash", "comm"}, commands...) {
		if _, err := exec.LookPath(name); err != nil {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return "", fmt.Errorf("缺少前置命令：%s", strings.Join(missing, ", "))
	}
	return profile, nil
}

func InheritedLock(root string) (int, bool) {
	fd, err := strconv.Atoi(os.Getenv("SYSTEM_SYNC_LOCK_FD"))
	if err != nil || fd < 3 {
		return 0, false
	}
	var actual, expected syscall.Stat_t
	if syscall.Fstat(fd, &actual) != nil || syscall.Stat(filepath.Join(root, ".system-sync/operation.lock"), &expected) != nil || actual.Dev != expected.Dev || actual.Ino != expected.Ino {
		return 0, false
	}
	return fd, syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB) == nil
}

type Lock struct {
	File  *os.File
	owned bool
}

func (lock *Lock) Close() error {
	if lock.owned {
		return lock.File.Close()
	}
	return nil
}

func OperationLock(root string) (*Lock, error) {
	if fd, ok := InheritedLock(root); ok {
		// Own a duplicate rather than wrapping the caller's descriptor with a
		// second os.File finalizer. The duplicate preserves the same flock.
		duplicate, err := syscall.Dup(fd)
		if err != nil {
			return nil, err
		}
		syscall.CloseOnExec(duplicate)
		return &Lock{os.NewFile(uintptr(duplicate), "inherited operation lock"), true}, nil
	}
	state := filepath.Join(root, ".system-sync")
	for _, p := range []string{state, filepath.Join(state, "operation.lock")} {
		if info, err := os.Lstat(p); err == nil && info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("%s 不能是符号链接", filepath.Base(p))
		}
	}
	if err := os.MkdirAll(state, 0755); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(state, "operation.lock"), os.O_CREATE|os.O_APPEND|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		return nil, fmt.Errorf("另一条配置或包操作正在运行（config-sync/vscode-status --capture/machine-init/package-install/package-uninstall/system-sync/upgrade），请稍后重试")
	}
	return &Lock{file, true}, nil
}

func Journal(directory, phase string, fields map[string]any) error {
	target := filepath.Join(directory, "transaction.json")
	content := map[string]any{}
	if before, err := os.ReadFile(target); err == nil {
		if json.Unmarshal(before, &content) != nil {
			return fmt.Errorf("事务日志无法读取")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if previous, ok := content["phase"].(string); ok && previous != "" && previous != phase {
		content["last_phase"] = previous
	}
	for k, v := range fields {
		content[k] = v
	}
	content["phase"] = phase
	content["updated_utc"] = time.Now().UTC().Format(time.RFC3339Nano)
	file, err := os.CreateTemp(directory, ".journal-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(content); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), target)
}

func copyFile(source, target string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func RunGuarded(root string, command []string, stdin *os.File, stdout, stderr io.Writer) (int, error) {
	if !terminal.IsTerminal(stdin) {
		return 1, fmt.Errorf("修改系统包必须在交互式终端运行；尚未执行安装、卸载或升级")
	}
	if len(command) == 0 {
		return 2, fmt.Errorf("缺少要执行的命令")
	}
	profile, err := Preflight(root)
	if err != nil {
		return 1, err
	}
	lock, err := OperationLock(root)
	if err != nil {
		return 1, err
	}
	defer lock.Close()
	history := filepath.Join(root, ".system-sync/history")
	if err := os.MkdirAll(history, 0755); err != nil {
		return 1, err
	}
	directory, err := os.MkdirTemp(history, time.Now().UTC().Format("20060102T150405Z-")+profile+"-")
	if err != nil {
		return 1, err
	}
	if err := Journal(directory, "preflight", map[string]any{"command": command, "profile": profile}); err != nil {
		return 1, err
	}
	fmt.Fprintln(stderr, "==> 全程审计："+directory)
	packageDir := filepath.Join(root, "packages/macos")
	if profile != "macos" {
		packageDir = filepath.Join(root, "packages/linux", profile)
	}
	entries, err := os.ReadDir(packageDir)
	if err != nil {
		_ = Journal(directory, "failed", nil)
		return 1, err
	}
	for _, entry := range entries {
		source := filepath.Join(packageDir, entry.Name())
		info, err := os.Stat(source)
		if err != nil {
			_ = Journal(directory, "failed", nil)
			return 1, err
		}
		if info.Mode().IsRegular() {
			if err := copyFile(source, filepath.Join(directory, entry.Name()+".before")); err != nil {
				_ = Journal(directory, "failed", nil)
				return 1, err
			}
		}
	}
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.ExtraFiles = []*os.File{lock.File}
	cmd.Env = append(os.Environ(), "SYSTEM_SYNC_LOCK_FD=3", "SYSTEM_SYNC_RUN_LOG_DIR="+directory)
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	if err := cmd.Start(); err != nil {
		_ = Journal(directory, "failed", nil)
		return 1, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	code := 0
	interrupted := false
	waiting := true
	for waiting {
		select {
		case sig := <-signals:
			interrupted = true
			_ = cmd.Process.Signal(sig)
		case err := <-done:
			waiting = false
			if err != nil {
				if exit, ok := err.(*exec.ExitError); ok {
					code = exit.ExitCode()
					if status, ok := exit.Sys().(syscall.WaitStatus); ok && status.Signaled() {
						code = 128 + int(status.Signal())
					}
				} else {
					code = 1
				}
			}
		}
	}
	if interrupted {
		code = 130
		_ = Journal(directory, "interrupted", map[string]any{"exit_code": code})
		return code, nil
	}
	if code == 0 {
		data, err := os.ReadFile(filepath.Join(directory, "transaction.json"))
		content := map[string]any{}
		if err != nil || json.Unmarshal(data, &content) != nil || content["phase"] != "complete" {
			fmt.Fprintln(stderr, "错误：平台脚本未记录事务完成，按失败处理；请检查审计记录")
			code = 1
		}
	}
	phase := "failed"
	if code == 0 {
		phase = "complete"
	} else if code == 2 {
		phase = "cancelled"
	}
	if err := Journal(directory, phase, map[string]any{"exit_code": code}); err != nil {
		return 1, err
	}
	return code, nil
}
