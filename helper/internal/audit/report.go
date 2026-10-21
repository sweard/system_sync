package audit

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/sweard/system_sync/helper/internal/query"
)

type PackageGroup struct {
	Title, Kind string
	Items       []string
}

func ParsePackageStatus(output string) ([]PackageGroup, error) {
	output = strings.ReplaceAll(output, "\r\n", "\n")
	pattern := regexp.MustCompile(`(?m)^([^\n]+)（([0-9]+)）\n((?:  [^\n]*(?:\n|$))*)`)
	namePattern := regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9@+._:/ -]*(?:\[[a-z_]+\])?$`)
	seen := map[string]bool{}
	groups := []PackageGroup{}
	for _, match := range pattern.FindAllStringSubmatch(output, -1) {
		title := match[1]
		kind, known := PackageGroups[title]
		if !known || seen[title] {
			return nil, query.Error("系统包状态区段无法识别；请单独运行 mise run package-status")
		}
		items := []string{}
		for _, line := range strings.Split(strings.TrimSuffix(match[3], "\n"), "\n") {
			if strings.HasPrefix(line, "  - ") {
				items = append(items, line[4:])
			}
		}
		count, err := strconv.Atoi(match[2])
		if err != nil || len(items) != count {
			return nil, query.Error("系统包状态计数与列表不一致；报告尚未完整")
		}
		for _, item := range items {
			if !namePattern.MatchString(item) {
				return nil, query.Error("系统包状态包含无法识别的名称")
			}
		}
		seen[title] = true
		groups = append(groups, PackageGroup{title, kind, items})
	}
	for _, required := range PackageRequired {
		complete := true
		for _, title := range required {
			if !seen[title] {
				complete = false
				break
			}
		}
		if complete {
			return groups, nil
		}
	}
	return nil, query.Error("系统包状态缺少必需区段；不能判定同步完成")
}

func redactReport(report *Report, environment map[string]string) {
	pattern := regexp.MustCompile(`(?i)TOKEN|SECRET|PASSWORD|API_KEY|CREDENTIAL`)
	secrets := []string{}
	for name, value := range environment {
		if value != "" && (pattern.MatchString(name) || name == "CASE_SUBMITTER" || name == "DASHBOARD_HOST") {
			secrets = append(secrets, value)
		}
	}
	sort.Slice(secrets, func(i, j int) bool {
		if len(secrets[i]) == len(secrets[j]) {
			return secrets[i] < secrets[j]
		}
		return len(secrets[i]) > len(secrets[j])
	})
	controls := regexp.MustCompile(`[\x00-\x08\x0b-\x1f\x7f]`)
	var clean func(any) any
	clean = func(value any) any {
		switch v := value.(type) {
		case string:
			for _, secret := range secrets {
				if v == secret {
					v = "[REDACTED]"
				} else if len([]rune(secret)) >= 4 {
					v = strings.ReplaceAll(v, secret, "[REDACTED]")
				}
			}
			return controls.ReplaceAllString(v, "?")
		case []string:
			result := make([]string, len(v))
			for i, s := range v {
				result[i] = clean(s).(string)
			}
			return result
		case Finding:
			for k, s := range v {
				if k != "kind" && k != "code" && k != "path_source" {
					v[k] = clean(s)
				}
			}
			return v
		case map[string]any:
			for k, s := range v {
				if k != "kind" && k != "code" && k != "path_source" {
					v[k] = clean(s)
				}
			}
			return v
		}
		return value
	}
	clean(report.Context)
	for _, items := range report.Sections {
		for _, item := range items {
			clean(item)
		}
	}
}

func ExitCode(report Report, strict bool) int {
	if report.Summary["error"] > 0 {
		return 2
	}
	if strict && report.Summary["drift"] > 0 {
		return 1
	}
	return 0
}

func UseColor(mode string, environment map[string]string, isTerminal bool) bool {
	if mode != "auto" {
		return mode == "always"
	}
	_, noColor := environment["NO_COLOR"]
	return !noColor && strings.ToLower(environment["TERM"]) != "dumb" && isTerminal
}

func Render(report Report, color bool) string {
	style := func(kind, text string) string {
		if color && Colors[kind] != "" {
			return Colors[kind] + text + "\x1b[0m"
		}
		return text
	}
	pathSource := "当前进程环境"
	if report.Context["path_source"] == "caller" {
		pathSource = "任务调整前的调用环境"
	}
	lines := []string{"只读审计：tools / packages / PATH / config", "PATH 来源：" + pathSource}
	for _, section := range Sections {
		items, ok := report.Sections[section]
		if !ok {
			continue
		}
		lines = append(lines, "\n["+section+"]")
		for _, finding := range items {
			kind := finding["kind"].(string)
			details := []string{fmt.Sprintf("  %s %s · %s：%s", Markers[kind], Kinds[kind], finding["item"], finding["message"])}
			for _, field := range []string{"source", "path", "actual", "expected"} {
				if value, ok := finding[field].(string); ok && value != "" {
					details = append(details, "    "+field+": "+value)
				}
			}
			items, _ := finding["items"].([]string)
			for i, item := range items {
				if i == 8 {
					details = append(details, "    更多条目见 --json 输出")
					break
				}
				details = append(details, "    - "+item)
			}
			lines = append(lines, style(kind, strings.Join(details, "\n")))
		}
	}
	counts := []string{}
	for _, kind := range []string{"ok", "drift", "candidate", "local", "error"} {
		text := fmt.Sprintf("%s %s %d", Markers[kind], Kinds[kind], report.Summary[kind])
		if report.Summary[kind] > 0 {
			text = style(kind, text)
		}
		counts = append(counts, text)
	}
	lines = append(lines, "\n汇总（报告条目数）："+strings.Join(counts, "，"), "候选项需要人工核对意图；本任务不安装、卸载或改写声明。")
	return strings.Join(lines, "\n")
}
