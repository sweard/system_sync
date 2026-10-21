// Package packageedit performs one native package operation and checked edits.
package packageedit

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/sweard/system_sync/helper/internal/localconfig"
	"github.com/sweard/system_sync/helper/internal/packagestate"
	"github.com/sweard/system_sync/helper/internal/syncstate"
	"github.com/sweard/system_sync/helper/internal/taskexec"
)

var Managers = map[string]string{"arch": "pacman", "debian": "apt", "fedora": "dnf", "opensuse": "zypper"}
var essential = map[string]string{
	"macos": "git mise", "arch": "base bash coreutils filesystem glibc pacman shadow systemd util-linux",
	"debian":   "apt base-files base-passwd bash coreutils dash dpkg libc6 login passwd sed systemd systemd-sysv tar util-linux",
	"fedora":   "bash coreutils dnf dnf5 filesystem glibc rpm setup shadow-utils systemd util-linux",
	"opensuse": "bash coreutils filesystem glibc openSUSE-release rpm shadow systemd util-linux zypper",
}
var boot = map[string]*regexp.Regexp{
	"arch":     regexp.MustCompile(`^(linux($|-)|amd-ucode$|intel-ucode$|grub$|refind$|limine$|syslinux$)`),
	"debian":   regexp.MustCompile(`^(linux-(image|headers|modules)|grub|shim|initramfs-tools|cryptsetup|lvm2|mdadm)`),
	"fedora":   regexp.MustCompile(`^(kernel($|-)|grub2|shim|dracut|cryptsetup|lvm2|mdadm)`),
	"opensuse": regexp.MustCompile(`^(kernel($|-)|grub2|shim|dracut|cryptsetup|lvm2|mdadm)`),
}

type Runner interface {
	Run([]string, bool) (string, error)
}
type Options struct {
	Action, Package                   string
	DryRun, Cask, Formula, AUR, Trust bool
}
type Command struct {
	Root, Profile, Manager, Kind, Directory, Manifest, Name, Audit string
	Options                                                        Options
	Runner                                                         Runner
	Aliases                                                        map[string]bool
	Before, After                                                  map[string]string
	readOrder, afterOrder                                          []string
	UID                                                            int
	LookPath                                                       func(string) (string, error)
	AtomicWrite                                                    func(string, string) error
}

