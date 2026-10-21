// Package audit produces the same read-only, redacted report as the original CLI.
package audit

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/sweard/system_sync/helper/internal/packagestate"
	"github.com/sweard/system_sync/helper/internal/query"
)

type Finding map[string]any
type Report struct {
	SchemaVersion int                  `json:"schema_version"`
	Context       map[string]any       `json:"context"`
	Summary       map[string]int       `json:"summary"`
	Sections      map[string][]Finding `json:"sections"`
}

type Audit struct {
	root, home, data, callerPath string
	environment                  map[string]string
	runner                       query.Runner
	once                         sync.Once
	tools                        []map[string]any
	toolsError                   error
	findings                     map[string][]Finding
	mu                           sync.Mutex
}

func New(root string, environment map[string]string, runner query.Runner) *Audit {
	root, _ = filepath.Abs(root)
	root = resolve(root)
	env := copyEnvironment(environment)
	home := env["HOME"]
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	home, _ = filepath.Abs(home)
	data := env["MISE_DATA_DIR"]
	if data == "" {
		base := env["XDG_DATA_HOME"]
		if base == "" {
			base = filepath.Join(home, ".local/share")
		}
		data = filepath.Join(base, "mise")
	}
	caller, ok := env["SYSTEM_SYNC_AUDIT_CALLER_PATH"]
	if !ok {
		caller = env["PATH"]
	}
	if runner == nil {
		qenv := copyEnvironment(env)
		for k, v := range map[string]string{"MISE_AUTO_INSTALL": "false", "MISE_TASK_RUN_AUTO_INSTALL": "false", "PYTHONDONTWRITEBYTECODE": "1", "HOMEBREW_NO_AUTO_UPDATE": "1", "NO_COLOR": "1", "TERM": "dumb"} {
			qenv[k] = v
		}
		runner = query.ProcessRunner{Root: root, Environment: qenv}
	}
	return &Audit{root: root, home: home, data: filepath.Clean(data), callerPath: caller, environment: env, runner: runner, findings: map[string][]Finding{}}
}

func copyEnvironment(env map[string]string) map[string]string {
	result := map[string]string{}
	for k, v := range env {
		result[k] = v
	}
	return result
}

func resolve(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return filepath.Clean(path)
}

