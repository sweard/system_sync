// Package extensions reconciles public VS Code extensions without touching extras.
package extensions

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/sweard/system_sync/helper/internal/localconfig"
	"github.com/sweard/system_sync/helper/internal/taskexec"
)

var identifier = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*\.[a-z0-9][a-z0-9-]*$`)
var version = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[a-zA-Z0-9.-]+)?$`)

func Declarations(path, platform string) (map[string]string, error) {
	data, err := localconfig.Read(path)
	if err != nil {
		return nil, err
	}
	return declarations(data, platform, false)
}

func declarations(data map[string]any, platform string, allowEmpty bool) (map[string]string, error) {
	wanted := map[string]string{}
	for group, value := range data {
		if group != "common" && group != "macos" && group != "linux" {
			return nil, fmt.Errorf("扩展清单只支持 common、macos 和 linux 分组")
		}
		entries := localconfig.Table(value)
		if entries == nil {
			return nil, fmt.Errorf("扩展分组必须是表")
		}
		for name, value := range entries {
			v, ok := value.(string)
			if !identifier.MatchString(name) || !ok || !version.MatchString(v) {
				return nil, fmt.Errorf("扩展必须声明 publisher.name 和完整版本号")
			}
			if group == "common" || group == platform {
				if old, ok := wanted[name]; ok && old != v {
					return nil, fmt.Errorf("同一平台的扩展版本冲突")
				}
				wanted[name] = v
			}
		}
	}
	if len(wanted) == 0 && !allowEmpty {
		return nil, fmt.Errorf("当前平台的扩展清单为空，已停止")
	}
	return wanted, nil
}

type Invoker interface {
	Invoke([]string, bool, time.Duration) (string, error)
}
type Options struct {
	Action                                     string
	DryRun                                     bool
	Manifest, Code, UserDataDir, ExtensionsDir string
}

func invoke(r Invoker, command []string) (string, error) {
	output, err := r.Invoke(command, true, time.Hour)
	if err == nil {
		return output, nil
	}
	if errors.Is(err, context.Canceled) {
		return "", err
	}
	var failure *taskexec.Failure
	if errors.As(err, &failure) {
		if failure.Timeout {
			return "", fmt.Errorf("VS Code 命令超时，已停止本次 CLI 及其子进程；已安装项保留")
		}
		return "", fmt.Errorf("VS Code 命令失败（退出码 %d），请检查 CLI、网络或扩展可用性", failure.Code)
	}
	return "", fmt.Errorf("VS Code 命令失败，请检查 CLI、网络或扩展可用性")
}
func Installed(r Invoker, command []string) (map[string]string, error) {
	output, err := invoke(r, append(append([]string{}, command...), "--list-extensions", "--show-versions"))
	if err != nil {
		return nil, err
	}
	versions := map[string]string{}
	for _, line := range strings.Split(strings.TrimSuffix(output, "\n"), "\n") {
		if line == "" && output == "" {
			continue
		}
		line = strings.TrimSpace(line)
		index := strings.LastIndexByte(line, '@')
		if index < 0 {
			return nil, fmt.Errorf("VS Code 返回了无法识别的扩展列表，已停止")
		}
		name, v := strings.ToLower(line[:index]), line[index+1:]
		if !identifier.MatchString(name) || !version.MatchString(v) {
			return nil, fmt.Errorf("VS Code 返回了无法识别的扩展列表，已停止")
		}
		if _, ok := versions[name]; ok {
			return nil, fmt.Errorf("VS Code 返回重复扩展，已停止")
		}
		versions[name] = v
	}
	return versions, nil
}
func command(options Options) ([]string, error) {
	platform := localconfig.Platform()
	if platform == "" {
		return nil, fmt.Errorf("仅支持 macOS / Linux")
	}
	if (options.UserDataDir == "") != (options.ExtensionsDir == "") {
		return nil, fmt.Errorf("隔离验收必须同时指定 --user-data-dir 和 --extensions-dir")
	}
	code := options.Code
	if code == "" {
		code, _ = exec.LookPath("code")
	}
	if code == "" && platform == "macos" {
		code = "/Applications/Visual Studio Code.app/Contents/Resources/app/bin/code"
	}
	if !localconfig.IsFile(code) {
		return nil, fmt.Errorf("未找到 VS Code CLI，请安装 code 或用 --code 指定")
	}
	command := []string{code}
	if options.UserDataDir != "" {
		user, err := localconfig.RealPath(options.UserDataDir)
		if err != nil {
			return nil, err
		}
		ext, err := localconfig.RealPath(options.ExtensionsDir)
		if err != nil {
			return nil, err
		}
		command = append(command, "--user-data-dir", user, "--extensions-dir", ext)
	}
	return command, nil
}

func Run(options Options, runner Invoker, out io.Writer) error {
	wanted, err := Declarations(options.Manifest, localconfig.Platform())
	if err != nil {
		return err
	}
	command, err := command(options)
	if err != nil {
		return err
	}
	var progressOut io.Writer
	if options.Action == "sync" && !options.DryRun {
		progressOut = out
	}
	var current map[string]string
	err = taskexec.Progress(progressOut, "查询 VS Code 已安装扩展", func() error {
		var err error
		current, err = Installed(runner, command)
		return err
	})
	if err != nil {
		return err
	}
	names, pending := []string{}, []string{}
	for name := range wanted {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if current[name] != wanted[name] {
			old := current[name]
			if old == "" {
				old = "未安装"
			}
			fmt.Fprintf(out, "[!] %s: %s -> %s\n", name, old, wanted[name])
			pending = append(pending, name)
		}
	}
	extras := []string{}
	for name := range current {
		if _, ok := wanted[name]; !ok {
			extras = append(extras, name)
		}
	}
	sort.Strings(extras)
	if len(extras) > 0 {
		fmt.Fprintf(out, "[~] 保留 %d 个清单外扩展：\n", len(extras))
		for _, name := range extras {
			fmt.Fprintf(out, "  - %s@%s\n", name, current[name])
		}
	}
	if len(pending) == 0 {
		fmt.Fprintf(out, "[+] %d 个公共扩展均符合清单，无需同步。\n", len(wanted))
		return nil
	}
	if options.Action == "sync" && !options.DryRun {
		for i, name := range pending {
			label := fmt.Sprintf("安装 VS Code 扩展 %d/%d：%s@%s", i+1, len(pending), name, wanted[name])
			err := taskexec.Progress(out, label, func() error {
				_, err := invoke(runner, append(append([]string{}, command...), "--install-extension", name+"@"+wanted[name], "--force"))
				return err
			})
			if err != nil {
				return err
			}
		}
		var after map[string]string
		err := taskexec.Progress(out, "复查 VS Code 扩展安装版本", func() error {
			var err error
			after, err = Installed(runner, command)
			return err
		})
		if err != nil {
			return err
		}
		for name, v := range wanted {
			if after[name] != v {
				return fmt.Errorf("安装后版本与清单不符，请重新核对；清单未修改")
			}
		}
		fmt.Fprintf(out, "[+] 已同步 %d 个公共扩展\n", len(pending))
	}
	return nil
}
