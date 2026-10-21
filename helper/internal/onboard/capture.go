// Package onboard captures the host baseline and coordinates additive setup.
// It never writes managed manifests or invokes the convergence/cleanup scripts.
package onboard

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/sweard/system_sync/helper/internal/localconfig"
	"github.com/sweard/system_sync/helper/internal/packageedit"
	"github.com/sweard/system_sync/helper/internal/packagestate"
	"github.com/sweard/system_sync/helper/internal/taskexec"
)

type Runner interface {
	Run([]string, bool) (string, error)
}

type Engine struct {
	Root, Home, Profile string
	Runner              Runner
	Environment         map[string]string
	UID                 int
}

type Package struct {
	Manager string `json:"manager"`
	Name    string `json:"name"`
	Line    string `json:"-"`
	Problem string `json:"problem,omitempty"`
}

func (p Package) ID() string { return p.Manager + ":" + p.Name }

type Candidate struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Diff    string `json:"diff"`
}

type Capture struct {
	SchemaVersion int               `json:"schema_version"`
	Profile       string            `json:"profile"`
	Installed     []string          `json:"installed"`
	Roots         []string          `json:"roots"`
	Foreign       []string          `json:"foreign"`
	Orphans       []string          `json:"orphans"`
	Undeclared    []string          `json:"undeclared"`
	Missing       []Package         `json:"missing"`
	Pending       []string          `json:"pending"`
	Candidate     Candidate         `json:"candidate"`
	Declarations  []Package         `json:"declarations"`
	Sources       map[string]string `json:"-"`
}

var packageName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9@+._-]*(?::[a-z0-9][a-z0-9-]*)?$`)
var brewName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9@+._-]*(?:/[A-Za-z0-9][A-Za-z0-9@+._-]*){0,2}$`)

func unique(values []string) []string {
	seen := map[string]bool{}
	for _, value := range values {
		seen[value] = true
	}
	result := make([]string, 0, len(seen))
	for value := range seen {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func set(values []string) map[string]bool {
	result := map[string]bool{}
	for _, value := range values {
		result[value] = true
	}
	return result
}

func names(output string, allowEmpty bool, brew bool) ([]string, error) {
	result := []string{}
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if line == "" {
			continue
		}
		valid := packageName.MatchString(line)
		if brew {
			valid = brewName.MatchString(line)
		}
		if !valid {
			return nil, fmt.Errorf("包查询返回了无法识别的名称，已停止")
		}
		result = append(result, line)
	}
	if len(result) == 0 && !allowEmpty {
		return nil, fmt.Errorf("已安装包查询为空，拒绝将其视作完整本机清单")
	}
	return unique(result), nil
}

func (e Engine) queryNames(command []string, emptyExitOne, allowEmpty bool) ([]string, error) {
	output, err := e.Runner.Run(command, true)
	if err != nil && emptyExitOne {
		var failure *taskexec.Failure
		if errors.As(err, &failure) && failure.Code == 1 && !failure.Timeout && strings.TrimSpace(failure.Output) == "" {
			return []string{}, nil
		}
	}
	if err != nil {
		return nil, fmt.Errorf("本机清单查询失败：%s", taskexec.Quote(command))
	}
	return names(output, allowEmpty, e.Profile == "macos")
}

func prefix(manager string, values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, manager+":"+value)
	}
	return result
}

