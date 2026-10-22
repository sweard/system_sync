// Package configops owns repository-scoped configuration planning and application.
// Both everyday synchronization and additive machine setup use this module.
package configops

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/sweard/system_sync/helper/internal/localconfig"
	"github.com/sweard/system_sync/helper/internal/packagestate"
	"github.com/sweard/system_sync/helper/internal/taskexec"
)

type Runner interface {
	Run([]string, bool) (string, error)
}
type Engine struct {
	Root, Home   string
	Runner       Runner
	Initializing bool
	Force        bool
}
type Plan struct {
	Targets   []string `json:"targets"`
	Diff      string   `json:"diff,omitempty"`
	Pending   []string `json:"pending"`
	Changed   bool     `json:"changed"`
	state     string
	selectors []string
}

func (e Engine) Plan(targets []string) (*Plan, error) {
	p := &Plan{Targets: []string{}, Pending: []string{}, selectors: append([]string{}, targets...)}
	output, err := e.Runner.Run([]string{"mise", "bootstrap", "dotfiles", "status", "--json"}, true)
	if err != nil {
		return nil, fmt.Errorf("配置状态查询失败：%w；请运行 mise run config-status 核对", taskexec.Redacted(err))
	}
	data, err := decodeState(output)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	p.state = string(encoded)
	other := map[string]bool{}
	for _, section := range []string{"files", "edits"} {
		items, ok := data[section].([]any)
		if !ok {
			return nil, fmt.Errorf("配置状态缺少 files/edits 数组")
		}
		for _, value := range items {
			item, ok := value.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("配置状态条目无法识别")
			}
			key := "target"
			if section == "edits" {
				key = "path"
			}
			value, ok := item[key].(string)
			if !ok || value == "" {
				return nil, fmt.Errorf("配置状态缺少目标")
			}
			origin := localconfig.Table(item["origin"])
			config, ok := origin["config"].(string)
			if !ok {
				return nil, fmt.Errorf("配置状态缺少来源")
			}
			resolved, err := localconfig.RealPath(config)
			if err != nil {
				return nil, err
			}
			owned := localconfig.Within(resolved, e.Root)
			if !owned {
				mode, _ := item["mode"].(string)
				state, _ := item["state"].(string)
				if mode == "track" || mode == "track-local" || state == "tracked" || state == "not_on_this_machine" {
					continue
				}
				other[value] = true
				continue
			}
			state, ok := item["state"].(string)
			if !ok {
				return nil, fmt.Errorf("配置状态无法识别")
			}
			mode, _ := item["mode"].(string)
			if state == "tracked" || state == "not_on_this_machine" || mode == "track" || mode == "track-local" {
				continue
			}
			if !selected(expand(value, e.Home), value, targets, e.Home) {
				continue
			}
			target, err := e.Target(value)
			if err != nil {
				return nil, err
			}
			if section == "files" && !has([]string{"symlink", "symlink-each", "copy", "template", "content", "permissions", "absent"}, mode) {
				p.Pending = append(p.Pending, "未知配置模式需人工处理："+mode+" ("+value+")")
			}
			p.Targets = append(p.Targets, target)
			switch state {
			case "applied":
			case "missing", "differs":
				p.Changed = true
			default:
				p.Pending = append(p.Pending, "配置状态需人工处理："+value+" ["+state+"]")
			}
			if e.Initializing && mode == "absent" {
				p.Pending = append(p.Pending, "初始化不删除配置："+value)
			}
			if e.Initializing && mode == "symlink" {
				if info, err := os.Lstat(target); err == nil {
					if info.Mode()&os.ModeSymlink == 0 {
						p.Pending = append(p.Pending, "已有普通文件需先备份并逐项接管："+value)
					} else {
						resolved, err := localconfig.RealPath(target)
						if err != nil || !localconfig.Within(resolved, e.Root) {
							p.Pending = append(p.Pending, "已有其他来源的配置链接需人工接管："+value)
						}
					}
				} else if !os.IsNotExist(err) {
					return nil, err
				}
			}
		}
	}
	p.Targets = unique(p.Targets)
	for value := range other {
		if strings.HasPrefix(value, "~/") {
			value = filepath.Join(e.Home, value[2:])
		}
		if has(p.Targets, value) {
			p.Pending = append(p.Pending, "同一目标还有仓库外的配置映射，需人工处理："+value)
		}
	}
	if (p.Changed || len(p.Pending) > 0) && len(p.Targets) > 0 {
		command := append([]string{"mise", "bootstrap", "dotfiles", "diff"}, p.Targets...)
		p.Diff, err = e.Runner.Run(command, true)
		if err != nil {
			return nil, fmt.Errorf("配置差异查询失败：%w；请运行 mise bootstrap dotfiles diff 核对", taskexec.Redacted(err))
		}
	}
	for _, selector := range targets {
		if !matched(selector, p.Targets, e.Home) {
			return nil, fmt.Errorf("目标不是本仓库的已启用配置：%s", selector)
		}
	}
	p.Pending = unique(p.Pending)
	return p, nil
}

