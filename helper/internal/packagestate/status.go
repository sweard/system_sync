// Package packagestate validates upstream output before it reaches package writers.
package packagestate

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
)

var ManagerName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
var packageName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9@._+:\-]*$`)

type Entry struct {
	Manager, Package, State string
}

// DecodeJSON accepts exactly one document. A truncated or appended response must
// never become a partial declaration set for a destructive operation.
func DecodeJSON(input io.Reader, target any) error {
	d := json.NewDecoder(input)
	if err := d.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return fmt.Errorf("JSON 含有附加或无法识别的内容")
	}
	return nil
}

func ParseStatus(data any, managers []string) ([]Entry, error) {
	wanted := make(map[string]bool)
	for _, manager := range managers {
		if !ManagerName.MatchString(manager) {
			return nil, fmt.Errorf("manager 名称不合法：%q", manager)
		}
		wanted[manager] = true
	}
	entries := make(map[Entry]bool)
	add := func(manager string, item any) error {
		var name any
		state := "unknown"
		switch v := item.(type) {
		case string:
			name = v
		case map[string]any:
			name = v["package"]
			if _, ok := v["package"]; !ok {
				name = v["name"]
			}
			state, _ = v["state"].(string)
			if state != "installed" && state != "missing" && state != "version_mismatch" && state != "needs_repair" {
				return fmt.Errorf("%q 的包状态不可用于根包收敛；不接受 skipped、absent 或未知状态", manager)
			}
			if desired, ok := v["desired_state"]; ok && desired != "present" {
				return fmt.Errorf("根包清单只接受 present 声明；absent 必须通过单包卸载入口处理")
			}
		default:
			return fmt.Errorf("%q 的 packages 项必须是对象或字符串", manager)
		}
		n, ok := name.(string)
		if !ok || !packageName.MatchString(n) {
			return fmt.Errorf("%q 的 package/name 缺失或包含不安全字符", manager)
		}
		entries[Entry{manager, n, state}] = true
		return nil
	}
	object, isObject := data.(map[string]any)
	hasManager := false
	for _, manager := range managers {
		if _, ok := object[manager]; ok {
			hasManager = true
		}
	}
	if isObject && hasManager {
		for _, manager := range managers {
			section, exists := object[manager]
			if !exists {
				continue
			}
			if m, ok := section.(map[string]any); ok {
				if m["available"] == false {
					return nil, fmt.Errorf("mise 报告 %s manager 不可用；为避免空列表误删，已停止", manager)
				}
				section = m["packages"]
			}
			items, ok := section.([]any)
			if !ok {
				return nil, fmt.Errorf("mise 的 %q 区段没有可识别的 packages 数组", manager)
			}
			for _, item := range items {
				if err := add(manager, item); err != nil {
					return nil, err
				}
			}
		}
	} else {
		flat := data
		if isObject {
			flat = object["packages"]
		}
		items, ok := flat.([]any)
		if !ok {
			return nil, fmt.Errorf("无法识别 mise packages status JSON 的顶层结构")
		}
		for _, item := range items {
			m, ok := item.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("扁平 packages 项必须是对象")
			}
			manager, _ := m["manager"].(string)
			if wanted[manager] {
				if err := add(manager, m); err != nil {
					return nil, err
				}
			}
		}
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("没有解析到任何指定 manager 声明；拒绝把空结果用于系统包收敛")
	}
	result := make([]Entry, 0, len(entries))
	for entry := range entries {
		result = append(result, entry)
	}
	sort.Slice(result, func(i, j int) bool {
		a, b := result[i], result[j]
		if a.Manager != b.Manager {
			return a.Manager < b.Manager
		}
		if a.Package != b.Package {
			return a.Package < b.Package
		}
		return a.State < b.State
	})
	return result, nil
}