// Inventory records architecture-qualified RPM/dpkg identities as well as names
// used for declaration matching. The identities detect loss of a multilib copy.
func (e Engine) inventory() ([]string, error) {
	switch e.Profile {
	case "macos":
		formulae, err := e.queryNames([]string{"brew", "list", "--formula", "--full-name"}, false, true)
		if err != nil {
			return nil, err
		}
		casks, err := e.queryNames([]string{"brew", "list", "--cask"}, false, true)
		return unique(append(prefix("brew", formulae), prefix("cask", casks)...)), err
	case "arch":
		values, err := e.queryNames([]string{"pacman", "-Qq"}, false, false)
		return prefix("pacman", values), err
	case "debian":
		output, err := e.Runner.Run([]string{"dpkg-query", "-W", "-f=${Package}:${Architecture}\t${db:Status-Status}\n"}, true)
		if err != nil {
			return nil, fmt.Errorf("dpkg 已安装状态查询失败")
		}
		result := []string{}
		for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
			name, state, ok := strings.Cut(line, "\t")
			if !ok || !packageName.MatchString(name) {
				return nil, fmt.Errorf("dpkg 安装状态格式无法识别")
			}
			if state == "installed" {
				result = append(result, "apt:"+name)
			}
		}
		if len(result) == 0 {
			return nil, fmt.Errorf("dpkg 已安装清单为空")
		}
		return unique(result), nil
	case "fedora", "opensuse":
		output, err := e.Runner.Run([]string{"rpm", "-qa", "--qf", "%{NAME}\t%{ARCH}\n"}, true)
		if err != nil {
			return nil, fmt.Errorf("RPM 已安装状态查询失败")
		}
		result := []string{}
		for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
			name, arch, ok := strings.Cut(line, "\t")
			if !ok || !packageName.MatchString(name) || (!packageName.MatchString(arch) && !(name == "gpg-pubkey" && arch == "(none)")) {
				return nil, fmt.Errorf("RPM 安装状态格式无法识别")
			}
			result = append(result, e.manager()+":"+name+":"+arch)
		}
		if len(result) == 0 {
			return nil, fmt.Errorf("RPM 已安装清单为空")
		}
		return unique(result), nil
	}
	return nil, fmt.Errorf("不支持的 profile：%s", e.Profile)
}

func (e Engine) manager() string {
	return map[string]string{"arch": "pacman", "debian": "apt", "fedora": "dnf", "opensuse": "zypper", "macos": "brew"}[e.Profile]
}

func present(installed []string, p Package) bool {
	manager, name := p.Manager, p.Name
	if manager == "aur" {
		manager = "pacman"
	}
	for _, item := range installed {
		if item == manager+":"+name {
			return true
		}
		if (manager == "apt" || manager == "dnf" || manager == "zypper") && strings.HasPrefix(item, manager+":"+name+":") {
			return true
		}
		if manager == "cask" && item == "cask:"+name[strings.LastIndex(name, "/")+1:] {
			return true
		}
		if manager == "brew" && strings.TrimPrefix(item, "brew:homebrew/core/") == name {
			return true
		}
	}
	return false
}

func readManifest(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("清单必须是普通文件：%s", path)
	}
	data, err := os.ReadFile(path)
	return string(data), err
}

