// Package localconfig reads TOML and locates machine-local configuration.
package localconfig

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

func Decode(data []byte) (map[string]any, error) {
	var result map[string]any
	if err := toml.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("TOML 配置无法安全解析")
	}
	return result, nil
}
func Read(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Decode(data)
}
func Table(value any) map[string]any { table, _ := value.(map[string]any); return table }
func Env(name, fallback string) string {
	if value, ok := os.LookupEnv(name); ok {
		return value
	}
	return fallback
}
func Within(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
func RealPath(path string) (string, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved, nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}
	// EvalSymlinks is strict; Path.resolve() in the reference still follows a
	// broken final link. Preserve that destination so ownership checks cannot
	// silently skip a missing repository source.
	if info, statErr := os.Lstat(path); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
		target, readErr := os.Readlink(path)
		if readErr != nil {
			return "", readErr
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(path), target)
		}
		return RealPath(target)
	}
	parent := filepath.Dir(path)
	if parent == path {
		return "", err
	}
	resolved, err = RealPath(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(resolved, filepath.Base(path)), nil
}
func IsLink(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode()&os.ModeSymlink != 0
}
func IsFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}
func Platform() string {
	if runtime.GOOS == "darwin" {
		return "macos"
	}
	if runtime.GOOS == "linux" {
		return "linux"
	}
	return ""
}
func Locations(home string) (enrollment, history string, candidates []string, err error) {
	config := Env("MISE_CONFIG_DIR", filepath.Join(Env("XDG_CONFIG_HOME", filepath.Join(home, ".config")), "mise"))
	state := Env("MISE_STATE_DIR", filepath.Join(Env("XDG_STATE_HOME", filepath.Join(home, ".local/state")), "mise"))
	code := filepath.Join(home, ".config/Code/User/settings.json")
	if Platform() == "macos" {
		code = filepath.Join(home, "Library/Application Support/Code/User/settings.json")
	} else if Platform() != "linux" {
		return "", "", nil, fmt.Errorf("仅支持 macOS / Linux")
	}
	return filepath.Join(config, "conf.d/system-sync-history.toml"), filepath.Join(state, "history-local"), []string{filepath.Join(home, ".codex/config.toml"), code, filepath.Join(home, ".zshrc.local")}, nil
}
func Quoted(value string) string { data, _ := json.Marshal(value); return string(data) }
func LocalEntries(data map[string]any, candidates []string) (map[string]any, bool) {
	entries := Table(data["dotfiles"])
	if len(data) != 1 || len(entries) == 0 {
		return nil, false
	}
	for path, value := range entries {
		found := false
		if !filepath.IsAbs(path) {
			return nil, false
		}
		for _, part := range strings.Split(path, string(filepath.Separator)) {
			if part == ".." {
				return nil, false
			}
		}
		for _, allowed := range candidates {
			found = found || filepath.Clean(path) == allowed
		}
		entry := Table(value)
		if !found || len(entry) != 2 || entry["mode"] != "track-local" || entry["autosave"] != false {
			return nil, false
		}
	}
	return entries, true
}
