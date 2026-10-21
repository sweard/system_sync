package helpercli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/sweard/system_sync/helper/internal/configdetach"
	"github.com/sweard/system_sync/helper/internal/confighistory"
	"github.com/sweard/system_sync/helper/internal/extensions"
	"github.com/sweard/system_sync/helper/internal/localconfig"
	"github.com/sweard/system_sync/helper/internal/packageedit"
	"github.com/sweard/system_sync/helper/internal/packagestate"
	"github.com/sweard/system_sync/helper/internal/query"
	"github.com/sweard/system_sync/helper/internal/syncstate"
	"github.com/sweard/system_sync/helper/internal/taskexec"
	"github.com/sweard/system_sync/helper/internal/terminal"
)

// Match the original tasks' argparse convention: options can follow arguments.
func interspersed(args []string, values map[string]bool) []string {
	flags, positional := []string{}, []string{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if strings.HasPrefix(arg, "-") && arg != "-" {
			flags = append(flags, arg)
			name, _, assigned := strings.Cut(arg, "=")
			if !assigned && values[name] && i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
		} else {
			positional = append(positional, arg)
		}
	}
	return append(append(flags, "--"), positional...)
}

func writer(command string, args []string, root string, stdin *os.File, stdout, stderr io.Writer) (int, error) {
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.SetOutput(stderr)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	env := query.Environment()
	env["MISE_AUTO_INSTALL"], env["MISE_TASK_RUN_AUTO_INSTALL"] = "false", "false"
	runner := taskexec.Runner{Context: ctx, Env: query.EnvList(env), Stdin: stdin, Stdout: stdout, Stderr: stderr}
	var err error
	code := 0
	switch command {
	case "package":
		options := packageedit.Options{}
		fs.BoolVar(&options.DryRun, "dry-run", false, "仅预览命令与清单差异")
		fs.BoolVar(&options.Cask, "cask", false, "Homebrew cask")
		fs.BoolVar(&options.Formula, "formula", false, "Homebrew formula")
		fs.BoolVar(&options.AUR, "aur", false, "Arch AUR 根包")
		fs.BoolVar(&options.Trust, "trust", false, "只信任此完整第三方 Homebrew 包名")
		if code, e := parse(fs, interspersed(args, nil)); e != nil {
			return code, nil
		}
		if fs.NArg() != 2 || (fs.Arg(0) != "install" && fs.Arg(0) != "uninstall") {
			return 2, fmt.Errorf("用法：package <install|uninstall> <准确包名> [--dry-run] [--cask|--formula] [--aur] [--trust]")
		}
		options.Action, options.Package = fs.Arg(0), fs.Arg(1)
		if options.Cask && options.Formula {
			return 2, fmt.Errorf("--cask 与 --formula 不能同时使用")
		}
		if options.Trust && options.Action != "install" {
			return 2, fmt.Errorf("--trust 只适用于 install")
		}
		var profile string
		profile, err = syncstate.Preflight(root)
		if err != nil {
			break
		}
		if !options.DryRun {
			var lock *syncstate.Lock
			lock, err = syncstate.OperationLock(root)
			if err != nil {
				break
			}
			defer lock.Close()
		}
		for key, value := range map[string]string{"LC_ALL": "C", "HOMEBREW_NO_AUTO_UPDATE": "1", "HOMEBREW_NO_INSTALL_CLEANUP": "1", "HOMEBREW_NO_AUTOREMOVE": "1", "HOMEBREW_NO_INSTALL_UPGRADE": "1"} {
			env[key] = value
		}
		runner.Env = query.EnvList(env)
		var operation *packageedit.Command
		operation, err = packageedit.New(root, profile, options, runner)
		if err == nil {
			err = operation.Execute(ctx, func() (bool, error) {
				return taskexec.Confirm(ctx, stdin, stdout, "输入 yes 以执行（其他输入取消）：")
			}, stdout)
		}
	case "vscode":
		options := extensions.Options{Manifest: filepath.Join(root, "dotfiles/common/vscode/extensions.toml")}
		fs.BoolVar(&options.DryRun, "dry-run", false, "只预览，不安装")
		fs.StringVar(&options.Manifest, "manifest", options.Manifest, "扩展清单")
		fs.StringVar(&options.Code, "code", "", "VS Code CLI 路径")
		fs.StringVar(&options.UserDataDir, "user-data-dir", "", "隔离用户目录")
		fs.StringVar(&options.ExtensionsDir, "extensions-dir", "", "隔离扩展目录")
		if code, e := parse(fs, interspersed(args, map[string]bool{"--manifest": true, "--code": true, "--user-data-dir": true, "--extensions-dir": true})); e != nil {
			return code, nil
		}
		if fs.NArg() != 1 || (fs.Arg(0) != "status" && fs.Arg(0) != "sync") {
			return 2, fmt.Errorf("用法：vscode <status|sync> [--dry-run] [--manifest 清单] [--code CLI]")
		}
		options.Action = fs.Arg(0)
		err = extensions.Run(options, runner, stdout)
	case "config-history":
		description := "manual system-sync checkpoint"
		fs.StringVar(&description, "description", description, "检查点描述")
		fs.StringVar(&description, "d", description, "检查点描述")
		if code, e := parse(fs, interspersed(args, map[string]bool{"--description": true, "-d": true})); e != nil {
			return code, nil
		}
		if fs.NArg() != 1 || (fs.Arg(0) != "init" && fs.Arg(0) != "save") {
			return 2, fmt.Errorf("用法：config-history <init|save> [-d 描述]")
		}
		var home string
		home, err = os.UserHomeDir()
		if err == nil {
			err = confighistory.Run(fs.Arg(0), description, home, runner, stdout)
		}
	case "config-detach":
		dryRun := fs.Bool("dry-run", false, "只预览，不修改配置或建立备份")
		yes := fs.Bool("yes", false, "已审阅预览，跳过交互确认")
		if code, e := parse(fs, args); e != nil {
			return code, nil
		}
		if fs.NArg() != 0 {
			return 2, fmt.Errorf("config-detach 不接受位置参数")
		}
		code, err = detach(ctx, root, *dryRun, *yes, runner, stdin, stdout, stderr)
	}
	if err != nil {
		prefix := "[x] "
		if command == "package" {
			prefix = "错误："
		}
		var pathError *os.PathError
		if command != "package" && errors.As(err, &pathError) {
			fmt.Fprintln(stderr, prefix+"配置或状态无法安全读取，已停止；请检查路径、格式和权限")
		} else {
			fmt.Fprintln(stderr, prefix+err.Error())
		}
		if errors.Is(err, context.Canceled) {
			return 130, nil
		}
		return 1, nil
	}
	return code, nil
}
func detach(ctx context.Context, root string, dryRun, yes bool, runner taskexec.Runner, stdin *os.File, out, stderr io.Writer) (int, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return 1, err
	}
	plan, err := configdetach.New(root, home)
	if err != nil {
		return 1, err
	}
	runner.Dir = root
	output, err := runner.Invoke([]string{"mise", "bootstrap", "dotfiles", "status", "--json"}, true, time.Minute)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return 130, err
		}
		return 1, fmt.Errorf("mise 配置状态查询失败，未修改配置；请先运行 config-status 核对")
	}
	var data map[string]any
	if err = packagestate.DecodeJSON(strings.NewReader(output), &data); err != nil {
		return 1, fmt.Errorf("mise 配置状态结构无法识别，未修改配置")
	}
	enrollment, _, _, err := localconfig.Locations(plan.Home)
	if err != nil {
		return 1, err
	}
	if err = plan.Collect(data, enrollment); err != nil {
		return 1, err
	}
	for _, change := range plan.Changes {
		fmt.Fprintf(out, "[!] %s：%s\n", change.Reason, change.Target)
	}
	fmt.Fprintf(out, "[+] 保留当前配置内容：%d 个目标；待处理 %d 项。\n", len(plan.Retained), len(plan.Changes))
	if dryRun {
		fmt.Fprintln(out, "[+] 以上仅为预览，未修改配置或建立备份。")
		return 0, nil
	}
	if len(plan.Changes) == 0 {
		fmt.Fprintln(out, "[+] 无需迁出，当前配置已是独立文件。")
		return 0, nil
	}
	if !yes {
		if !terminal.IsTerminal(stdin) {
			return 1, fmt.Errorf("请在交互终端运行；自动化请先审阅 --dry-run，再显式使用 --yes")
		}
		confirmed, err := taskexec.Confirm(ctx, stdin, out, "将上述配置迁为本机文件，并取消本项目的本机历史登记。输入 yes 确认（其他输入取消）：")
		if err != nil {
			return 1, err
		}
		if !confirmed {
			fmt.Fprintln(out, "[-] 已取消，未修改配置。")
			return 2, nil
		}
	}
	backupRoot := filepath.Join(localconfig.Env("XDG_STATE_HOME", filepath.Join(plan.Home, ".local/state")), "system-sync/config-detach")
	lock, err := syncstate.OperationLock(root)
	if err != nil {
		return 1, err
	}
	defer lock.Close()
	backup, err := plan.Apply(ctx, backupRoot, stderr)
	if err != nil {
		return 1, err
	}
	fmt.Fprintf(out, "[+] 配置已保留在本机位置；备份与操作记录：%s\n", backup)
	fmt.Fprintln(out, "[+] 开发工具继续由 mise 管理。重新打开终端加载本机配置；再次 config-sync 会重新接管配置。")
	return 0, nil
}
