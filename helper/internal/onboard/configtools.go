package onboard

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sweard/system_sync/helper/internal/configops"
	"github.com/sweard/system_sync/helper/internal/localconfig"
	"github.com/sweard/system_sync/helper/internal/taskexec"
)

func (e Engine) configuration() configops.Engine {
	return configops.Engine{Root: e.Root, Home: e.Home, Runner: e.Runner, Initializing: true}
}
func (e Engine) configPlan(p *Plan) error {
	plan, err := e.configuration().Plan(nil)
	if err != nil {
		return err
	}
	p.config = plan
	p.ConfigTargets, p.ConfigDiff = plan.Targets, plan.Diff
	p.Pending = append(p.Pending, plan.Pending...)
	return nil
}

func shortTool(value string) string { return value[strings.LastIndex(value, ":")+1:] }

func (e Engine) desiredTools() (map[string]string, map[string]string, error) {
	result := map[string]string{}
	sources := map[string]string{}
	paths := []string{filepath.Join(e.Root, "dotfiles/common/mise/config.toml")}
	if e.Profile == "macos" {
		paths = append(paths, filepath.Join(e.Root, "dotfiles/macos/mise/config.toml"))
	}
	for i, path := range paths {
		text, err := readManifest(path)
		if i > 0 && os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		sources[path] = text
		data, err := localconfig.Decode([]byte(text))
		if err != nil {
			return nil, nil, err
		}
		for name, value := range localconfig.Table(data["tools"]) {
			version, ok := value.(string)
			if !ok {
				version, ok = localconfig.Table(value)["version"].(string)
			}
			if !ok || version == "" {
				return nil, nil, fmt.Errorf("开发工具声明不是单一版本，请单独处理：%s", name)
			}
			result[name] = version
		}
	}
	return result, sources, nil
}

func (e Engine) globalToolsReady() bool {
	directory := e.Environment["MISE_CONFIG_DIR"]
	if directory == "" {
		directory = e.Environment["XDG_CONFIG_HOME"]
		if directory == "" {
			directory = filepath.Join(e.Home, ".config")
		}
		directory = filepath.Join(directory, "mise")
	}
	path := filepath.Join(directory, "config.toml")
	if override := e.Environment["MISE_GLOBAL_CONFIG_FILE"]; override != "" {
		path = override
	}
	actual, err := localconfig.RealPath(path)
	expected, expectedErr := localconfig.RealPath(filepath.Join(e.Root, "dotfiles/common/mise/config.toml"))
	return err == nil && expectedErr == nil && actual == expected
}

func (e Engine) readTools() (string, map[string]bool, error) {
	output, err := e.Runner.Run([]string{"mise", "ls", "--json"}, true)
	if err != nil {
		return "", nil, fmt.Errorf("开发工具状态查询失败：%w；请运行 mise ls --json 核对", taskexec.Redacted(err))
	}
	data, err := decodeState(output)
	if err != nil {
		return "", nil, err
	}
	installed := map[string]bool{}
	for name, value := range data {
		rows, ok := value.([]any)
		if !ok {
			return "", nil, fmt.Errorf("开发工具版本列表无法识别")
		}
		for _, value := range rows {
			row, ok := value.(map[string]any)
			if !ok {
				return "", nil, fmt.Errorf("开发工具状态无法识别")
			}
			version, versionOK := row["version"].(string)
			ready, readyOK := row["installed"].(bool)
			if !versionOK || !readyOK {
				return "", nil, fmt.Errorf("开发工具状态缺少版本/安装标记")
			}
			if ready {
				installed[shortTool(name)+"@"+version] = true
			}
		}
	}
	bytes, err := jsonBytes(data)
	return string(bytes), installed, err
}

