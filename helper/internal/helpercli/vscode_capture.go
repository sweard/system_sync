package helpercli

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/sweard/system_sync/helper/internal/extensions"
	"github.com/sweard/system_sync/helper/internal/taskexec"
	"github.com/sweard/system_sync/helper/internal/terminal"
)

func captureExtensions(ctx context.Context, root string, options extensions.CaptureOptions, apply, yes bool, runner extensions.Invoker, stdin *os.File, out io.Writer) (int, error) {
	var plan *extensions.CapturePlan
	err := taskexec.Progress(options.Progress, "采集 VS Code 未纳管扩展与版本", func() error {
		var err error
		plan, err = extensions.NewCapture(options, runner)
		return err
	})
	if err != nil {
		return 1, err
	}
	plan.Render(out)
	if !apply {
		fmt.Fprintln(out, "[+] 以上仅为预览，未修改清单或建立备份。")
		if plan.HasChanges() {
			fmt.Fprintln(out, "[!] 如需纳管，在相同筛选命令后加 --apply 写入。")
		}
		return 0, nil
	}
	if !plan.HasChanges() {
		return 0, nil
	}
	if !yes {
		if !terminal.IsTerminal(stdin) {
			return 1, fmt.Errorf("请在交互终端运行并输入 yes；自动化请先审阅预览，再使用 --apply --yes")
		}
		confirmed, err := taskexec.Confirm(ctx, stdin, out, "将上述扩展及本机版本追加到清单。输入 yes 确认（其他输入取消）：")
		if err != nil {
			return 1, err
		}
		if !confirmed {
			fmt.Fprintln(out, "[-] 已取消，未修改清单。")
			return 2, nil
		}
	}
	var backup string
	err = taskexec.Progress(options.Progress, "复核扩展状态并备份、更新清单", func() error {
		var err error
		backup, err = plan.Apply(ctx, root, runner)
		return err
	})
	if err != nil {
		return 1, err
	}
	fmt.Fprintf(out, "[+] 已追加扩展到清单；原清单与候选备份：%s\n", backup)
	return 0, nil
}
