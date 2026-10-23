package extensions

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2/unstable"
	"github.com/sweard/system_sync/helper/internal/localconfig"
	"github.com/sweard/system_sync/helper/internal/packageedit"
	"github.com/sweard/system_sync/helper/internal/syncstate"
)

type CaptureOptions struct {
	Options
	Group         string
	Only, Exclude []string
}

type extension struct{ name, version string }

type versionChange struct {
	extension
	before string
	groups []string
}

type CapturePlan struct {
	Manifest, Group, Before, After string
	added, excluded                []extension
	updated                        []versionChange
	command                        []string
	info                           os.FileInfo
}

func selection(values []string) (map[string]bool, error) {
	result := map[string]bool{}
	for _, value := range values {
		for _, name := range strings.Split(value, ",") {
			name = strings.ToLower(strings.TrimSpace(name))
			if !identifier.MatchString(name) {
				return nil, fmt.Errorf("筛选扩展须使用准确的 publisher.name，不接受版本或通配符")
			}
			result[name] = true
		}
	}
	return result, nil
}

func snapshot(path string) (string, os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", nil, err
	}
	if !info.Mode().IsRegular() {
		return "", nil, fmt.Errorf("扩展清单必须是普通文件，不能是符号链接")
	}
	data, err := os.ReadFile(path)
	return string(data), info, err
}

