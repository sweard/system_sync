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
		runner.Progress = stderr
		var operation *packageedit.Command
		operation, err = packageedit.New(root, profile, options, runner)
		if err == nil {
			err = operation.Execute(ctx, func() (bool, error) {
				return taskexec.Confirm(ctx, stdin, stdout, "输入 yes 以执行（其他输入取消）：")
			}, stdout)
		}
	case "vscode":
		options := extensions.Options{Manifest: filepath.Join(root, "dotfiles/common/vscode/extensions.toml"), Progress: stderr}
		fs.BoolVar(&options.DryRun, "dry-run", false, "只预览，不修改")
		fs.StringVar(&options.Manifest, "manifest", options.Manifest, "扩展清单")
		fs.StringVar(&options.Code, "code", "", "VS Code CLI 路径")
		fs.StringVar(&options.UserDataDir, "user-data-dir", "", "隔离用户目录")
		fs.StringVar(&options.ExtensionsDir, "extensions-dir", "", "隔离扩展目录")
		capture := fs.Bool("capture", false, "status：预览新增扩展及已纳管扩展的本机版本差异；--apply 确认后写入")
		apply := fs.Bool("apply", false, "capture：备份并更新清单；默认只预览")
		yes := fs.Bool("yes", false, "capture：已审阅候选，跳过 yes 确认；须同时使用 --apply")
		group := fs.String("group", "common", "capture：新增项分组，common 或当前平台；默认 common，已有项保持原分组")
		var only, exclude stringsFlag
		fs.Var(&only, "only", "capture：仅采集指定 publisher.name；可重复或逗号分隔")
		fs.Var(&exclude, "exclude", "capture：排除指定 publisher.name；可重复或逗号分隔，优先于 --only")
		if code, e := parse(fs, interspersed(args, map[string]bool{"--manifest": true, "--code": true, "--user-data-dir": true, "--extensions-dir": true, "--group": true, "--only": true, "--exclude": true})); e != nil {
			return code, nil
		}
		if fs.NArg() != 1 || (fs.Arg(0) != "status" && fs.Arg(0) != "sync" && fs.Arg(0) != "capture") {
			return 2, fmt.Errorf("用法：vscode <status|sync|capture> [--dry-run] [--manifest 清单] [--code CLI]；status --capture 支持 --only、--exclude、--group、--apply、--yes")
		}
		options.Action = fs.Arg(0)
		if *capture {
			if options.Action == "sync" {
				return 2, fmt.Errorf("--capture 只适用于 vscode status")
			}
			options.Action = "capture"
		}
		if options.Action == "capture" {
			if *apply && options.DryRun || *yes && !*apply {
				return 2, fmt.Errorf("--apply 不能与 --dry-run 同时使用；--yes 须同时使用 --apply")
			}
			code, err = captureExtensions(ctx, root, extensions.CaptureOptions{Options: options, Group: *group, Only: only, Exclude: exclude}, *apply, *yes, runner, stdin, stdout)
		} else {
			captureFlags := false
			fs.Visit(func(f *flag.Flag) {
				if f.Name == "apply" || f.Name == "yes" || f.Name == "group" || f.Name == "only" || f.Name == "exclude" {
					captureFlags = true
				}
			})
			if captureFlags {
				return 2, fmt.Errorf("采集参数 --apply、--yes、--group、--only、--exclude 须同时使用 --capture")
			}
			err = extensions.Run(options, runner, stdout)
		}
	case "config-history":
		if len(args) > 0 && args[0] == "list" {
			runner.Dir = root
			return nativeQuery(runner, append([]string{"mise", "dot", "--local", "history", "ls"}, args[1:]...), "查询本机配置历史", "mise dot --local history ls", stdout, stderr)
		}
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
		var nativeFailure *taskexec.Failure
		if command != "package" && errors.As(err, &pathError) && !errors.As(err, &nativeFailure) {
			reason := "路径访问失败"
			if errors.Is(err, os.ErrNotExist) {
				reason = "文件或目录不存在"
			} else if errors.Is(err, os.ErrPermission) {
				reason = "权限不足"
			}
			fmt.Fprintf(stderr, "%s配置或状态无法安全读取（%s），已停止；请检查路径、格式和权限\n", prefix, reason)
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
	var output string
	err = taskexec.Progress(stderr, "读取配置迁出状态", func() error {
		var err error
		output, err = runner.Invoke([]string{"mise", "bootstrap", "dotfiles", "status", "--json"}, true, time.Minute)
		return err
	})
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return 130, taskexec.Redacted(err)
		}
		return 1, fmt.Errorf("mise 配置状态查询失败：%w；未修改配置，请先运行 mise run config-status 核对", taskexec.Redacted(err))
	}
	var data map[string]any
	if err = packagestate.DecodeJSON(strings.NewReader(output), &data); err != nil {
		return 1, fmt.Errorf("mise 配置状态结构无法识别，未修改配置")
	}
	enrollment, _, _, err := localconfig.Locations(plan.Home)
	if err != nil {
		return 1, err
	}
	if err = taskexec.Progress(stderr, "核对待迁出文件与锁文件依赖", func() error { return plan.Collect(data, enrollment) }); err != nil {
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
	var backup string
	err = taskexec.Progress(out, "备份并迁出受管配置，保留本机独立文件", func() error {
		var err error
		backup, err = plan.Apply(ctx, backupRoot, stderr)
		return err
	})
	if err != nil {
		return 1, err
	}
	fmt.Fprintf(out, "[+] 配置已保留在本机位置；备份与操作记录：%s\n", backup)
	fmt.Fprintln(out, "[+] 开发工具继续由 mise 管理。重新打开终端加载本机配置；再次 config-sync 会重新接管配置。")
	return 0, nil
}
