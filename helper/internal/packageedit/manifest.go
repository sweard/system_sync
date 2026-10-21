package packageedit

import (
	"fmt"
	"regexp"
	"strings"
)

const token = `[A-Za-z0-9][A-Za-z0-9@+._-]*`

var brewName = regexp.MustCompile(`^` + token + `(?:/` + token + `/` + token + `)?$`)
var linuxName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9+._-]*(?::[a-z0-9][a-z0-9-]*)?$`)
var brewLine = regexp.MustCompile(`^\s*(tap|brew|cask)\s+(["'])(` + token + `(?:/` + token + `){0,2})(["'])((?:\s*,\s*(?:trusted|link):\s*(?:true|false))*)\s*(?:#.*)?$`)
var tomlLine = regexp.MustCompile(`^\s*(["'])((?:pacman|aur|apt|dnf|zypper):[^"']+)(["'])\s*=\s*(.+)$`)
var latestValue = regexp.MustCompile(`^(?:["']latest["']|\{\s*version\s*=\s*["']latest["']\s*\})\s*(?:#.*)?$`)

type brewEntry struct {
	Index               int
	Kind, Name, Options string
}

func lines(source string) []string {
	result := strings.SplitAfter(source, "\n")
	if result[len(result)-1] == "" {
		result = result[:len(result)-1]
	}
	return result
}
func names(source string) map[string]bool {
	result := map[string]bool{}
	for _, line := range strings.Split(source, "\n") {
		line, _, _ = strings.Cut(line, "#")
		if line = strings.TrimSpace(line); line != "" {
			result[line] = true
		}
	}
	return result
}
func appendLine(source, line string) string {
	if source != "" && !strings.HasSuffix(source, "\n") {
		source += "\n"
	}
	return source + line + "\n"
}
func brewEntries(source string) ([]brewEntry, error) {
	entries := []brewEntry{}
	seen := map[string]bool{}
	for index, line := range lines(source) {
		line = strings.TrimRight(line, "\r\n")
		stripped := strings.TrimSpace(line)
		if stripped == "" || strings.HasPrefix(stripped, "#") {
			continue
		}
		match := brewLine.FindStringSubmatch(line)
		if match == nil || match[2] != match[4] {
			return nil, fmt.Errorf("Brewfile 第 %d 行不是支持的静态声明；请手工处理动态 Ruby、多行或其他选项", index+1)
		}
		kind, name := match[1], match[3]
		if (kind == "tap" && strings.Count(name, "/") != 1) || (kind != "tap" && !brewName.MatchString(name)) {
			return nil, fmt.Errorf("无效的 Brewfile 名称：%s", name)
		}
		key := kind + " " + name
		if seen[key] {
			return nil, fmt.Errorf("Brewfile 重复声明：%s", key)
		}
		seen[key] = true
		entries = append(entries, brewEntry{index, kind, name, match[5]})
	}
	return entries, nil
}
func tomlEntries(source string) (map[string]int, int, error) {
	entries := map[string]int{}
	active, insertion := false, -1
	header := regexp.MustCompile(`^\[bootstrap\.packages\]\s*(?:#.*)?$`)
	for index, line := range lines(source) {
		stripped := strings.TrimSpace(line)
		if stripped == "" || strings.HasPrefix(stripped, "#") {
			continue
		}
		if strings.HasPrefix(stripped, "[") {
			if header.MatchString(stripped) {
				if insertion >= 0 {
					return nil, 0, fmt.Errorf("重复的 [bootstrap.packages]")
				}
				active, insertion = true, index+1
			} else {
				if strings.HasPrefix(stripped, "[bootstrap.packages.") || strings.HasPrefix(stripped, "[bootstrap.packages[") {
					return nil, 0, fmt.Errorf("包声明必须使用 [bootstrap.packages] 下的单行 key = value 格式")
				}
				active = false
			}
			continue
		}
		if active {
			match := tomlLine.FindStringSubmatch(strings.TrimRight(line, "\r\n"))
			if match == nil || match[1] != match[3] {
				return nil, 0, fmt.Errorf("包清单第 %d 行无法安全编辑；不支持多行值或非静态包名", index+1)
			}
			key := match[2]
			_, name, _ := strings.Cut(key, ":")
			if _, duplicate := entries[key]; !linuxName.MatchString(name) || duplicate {
				return nil, 0, fmt.Errorf("无效或重复的包声明：%s", key)
			}
			entries[key], insertion = index, index+1
		}
	}
	if insertion < 0 {
		return nil, 0, fmt.Errorf("缺少独立的 [bootstrap.packages]；不会猜测其他 TOML 布局")
	}
	return entries, insertion, nil
}
func replaceLines(source string, remove map[int]bool, addition string, insertion int) string {
	parts := lines(source)
	if insertion < 0 {
		insertion = len(parts)
	}
	var result strings.Builder
	for i := 0; i <= len(parts); i++ {
		if addition != "" && i == insertion {
			if i > 0 && !strings.HasSuffix(parts[i-1], "\n") {
				result.WriteByte('\n')
			}
			result.WriteString(addition + "\n")
		}
		if i < len(parts) && !remove[i] {
			result.WriteString(parts[i])
		}
	}
	return result.String()
}
func (p *Command) editBrew(source string) (string, error) {
	entries, err := brewEntries(source)
	if err != nil {
		return "", err
	}
	declaration := "brew"
	if p.Kind == "cask" {
		declaration = "cask"
	}
	matches := []brewEntry{}
	for _, entry := range entries {
		if entry.Kind == declaration && (p.Aliases[entry.Name] || entry.Name == p.Name) {
			matches = append(matches, entry)
		}
		if entry.Kind == declaration && strings.Count(entry.Name, "/") == 2 && strings.Count(p.Name, "/") == 2 && entry.Name != p.Name && short(entry.Name) == short(p.Name) {
			return "", fmt.Errorf("同名包已从另一个 tap 声明；请人工确认来源切换")
		}
	}
	if len(matches) > 1 {
		return "", fmt.Errorf("同一包有多条声明（含别名）；请先手工去重")
	}
	full := thirdParty(p.Name)
	trustName := "trusted-formulae.txt"
	if p.Kind == "cask" {
		trustName = "trusted-casks.txt"
	}
	trustFile := p.Directory + "/" + trustName
	trustedSource, err := p.read(trustFile)
	if err != nil {
		return "", err
	}
	trusted := names(trustedSource)
	remove := map[int]bool{}
	for _, match := range matches {
		remove[match.Index] = true
	}
	result := source
	if p.Options.Action == "uninstall" {
		result = replaceLines(source, remove, "", -1)
		trustRemove := map[int]bool{}
		for i, line := range lines(trustedSource) {
			name, _, _ := strings.Cut(line, "#")
			if p.Aliases[strings.TrimSpace(name)] || strings.TrimSpace(name) == p.Name {
				trustRemove[i] = true
			}
		}
		p.setAfter(trustFile, replaceLines(trustedSource, trustRemove, "", -1))
	} else if full {
		already := trusted[p.Name] && len(matches) > 0 && regexp.MustCompile(`^\s*,\s*trusted:\s*true$`).MatchString(matches[0].Options)
		if !p.Options.Trust && !already {
			return "", fmt.Errorf("第三方包需要显式 --trust；仅信任这一完整包名，不信任整个 tap")
		}
		if len(matches) > 0 && matches[0].Options != "" && !regexp.MustCompile(`^\s*,\s*trusted:\s*(true|false)$`).MatchString(matches[0].Options) {
			return "", fmt.Errorf("第三方声明含其他选项，无法满足现有精确信任格式；请手工调整")
		}
		insertion := -1
		if len(matches) > 0 {
			insertion = matches[0].Index
		}
		result = replaceLines(source, remove, declaration+` "`+p.Name+`", trusted: true`, insertion)
		if !trusted[p.Name] {
			p.setAfter(trustFile, appendLine(trustedSource, p.Name))
		}
		tap := p.Name[:strings.LastIndex(p.Name, "/")]
		tapped := false
		for _, entry := range entries {
			tapped = tapped || entry.Kind == "tap" && entry.Name == tap
		}
		if !tapped {
			result = `tap "` + tap + `"` + "\n" + result
		}
	} else if len(matches) == 0 {
		result = appendLine(source, declaration+` "`+p.Name+`"`)
	}
	after, err := brewEntries(result)
	if err != nil {
		return "", err
	}
	for _, entry := range after {
		if entry.Kind == "brew" || entry.Kind == "cask" {
			return result, nil
		}
	}
	return "", fmt.Errorf("拒绝生成空的软件包清单")
}
func (p *Command) editLinux(source string) (string, error) {
	entries, insertion, err := tomlEntries(source)
	if err != nil {
		return "", err
	}
	key := p.Manager + ":" + p.Name
	remove := map[int]bool{}
	for declared, index := range entries {
		manager, name, _ := strings.Cut(declared, ":")
		if manager != p.Manager && !(p.Profile == "arch" && (manager == "pacman" || manager == "aur")) {
			return "", fmt.Errorf("此 profile 包含其他平台的 manager 声明；请先整理清单")
		}
		if name == p.Name {
			remove[index] = true
			if p.Options.Action == "install" && declared != key {
				return "", fmt.Errorf("同名包已由另一 manager 声明；不会自动切换 pacman/AUR 来源")
			}
		}
	}
	result := source
	if p.Options.Action == "install" {
		if index, exists := entries[key]; exists {
			match := tomlLine.FindStringSubmatch(strings.TrimRight(lines(source)[index], "\r\n"))
			if !latestValue.MatchString(match[4]) {
				return "", fmt.Errorf("目标包带有版本固定或其他选项；此入口仅安装 latest，请手工处理该声明")
			}
		} else {
			result = replaceLines(source, nil, `"`+key+`" = "latest"`, insertion)
		}
	} else {
		result = replaceLines(source, remove, "", -1)
	}
	after, _, err := tomlEntries(result)
	if err != nil {
		return "", err
	}
	if len(after) == 0 {
		return "", fmt.Errorf("拒绝生成空的软件包清单")
	}
	return result, nil
}