func decodeState(output string) (map[string]any, error) {
	var data map[string]any
	if err := packagestate.DecodeJSON(strings.NewReader(output), &data); err != nil || data == nil {
		return nil, fmt.Errorf("mise 状态 JSON 无法识别")
	}
	return data, nil
}
func has(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}
func unique(values []string) []string {
	result := []string{}
	for _, value := range values {
		if !has(result, value) {
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}
func expand(value, home string) string {
	if strings.HasPrefix(value, "~/") {
		value = filepath.Join(home, value[2:])
	}
	return filepath.Clean(value)
}
func selected(target, value string, selectors []string, home string) bool {
	if len(selectors) == 0 {
		return true
	}
	for _, selector := range selectors {
		if expand(selector, home) == target || selector == value {
			return true
		}
	}
	return false
}
func matched(selector string, targets []string, home string) bool {
	return has(targets, expand(selector, home))
}
func (p *Plan) Render(out io.Writer) {
	if p.Diff != "" {
		fmt.Fprintf(out, "[!] 检测到以下受管配置差异：\n\n%s\n", p.Diff)
	}
	for _, item := range p.Pending {
		fmt.Fprintln(out, "[!] 待处理："+item)
	}
	if !p.Changed && p.Diff == "" && len(p.Pending) == 0 {
		fmt.Fprintln(out, "[+] 未发现需要应用的配置差异，无需同步。")
	} else {
		fmt.Fprintln(out, "以上仅为预览，未写入配置。")
	}
}
func (e Engine) Check(p *Plan) error {
	current, err := e.Plan(p.selectors)
	if err != nil {
		return err
	}
	if current.state != p.state || current.Diff != p.Diff ||
		!reflect.DeepEqual(current.Targets, p.Targets) || !reflect.DeepEqual(current.Pending, p.Pending) {
		return fmt.Errorf("确认后配置状态已改变，请重新预览")
	}
	return nil
}
func (e Engine) Backup(p *Plan, directory string) error {
	if err := e.Check(p); err != nil {
		return err
	}
	return BackupTargets(e.Home, directory, p.Targets)
}
func (e Engine) Apply(p *Plan, out io.Writer) error {
	if len(p.Pending) > 0 {
		return fmt.Errorf("配置仍有待处理项，未应用配置")
	}
	if err := taskexec.Progress(out, "复核配置应用前的状态", func() error { return e.Check(p) }); err != nil {
		return err
	}
	if !p.Changed {
		fmt.Fprintln(out, "[+] 受管配置无需同步。")
		return nil
	}
	if len(p.Targets) == 0 {
		return fmt.Errorf("没有可靠配置目标，拒绝应用全部配置")
	}
	command := []string{"mise", "bootstrap", "dotfiles", "apply", "--yes"}
	if e.Force && !e.Initializing {
		command = append(command, "--force")
	}
	command = append(command, p.Targets...)
	fmt.Fprintf(out, "[~] 应用 %d 个受管配置目标\n", len(p.Targets))
	if _, err := e.Runner.Run(command, false); err != nil {
		return err
	}
	var current *Plan
	err := taskexec.Progress(out, "复查配置应用结果", func() error {
		var err error
		current, err = e.Plan(p.selectors)
		return err
	})
	if err != nil {
		return err
	}
	if current.Changed || len(current.Pending) > 0 || current.Diff != "" {
		return fmt.Errorf("配置应用后仍有差异，请运行 config-dry-run 核对")
	}
	fmt.Fprintln(out, "[+] 配置已部署并复查；原文件备份仅保存在本机审计目录。")
	return nil
}

func (e Engine) Target(value string) (string, error) {
	if strings.HasPrefix(value, "~/") {
		value = filepath.Join(e.Home, value[2:])
	}
	if !filepath.IsAbs(value) || value == e.Home || !localconfig.Within(value, e.Home) || localconfig.Within(value, e.Root) {
		return "", fmt.Errorf("配置目标必须位于 HOME 内且不在仓库中：%s", value)
	}
	parent, err := localconfig.RealPath(filepath.Dir(value))
	if err != nil || !localconfig.Within(parent, e.Home) {
		return "", fmt.Errorf("配置目标的父路径超出 HOME：%s", value)
	}
	if localconfig.IsLink(value) {
		resolved, err := localconfig.RealPath(value)
		if err != nil || (!localconfig.Within(resolved, e.Home) && !localconfig.Within(resolved, e.Root)) {
			return "", fmt.Errorf("配置目标链接指向 HOME/仓库外：%s", value)
		}
	}
	return filepath.Clean(value), nil
}
