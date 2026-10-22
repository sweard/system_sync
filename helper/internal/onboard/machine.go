package onboard

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/sweard/system_sync/helper/internal/configops"
	"github.com/sweard/system_sync/helper/internal/packagestate"
	"github.com/sweard/system_sync/helper/internal/syncstate"
	"github.com/sweard/system_sync/helper/internal/taskexec"
)

type Plan struct {
	SchemaVersion int      `json:"schema_version"`
	Profile       string   `json:"profile"`
	Stages        []string `json:"stages"`
	WSL           bool     `json:"wsl"`
	Packages      *Capture `json:"packages,omitempty"`
	ConfigTargets []string `json:"config_targets"`
	ConfigDiff    string   `json:"config_diff,omitempty"`
	Tools         []string `json:"tools"`
	Pending       []string `json:"pending"`
	config        *configops.Plan
	toolInstalled map[string]bool
	toolSources   map[string]string
}

func has(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}

func Stages(only []string) ([]string, error) {
	if len(only) == 0 {
		return []string{"packages", "config", "tools"}, nil
	}
	requested := map[string]bool{}
	for _, value := range only {
		for _, name := range strings.Split(value, ",") {
			if name != "packages" && name != "config" && name != "tools" {
				return nil, fmt.Errorf("--only 只支持 packages、config、tools")
			}
			requested[name] = true
		}
	}
	result := []string{}
	for _, name := range []string{"packages", "config", "tools"} {
		if requested[name] {
			result = append(result, name)
		}
	}
	return result, nil
}

func (e Engine) Plan(only []string) (*Plan, error) {
	stages, err := Stages(only)
	if err != nil {
		return nil, err
	}
	p := &Plan{SchemaVersion: 1, Profile: e.Profile, Stages: stages, WSL: e.Profile != "macos" && (e.Environment["WSL_DISTRO_NAME"] != "" || e.Environment["WSL_INTEROP"] != ""), ConfigTargets: []string{}, Tools: []string{}, Pending: []string{}}
	if has(stages, "packages") {
		p.Packages, err = e.Capture()
		if err != nil {
			return nil, err
		}
		for _, item := range p.Packages.Declarations {
			if item.Problem != "" {
				p.Pending = append(p.Pending, item.Problem)
			}
		}
	}
	if has(stages, "config") {
		if err = e.configPlan(p); err != nil {
			return nil, err
		}
	}
	if has(stages, "tools") {
		if err = e.toolsPlan(p); err != nil {
			return nil, err
		}
	}
	p.Pending = unique(p.Pending)
	return p, nil
}

func (p *Plan) Render(out io.Writer) {
	fmt.Fprintf(out, "[+] 接入平台：%s；阶段：%s。\n", p.Profile, strings.Join(p.Stages, ", "))
	if p.WSL {
		fmt.Fprintln(out, "[!] WSL：保留集成包和 Windows PATH；Windows 应用、发行版导出及服务启用需单独处理。")
	}
	if p.Packages != nil {
		p.Packages.Render(out)
	}
	for _, target := range p.ConfigTargets {
		fmt.Fprintf(out, "[!] 核对配置目标：%s\n", target)
	}
	if p.ConfigDiff != "" {
		fmt.Fprint(out, p.ConfigDiff)
	}
	for _, tool := range p.Tools {
		fmt.Fprintf(out, "[!] 待补装开发工具：%s\n", tool)
	}
	for _, item := range p.Pending {
		fmt.Fprintf(out, "[x] 待人工处理：%s\n", item)
	}
	fmt.Fprintln(out, "[+] 初始化保留现有软件及工具版本；不执行整机收敛、cleanup、autoremove 或安装原因降级。")
}

