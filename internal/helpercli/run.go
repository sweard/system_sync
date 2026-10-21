package helpercli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/sweard/system_sync/internal/audit"
	"github.com/sweard/system_sync/internal/packagestate"
	"github.com/sweard/system_sync/internal/query"
	"github.com/sweard/system_sync/internal/syncstate"
	"github.com/sweard/system_sync/internal/terminal"
)

type stringsFlag []string

func (v *stringsFlag) String() string         { return fmt.Sprint([]string(*v)) }
func (v *stringsFlag) Set(value string) error { *v = append(*v, value); return nil }

func help(out io.Writer) {
	fmt.Fprintln(out, "system-sync-helper [--root 仓库绝对路径] <命令>\n\n命令：audit、parse-status、parse-zypper、runtime\n--version 显示版本；各命令支持 --help。日常调用 scripts/system-sync-helper.sh。")
}

func Run(args []string, stdin *os.File, stdout, stderr io.Writer, version, revision string) int {
	global := flag.NewFlagSet("system-sync-helper", flag.ContinueOnError)
	global.SetOutput(stderr)
	root := global.String("root", "", "仓库路径；由脚本入口显式传入")
	showVersion := global.Bool("version", false, "显示版本")
	global.Usage = func() { help(stderr) }
	if err := global.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *showVersion {
		fmt.Fprintf(stdout, "system-sync-helper %s protocol=1 target=%s/%s revision=%s\n", version, runtime.GOOS, runtime.GOARCH, revision)
		return 0
	}
	args = global.Args()
	if len(args) == 0 {
		help(stderr)
		return 2
	}
	command, args := args[0], args[1:]
	if command == "audit" || command == "runtime" {
		if *root == "" {
			fmt.Fprintln(stderr, "错误：需要 --root 指定仓库；请使用 scripts/system-sync-helper.sh")
			return 2
		}
		absolute, err := filepath.Abs(*root)
		if err != nil {
			return 2
		}
		*root = absolute
		if info, err := os.Stat(*root); err != nil || !info.IsDir() {
			fmt.Fprintln(stderr, "错误：仓库路径不存在或不可访问")
			return 2
		}
	}
	code, err := dispatch(command, args, *root, stdin, stdout, stderr)
	if err != nil {
		fmt.Fprintln(stderr, "错误："+err.Error())
	}
	return code
}

func parse(fs *flag.FlagSet, args []string) (int, error) {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0, err
		}
		return 2, err
	}
	return 0, nil
}

func inputFile(path string, stdin *os.File) (io.Reader, func(), error) {
	if path == "-" {
		return stdin, func() {}, nil
	}
	file, err := os.Open(path)
	return file, func() {
		if file != nil {
			file.Close()
		}
	}, err
}

