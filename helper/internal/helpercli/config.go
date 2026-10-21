package helpercli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/sweard/system_sync/helper/internal/configops"
	"github.com/sweard/system_sync/helper/internal/localconfig"
	"github.com/sweard/system_sync/helper/internal/query"
	"github.com/sweard/system_sync/helper/internal/syncstate"
	"github.com/sweard/system_sync/helper/internal/taskexec"
	"github.com/sweard/system_sync/helper/internal/terminal"
)

func configuration(args []string, root string, stdin *os.File, out, stderr io.Writer) (int, error) {
	if len(args) == 0 || (args[0] != "diff" && args[0] != "sync") {
		return 2, fmt.Errorf("用法：config <diff|sync> [目标路径…] [--dry-run] [--yes]")
	}
	action, args := args[0], args[1:]
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	env := query.Environment()
	env["MISE_AUTO_INSTALL"], env["MISE_TASK_RUN_AUTO_INSTALL"] = "false", "false"
	runner := taskexec.Runner{Context: ctx, Dir: root, Env: query.EnvList(env), Stdin: stdin, Stdout: out, Stderr: stderr}
	// Keep the existing preview script's native help available.
	if action == "diff" && len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		_, err := runner.Run([]string{"mise", "bootstrap", "dotfiles", "diff", "--help"}, false)
		return 0, err
	}
	fs := flag.NewFlagSet("config "+action, flag.ContinueOnError)
	fs.SetOutput(stderr)
	dryRun := fs.Bool("dry-run", false, "仅预览实际差异")
	yes := fs.Bool("yes", false, "已审阅计划，跳过交互确认")
	force := fs.Bool("force", false, "备份后允许原生 apply 覆盖冲突文件；仅用于日常同步")
	if code, err := parse(fs, interspersed(args, nil)); err != nil {
		return code, nil
	}
	preview := action == "diff" || *dryRun
	if preview && *yes {
		return 2, fmt.Errorf("--yes 仅适用于 config sync 的实际应用")
	}
	if action == "diff" && *force {
		return 2, fmt.Errorf("--force 仅适用于 config sync")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return 1, err
	}
	home, err = localconfig.RealPath(home)
	if err != nil {
		return 1, err
	}
	root, err = localconfig.RealPath(root)
	if err != nil {
		return 1, err
	}
	if !preview {
		lock, err := syncstate.OperationLock(root)
		if err != nil {
			return 1, err
		}
		defer lock.Close()
	}
	e := configops.Engine{Root: root, Home: home, Runner: runner, Force: *force}
	plan, err := e.Plan(fs.Args())
	if err != nil {
		return 1, fmt.Errorf("配置差异检查失败，无法确认是否需要同步；未写入配置：%w", err)
	}
	plan.Render(out)
	if preview || (!plan.Changed && plan.Diff == "" && len(plan.Pending) == 0) {
		return 0, nil
	}
	if len(plan.Pending) > 0 {
		return 1, fmt.Errorf("配置仍有待处理项，未修改配置或建立备份")
	}
	if !*yes {
		if !terminal.IsTerminal(stdin) {
			return 1, fmt.Errorf("请在交互终端运行；自动化请先审阅 config-dry-run，再显式使用 --yes")
		}
		confirmed, err := taskexec.Confirm(ctx, stdin, out, "备份并应用上述配置。输入 yes 确认（其他输入取消）：")
		if err != nil {
			return 1, err
		}
		if !confirmed {
			fmt.Fprintln(out, "[-] 已取消，未修改配置或建立备份。")
			return 2, nil
		}
	}
	directory, err := e.Sync(plan, out)
	if err != nil {
		if ctx.Err() != nil {
			return 130, err
		}
		return 1, err
	}
	fmt.Fprintln(out, "[+] 本机备份与操作记录："+directory)
	return 0, nil
}