func New(root, profile string, options Options, runner Runner) (*Command, error) {
	manager := Managers[profile]
	directory := filepath.Join(root, "packages/linux", profile)
	filename := "mise.toml"
	if profile == "macos" {
		manager, directory, filename = "brew", filepath.Join(root, "packages/macos"), "Brewfile"
	}
	if manager == "" {
		return nil, fmt.Errorf("未知平台：%s", profile)
	}
	if options.AUR {
		if profile != "arch" {
			return nil, fmt.Errorf("--aur 仅适用于 Arch 系 Linux")
		}
		manager = "aur"
	}
	if profile != "macos" && (options.Cask || options.Formula || options.Trust) {
		return nil, fmt.Errorf("--cask、--formula、--trust 仅适用于 macOS Homebrew")
	}
	kind := "formula"
	if options.Cask {
		kind = "cask"
	}
	p := &Command{Root: root, Profile: profile, Manager: manager, Kind: kind, Directory: directory, Manifest: filepath.Join(directory, filename), Name: options.Package, Options: options, Runner: runner, Aliases: map[string]bool{options.Package: true}, Before: map[string]string{}, After: map[string]string{}, UID: os.Geteuid(), LookPath: exec.LookPath, AtomicWrite: atomicWrite}
	if err := p.validateName(p.Name); err != nil {
		return nil, err
	}
	return p, nil
}
func short(name string) string { return name[strings.LastIndex(name, "/")+1:] }
func thirdParty(name string) bool {
	return strings.Count(name, "/") == 2 && !strings.HasPrefix(name, "homebrew/core/") && !strings.HasPrefix(name, "homebrew/cask/")
}
func (p *Command) validateName(name string) error {
	valid := linuxName.MatchString(name)
	if p.Profile == "macos" {
		valid = brewName.MatchString(name)
	}
	if !valid || p.Profile != "debian" && strings.Contains(name, ":") {
		return fmt.Errorf("请指定一个准确包名，不接受路径、URL、通配符、版本表达式或原生命令选项：%s", name)
	}
	return nil
}
func (p *Command) read(path string) (string, error) {
	if value, ok := p.Before[path]; ok {
		return value, nil
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	directory, err := filepath.EvalSymlinks(p.Directory)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || filepath.Dir(resolved) != directory {
		return "", fmt.Errorf("要求普通的本地清单文件：%s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	p.Before[path] = string(data)
	p.readOrder = append(p.readOrder, path)
	return string(data), nil
}
func (p *Command) setAfter(path, content string) {
	if _, ok := p.After[path]; !ok {
		p.afterOrder = append(p.afterOrder, path)
	}
	p.After[path] = content
}
func lineSet(output string) map[string]bool {
	result := map[string]bool{}
	for _, line := range strings.Split(strings.TrimRight(output, "\n"), "\n") {
		if line != "" {
			result[line] = true
		}
	}
	return result
}
func (p *Command) Installed() (map[string]bool, error) {
	command := []string{"rpm", "-qa", "--qf", "%{NAME}\n"}
	switch p.Profile {
	case "macos":
		command = []string{"brew", "list", "--" + p.Kind}
		if p.Kind == "formula" {
			command = append(command, "--full-name")
		}
	case "arch":
		command = []string{"pacman", "-Qq"}
	case "debian":
		command = []string{"dpkg-query", "-W", "-f=${Package}:${Architecture}\t${db:Status-Status}\n"}
	}
	output, err := p.Runner.Run(command, true)
	if err != nil {
		return nil, err
	}
	if p.Profile != "debian" {
		return lineSet(output), nil
	}
	result := map[string]bool{}
	for _, line := range strings.Split(strings.TrimRight(output, "\n"), "\n") {
		if line == "" {
			continue
		}
		name, status, ok := strings.Cut(line, "\t")
		if !ok {
			return nil, fmt.Errorf("dpkg-query 返回无法识别的安装状态")
		}
		if status == "installed" {
			result[name] = true
			base, _, _ := strings.Cut(name, ":")
			result[base] = true
		}
	}
	return result, nil
}
func (p *Command) present(installed map[string]bool) bool {
	key := p.Name
	if p.Profile == "macos" && p.Kind == "cask" {
		key = short(key)
	}
	return installed[key]
}
func (p *Command) brewInfo() (map[string]any, error) {
	output, err := p.Runner.Run([]string{"brew", "info", "--json=v2", "--" + p.Kind, p.Name}, true)
	if err != nil {
		return nil, err
	}
	var data map[string]any
	if err := packagestate.DecodeJSON(strings.NewReader(output), &data); err != nil {
		return nil, fmt.Errorf("Homebrew 包信息无法识别")
	}
	key := "formulae"
	if p.Kind == "cask" {
		key = "casks"
	}
	entries, ok := data[key].([]any)
	if !ok || len(entries) != 1 {
		return nil, fmt.Errorf("Homebrew 包名未解析成唯一项目，请使用准确的 formula/cask 名称")
	}
	item, ok := entries[0].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("Homebrew 包信息无法识别")
	}
	return item, nil
}
func (p *Command) canonical(item map[string]any) (string, error) {
	key := "full_name"
	if p.Kind == "cask" {
		key = "full_token"
	}
	name, ok := item[key].(string)
	if !ok {
		return "", fmt.Errorf("Homebrew 缺少规范包名")
	}
	return name, p.validateName(name)
}
func (p *Command) resolveBrew() error {
	if thirdParty(p.Name) && p.Options.Action == "install" {
		p.Aliases[short(p.Name)] = true
		return nil
	}
	item, err := p.brewInfo()
	if err != nil {
		return err
	}
	name, err := p.canonical(item)
	if err != nil {
		return err
	}
	old := "oldnames"
	if p.Kind == "cask" {
		old = "old_tokens"
	}
	for _, key := range []string{"aliases", old} {
		if values, ok := item[key]; ok {
			array, ok := values.([]any)
			if !ok {
				return fmt.Errorf("Homebrew 别名列表无法识别")
			}
			for _, value := range array {
				alias, ok := value.(string)
				if !ok {
					return fmt.Errorf("Homebrew 别名列表无法识别")
				}
				p.Aliases[alias] = true
			}
		}
	}
	p.Aliases[name], p.Aliases[short(name)] = true, true
	p.Name = name
	return nil
}
func (p *Command) protected() error {
	source, err := p.read(filepath.Join(p.Directory, "protected-packages.txt"))
	if err != nil {
		return err
	}
	protected := names(source)
	blocked := false
	if p.Profile == "macos" {
		tokens := map[string]bool{p.Name: true, short(p.Name): true}
		for name := range p.Aliases {
			tokens[name] = true
		}
		for name := range tokens {
			blocked = blocked || protected[p.Kind+":"+name] || p.Kind == "formula" && strings.Contains(" "+essential["macos"]+" ", " "+name+" ")
		}
	} else {
		name, _, _ := strings.Cut(p.Name, ":")
		blocked = protected[name] || strings.Contains(" "+essential[p.Profile]+" ", " "+name+" ") || boot[p.Profile].MatchString(name)
	}
	if p.Options.Action == "uninstall" && blocked {
		return fmt.Errorf("拒绝卸载受保护包：%s；保护清单/内置启动保护不能由此入口绕过", p.Name)
	}
	return nil
}
func (p *Command) validateTOML(source string) error {
	directory, err := os.MkdirTemp("", "system-sync-toml-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(directory)
	path := filepath.Join(directory, "mise.toml")
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		return err
	}
	_, err = p.Runner.Run([]string{"mise", "config", "get", "--file", path, "bootstrap.packages"}, true)
	return err
}
func (p *Command) Prepare() error {
	source, err := p.read(p.Manifest)
	if err != nil {
		return err
	}
	if p.Profile == "macos" {
		if _, err = brewEntries(source); err != nil {
			return err
		}
		if err = p.resolveBrew(); err != nil {
			return err
		}
	} else {
		if err = p.validateTOML(source); err != nil {
			return err
		}
	}
	if err = p.protected(); err != nil {
		return err
	}
	var candidate string
	if p.Profile == "macos" {
		candidate, err = p.editBrew(source)
	} else {
		candidate, err = p.editLinux(source)
	}
	if err != nil {
		return err
	}
	if p.Profile != "macos" {
		if err = p.validateTOML(candidate); err != nil {
			return err
		}
	}
	p.setAfter(p.Manifest, candidate)
	order := []string{}
	for _, path := range p.afterOrder {
		if p.Before[path] == p.After[path] {
			delete(p.After, path)
		} else {
			order = append(order, path)
		}
	}
	p.afterOrder = order
	return nil
}
func (p *Command) NativeCommand() ([]string, error) {
	action, name := p.Options.Action, p.Name
	if p.Profile == "macos" {
		command := []string{"brew", action, "--" + p.Kind}
		if action == "install" && p.Kind == "formula" {
			source, err := p.read(p.Manifest)
			if err != nil {
				return nil, err
			}
			entries, err := brewEntries(source)
			if err != nil {
				return nil, err
			}
			for _, entry := range entries {
				if entry.Kind == "brew" && (p.Aliases[entry.Name] || entry.Name == name) && regexp.MustCompile(`link:\s*false`).MatchString(entry.Options) {
					command = append(command, "--skip-link")
					break
				}
			}
		}
		return append(command, name), nil
	}
	prefix := []string{}
	if p.UID != 0 {
		prefix = []string{"sudo"}
	}
	switch p.Profile {
	case "arch":
		if action == "install" && p.Manager == "aur" {
			helper, err := p.LookPath("paru")
			if err != nil {
				helper, err = p.LookPath("yay")
			}
			if err != nil {
				return nil, fmt.Errorf("--aur 需要先安装 paru 或 yay")
			}
			if p.UID == 0 {
				return nil, fmt.Errorf("AUR helper 必须由普通用户运行，不能 sudo mise")
			}
			return []string{helper, "-S", "--needed", "--asexplicit", "--", name}, nil
		}
		command := append(prefix, "pacman")
		if action == "install" {
			command = append(command, "-S", "--needed", "--asexplicit")
		} else {
			command = append(command, "-R")
		}
		return append(command, "--", name), nil
	case "debian":
		command := append(prefix, "apt-get", "--no-auto-remove", "-o", "APT::Get::Purge=false")
		if action == "install" {
			command = append(command, "--no-upgrade", "--no-remove", "install")
		} else {
			command = append(command, "remove")
		}
		return append(command, "--", name), nil
	case "fedora":
		native := "remove"
		if action == "install" {
			native = "install"
		}
		return append(prefix, "dnf", "--setopt=clean_requirements_on_remove=False", native, "--", name), nil
	default:
		native, option := "remove", "--no-clean-deps"
		if action == "install" {
			native, option = "install", "--name"
		}
		return append(prefix, "zypper", native, "--type", "package", option, "--", name), nil
	}
}
func (p *Command) checkNativeTarget(installed map[string]bool) error {
	name := p.Name
	var err error
	switch {
	case p.Profile == "debian" && p.Options.Action == "install":
		output, err := p.Runner.Run([]string{"apt-cache", "show", "--", name}, true)
		if err != nil {
			return err
		}
		found := false
		for _, match := range regexp.MustCompile(`(?m)^Package: (\S+)$`).FindAllStringSubmatch(output, -1) {
			base, _, _ := strings.Cut(name, ":")
			found = found || match[1] == base
		}
		if !found {
			return fmt.Errorf("APT 入口只接受准确的实际包名，不接受虚拟包或匹配表达式")
		}
	case p.Profile == "debian" && p.present(installed):
		output, err := p.Runner.Run([]string{"apt-get", "--simulate", "--no-auto-remove", "-o", "APT::Get::Purge=false", "remove", "--", name}, true)
		if err != nil {
			return err
		}
		removals := regexp.MustCompile(`(?m)^(?:Remv|Purg) (\S+)`).FindAllStringSubmatch(output, -1)
		valid := len(removals) > 0
		target, _, _ := strings.Cut(name, ":")
		for _, match := range removals {
			base, _, _ := strings.Cut(match[1], ":")
			valid = valid && base == target
		}
		if !valid {
			return fmt.Errorf("APT 将连带移除其他包或返回未知预演格式；拒绝执行\n%s", output)
		}
	case p.Profile == "arch" && p.Options.Action == "install":
		manager := "pacman"
		if p.Manager == "aur" {
			command, err := p.NativeCommand()
			if err != nil {
				return err
			}
			manager = command[0]
		}
		_, err = p.Runner.Run([]string{manager, "-Si", "--", name}, true)
	case (p.Profile == "fedora" || p.Profile == "opensuse") && p.Options.Action == "uninstall" && p.present(installed):
		_, err = p.Runner.Run([]string{"rpm", "-e", "--test", "--", name}, true)
	case p.Profile == "fedora" && p.Options.Action == "install":
		output, err := p.Runner.Run([]string{"dnf", "repoquery", "--queryformat", "%{name}", "--", name}, true)
		if err != nil {
			return err
		}
		if !lineSet(output)[name] {
			return fmt.Errorf("DNF 入口只接受准确的实际包名，不接受虚拟 capability 或包组")
		}
	case p.Profile == "opensuse" && p.Options.Action == "install":
		output, err := p.Runner.Run([]string{"zypper", "--xmlout", "--no-refresh", "search", "--match-exact", "--type", "package", "--", name}, true)
		if err != nil {
			return err
		}
		found, err := packagestate.ParseZypper(strings.NewReader(output), "solvables")
		if err != nil {
			return fmt.Errorf("无法解析 zypper 包查询 XML")
		}
		ok := false
		for _, v := range found {
			ok = ok || v == name
		}
		if !ok {
			return fmt.Errorf("zypper 本地元数据找不到这个准确包名；请检查名称或先刷新仓库")
		}
	}
	return err
}
func (p *Command) unchanged() error {
	for _, path := range p.readOrder {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if localconfig.IsLink(path) || string(data) != p.Before[path] {
			return fmt.Errorf("操作期间清单被修改，拒绝覆盖：%s", path)
		}
	}
	return nil
}
func (p *Command) saveAudit(phase string, command []string, detail string) error {
	return syncstate.Journal(p.Audit, phase, map[string]any{"action": p.Options.Action, "profile": p.Profile, "package": p.Name, "command": command, "detail": detail})
}
func (p *Command) backup(command []string) error {
	history := filepath.Join(p.Root, ".system-sync/history")
	if err := os.MkdirAll(history, 0755); err != nil {
		return err
	}
	directory, err := os.MkdirTemp(history, time.Now().UTC().Format("20060102T150405Z-package-"))
	if err != nil {
		return err
	}
	p.Audit = directory
	for _, path := range p.readOrder {
		if err := os.WriteFile(filepath.Join(directory, filepath.Base(path)+".before"), []byte(p.Before[path]), 0600); err != nil {
			return err
		}
	}
	for _, path := range p.afterOrder {
		if err := os.WriteFile(filepath.Join(directory, filepath.Base(path)+".after"), []byte(p.After[path]), 0600); err != nil {
			return err
		}
	}
	return p.saveAudit("pending", command, "")
}
func atomicWrite(path, content string) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".package-command-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if _, err = io.WriteString(file, content); err != nil {
		return err
	}
	if err = file.Sync(); err != nil {
		return err
	}
	if err = file.Chmod(info.Mode().Perm()); err != nil {
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
func (p *Command) commitFiles(ctx context.Context) (err error) {
	if err = p.unchanged(); err != nil {
		return err
	}
	saved := []string{}
	defer func() {
		if err != nil {
			for _, path := range saved {
				data, readErr := os.ReadFile(path)
				if readErr == nil && string(data) == p.After[path] {
					if rollbackErr := p.AtomicWrite(path, p.Before[path]); rollbackErr != nil {
						err = fmt.Errorf("%w；清单回滚失败：%s", err, path)
					}
				}
			}
		}
	}()
	for _, path := range p.afterOrder {
		if err = ctx.Err(); err != nil {
			return err
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if string(data) != p.Before[path] {
			return fmt.Errorf("清单已改变：%s", path)
		}
		if err = p.AtomicWrite(path, p.After[path]); err != nil {
			return err
		}
		saved = append(saved, path)
	}
	return ctx.Err()
}
func (p *Command) Execute(ctx context.Context, confirm func() (bool, error), out io.Writer) (err error) {
	if err = p.Prepare(); err != nil {
		return err
	}
	command, err := p.NativeCommand()
	if err != nil {
		return err
	}
	installed, err := p.Installed()
	if err != nil {
		return err
	}
	if err = p.checkNativeTarget(installed); err != nil {
		return err
	}
	skip := p.Options.Action == "uninstall" && !p.present(installed)
	fmt.Fprintf(out, "==> %s / %s / %s\n", p.Profile, p.Manager, p.Name)
	if skip {
		fmt.Fprintln(out, "包已不在已安装列表中；确认后仅移除声明。")
	} else {
		fmt.Fprintln(out, "$ "+taskexec.Quote(command))
	}
	if p.Profile == "macos" && p.Options.Action == "install" && strings.Count(p.Name, "/") == 2 {
		fmt.Fprintln(out, "将按完整包名安装；Homebrew 可能添加对应 tap，并信任此具体 formula/cask。")
	}
	for _, path := range p.afterOrder {
		relative, _ := filepath.Rel(p.Root, path)
		fmt.Fprint(out, UnifiedDiff(p.Before[path], p.After[path], relative))
	}
	if len(p.After) == 0 {
		fmt.Fprintln(out, "声明已符合目标，无需修改文件。")
	}
	if p.Options.DryRun {
		fmt.Fprintln(out, "预览结束：未执行安装/卸载，也未修改清单。依赖下载与安装细节以包管理器实际事务为准。")
		return nil
	}
	yes, err := confirm()
	if err != nil {
		return err
	}
	if !yes {
		fmt.Fprintln(out, "已取消；未执行安装/卸载，未修改清单。")
		return nil
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = p.unchanged(); err != nil {
		return err
	}
	again, err := p.Installed()
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(again, installed) {
		return fmt.Errorf("确认期间安装状态已改变，请重新运行以计算计划")
	}
	if err = p.backup(command); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = p.saveAudit("needs-review", command, err.Error())
			err = fmt.Errorf("%w\n系统包操作不能自动回滚。请检查实际安装状态及审计备份：%s；不要直接运行整机收敛。", err, p.Audit)
		}
	}()
	if !skip {
		if _, err = p.Runner.Run(command, false); err != nil {
			return err
		}
	}
	remaining, err := p.Installed()
	if err != nil {
		return err
	}
	if p.present(remaining) != (p.Options.Action == "install") {
		return fmt.Errorf("包管理器返回成功，但安装状态未达到目标；配置保持不变")
	}
	removed := []string{}
	base, _, _ := strings.Cut(p.Name, ":")
	for name := range installed {
		if remaining[name] {
			continue
		}
		allowed := p.Options.Action == "uninstall" && (name == p.Name || name == short(p.Name))
		if p.Profile == "debian" && p.Options.Action == "uninstall" {
			current, _, _ := strings.Cut(name, ":")
			allowed = current == base
		}
		if !allowed {
			removed = append(removed, name)
		}
	}
	if len(removed) > 0 {
		sort.Strings(removed)
		return fmt.Errorf("检测到未计划的其他包移除，请先人工核对：%s", strings.Join(removed, ", "))
	}
	if p.Profile == "macos" && p.Options.Action == "install" {
		item, err := p.brewInfo()
		if err != nil {
			return err
		}
		canonical, err := p.canonical(item)
		if err != nil {
			return err
		}
		if canonical != p.Name {
			return fmt.Errorf("安装后的规范包名与预演不同；请用实际完整名称重试，配置保持不变")
		}
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = p.saveAudit("native-succeeded", command, ""); err != nil {
		return err
	}
	if err = p.commitFiles(ctx); err != nil {
		return err
	}
	if err = p.saveAudit("complete", command, ""); err != nil {
		return err
	}
	fmt.Fprintf(out, "完成；只同步了指定根包，未运行整机 cleanup/autoremove。审计：%s\n", p.Audit)
	return nil
}