func (e Engine) declarations() ([]Package, map[string]string, string, error) {
	directory := filepath.Join(e.Root, "packages/linux", e.Profile)
	filename := "mise.toml"
	if e.Profile == "macos" {
		directory, filename = filepath.Join(e.Root, "packages/macos"), "Brewfile"
	}
	path := filepath.Join(directory, filename)
	source, err := readManifest(path)
	if err != nil {
		return nil, nil, "", err
	}
	sources := map[string]string{path: source}
	result := []Package{}
	if e.Profile == "macos" {
		entries, err := packageedit.InspectBrew(source)
		if err != nil {
			return nil, nil, "", err
		}
		for _, entry := range entries {
			result = append(result, Package{Manager: entry.Kind, Name: entry.Name, Line: entry.Line})
		}
		return result, sources, path, nil
	}
	// Read final native state as well: this catches inherited/environment config
	// instead of silently omitting a local override from the install plan.
	output, err := e.Runner.Run([]string{"mise", "-C", directory, "bootstrap", "packages", "status", "--json"}, true)
	if err != nil {
		return nil, nil, "", fmt.Errorf("mise 包声明状态查询失败")
	}
	var state any
	if err = packagestate.DecodeJSON(strings.NewReader(output), &state); err != nil {
		return nil, nil, "", fmt.Errorf("mise 包状态 JSON 无法识别")
	}
	managers := []string{e.manager()}
	if e.Profile == "arch" {
		managers = append(managers, "aur")
	}
	entries, err := packagestate.ParseStatus(state, managers)
	if err != nil {
		return nil, nil, "", err
	}
	// Preserve the original text and options; capture only appends missing roots.
	for _, name := range []string{"mise.local.toml", "mise.linux.toml", "mise.linux.local.toml"} {
		local := filepath.Join(directory, name)
		if _, err := os.Lstat(local); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return nil, nil, "", err
		}
		text, err := readManifest(local)
		if err != nil {
			return nil, nil, "", err
		}
		sources[local] = text
	}
	for _, text := range sources {
		if _, err := localconfig.Decode([]byte(text)); err != nil {
			return nil, nil, "", err
		}
	}
	for _, entry := range entries {
		p := Package{Manager: entry.Manager, Name: entry.Package}
		if !packageName.MatchString(p.Name) {
			return nil, nil, "", fmt.Errorf("包名不在初始化支持范围")
		}
		if entry.State != "installed" && entry.State != "missing" {
			p.Problem = "现有版本或安装状态需要人工处理：" + p.ID()
		}
		if entry.State == "missing" {
			found, supported := false, true
			for _, text := range sources {
				data, err := localconfig.Decode([]byte(text))
				if err != nil {
					return nil, nil, "", err
				}
				values := localconfig.Table(localconfig.Table(data["bootstrap"])["packages"])
				if value, ok := values[p.ID()]; ok {
					found = true
					switch value := value.(type) {
					case string:
						supported = supported && value == "latest"
					case map[string]any:
						supported = supported && value["version"] == "latest"
						for key, item := range value {
							if key != "version" && (key != "state" || item != "present") {
								supported = false
							}
						}
					default:
						supported = false
					}
				}
			}
			if !found || !supported {
				p.Problem = "缺失声明含固定版本、其他选项或继承来源，需单独处理：" + p.ID()
			}
		}
		if p.Manager == "aur" && entry.State == "missing" {
			p.Problem = "AUR 构建需单独审阅，请使用 package-install --aur：" + p.Name
		}
		result = append(result, p)
	}
	return result, sources, path, nil
}