func (e Engine) toolsPlan(p *Plan) error {
	wanted, sources, err := e.desiredTools()
	if err != nil {
		return err
	}
	_, installed, err := e.readTools()
	if err != nil {
		return err
	}
	p.toolInstalled, p.toolSources = installed, sources
	brewRoots := []string{}
	if e.Profile == "macos" {
		if p.Packages != nil {
			for _, root := range p.Packages.Roots {
				if strings.HasPrefix(root, "brew:") {
					brewRoots = append(brewRoots, strings.TrimPrefix(root, "brew:"))
				}
			}
		} else {
			brewRoots, err = e.queryNames([]string{"brew", "list", "--formula", "--full-name", "--installed-on-request"}, false, true)
			if err != nil {
				return err
			}
		}
	}
	for name, version := range wanted {
		if installed[shortTool(name)+"@"+version] {
			continue
		}
		p.Tools = append(p.Tools, name+"@"+version)
		for _, root := range brewRoots {
			root = strings.TrimPrefix(root, "homebrew/core/")
			base, _, _ := strings.Cut(root, "@")
			if base == shortTool(name) || (shortTool(name) == "go" && base == "golang") {
				p.Pending = append(p.Pending, "Homebrew 另有显式安装的开发工具，先核对迁移来源："+root)
			}
		}
		patterns := map[string][]string{
			"node":   {".nvm/versions/node/*/bin/node", ".asdf/installs/nodejs/*/bin/node"},
			"python": {".pyenv/versions/*/bin/python3", ".local/share/uv/python/*/bin/python3", ".asdf/installs/python/*/bin/python3"},
			"ruby":   {".rbenv/versions/*/bin/ruby"}, "bun": {".bun/bin/bun"}, "flutter": {"development/flutter/bin/flutter"},
		}
		for _, pattern := range patterns[shortTool(name)] {
			paths, _ := filepath.Glob(filepath.Join(e.Home, pattern))
			for _, path := range paths {
				if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0 {
					p.Pending = append(p.Pending, "开发工具另有安装来源，先核对/登记已有版本："+shortTool(name)+" ("+path+")")
				}
			}
		}
	}
	sort.Strings(p.Tools)
	if len(p.Tools) > 0 && !e.globalToolsReady() && !has(p.Stages, "config") {
		p.Pending = append(p.Pending, "请先部署本仓库的全局工具配置，再执行 --only tools")
	}
	return nil
}

func (e Engine) applyTools(p *Plan, out io.Writer) error {
	if len(p.Tools) == 0 {
		fmt.Fprintln(out, "[+] 开发工具无需补装，现有版本保留。")
		return nil
	}
	if !e.globalToolsReady() {
		return fmt.Errorf("全局工具配置不是本仓库来源，未安装工具")
	}
	if err := unchanged(p.toolSources); err != nil {
		return err
	}
	_, before, err := e.readTools()
	if err != nil {
		return err
	}
	if len(before) != len(p.toolInstalled) {
		return fmt.Errorf("预览后开发工具安装状态发生变化，请重新运行")
	}
	for tool := range before {
		if !p.toolInstalled[tool] {
			return fmt.Errorf("预览后开发工具安装状态发生变化，请重新运行")
		}
	}
	fmt.Fprintln(out, "[~] 补装固定版本开发工具；实时显示 mise 安装输出")
	command := []string{"mise", "install", "--locked", "--raw"}
	command = append(command, p.Tools...)
	if _, err = e.Runner.Run(command, false); err != nil {
		return err
	}
	fmt.Fprintln(out, "[~] 复核开发工具版本及原有版本是否保留")
	_, after, err := e.readTools()
	if err != nil {
		return err
	}
	for tool := range before {
		if !after[tool] {
			return fmt.Errorf("现有开发工具版本未保留：%s", tool)
		}
	}
	for _, tool := range p.Tools {
		name, version, _ := strings.Cut(tool, "@")
		if !after[shortTool(name)+"@"+version] {
			return fmt.Errorf("开发工具未完成补装：%s", tool)
		}
	}
	fmt.Fprintln(out, "[+] 开发工具已补装；未删除其他版本或旧管理器。")
	return nil
}