func within(path, parent string) bool {
	rel, err := filepath.Rel(parent, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func regular(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}
func directory(path string) bool { info, err := os.Stat(path); return err == nil && info.IsDir() }
func exists(path string) bool    { _, err := os.Stat(path); return err == nil }
func executable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0
}

func (a *Audit) display(value string) string {
	if strings.HasPrefix(value, "~") {
		return value
	}
	for _, p := range [][2]string{{a.root, "<repo>"}, {a.home, "~"}} {
		if value == p[0] || strings.HasPrefix(value, p[0]+"/") {
			return p[1] + strings.TrimPrefix(value, p[0])
		}
	}
	return value
}

func (a *Audit) expand(value string) string {
	if strings.HasPrefix(value, "~/") {
		return filepath.Join(a.home, value[2:])
	}
	return filepath.Clean(value)
}

func (a *Audit) add(section, kind, code, item, message string, details Finding) {
	finding := Finding{"kind": kind, "code": code, "item": a.display(item), "message": message}
	for k, v := range details {
		finding[k] = v
	}
	a.mu.Lock()
	a.findings[section] = append(a.findings[section], finding)
	a.mu.Unlock()
}

func (a *Audit) queryJSON(command []string, target any) error {
	output, err := a.runner.Run(command, nil)
	if err != nil {
		return err
	}
	if err := packagestate.DecodeJSON(strings.NewReader(output), target); err != nil {
		return query.Error("状态 JSON 无法识别；报告尚未完成")
	}
	return nil
}

func (a *Audit) loadTools() ([]map[string]any, error) {
	a.once.Do(func() { a.tools, a.toolsError = a.readTools() })
	return a.tools, a.toolsError
}

func (a *Audit) readTools() ([]map[string]any, error) {
	output, err := a.runner.Run([]string{"mise", "ls", "--current", "--json"}, nil)
	if err != nil {
		return nil, err
	}
	var data any
	if packagestate.DecodeJSON(strings.NewReader(output), &data) != nil {
		return nil, query.Error("状态 JSON 无法识别；报告尚未完成")
	}
	if _, ok := data.(map[string]any); !ok {
		return nil, query.Error("工具状态顶层结构无法识别")
	}
	// Decode object members in upstream order; sorting a map would change the
	// order of command findings and the point at which a failed batch is reported.
	d := json.NewDecoder(strings.NewReader(output))
	_, _ = d.Token()
	rows := []map[string]any{}
	for d.More() {
		key, _ := d.Token()
		name := key.(string)
		var value any
		if err := d.Decode(&value); err != nil {
			return nil, query.Error("工具版本列表无法识别")
		}
		versions, ok := value.([]any)
		if !ok || len(versions) == 0 {
			return nil, query.Error("工具版本列表无法识别")
		}
		for _, value := range versions {
			row, ok := value.(map[string]any)
			if !ok {
				return nil, query.Error("工具版本状态缺少必需字段")
			}
			if _, ok := row["version"].(string); !ok {
				return nil, query.Error("工具版本状态缺少必需字段")
			}
			if _, ok := row["installed"].(bool); !ok {
				return nil, query.Error("工具版本状态缺少必需字段")
			}
			row["tool"] = name
			rows = append(rows, row)
		}
	}
	return rows, nil
}

func (a *Audit) collectTools() error {
	rows, err := a.loadTools()
	if err != nil {
		return err
	}
	installed := []string{}
	for _, row := range rows {
		name, version := row["tool"].(string), row["version"].(string)
		source := "unknown"
		if value := row["source"]; value != nil && value != false && value != "" {
			m, ok := value.(map[string]any)
			if !ok {
				return query.Error("工具配置来源结构无法识别")
			}
			v := m["path"]
			if v == nil || v == "" {
				v = m["type"]
			}
			if v == nil || v == "" {
				v = "unknown"
			}
			var valid bool
			source, valid = v.(string)
			if !valid {
				return query.Error("工具配置来源路径无法识别")
			}
		}
		kind, code, suffix := "drift", "tool_missing", " 尚未安装"
		if row["installed"] == true {
			kind, code, suffix = "ok", "tool_installed", " 已安装"
		}
		a.add("tools", kind, code, name, "声明版本 "+version+suffix, Finding{"version": version, "source": a.display(source)})
		location := row["install_path"]
		if location != nil {
			p, ok := location.(string)
			if !ok {
				return query.Error("工具安装路径无法识别")
			}
			if row["installed"] == true {
				installed = append(installed, resolve(p))
				if info, err := os.Lstat(p); err == nil && info.Mode()&os.ModeSymlink != 0 {
					a.add("tools", "local", "linked_install", name, "已注册的链接安装；原来源需要保留", Finding{"path": a.display(resolve(p))})
				}
			}
		}
	}
	patterns := [][2]string{{"nvm", ".nvm/versions/node/*/bin/node"}, {"pyenv", ".pyenv/versions/*/bin/python3"}, {"uv Python", ".local/share/uv/python/*/bin/python3"}, {"rbenv", ".rbenv/versions/*/bin/ruby"}, {"asdf", ".asdf/installs/*/*/bin/*"}, {"Bun installer", ".bun/bin/bun"}}
	for _, pattern := range patterns {
		paths, _ := filepath.Glob(filepath.Join(a.home, pattern[1]))
		candidates := []string{}
		for _, p := range paths {
			if !executable(p) {
				continue
			}
			owned := false
			for _, prefix := range installed {
				if within(resolve(p), prefix) {
					owned = true
					break
				}
			}
			if !owned {
				candidates = append(candidates, a.display(p))
			}
		}
		if len(candidates) > 0 {
			sort.Strings(candidates)
			a.add("tools", "candidate", "external_installations", pattern[0], "发现其他安装来源；可能是历史版本或备用安装，需要核对意图", Finding{"items": candidates, "count": len(candidates)})
		}
	}
	if len(rows) == 0 {
		a.add("tools", "ok", "no_tools", "[tools]", "当前配置没有工具版本声明", nil)
	}
	return nil
}

type commandPath struct {
	value string
	err   error
}

func (a *Audit) commandPaths(rows []map[string]any) map[string]commandPath {
	commands := []string{}
	seen := map[string]bool{}
	for _, row := range rows {
		command := Commands[row["tool"].(string)]
		if row["installed"] == true && command != "" && !seen[command] {
			commands = append(commands, command)
			seen[command] = true
		}
	}
	paths := map[string]commandPath{}
	for start := 0; start < len(commands); start += 4 {
		end := start + 4
		if end > len(commands) {
			end = len(commands)
		}
		batch := commands[start:end]
		values := make([]commandPath, len(batch))
		var wg sync.WaitGroup
		for i, command := range batch {
			wg.Add(1)
			go func(i int, command string) {
				defer wg.Done()
				value, err := a.runner.Run([]string{"mise", "which", command}, nil)
				values[i] = commandPath{strings.TrimSpace(value), err}
			}(i, command)
		}
		wg.Wait()
		failed := false
		for i, command := range batch {
			paths[command] = values[i]
			failed = failed || values[i].err != nil
		}
		if failed {
			break
		}
	}
	return paths
}

func (a *Audit) policyPaths() (map[string]bool, error) {
	source := filepath.Join(a.root, "dotfiles/common/shell/path.sh")
	if !regular(source) {
		return nil, query.Error("共享 PATH 来源文件缺失")
	}
	env := map[string]string{}
	for _, name := range []string{"HOME", "ANDROID_HOME", "CARGO_HOME", "MISE_DATA_DIR", "XDG_DATA_HOME"} {
		if value, ok := a.environment[name]; ok {
			env[name] = value
		}
	}
	env["HOME"] = a.home
	env["PATH"] = "/usr/bin:/bin:/usr/sbin:/sbin"
	env["MISE_AUTO_INSTALL"] = "false"
	env["MISE_TASK_RUN_AUTO_INSTALL"] = "false"
	bash := "/bin/bash"
	if !regular(bash) {
		bash = "bash"
	}
	output, err := a.runner.Run([]string{bash, "--noprofile", "--norc", "-c", `set -e; . "$1"; _system_sync_user_path; printf "%s" "$PATH"`, "audit-path", source}, env)
	if err != nil {
		return nil, err
	}
	paths := map[string]bool{}
	for _, p := range strings.Split(output, ":") {
		paths[p] = true
	}
	for _, p := range []string{"/usr/bin", "/bin", "/usr/sbin", "/sbin"} {
		delete(paths, p)
	}
	return paths, nil
}

func (a *Audit) retainedPath(entry string) string {
	if SystemPaths[entry] {
		return "系统、IDE 或包管理器提供的路径"
	}
	for _, prefix := range []string{"/var/run/com.apple.security.cryptexd/", "/Applications/Xcode.app/", "/opt/homebrew/", "/home/linuxbrew/.linuxbrew/"} {
		if strings.HasPrefix(entry, prefix) {
			return "系统、IDE 或包管理器提供的路径"
		}
	}
	for _, prefix := range []string{filepath.Join(a.home, ".codex/tmp"), filepath.Join(a.home, ".cache/codex-runtimes"), "/Applications/ChatGPT.app/Contents", "/pkg/env"} {
		if within(entry, prefix) {
			return "宿主应用或临时运行环境提供的路径"
		}
	}
	return ""
}

func (a *Audit) collectPath() error {
	entries := strings.Split(a.callerPath, ":")
	counts := map[string]int{}
	unique := []string{}
	for _, entry := range entries {
		if counts[entry] == 0 {
			unique = append(unique, entry)
		}
		counts[entry]++
	}
	for _, entry := range unique {
		label := entry
		if label == "" {
			label = "<空项>"
		}
		if entry == "" || !filepath.IsAbs(entry) {
			a.add("path", "drift", "unsafe_path", label, "空或相对 PATH 项依赖当前目录", nil)
		} else if !directory(entry) {
			kind, message := "drift", "PATH 目录不存在或不可访问"
			if retained := a.retainedPath(entry); retained != "" {
				kind, message = "local", retained+"；当前目录不存在或不可访问"
			}
			a.add("path", kind, "missing_directory", entry, message, nil)
		}
		if counts[entry] > 1 {
			a.add("path", "drift", "duplicate_path", label, fmt.Sprintf("重复 %d 次", counts[entry]), nil)
		}
	}
	declared, err := a.policyPaths()
	if err != nil {
		return err
	}
	missing := []string{}
	for p := range declared {
		if counts[p] == 0 {
			missing = append(missing, p)
		}
	}
	sort.Strings(missing)
	for _, p := range missing {
		a.add("path", "drift", "policy_path_missing", p, "共享 PATH 规则声明的现有目录未进入调用环境", nil)
	}
	for _, entry := range unique {
		if entry == "" || !filepath.IsAbs(entry) || !directory(entry) || declared[entry] || within(entry, a.data) {
			continue
		}
		if retained := a.retainedPath(entry); retained != "" {
			a.add("path", "local", "system_path", entry, retained, nil)
		} else {
			a.add("path", "candidate", "unmanaged_path", entry, "未由共享 PATH 规则声明，需要核对来源", nil)
		}
	}
	rows, err := a.loadTools()
	if err != nil {
		return err
	}
	expectedPaths := a.commandPaths(rows)
	requested := map[string]bool{}
	for _, row := range rows {
		tool := row["tool"].(string)
		requested[tool] = true
		command := Commands[tool]
		if row["installed"] != true || command == "" {
			continue
		}
		expected := expectedPaths[command]
		if expected.err != nil {
			return expected.err
		}
		if expected.value == "" || strings.Contains(expected.value, "\n") || !filepath.IsAbs(expected.value) {
			return query.Error("mise which 返回了无法识别的命令路径")
		}
		found := []string{}
		resolved := map[string]bool{}
		for _, entry := range entries {
			if entry == "" || !filepath.IsAbs(entry) {
				continue
			}
			p := filepath.Join(entry, command)
			canonical := resolve(p)
			if executable(p) && !resolved[canonical] {
				found = append(found, p)
				resolved[canonical] = true
			}
		}
		if len(found) == 0 {
			a.add("path", "drift", "command_missing", command, "调用环境找不到已声明工具的主命令", nil)
			continue
		}
		first := found[0]
		shim := filepath.Join(a.data, "shims", command)
		virtual := a.environment["VIRTUAL_ENV"]
		if tool == "python" && virtual != "" && within(first, virtual) {
			a.add("path", "local", "virtual_environment", command, "当前虚拟环境接管 Python", Finding{"path": a.display(first)})
		} else if first != shim && resolve(first) != resolve(expected.value) {
			a.add("path", "drift", "command_shadowed", command, "PATH 中优先命令与 mise 选择不一致", Finding{"actual": a.display(first), "expected": a.display(expected.value)})
		} else {
			a.add("path", "ok", "command_selected", command, "命令来源与 mise 选择一致", Finding{"path": a.display(first)})
		}
		alternatives := []string{}
		local := true
		for _, p := range found[1:] {
			if p == shim || within(p, a.data) || resolve(p) == resolve(expected.value) {
				continue
			}
			alternatives = append(alternatives, a.display(p))
			if !strings.HasPrefix(p, "/usr/bin/") && !strings.HasPrefix(p, "/bin/") && !strings.HasPrefix(p, "/Applications/Xcode.app/") {
				local = false
			}
		}
		if len(alternatives) > 0 {
			kind := "candidate"
			if local {
				kind = "local"
			}
			a.add("path", kind, "alternative_commands", command, "PATH 中另有同名命令；不等同于重复管理", Finding{"items": alternatives, "count": len(alternatives)})
		}
	}
	flutter := filepath.Join(a.home, "development/flutter/bin/flutter")
	if !requested["flutter"] && regular(flutter) && declared[filepath.Dir(flutter)] {
		a.add("path", "local", "retained_flutter", "flutter", "共享 PATH 明确保留原安装，版本尚未由 mise 管理", Finding{"path": a.display(flutter)})
	}
	return nil
}

func (a *Audit) collectConfig() error {
	var data map[string]any
	if err := a.queryJSON([]string{"mise", "bootstrap", "dotfiles", "status", "--json"}, &data); err != nil {
		return err
	}
	targets := []string{}
	for _, section := range [][2]string{{"files", "target"}, {"edits", "path"}} {
		items, ok := data[section[0]].([]any)
		if !ok {
			return query.Error("配置映射状态结构无法识别")
		}
		for _, value := range items {
			item, ok := value.(map[string]any)
			if !ok {
				return query.Error("配置映射缺少目标路径")
			}
			target, ok := item[section[1]].(string)
			if !ok {
				return query.Error("配置映射缺少目标路径")
			}
			state, ok := item["state"].(string)
			if !ok {
				return query.Error("配置映射状态无法识别")
			}
			targets = append(targets, a.expand(target))
			kind := ""
			switch state {
			case "applied", "absent":
				kind = "ok"
			case "missing", "source_missing", "differs", "would_remove":
				kind = "drift"
			case "tracked", "not_on_this_machine":
				kind = "local"
			default:
				return query.Error("配置映射出现未知状态；不能判定同步完成")
			}
			a.add("config", kind, "dotfile_state", target, "映射状态："+state, nil)
		}
	}
	if value, exists := data["orphaned"]; exists {
		items, ok := value.([]any)
		if !ok {
			return query.Error("已取消部署的配置列表无法识别")
		}
		for _, value := range items {
			item, ok := value.(map[string]any)
			if !ok {
				return query.Error("已取消部署的配置路径无法识别")
			}
			target, exists := item["target"]
			if !exists {
				target = item["path"]
			}
			p, ok := target.(string)
			if !ok {
				return query.Error("已取消部署的配置路径无法识别")
			}
			a.add("config", "candidate", "orphaned_mapping", p, "以前部署过的文件不再由当前映射管理", nil)
		}
	}
	for _, relative := range ConfigCandidates {
		p := filepath.Join(a.home, relative)
		if !exists(p) {
			continue
		}
		owned := false
		for _, target := range targets {
			if within(p, target) {
				owned = true
				break
			}
		}
		if owned {
			continue
		}
		if directory(p) {
			children, err := os.ReadDir(p)
			if err != nil {
				return err
			}
			if len(children) == 0 {
				continue
			}
		}
		a.add("config", "candidate", "unmanaged_config", p, "已存在，但不在当前配置映射中；仅列路径", nil)
	}
	for _, p := range []string{filepath.Join(a.home, ".config/mise/secrets.env"), filepath.Join(a.home, ".zshrc.local"), filepath.Join(a.root, "mise.local.toml")} {
		if exists(p) {
			a.add("config", "local", "private_local", p, "按约定保留在本机，不读取内容", nil)
		}
	}
	return nil
}

func (a *Audit) collectPackages() error {
	output, err := a.runner.Run([]string{filepath.Join(a.root, "scripts/system-sync.sh"), "--status"}, nil)
	if err != nil {
		return err
	}
	groups, err := ParsePackageStatus(output)
	if err != nil {
		return err
	}
	for _, group := range groups {
		if len(group.Items) > 0 || group.Kind == "ok" {
			a.add("packages", group.Kind, "package_group", group.Title, fmt.Sprintf("%d 项", len(group.Items)), Finding{"items": group.Items, "count": len(group.Items)})
		}
		if group.Title == "mise 最终生效声明" {
			mismatched := []string{}
			for _, item := range group.Items {
				if strings.HasSuffix(item, "[version_mismatch]") || strings.HasSuffix(item, "[needs_repair]") {
					mismatched = append(mismatched, item)
				}
			}
			if len(mismatched) > 0 {
				a.add("packages", "drift", "package_version", group.Title, "存在版本不符或需要修复的声明", Finding{"items": mismatched})
			}
		}
	}
	if match := regexp.MustCompile(`bundle check 退出码为 ([0-9]+)`).FindStringSubmatch(output); len(match) > 0 {
		code, _ := strconv.Atoi(match[1])
		if code != 0 {
			a.add("packages", "drift", "bundle_check", "Homebrew", "bundle check 未通过，需单独运行 mise run package-status", nil)
		}
	}
	if _, preview, ok := strings.Cut(output, "Homebrew cleanup 精确预演\n"); ok {
		preview, _, _ = strings.Cut(preview, "状态模式：")
		preview = strings.TrimSpace(preview)
		if preview != "" && preview != "（无输出）" {
			a.add("packages", "candidate", "cleanup_review", "Homebrew", "cleanup 预演含额外输出，需单独运行 mise run package-dry-run 审阅", nil)
		}
	}
	return nil
}

func (a *Audit) Execute(selected []string) Report {
	collectors := map[string]func() error{"tools": a.collectTools, "packages": a.collectPackages, "path": a.collectPath, "config": a.collectConfig}
	wanted := map[string]bool{}
	for _, name := range selected {
		wanted[name] = true
	}
	var wg sync.WaitGroup
	for _, section := range Sections {
		if !wanted[section] {
			continue
		}
		a.findings[section] = []Finding{}
	}
	for _, section := range Sections {
		if !wanted[section] {
			continue
		}
		wg.Add(1)
		go func(section string) {
			defer wg.Done()
			if err := collectors[section](); err != nil {
				message := "文件或状态无法读取；该项尚未核对"
				var safe query.Error
				if errors.As(err, &safe) {
					message = string(safe)
				}
				a.add(section, "error", "collector_failed", section, message, nil)
			}
		}(section)
	}
	wg.Wait()
	summary := map[string]int{}
	for kind := range Kinds {
		summary[kind] = 0
	}
	for _, items := range a.findings {
		for _, item := range items {
			summary[item["kind"].(string)]++
		}
	}
	pathSource := "process"
	if _, ok := a.environment["SYSTEM_SYNC_AUDIT_CALLER_PATH"]; ok {
		pathSource = "caller"
	}
	report := Report{1, map[string]any{"root": a.root, "path_source": pathSource, "path_entries": len(strings.Split(a.callerPath, ":"))}, summary, a.findings}
	redactReport(&report, a.environment)
	return report
}