func (e Engine) Capture() (*Capture, error) {
	installed, err := e.inventory()
	if err != nil {
		return nil, err
	}
	declarations, sources, manifest, err := e.declarations()
	if err != nil {
		return nil, err
	}
	c := &Capture{SchemaVersion: 1, Profile: e.Profile, Installed: installed, Roots: []string{}, Foreign: []string{}, Orphans: []string{}, Undeclared: []string{}, Missing: []Package{}, Pending: []string{}, Declarations: declarations, Sources: sources}
	known := map[string]bool{}
	for _, p := range declarations {
		known[p.ID()] = true
		if p.Manager != "tap" && !present(installed, p) {
			c.Missing = append(c.Missing, p)
		}
		if p.Problem != "" {
			c.Pending = append(c.Pending, p.Problem)
		}
	}
	additions := []string{}
	if e.Profile == "macos" {
		// Homebrew writes directly to a file: /dev/stdout is not portable under
		// macOS sandboxing. Temporary capture data is removed even on failure.
		dir, err := os.MkdirTemp("", "system-sync-brew-capture-")
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(dir)
		snapshot := filepath.Join(dir, "Brewfile")
		_, err = e.Runner.Run([]string{"brew", "bundle", "dump", "--file=" + snapshot, "--formula", "--cask", "--tap", "--no-describe", "--no-restart"}, true)
		if err != nil {
			return nil, fmt.Errorf("Homebrew 清单导出失败")
		}
		text, err := readManifest(snapshot)
		if err != nil {
			return nil, err
		}
		entries, err := packageedit.InspectBrew(text)
		if err != nil {
			return nil, err
		}
		for _, p := range entries {
			id := p.Kind + ":" + p.Name
			c.Roots = append(c.Roots, id)
			if known[id] {
				continue
			}
			c.Undeclared = append(c.Undeclared, id)
			if strings.Contains(p.Name, "/") && !strings.HasPrefix(p.Name, "homebrew/core/") && !strings.HasPrefix(p.Name, "homebrew/cask/") {
				c.Pending = append(c.Pending, "需核对 tap 来源与精确信任："+id)
				continue
			}
			additions = append(additions, strings.TrimSpace(p.Line))
		}
	} else {
		var roots, foreign, orphans []string
		switch e.Profile {
		case "arch":
			roots, err = e.queryNames([]string{"pacman", "-Qqen"}, true, true)
			if err == nil {
				foreign, err = e.queryNames([]string{"pacman", "-Qqem"}, true, true)
			}
			if err == nil {
				orphans, err = e.queryNames([]string{"pacman", "-Qdtq"}, true, true)
			}
		case "debian":
			roots, err = e.queryNames([]string{"apt-mark", "showmanual"}, false, true)
		case "fedora":
			roots, err = e.queryNames([]string{"dnf", "repoquery", "--installed", "--userinstalled", "--queryformat", "%{name}"}, false, true)
		case "opensuse":
			var output string
			output, err = e.Runner.Run([]string{"zypper", "--xmlout", "--no-refresh", "packages", "--userinstalled"}, true)
			if err == nil {
				roots, err = packagestate.ParseZypper(strings.NewReader(output), "solvables")
			}
		}
		if err != nil {
			return nil, err
		}
		c.Roots, c.Foreign, c.Orphans = prefix(e.manager(), roots), unique(foreign), unique(orphans)
		for _, name := range roots {
			p := Package{Manager: e.manager(), Name: name}
			if !present(installed, p) {
				return nil, fmt.Errorf("根包未出现在已安装清单中，状态发生变化或查询不完整：%s", name)
			}
			if known[p.ID()] {
				continue
			}
			c.Undeclared = append(c.Undeclared, p.ID())
			additions = append(additions, localconfig.Quoted(p.ID())+` = "latest"`)
		}
		for _, name := range foreign {
			if !present(installed, Package{Manager: "pacman", Name: name}) {
				return nil, fmt.Errorf("foreign 查询与已安装清单不一致")
			}
			c.Roots = append(c.Roots, "foreign:"+name)
			if !known["aur:"+name] && !known["pacman:"+name] {
				c.Pending = append(c.Pending, "foreign 来源待核对，不自动认定为 AUR："+name)
			}
		}
	}
	c.Roots, c.Pending, c.Undeclared = unique(c.Roots), unique(c.Pending), unique(c.Undeclared)
	before := sources[manifest]
	after := before
	if len(additions) > 0 {
		block := "\n# 本机采集的新增根包候选；审阅后合并，原声明及选项保持。\n" + strings.Join(unique(additions), "\n") + "\n"
		if e.Profile == "macos" {
			after = strings.TrimRight(before, "\n") + "\n" + block
		} else {
			// Append within the existing table, before any following table.
			lines := strings.SplitAfter(before, "\n")
			active, inserted := false, false
			var out strings.Builder
			for _, line := range lines {
				trimmed := strings.TrimSpace(line)
				if strings.HasPrefix(trimmed, "[") {
					if active {
						out.WriteString(block)
						inserted = true
						active = false
					}
					if trimmed == "[bootstrap.packages]" || strings.HasPrefix(trimmed, "[bootstrap.packages] #") {
						active = true
					}
				}
				out.WriteString(line)
			}
			if !inserted && active {
				if !strings.HasSuffix(before, "\n") {
					out.WriteByte('\n')
				}
				out.WriteString(block)
				inserted = true
			}
			if !inserted {
				return nil, fmt.Errorf("清单没有独立的 [bootstrap.packages] 表；请手工合并")
			}
			after = out.String()
			if _, err := localconfig.Decode([]byte(after)); err != nil {
				return nil, err
			}
		}
	}
	relative, _ := filepath.Rel(e.Root, manifest)
	c.Candidate = Candidate{relative, after, packageedit.UnifiedDiff(before, after, relative)}
	// Refuse a capture taken across a concurrent package transaction.
	again, err := e.inventory()
	if err != nil {
		return nil, err
	}
	if strings.Join(installed, "\n") != strings.Join(again, "\n") {
		return nil, fmt.Errorf("采集期间安装状态发生变化，请重试")
	}
	return c, nil
}

func jsonBytes(value any) ([]byte, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	return append(data, '\n'), err
}