func (c *Capture) Render(out io.Writer) {
	fmt.Fprintf(out, "[+] 本机已安装 %d 项；根包 %d 项；清单外根包 %d 项；声明缺失 %d 项。\n", len(c.Installed), len(c.Roots), len(c.Undeclared), len(c.Missing))
	for _, name := range c.Undeclared {
		fmt.Fprintf(out, "[!] 保留本机清单外根包：%s\n", name)
	}
	for _, p := range c.Missing {
		fmt.Fprintf(out, "[!] 声明缺失：%s\n", p.ID())
	}
	for _, name := range c.Orphans {
		fmt.Fprintf(out, "[!] 保留现有 orphan：%s\n", name)
	}
	for _, item := range c.Pending {
		fmt.Fprintf(out, "[!] 来源或状态待核对：%s\n", item)
	}
	if c.Candidate.Diff == "" {
		fmt.Fprintln(out, "[+] 无新增清单候选，无需合并。")
	} else {
		fmt.Fprint(out, c.Candidate.Diff)
	}
	if len(c.Pending) > 0 {
		fmt.Fprintln(out, "[!] 候选未自动纳入来源不明项目；审阅完成前不要运行 system-sync。")
	}
}

func (e Engine) installCommands(c *Capture) ([][]string, error) {
	missing := []string{}
	for _, p := range c.Missing {
		if p.Problem != "" {
			return nil, fmt.Errorf("%s", p.Problem)
		}
		missing = append(missing, p.Name)
	}
	if len(missing) == 0 {
		return nil, nil
	}
	command := []string{}
	switch e.Profile {
	case "arch":
		command = []string{"pacman", "-S", "--needed", "--noconfirm", "--"}
	case "debian":
		command = []string{"apt-get", "--no-auto-remove", "--no-upgrade", "--no-remove", "-o", "APT::Get::Purge=false", "install", "--"}
	case "fedora":
		command = []string{"dnf", "--setopt=clean_requirements_on_remove=False", "--setopt=obsoletes=False", "install", "--"}
	case "opensuse":
		command = []string{"zypper", "--non-interactive", "install", "--no-force-resolution", "--no-allow-name-change", "--no-allow-arch-change", "--no-allow-vendor-change", "--no-allow-downgrade", "--type", "package", "--name", "--"}
	case "macos":
		return nil, nil
	default:
		return nil, fmt.Errorf("不支持的初始化平台")
	}
	if e.UID != 0 {
		command = append([]string{"sudo"}, command...)
	}
	return [][]string{append(command, missing...)}, nil
}

func (e Engine) installPackages(ctx context.Context, c *Capture, directory string, out io.Writer) error {
	fmt.Fprintln(out, "[~] 复核补装前的系统软件状态")
	if err := unchanged(c.Sources); err != nil {
		return err
	}
	before, err := e.inventory()
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(before, c.Installed) {
		return fmt.Errorf("确认后包状态已改变，请重新预览")
	}
	if len(c.Missing) == 0 {
		fmt.Fprintln(out, "[+] 系统软件无需补装；现有软件全部保留。")
		return nil
	}
	commands, err := e.installCommands(c)
	if err != nil {
		return err
	}
	if e.Profile == "macos" {
		var content strings.Builder
		for _, p := range c.Declarations {
			if p.Manager == "tap" {
				content.WriteString(p.Line)
			}
		}
		for _, p := range c.Missing {
			content.WriteString(p.Line)
		}
		path := filepath.Join(directory, "Brewfile.install-only")
		if err = os.WriteFile(path, []byte(content.String()), 0600); err != nil {
			return err
		}
		commands = [][]string{{"brew", "bundle", "install", "--no-upgrade", "--verbose", "--file=" + path}}
	}
	// APT can enforce a no-removal transaction itself; also inspect its preview.
	if e.Profile == "debian" {
		command := []string{"apt-get", "--simulate", "--no-auto-remove", "--no-upgrade", "--no-remove", "install", "--"}
		for _, p := range c.Missing {
			command = append(command, p.Name)
		}
		output, err := e.Runner.Run(command, true)
		if err != nil {
			return fmt.Errorf("APT 补装预演失败，未执行安装")
		}
		for _, line := range strings.Split(output, "\n") {
			if strings.HasPrefix(line, "Remv ") || strings.HasPrefix(line, "Purg ") {
				return fmt.Errorf("APT 补装预演包含移除，已停止")
			}
		}
	}
	for _, command := range commands {
		if err = ctx.Err(); err != nil {
			return err
		}
		fmt.Fprintln(out, "$ "+taskexec.Quote(command))
		if _, err = e.Runner.Run(command, false); err != nil {
			return err
		}
	}
	fmt.Fprintln(out, "[~] 复核补装结果及原有软件是否保留")
	remaining, err := e.inventory()
	if err != nil {
		return err
	}
	remainingSet := set(remaining)
	for _, name := range before {
		if !remainingSet[name] {
			return fmt.Errorf("检测到原有软件被移除，需人工检查事务记录：%s", name)
		}
	}
	for _, p := range c.Missing {
		if !present(remaining, p) {
			return fmt.Errorf("包管理器未完成补装：%s", p.ID())
		}
	}
	if err = unchanged(c.Sources); err != nil {
		return err
	}
	fmt.Fprintln(out, "[+] 缺失软件已补装，原有软件清单已复核保留。")
	return nil
}