func dispatch(command string, args []string, root string, stdin *os.File, stdout, stderr io.Writer) (int, error) {
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.SetOutput(stderr)
	switch command {
	case "parse-status":
		var managers stringsFlag
		fs.Var(&managers, "manager", "需要提取的 manager，可重复")
		if code, err := parse(fs, args); err != nil {
			return code, nil
		}
		if len(managers) == 0 || fs.NArg() != 1 {
			return 2, fmt.Errorf("用法：parse-status --manager <manager> <JSON 文件或 ->")
		}
		reader, closeInput, err := inputFile(fs.Arg(0), stdin)
		if err != nil {
			return 2, fmt.Errorf("无法安全解析 mise 包状态：输入文件不可读")
		}
		defer closeInput()
		var data any
		if err := packagestate.DecodeJSON(reader, &data); err != nil {
			return 2, fmt.Errorf("无法安全解析 mise 包状态：JSON 无法识别")
		}
		entries, err := packagestate.ParseStatus(data, managers)
		if err != nil {
			return 2, fmt.Errorf("无法安全解析 mise 包状态：%w", err)
		}
		for _, entry := range entries {
			fmt.Fprintf(stdout, "%s\t%s\t%s\n", entry.Manager, entry.Package, entry.State)
		}
		return 0, nil
	case "parse-zypper":
		if len(args) != 2 || (args[0] != "solvables" && args[0] != "to-remove") {
			if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
				fmt.Fprintln(stdout, "用法：parse-zypper <solvables|to-remove> <XML 文件或 ->")
				return 0, nil
			}
			return 2, fmt.Errorf("用法：parse-zypper <solvables|to-remove> <XML 文件或 ->")
		}
		reader, closeInput, err := inputFile(args[1], stdin)
		if err != nil {
			return 2, fmt.Errorf("无法安全解析 zypper XML：输入文件不可读")
		}
		defer closeInput()
		names, err := packagestate.ParseZypper(reader, args[0])
		if err != nil {
			return 2, fmt.Errorf("无法安全解析 zypper XML：%w", err)
		}
		for _, name := range names {
			fmt.Fprintln(stdout, name)
		}
		return 0, nil
	case "audit":
		jsonOutput := fs.Bool("json", false, "输出 JSON")
		strict := fs.Bool("strict", false, "漂移时返回非零")
		color := fs.String("color", "auto", "auto、always 或 never")
		var only stringsFlag
		fs.Var(&only, "only", "tools、packages、path 或 config，可重复")
		if code, err := parse(fs, args); err != nil {
			return code, nil
		}
		if fs.NArg() != 0 {
			return 2, fmt.Errorf("audit 不接受位置参数")
		}
		if *color != "auto" && *color != "always" && *color != "never" {
			return 2, fmt.Errorf("--color 只支持 auto、always、never")
		}
		if len(only) == 0 {
			only = audit.Sections
		}
		for _, name := range only {
			valid := false
			for _, section := range audit.Sections {
				valid = valid || name == section
			}
			if !valid {
				return 2, fmt.Errorf("未知审计类别：%s", name)
			}
		}
		env := query.Environment()
		report := audit.New(root, env, nil).Execute(only)
		if *jsonOutput {
			encoder := json.NewEncoder(stdout)
			encoder.SetIndent("", "  ")
			encoder.SetEscapeHTML(false)
			if err := encoder.Encode(report); err != nil {
				return 2, err
			}
		} else {
			isTerminal := false
			if file, ok := stdout.(*os.File); ok {
				isTerminal = terminal.IsTerminal(file)
			}
			fmt.Fprintln(stdout, audit.Render(report, audit.UseColor(*color, env, isTerminal)))
		}
		return audit.ExitCode(report, *strict), nil
	case "runtime":
		check := fs.Bool("check", false, "只读前置检查")
		inherited := fs.Bool("inherited-lock", false, "验证继承的锁")
		phase := fs.String("phase", "", "记录事务阶段")
		if code, err := parse(fs, args); err != nil {
			return code, nil
		}
		actions := 0
		if *check {
			actions++
		}
		if *inherited {
			actions++
		}
		if *phase != "" {
			actions++
		}
		if actions > 1 || (actions > 0 && fs.NArg() > 0) {
			return 2, fmt.Errorf("runtime 一次只能执行一种操作")
		}
		if *inherited {
			if _, ok := syncstate.InheritedLock(root); ok {
				return 0, nil
			}
			return 1, nil
		}
		if *phase != "" {
			if _, ok := syncstate.InheritedLock(root); !ok {
				return 1, fmt.Errorf("阶段更新要求持有本仓库操作锁")
			}
			directory := os.Getenv("SYSTEM_SYNC_RUN_LOG_DIR")
			if directory == "" {
				return 1, fmt.Errorf("缺少事务日志目录")
			}
			if err := syncstate.Journal(directory, *phase, nil); err != nil {
				return 1, err
			}
			return 0, nil
		}
		if *check {
			profile, err := syncstate.Preflight(root)
			if err != nil {
				return 1, err
			}
			fmt.Fprintf(stdout, "前置检查通过：mise >= %s，profile=%s\n", syncstate.MinMise, profile)
			return 0, nil
		}
		return syncstate.RunGuarded(root, fs.Args(), stdin, stdout, stderr)
	default:
		return 2, fmt.Errorf("未知命令：%s", command)
	}
}