func NewCapture(options CaptureOptions, runner Invoker) (*CapturePlan, error) {
	platform := localconfig.Platform()
	if platform == "" {
		return nil, fmt.Errorf("仅支持 macOS / Linux")
	}
	group := options.Group
	if group == "" {
		group = "common"
	}
	if group != "common" && group != platform {
		return nil, fmt.Errorf("--group 只能是 common 或当前平台 %s", platform)
	}
	only, err := selection(options.Only)
	if err != nil {
		return nil, err
	}
	exclude, err := selection(options.Exclude)
	if err != nil {
		return nil, err
	}
	path, err := filepath.Abs(options.Manifest)
	if err != nil {
		return nil, err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	path = filepath.Join(parent, filepath.Base(path))
	before, info, err := snapshot(path)
	if err != nil {
		return nil, err
	}
	data, err := localconfig.Decode([]byte(before))
	if err != nil {
		return nil, err
	}
	wanted, err := declarations(data, platform, true)
	if err != nil {
		return nil, err
	}
	cli, err := command(options.Options)
	if err != nil {
		return nil, err
	}
	installed, err := Installed(runner, cli)
	if err != nil {
		return nil, err
	}
	for name := range only {
		if _, ok := installed[name]; !ok {
			return nil, fmt.Errorf("--only 指定的扩展未安装：%s", name)
		}
	}
	plan := &CapturePlan{Manifest: path, Group: group, Before: before, After: before, command: cli, info: info}
	names := []string{}
	for name := range installed {
		if installed[name] != wanted[name] {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		entry := extension{name, installed[name]}
		if exclude[name] || len(only) > 0 && !only[name] {
			plan.excluded = append(plan.excluded, entry)
		} else if old, managed := wanted[name]; managed {
			change := versionChange{extension: entry, before: old}
			for _, group := range []string{"common", platform} {
				if _, exists := localconfig.Table(data[group])[name]; exists {
					change.groups = append(change.groups, group)
				}
			}
			plan.updated = append(plan.updated, change)
		} else {
			plan.added = append(plan.added, entry)
		}
	}
	if plan.HasChanges() {
		plan.After, err = plan.candidate()
		if err != nil {
			return nil, err
		}
	}
	return plan, nil
}

// Replace only selected version tokens, keeping comments and table scope.
// Layouts without separate tables fail closed instead of rewriting the file.
func (p *CapturePlan) updateVersions() (string, error) {
	if len(p.updated) == 0 {
		return p.Before, nil
	}
	wanted := map[string]map[string]versionChange{}
	for _, change := range p.updated {
		for _, group := range change.groups {
			if wanted[group] == nil {
				wanted[group] = map[string]versionChange{}
			}
			wanted[group][change.name] = change
		}
	}
	var parser unstable.Parser
	parser.Reset([]byte(p.Before))
	var result strings.Builder
	group, offset := "", 0
	for parser.NextExpression() {
		node := parser.Expression()
		switch node.Kind {
		case unstable.Table:
			group = ""
			key := node.Key()
			if key.Next() {
				name := string(key.Node().Data)
				if !key.Next() {
					group = name
				}
			}
		case unstable.KeyValue:
			key := node.Key()
			if !key.Next() {
				continue
			}
			name := string(key.Node().Data)
			change, selected := wanted[group][name]
			if !selected || key.Next() {
				continue
			}
			value := node.Value()
			if value.Kind != unstable.String || string(value.Data) != change.before {
				return "", fmt.Errorf("无法安全更新扩展 %s 的版本，请先人工核对清单", name)
			}
			start, end := int(value.Raw.Offset), int(value.Raw.Offset+value.Raw.Length)
			quote := p.Before[start : start+1]
			if strings.HasPrefix(p.Before[start:end], strings.Repeat(quote, 3)) {
				quote = strings.Repeat(quote, 3)
			}
			result.WriteString(p.Before[offset:start])
			result.WriteString(quote + change.version + quote)
			offset = end
			delete(wanted[group], name)
		}
	}
	if parser.Error() != nil {
		return "", fmt.Errorf("无法安全定位扩展版本，请先人工核对清单")
	}
	for _, entries := range wanted {
		if len(entries) > 0 {
			return "", fmt.Errorf("无法在保留原布局的前提下更新扩展；请使用独立的 [common]、[macos]、[linux] 分组")
		}
	}
	result.WriteString(p.Before[offset:])
	return result.String(), nil
}

// Add new declarations to the selected table and validate both platforms.
func (p *CapturePlan) candidate() (string, error) {
	after, err := p.updateVersions()
	if err != nil {
		return "", err
	}
	if len(p.added) > 0 {
		header := regexp.MustCompile(`^\[\s*` + p.Group + `\s*\]\s*(?:#.*)?$`)
		parts := strings.SplitAfter(after, "\n")
		found, insertion := false, len(parts)
		for i, part := range parts {
			line := strings.TrimSpace(part)
			if header.MatchString(line) {
				found = true
				insertion = i + 1
			} else if found && strings.HasPrefix(line, "[") {
				break
			} else if found && line != "" && !strings.HasPrefix(line, "#") {
				insertion = i + 1
			}
		}
		prefix := strings.Join(parts[:insertion], "")
		if prefix != "" && !strings.HasSuffix(prefix, "\n") {
			prefix += "\n"
		}
		if !found {
			if prefix != "" && !strings.HasSuffix(prefix, "\n\n") {
				prefix += "\n"
			}
			prefix += "[" + p.Group + "]\n"
		}
		for _, entry := range p.added {
			prefix += fmt.Sprintf("%q = %q\n", entry.name, entry.version)
		}
		after = prefix + strings.Join(parts[insertion:], "")
	}
	data, err := localconfig.Decode([]byte(after))
	if err != nil {
		return "", fmt.Errorf("无法在保留原布局的前提下采集扩展；请使用独立的 [common]、[macos]、[linux] 分组")
	}
	for _, platform := range []string{"macos", "linux"} {
		if _, err := declarations(data, platform, true); err != nil {
			return "", fmt.Errorf("采集后 %s 清单校验失败：%w；请先人工处理版本冲突", platform, err)
		}
	}
	return after, nil
}

func (p *CapturePlan) HasChanges() bool { return len(p.added)+len(p.updated) > 0 }

func (p *CapturePlan) Render(out io.Writer) {
	if len(p.excluded) > 0 {
		fmt.Fprintf(out, "[~] 跳过 %d 个采集候选（排除或未选中，保留清单与本机安装）：\n", len(p.excluded))
		for _, entry := range p.excluded {
			fmt.Fprintf(out, "  - %s@%s\n", entry.name, entry.version)
		}
	}
	if !p.HasChanges() {
		fmt.Fprintln(out, "[+] 没有需要新增或更新的扩展，无需修改清单。")
		return
	}
	if len(p.added) > 0 {
		fmt.Fprintf(out, "[!] 待追加 %d 个扩展到 [%s]：%s\n", len(p.added), p.Group, p.Manifest)
		for _, entry := range p.added {
			fmt.Fprintf(out, "  - %s@%s\n", entry.name, entry.version)
		}
	}
	if len(p.updated) > 0 {
		fmt.Fprintf(out, "[!] 待更新 %d 个已纳管扩展版本（清单 -> 本机；保留原分组）：%s\n", len(p.updated), p.Manifest)
		for _, change := range p.updated {
			fmt.Fprintf(out, "  - %s: %s -> %s [%s]\n", change.name, change.before, change.version, strings.Join(change.groups, ", "))
		}
	}
	fmt.Fprint(out, packageedit.UnifiedDiff(p.Before, p.After, p.Manifest))
}

func (p *CapturePlan) unchanged() error {
	current, info, err := snapshot(p.Manifest)
	if err != nil || !os.SameFile(p.info, info) || current != p.Before || info.Mode() != p.info.Mode() {
		return fmt.Errorf("预览后扩展清单发生变化，请重新运行；未改写清单")
	}
	return nil
}

func (p *CapturePlan) Apply(ctx context.Context, root string, runner Invoker) (string, error) {
	if !p.HasChanges() {
		return "", nil
	}
	lock, err := syncstate.OperationLock(root)
	if err != nil {
		return "", err
	}
	defer lock.Close()
	current, err := Installed(runner, p.command)
	if err != nil {
		return "", err
	}
	checked := append([]extension{}, p.added...)
	for _, change := range p.updated {
		checked = append(checked, change.extension)
	}
	for _, entry := range checked {
		if current[entry.name] != entry.version {
			return "", fmt.Errorf("预览后扩展 %s 的安装或版本发生变化，请重新运行；未改写清单", entry.name)
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := p.unchanged(); err != nil {
		return "", err
	}
	parent, err := syncstate.LocalDirectory(root, "capture")
	if err != nil {
		return "", err
	}
	backup, err := os.MkdirTemp(parent, time.Now().UTC().Format("20060102T150405Z-")+"vscode-"+localconfig.Platform()+"-")
	if err != nil {
		return "", err
	}
	for name, content := range map[string]string{"extensions.toml.before": p.Before, "extensions.toml.candidate": p.After} {
		if err := os.WriteFile(filepath.Join(backup, name), []byte(content), 0600); err != nil {
			return "", err
		}
	}
	file, err := os.CreateTemp(filepath.Dir(p.Manifest), ".extensions-capture-")
	if err != nil {
		return "", err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if err = file.Chmod(p.info.Mode().Perm()); err == nil {
		_, err = file.WriteString(p.After)
	}
	if err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := p.unchanged(); err != nil {
		return "", err
	}
	if err := os.Rename(file.Name(), p.Manifest); err != nil {
		return "", err
	}
	return backup, nil
}