// Apply executes a reviewed plan. Package transactions cannot be rolled back;
// every failure records the completed phases and requires state verification.
func (e Engine) Apply(ctx context.Context, p *Plan, out io.Writer) (directory string, err error) {
	if len(p.Pending) > 0 {
		return "", fmt.Errorf("接入仍有待处理项；请解决后重跑，或用 --only 选择独立阶段")
	}
	parent, err := syncstate.LocalDirectory(e.Root, "history")
	if err != nil {
		return "", err
	}
	directory, err = os.MkdirTemp(parent, time.Now().UTC().Format("20060102T150405Z-")+"machine-init-")
	if err != nil {
		return "", err
	}
	completed := []string{}
	defer func() {
		if err != nil {
			_ = syncstate.Journal(directory, "needs-review", map[string]any{"completed_stages": completed})
			err = fmt.Errorf("%w；已完成阶段：%s。请检查本机状态及审计：%s", err, strings.Join(completed, ", "), directory)
		}
	}()
	data, err := jsonBytes(p)
	if err != nil {
		return directory, err
	}
	if err = os.WriteFile(filepath.Join(directory, "plan.json"), data, 0600); err != nil {
		return directory, err
	}
	if len(p.ConfigTargets) > 0 {
		err = taskexec.Progress(out, "复核并备份原有配置", func() error {
			return e.configuration().Backup(p.config, directory)
		})
		if err != nil {
			return directory, err
		}
	}
	if err = syncstate.Journal(directory, "pending", map[string]any{"profile": e.Profile}); err != nil {
		return directory, err
	}
	for i, stage := range p.Stages {
		if err = ctx.Err(); err != nil {
			return directory, err
		}
		if err = syncstate.Journal(directory, stage, map[string]any{"completed_stages": completed}); err != nil {
			return directory, err
		}
		fmt.Fprintf(out, "[~] 开始接入阶段 %d/%d：%s\n", i+1, len(p.Stages), stage)
		switch stage {
		case "packages":
			var saved string
			saved, err = p.Packages.Save(e.Root, "capture")
			if err == nil {
				fmt.Fprintln(out, "[+] 软件清单快照与合并候选："+saved)
				err = e.installPackages(ctx, p.Packages, directory, out)
			}
		case "config":
			err = e.configuration().Apply(p.config, out)
		case "tools":
			err = e.applyTools(p, out)
		}
		if err != nil {
			return directory, err
		}
		completed = append(completed, stage)
	}
	err = syncstate.Journal(directory, "complete", map[string]any{"completed_stages": completed})
	return directory, err
}

func decodeState(output string) (map[string]any, error) {
	var data map[string]any
	if err := packagestate.DecodeJSON(strings.NewReader(output), &data); err != nil || data == nil {
		return nil, fmt.Errorf("mise 状态 JSON 无法识别")
	}
	return data, nil
}
