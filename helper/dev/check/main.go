package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/sweard/system_sync/helper/dev/checkrun"
)

func main() {
	all := flag.Bool("all-platforms", false, "运行完整跨平台验收")
	jobs := flag.Int("jobs", min(2, runtime.NumCPU()), "集成测试并发数；1 为串行")
	report := flag.String("report", ".system-sync/check/latest.json", "JSON 耗时报告路径")
	root := flag.String("repo", os.Getenv("SYSTEM_SYNC_REPO_ROOT"), "仓库路径")
	flag.Parse()
	if flag.NArg() > 0 {
		fmt.Fprintln(os.Stderr, "不支持的位置参数：", flag.Args())
		os.Exit(2)
	}
	if *root == "" {
		*root = "."
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	os.Exit(checkrun.Run(ctx, checkrun.Config{Root: *root, ReportPath: *report, All: *all, Jobs: *jobs}))
}
