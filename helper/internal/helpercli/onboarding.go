package helpercli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/sweard/system_sync/helper/internal/localconfig"
	"github.com/sweard/system_sync/helper/internal/onboard"
	"github.com/sweard/system_sync/helper/internal/query"
	"github.com/sweard/system_sync/helper/internal/syncstate"
	"github.com/sweard/system_sync/helper/internal/taskexec"
	"github.com/sweard/system_sync/helper/internal/terminal"
)

func onboarding(command string, args []string, root string, stdin *os.File, out, stderr io.Writer) (int, error) {
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.SetOutput(stderr)
	apply := fs.Bool("apply", false, "执行；package-capture 仅保存本机候选，machine-init 仅补装")
	dryRun := fs.Bool("dry-run", false, "只预览（默认）")
	jsonOutput := fs.Bool("json", false, "以 JSON 显示预览")
	yes := fs.Bool("yes", false, "machine-init 已审阅计划，跳过项目确认")
	var only stringsFlag
	if command == "machine-init" {
		fs.Var(&only, "only", "packages、config、tools，可重复或用逗号分隔")
	}
	if code, err := parse(fs, args); err != nil {
		return code, nil
	}
	if fs.NArg() != 0 {
		return 2, fmt.Errorf("%s 不接受位置参数", command)
	}
	if *apply && *dryRun {
		return 2, fmt.Errorf("--apply 与 --dry-run 不能同时使用")
	}
	if *yes && (!*apply || command != "machine-init") {
		return 2, fmt.Errorf("--yes 只适用于 machine-init --apply")
	}
	if *jsonOutput && *apply {
		return 2, fmt.Errorf("--json 仅用于预览")
	}
	if _, err := onboard.Stages(only); err != nil {
		return 2, err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	profile, err := syncstate.Preflight(root)
	if err != nil {
		return 1, err
	}
	lock, err := syncstate.OperationLock(root)
	if err != nil {
		return 1, err
	}
	defer lock.Close()
	env := query.Environment()
	for key, value := range map[string]string{"LC_ALL": "C", "MISE_AUTO_INSTALL": "false", "MISE_TASK_RUN_AUTO_INSTALL": "false", "HOMEBREW_NO_AUTO_UPDATE": "1", "HOMEBREW_NO_INSTALL_CLEANUP": "1", "HOMEBREW_NO_AUTOREMOVE": "1", "HOMEBREW_NO_INSTALL_UPGRADE": "1", "HOMEBREW_BUNDLE_NO_UPGRADE": "1"} {
		env[key] = value
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
	runner := taskexec.Runner{Context: ctx, Dir: root, Env: query.EnvList(env), Stdin: stdin, Stdout: out, Stderr: stderr}
	e := onboard.Engine{Root: root, Home: home, Profile: profile, Runner: runner, Environment: env, UID: os.Geteuid()}
	if command == "package-capture" {
		capture, err := e.Capture()
		if err != nil {
			return 1, err
		}
		if *jsonOutput {
			return 0, json.NewEncoder(out).Encode(capture)
		}
		capture.Render(out)
		if !*apply {
			fmt.Fprintln(out, "[+] 以上仅为预览；未保存候选、修改清单或系统软件。使用 --apply 保存本机候选。")
			return 0, nil
		}
		directory, err := capture.Save(root, "capture")
		if err != nil {
			return 1, err
		}
		fmt.Fprintln(out, "[+] 本机快照与合并候选已保存："+directory)
		fmt.Fprintln(out, "[+] 受管清单未修改；请审阅候选、来源和保护项后手工合并。")
		return 0, nil
	}
	var plan *onboard.Plan
	progressOut := out
	if !*apply {
		progressOut = nil
	}
	err = taskexec.Progress(progressOut, "读取机器接入计划", func() error {
		var err error
		plan, err = e.Plan(only)
		return err
	})
	if err != nil {
		return 1, err
	}
	if *jsonOutput {
		return 0, json.NewEncoder(out).Encode(plan)
	}
	plan.Render(out)
	if !*apply {
		fmt.Fprintln(out, "[+] 以上仅为预览，未安装软件、部署配置或保存备份。使用 --apply 执行。")
		return 0, nil
	}
	if len(plan.Pending) > 0 {
		return 1, fmt.Errorf("仍有待处理项，未执行初始化；可用 --only 选择独立阶段")
	}
	if !*yes {
		if !terminal.IsTerminal(stdin) {
			return 1, fmt.Errorf("请在交互终端运行；自动化请先审阅预览，再显式使用 --apply --yes")
		}
		confirmed, err := taskexec.Confirm(ctx, stdin, out, "按以上计划补装并接管所选配置。输入 yes 确认（其他输入取消）：")
		if err != nil {
			return 1, err
		}
		if !confirmed {
			fmt.Fprintln(out, "[-] 已取消，未执行初始化。")
			return 2, nil
		}
	}
	directory, err := e.Apply(ctx, plan, out)
	if err != nil {
		return 1, err
	}
	fmt.Fprintln(out, "[+] 所选阶段完成；本机备份与审计："+directory)
	fmt.Fprintln(out, "[!] 请运行 mise run audit 核对 PATH 和配置；软件收敛清理仍由 system-sync 单独执行。")
	return 0, nil
}
