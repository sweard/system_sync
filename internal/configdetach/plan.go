// Package configdetach materializes repository links using a local transaction.
package configdetach

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/sweard/system_sync/internal/localconfig"
	"github.com/sweard/system_sync/internal/syncstate"
)

type Change struct {
	Target, Reason, Source      string
	Before, After, SourceBefore *Node
}
type Plan struct {
	Root, Home string
	Changes    []*Change
	Retained   map[string]bool
	rename     func(string, string) error
	journal    func(string, string, map[string]any) error
}

func New(root, home string) (*Plan, error) {
	root, err := localconfig.RealPath(root)
	if err != nil {
		return nil, err
	}
	home, err = localconfig.RealPath(home)
	if err != nil {
		return nil, err
	}
	return &Plan{Root: root, Home: home, Retained: map[string]bool{}, rename: os.Rename, journal: syncstate.Journal}, nil
}
func (p *Plan) target(value string) (string, error) {
	if value == "~" {
		value = p.Home
	} else if strings.HasPrefix(value, "~/") {
		value = filepath.Join(p.Home, value[2:])
	}
	path, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	if path == p.Home || !localconfig.Within(path, p.Home) {
		return "", fmt.Errorf("只处理 HOME 内的配置目标，未修改配置：%s", path)
	}
	return path, nil
}
func (p *Plan) change(target string) *Change {
	for _, change := range p.Changes {
		if change.Target == target {
			return change
		}
	}
	return nil
}
func (p *Plan) delete(target string) {
	for i, change := range p.Changes {
		if change.Target == target {
			p.Changes = append(p.Changes[:i], p.Changes[i+1:]...)
			return
		}
	}
}
func (p *Plan) add(target string, after *Node, reason, source string) error {
	target, err := p.target(target)
	if err != nil {
		return err
	}
	before, err := snapshot(target, "", false, nil)
	if err != nil {
		return err
	}
	if reflect.DeepEqual(before, after) {
		return nil
	}
	var sourceBefore *Node
	if source != "" {
		sourceBefore, err = snapshot(source, p.Root, true, nil)
		if err != nil {
			return err
		}
	}
	if previous := p.change(target); previous != nil {
		if !reflect.DeepEqual(previous.After, after) {
			return fmt.Errorf("配置目标有相互冲突的迁出操作：%s", target)
		}
		*previous = Change{target, reason, source, before, after, sourceBefore}
		return nil
	}
	p.Changes = append(p.Changes, &Change{target, reason, source, before, after, sourceBefore})
	return nil
}
func (p *Plan) links(path string) error {
	path, err := p.target(path)
	if err != nil {
		return err
	}
	if localconfig.IsLink(path) {
		resolved, err := localconfig.RealPath(path)
		if err != nil {
			return err
		}
		if localconfig.Within(resolved, p.Root) {
			after, err := snapshot(path, p.Root, true, nil)
			if err != nil {
				return err
			}
			return p.add(path, after, "链接转为本机文件", path)
		}
	} else if info, err := os.Stat(path); err == nil && info.IsDir() {
		entries, err := os.ReadDir(path)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err := p.links(filepath.Join(path, entry.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}
func (p *Plan) owns(value any) (map[string]any, bool, error) {
	entry := localconfig.Table(value)
	if entry == nil {
		return nil, false, fmt.Errorf("mise 状态条目结构无法识别，未修改配置")
	}
	origin := localconfig.Table(entry["origin"])
	config, ok := origin["config"].(string)
	if !ok {
		return nil, false, fmt.Errorf("mise 状态缺少配置来源，未修改配置")
	}
	resolved, err := localconfig.RealPath(config)
	return entry, err == nil && localconfig.Within(resolved, p.Root), err
}
func entryString(entry map[string]any, key string) (string, error) {
	value, ok := entry[key].(string)
	if !ok || value == "" {
		return "", fmt.Errorf("mise 配置状态缺少有效的 %s，未修改配置", key)
	}
	return value, nil
}
func (p *Plan) Collect(data map[string]any, enrollment string) error {
	fileValues, filesOK := data["files"].([]any)
	editValues, editsOK := data["edits"].([]any)
	if !filesOK || !editsOK {
		return fmt.Errorf("mise 配置状态结构无法识别，未修改配置")
	}
	files := []map[string]any{}
	sources := map[string]string{}
	for _, value := range fileValues {
		entry, owned, err := p.owns(value)
		if err != nil {
			return err
		}
		if !owned {
			continue
		}
		files = append(files, entry)
		mode, _ := entry["mode"].(string)
		if mode == "track" || mode == "track-local" || mode == "absent" {
			continue
		}
		switch mode {
		case "symlink", "symlink-each", "copy", "template", "content", "permissions":
		default:
			return fmt.Errorf("尚不支持的配置模式：%s", mode)
		}
		target, err := entryString(entry, "target")
		if err != nil {
			return err
		}
		target, err = p.target(target)
		if err != nil {
			return err
		}
		if err = p.links(target); err != nil {
			return err
		}
		p.Retained[target] = true
		origin := localconfig.Table(entry["origin"])
		if source, present := origin["source"]; present && source != "" && source != nil {
			path, ok := source.(string)
			if !ok {
				return fmt.Errorf("mise 配置来源结构无法识别")
			}
			path, err = localconfig.RealPath(path)
			if err != nil {
				return err
			}
			sources[path] = target
		}
	}
	for _, value := range editValues {
		entry, owned, err := p.owns(value)
		if err != nil {
			return err
		}
		if !owned {
			continue
		}
		target, err := entryString(entry, "path")
		if err != nil {
			return err
		}
		target, err = p.target(target)
		if err != nil {
			return err
		}
		if err = p.links(target); err != nil {
			return err
		}
		p.Retained[target] = true
	}
	if values, present := data["orphaned"]; present {
		orphans, ok := values.([]any)
		if !ok {
			return fmt.Errorf("mise 配置状态结构无法识别，未修改配置")
		}
		for _, value := range orphans {
			entry, owned, err := p.owns(value)
			if err != nil {
				return err
			}
			if !owned {
				continue
			}
			key := "target"
			if _, ok := entry[key]; !ok {
				key = "path"
			}
			target, err := entryString(entry, key)
			if err != nil {
				return err
			}
			if err = p.links(target); err != nil {
				return err
			}
		}
	}
	config := sources[filepath.Join(p.Root, "dotfiles/common/mise/config.toml")]
	pathScript := sources[filepath.Join(p.Root, "dotfiles/common/shell/path.sh")]
	if config != "" && localconfig.IsFile(config) {
		resolved, err := localconfig.RealPath(config)
		if err != nil {
			return err
		}
		if !localconfig.IsLink(config) || localconfig.Within(resolved, p.Root) {
			if err = p.localizeEnvironment(config, pathScript); err != nil {
				return err
			}
		}
	}
	for _, entry := range files {
		target, err := entryString(entry, "target")
		if err != nil {
			return err
		}
		target, err = p.target(target)
		if err != nil {
			return err
		}
		if strings.HasPrefix(filepath.Base(target), "mise") && filepath.Ext(target) == ".lock" && localconfig.IsFile(target) {
			resolved, err := localconfig.RealPath(target)
			if err != nil {
				return err
			}
			if localconfig.IsLink(target) && !localconfig.Within(resolved, p.Root) {
				continue
			}
			if err = p.copySidecars(target); err != nil {
				return err
			}
		}
	}
	if err := p.stopHistory(enrollment); err != nil {
		return err
	}
	changes := append([]*Change{}, p.Changes...)
	sort.SliceStable(changes, func(i, j int) bool {
		return len(strings.Split(changes[i].Target, string(filepath.Separator))) < len(strings.Split(changes[j].Target, string(filepath.Separator)))
	})
	for _, change := range changes {
		if p.change(change.Target) == nil {
			continue
		}
		for parent := filepath.Dir(change.Target); parent != filepath.Dir(parent); parent = filepath.Dir(parent) {
			if ancestor := p.change(parent); ancestor != nil && ancestor.After != nil && ancestor.After.Kind == "directory" {
				if change.Reason != "链接转为本机文件" {
					return fmt.Errorf("需要先单独迁出父目录链接：%s", parent)
				}
				p.delete(change.Target)
				break
			}
		}
	}
	for _, change := range p.Changes {
		if err := p.checkParents(change.Target); err != nil {
			return err
		}
	}
	return nil
}
func (p *Plan) localizeEnvironment(config, pathScript string) error {
	current, err := snapshot(config, p.Root, true, nil)
	if err != nil {
		return err
	}
	if current == nil || current.Kind != "file" {
		return fmt.Errorf("共享配置不是普通文件")
	}
	data, err := localconfig.Decode(current.Data)
	if err != nil {
		return err
	}
	source := localconfig.Table(localconfig.Table(data["env"])["_"])["source"]
	old := "{{ config_source | canonicalize | dirname }}/../shell/path.sh"
	if localconfig.Table(source)["path"] != old {
		representation := fmt.Sprint(source)
		if strings.Contains(representation, "config_source") || strings.Contains(representation, p.Root) {
			return fmt.Errorf("环境脚本引用已自定义，请先改为独立的本机路径；未修改配置")
		}
		return nil
	}
	if !localconfig.IsFile(pathScript) {
		return fmt.Errorf("找不到已部署的共享 PATH 脚本，未修改配置")
	}
	text, literal := string(current.Data), localconfig.Quoted(old)
	if strings.Count(text, literal) != 1 {
		return fmt.Errorf("共享 PATH 引用格式已变化，需要人工核对；未修改配置")
	}
	updated := *current
	updated.Data = []byte(strings.Replace(text, literal, localconfig.Quoted(pathScript), 1))
	p.delete(config)
	return p.add(config, &updated, "将 PATH 脚本引用改为本机位置", config)
}
func (p *Plan) copySidecars(lock string) error {
	data, err := localconfig.Read(lock)
	if err != nil {
		return err
	}
	tools := localconfig.Table(data["tools"])
	if data["tools"] != nil && tools == nil {
		return fmt.Errorf("下载锁结构无法识别")
	}
	keys := []string{}
	for key := range tools {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		entries, ok := tools[key].([]any)
		if !ok {
			return fmt.Errorf("下载锁结构无法识别")
		}
		for _, value := range entries {
			entry := localconfig.Table(value)
			if entry == nil {
				return fmt.Errorf("下载锁结构无法识别")
			}
			for _, graph := range []string{"aube", "uv"} {
				value, present := entry[graph]
				if !present {
					continue
				}
				relative, ok := localconfig.Table(value)["path"].(string)
				if !ok || relative == "" || filepath.IsAbs(relative) || filepath.Clean(relative) == "." {
					return fmt.Errorf("下载锁的依赖目录不是安全相对路径：%s", lock)
				}
				for _, part := range strings.Split(relative, string(filepath.Separator)) {
					if part == ".." {
						return fmt.Errorf("下载锁的依赖目录不是安全相对路径：%s", lock)
					}
				}
				resolved, err := filepath.EvalSymlinks(lock)
				if err != nil {
					return err
				}
				source := filepath.Join(filepath.Dir(resolved), relative)
				info, err := os.Stat(source)
				if err != nil || !info.IsDir() {
					return fmt.Errorf("下载锁的依赖文件缺失：%s", source)
				}
				target, err := p.target(filepath.Join(filepath.Dir(lock), relative))
				if err != nil {
					return err
				}
				after, err := snapshot(source, p.Root, true, nil)
				if err != nil {
					return err
				}
				before, err := snapshot(target, "", false, nil)
				if err != nil {
					return err
				}
				if reflect.DeepEqual(before, after) {
					continue
				}
				if before != nil {
					resolvedTarget, err := localconfig.RealPath(target)
					if err != nil {
						return err
					}
					resolvedSource, err := localconfig.RealPath(source)
					if err != nil {
						return err
					}
					if before.Kind != "link" || resolvedTarget != resolvedSource {
						return fmt.Errorf("本机依赖目录已有不同内容，未修改配置：%s", target)
					}
				}
				if err = p.add(target, after, "复制锁文件依赖目录", source); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
func (p *Plan) stopHistory(enrollment string) error {
	_, err := os.Lstat(enrollment)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if localconfig.IsLink(enrollment) {
		return fmt.Errorf("本机历史登记文件不能是链接，未修改配置")
	}
	data, err := localconfig.Read(enrollment)
	if err != nil {
		return err
	}
	candidates := []string{filepath.Join(p.Home, ".codex/config.toml"), filepath.Join(p.Home, ".zshrc.local"), filepath.Join(p.Home, "Library/Application Support/Code/User/settings.json"), filepath.Join(p.Home, ".config/Code/User/settings.json")}
	if _, valid := localconfig.LocalEntries(data, candidates); !valid {
		return fmt.Errorf("本机历史登记有自定义内容，需先人工核对；未修改配置")
	}
	return p.add(enrollment, nil, "取消本项目的本机历史登记，保留历史数据", "")
}
func (p *Plan) checkParents(target string) error {
	for parent := filepath.Dir(target); parent != p.Home && parent != filepath.Dir(parent); parent = filepath.Dir(parent) {
		if localconfig.IsLink(parent) {
			return fmt.Errorf("目标的父目录是链接，未修改配置：%s", parent)
		}
	}
	return nil
}
func (p *Plan) verify(changes []*Change) error {
	for _, change := range changes {
		if err := p.checkParents(change.Target); err != nil {
			return err
		}
		before, err := snapshot(change.Target, "", false, nil)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(before, change.Before) {
			return fmt.Errorf("配置在预览后发生变化，已停止：%s", change.Target)
		}
		if change.Source != "" {
			source, err := snapshot(change.Source, p.Root, true, nil)
			if err != nil {
				return err
			}
			if !reflect.DeepEqual(source, change.SourceBefore) {
				return fmt.Errorf("配置来源在预览后发生变化，已停止：%s", change.Source)
			}
		}
	}
	return nil
}

func (p *Plan) Apply(ctx context.Context, backupRoot string, stderr io.Writer) (directory string, err error) {
	backupRoot, err = p.target(backupRoot)
	if err != nil {
		return "", err
	}
	if localconfig.Within(backupRoot, p.Root) {
		return "", fmt.Errorf("备份必须位于仓库外，且不能放在待迁出的目标内")
	}
	for _, change := range p.Changes {
		if localconfig.Within(backupRoot, change.Target) {
			return "", fmt.Errorf("备份必须位于仓库外，且不能放在待迁出的目标内")
		}
	}
	if err = p.checkParents(filepath.Join(backupRoot, "placeholder")); err != nil {
		return "", err
	}
	changes := append([]*Change{}, p.Changes...)
	sort.Slice(changes, func(i, j int) bool {
		if (changes[i].After == nil) != (changes[j].After == nil) {
			return changes[i].After != nil
		}
		return changes[i].Target < changes[j].Target
	})
	if err = p.verify(changes); err != nil {
		return "", err
	}
	if err = ctx.Err(); err != nil {
		return "", err
	}
	if err = os.MkdirAll(backupRoot, 0700); err != nil {
		return "", err
	}
	directory, err = os.MkdirTemp(backupRoot, time.Now().UTC().Format("20060102T150405Z-"))
	if err != nil {
		return "", err
	}
	for _, label := range []string{"before", "after"} {
		if err = os.Mkdir(filepath.Join(directory, label), 0700); err != nil {
			return directory, err
		}
	}
	records := []map[string]any{}
	staged, created := []string{}, []string{}
	attempted := []*Change{}
	unlinked := map[string]bool{}
	defer func() {
		if err != nil {
			conflicts := []string{}
			for i := len(attempted) - 1; i >= 0; i-- {
				change := attempted[i]
				current, readErr := snapshot(change.Target, "", false, nil)
				if readErr != nil {
					conflicts = append(conflicts, change.Target)
					continue
				}
				if reflect.DeepEqual(current, change.Before) {
					continue
				}
				if !reflect.DeepEqual(current, change.After) && !(current == nil && unlinked[change.Target]) {
					conflicts = append(conflicts, change.Target)
					continue
				}
				staging, restoreErr := os.MkdirTemp(filepath.Dir(change.Target), ".system-sync-rollback-")
				if restoreErr == nil {
					staged = append(staged, staging)
					if change.Before != nil {
						restoreErr = writeNode(change.Before, filepath.Join(staging, "value"), false)
					}
					if restoreErr == nil {
						restoreErr = removeNode(change.Target)
					}
					if restoreErr == nil && change.Before != nil {
						restoreErr = p.rename(filepath.Join(staging, "value"), change.Target)
					}
				}
				if restoreErr != nil {
					conflicts = append(conflicts, change.Target)
				}
			}
			phase, message := "rolled_back", "已恢复本次替换的目标"
			if len(conflicts) > 0 {
				phase, message = "recovery_required", "部分目标需要人工恢复"
			}
			if journalErr := p.journal(directory, phase, map[string]any{"conflicts": conflicts}); journalErr != nil {
				err = fmt.Errorf("%w；恢复记录写入失败", err)
			}
			fmt.Fprintf(stderr, "[x] 迁出失败；%s。备份：%s\n", message, directory)
		}
		for _, path := range staged {
			_ = os.RemoveAll(path)
		}
		for i := len(created) - 1; i >= 0; i-- {
			_ = os.Remove(created[i])
		}
	}()
	for index, change := range changes {
		if err = ctx.Err(); err != nil {
			return directory, err
		}
		var beforePath, afterPath any
		if change.Before != nil {
			beforePath = fmt.Sprintf("before/%d", index)
			if err = writeNode(change.Before, filepath.Join(directory, beforePath.(string)), true); err != nil {
				return directory, err
			}
		}
		if change.After != nil {
			afterPath = fmt.Sprintf("after/%d", index)
			if err = writeNode(change.After, filepath.Join(directory, afterPath.(string)), true); err != nil {
				return directory, err
			}
		}
		records = append(records, map[string]any{"target": change.Target, "reason": change.Reason, "before": beforePath, "after": afterPath, "before_metadata": metadata(change.Before), "after_metadata": metadata(change.After)})
	}
	if err = p.journal(directory, "prepared", map[string]any{"repository": p.Root, "changes": records}); err != nil {
		return directory, err
	}
	for _, change := range changes {
		if err = ctx.Err(); err != nil {
			return directory, err
		}
		if err = p.checkParents(change.Target); err != nil {
			return directory, err
		}
		missing := []string{}
		for parent := filepath.Dir(change.Target); ; parent = filepath.Dir(parent) {
			if _, statErr := os.Stat(parent); statErr == nil {
				break
			} else if !os.IsNotExist(statErr) {
				return directory, statErr
			}
			missing = append(missing, parent)
		}
		for i := len(missing) - 1; i >= 0; i-- {
			if err = os.Mkdir(missing[i], 0700); err != nil {
				return directory, err
			}
			created = append(created, missing[i])
		}
		var staging string
		staging, err = os.MkdirTemp(filepath.Dir(change.Target), ".system-sync-detach-")
		if err != nil {
			return directory, err
		}
		staged = append(staged, staging)
		if change.After != nil {
			if err = writeNode(change.After, filepath.Join(staging, "value"), false); err != nil {
				return directory, err
			}
		}
	}
	if err = p.verify(changes); err != nil {
		return directory, err
	}
	if err = p.journal(directory, "applying", map[string]any{"completed": []string{}}); err != nil {
		return directory, err
	}
	for index, change := range changes {
		if err = ctx.Err(); err != nil {
			return directory, err
		}
		if err = p.checkParents(change.Target); err != nil {
			return directory, err
		}
		var current *Node
		current, err = snapshot(change.Target, "", false, nil)
		if err != nil {
			return directory, err
		}
		if !reflect.DeepEqual(current, change.Before) {
			return directory, fmt.Errorf("配置在替换前发生变化，已停止：%s", change.Target)
		}
		attempted = append(attempted, change)
		if change.After == nil {
			err = os.Remove(change.Target)
		} else {
			if change.Before != nil && change.Before.Kind == "link" && change.After.Kind == "directory" {
				if err = os.Remove(change.Target); err != nil {
					return directory, err
				}
				unlinked[change.Target] = true
			}
			err = p.rename(filepath.Join(staged[index], "value"), change.Target)
			if err == nil {
				delete(unlinked, change.Target)
			}
		}
		if err != nil {
			return directory, err
		}
		completed := []string{}
		for _, value := range attempted {
			completed = append(completed, value.Target)
		}
		if err = p.journal(directory, "applying", map[string]any{"completed": completed}); err != nil {
			return directory, err
		}
	}
	if err = ctx.Err(); err != nil {
		return directory, err
	}
	if err = p.journal(directory, "complete", nil); err != nil {
		return directory, err
	}
	return directory, nil
}
