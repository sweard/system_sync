package checkrun

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
	"github.com/sweard/system_sync/helper/internal/syncstate"
	"github.com/sweard/system_sync/helper/internal/terminal"
)

const goConfig = "dotfiles/common/mise/config.toml"
const goLock = "dotfiles/common/mise/mise.lock"
const goInput = "../dotfiles/common/mise/config.toml#tools.go"
const lockPlatforms = "macos-arm64,macos-x64,linux-x64,linux-arm64"

var fixedGo = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
var digest = regexp.MustCompile(`^[0-9a-f]{64}$`)
var bundleVersion = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)
var bundleTargets = []string{"darwin_arm64", "darwin_amd64", "linux_arm64", "linux_amd64"}

type preparePlan struct {
	Version, Bundle string
	Reasons         []string
	Inputs          []byte
	Files           map[string]savedFile
}
type savedFile struct {
	Data []byte
	Mode os.FileMode
}

// Every destination is repository-local and regular. In particular, never follow
// a deployed global config/lock symlink or a symlink inside the artifact tree.
func readRegular(root, rel string) (savedFile, error) {
	i, e := regularDestination(root, rel, false)
	if e != nil {
		return savedFile{}, e
	}
	b, e := os.ReadFile(filepath.Join(root, rel))
	return savedFile{b, i.Mode().Perm()}, e
}
func regularDestination(root, rel string, allowMissing bool) (os.FileInfo, error) {
	p := root
	parts := strings.Split(filepath.Clean(rel), string(filepath.Separator))
	for n, part := range parts {
		if part == ".." || part == "." || part == "" {
			return nil, fmt.Errorf("非法准备路径：%s", rel)
		}
		p = filepath.Join(p, part)
		i, e := os.Lstat(p)
		if allowMissing && n == len(parts)-1 && os.IsNotExist(e) {
			return nil, nil
		}
		if e != nil {
			return nil, fmt.Errorf("无法读取 %s：%w", rel, e)
		}
		if i.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("准备路径不能是符号链接：%s", rel)
		}
	}
	i, e := os.Lstat(p)
	if e != nil || !i.Mode().IsRegular() {
		return nil, fmt.Errorf("准备路径必须是普通文件：%s", rel)
	}
	return i, nil
}
func parseManifest(b []byte) (map[string]string, error) {
	m := map[string]string{}
	for _, line := range strings.Split(strings.TrimSuffix(string(b), "\n"), "\n") {
		hash, path, ok := strings.Cut(line, "  ")
		if !ok || !digest.MatchString(hash) || path == "" || m[path] != "" {
			return nil, errors.New("预编译清单格式错误或存在重复项；请人工核对并运行 mise run helper-bundle")
		}
		m[path] = hash
	}
	return m, nil
}
func parseLock(b []byte) (map[string]any, error) {
	var m map[string]any
	if toml.Unmarshal(b, &m) != nil || m["lockfile_version"] != int64(3) {
		return nil, errors.New("Go 下载锁无法解析或不是支持的格式 3；请先人工核对 dotfiles/common/mise/mise.lock")
	}
	if _, ok := m["tools"].(map[string]any); !ok {
		return nil, errors.New("下载锁缺少 tools 表")
	}
	return m, nil
}
func goLockReady(m map[string]any, version string) bool {
	entries, ok := m["tools"].(map[string]any)["go"].([]any)
	if !ok || len(entries) != 1 {
		return false
	}
	g, ok := entries[0].(map[string]any)
	if !ok || g["version"] != version || g["backend"] != "core:go" {
		return false
	}
	bound := false
	for _, v := range asList(g["specifiers"]) {
		bound = bound || v == version
	}
	if !bound {
		return false
	}
	for _, p := range strings.Split(lockPlatforms, ",") {
		meta, ok := g["platforms."+p].(map[string]any)
		if !ok {
			return false
		}
		checksum, _ := meta["checksum"].(string)
		archive := strings.ReplaceAll(strings.ReplaceAll(p, "macos", "darwin"), "x64", "amd64")
		if meta["url"] != "https://dl.google.com/go/go"+version+"."+archive+".tar.gz" || !strings.HasPrefix(checksum, "sha256:") || !digest.MatchString(strings.TrimPrefix(checksum, "sha256:")) {
			return false
		}
	}
	return true
}
func asList(v any) []any { a, _ := v.([]any); return a }
func sourceInputs(ctx context.Context, root string) ([]byte, error) {
	var out, errout bytes.Buffer
	code := Execute(ctx, root, []string{"bash", "-Eeuo", "pipefail", "-c", `source "$1"; helper_source_manifest`, "check", filepath.Join(root, "helper/scripts/common.sh")}, os.Environ(), &out, &errout)
	if code != 0 {
		// TOML and user environment can be private; report only the public entry.
		return nil, fmt.Errorf("无法核对 helper 构建输入（退出码 %d）；请运行 ./helper/scripts/verify.sh 查看诊断", code)
	}
	return out.Bytes(), nil
}
func inspectGo(ctx context.Context, root string, all bool) (preparePlan, error) {
	p := preparePlan{Files: map[string]savedFile{}}
	load := func(rel string) ([]byte, error) {
		f, e := readRegular(root, rel)
		if e == nil {
			p.Files[rel] = f
		}
		return f.Data, e
	}
	b, e := load(goConfig)
	if e != nil {
		return p, e
	}
	var config struct{ Tools struct{ Go string } }
	if toml.Unmarshal(b, &config) != nil || !fixedGo.MatchString(config.Tools.Go) {
		return p, errors.New("dotfiles/common/mise/config.toml 的 tools.go 必须是完整 Go 版本号字符串")
	}
	p.Version = config.Tools.Go
	b, e = load("helper/VERSION")
	if e != nil {
		return p, e
	}
	p.Bundle = strings.TrimSpace(string(b))
	if !bundleVersion.MatchString(p.Bundle) {
		return p, errors.New("helper/VERSION 格式不正确")
	}
	dir := "helper/prebuilt/" + p.Bundle + "/"
	b, e = load(dir + "SOURCE_SHA256SUMS")
	if e != nil {
		return p, fmt.Errorf("缺少可核对的构建输入清单；请先运行 mise run helper-bundle：%w", e)
	}
	previous, e := parseManifest(b)
	if e != nil {
		return p, e
	}
	p.Inputs, e = sourceInputs(ctx, root)
	if e != nil {
		return p, e
	}
	current, e := parseManifest(p.Inputs)
	if e != nil || current[goInput] == "" || previous[goInput] == "" {
		return p, errors.New("构建输入清单缺少 tools.go；请先人工核对并运行 mise run helper-bundle")
	}
	if current[goInput] != previous[goInput] {
		p.Reasons = append(p.Reasons, "预编译包使用的 Go 与 tools.go 不同")
	}
	delete(previous, goInput)
	delete(current, goInput)
	if !reflect.DeepEqual(previous, current) {
		return p, errors.New("helper 源码或其他构建输入已变化，不能作为 Go 升级自动修复；请先运行 mise run helper-bundle，再重新运行 check")
	}
	b, e = load(goLock)
	if e != nil {
		return p, e
	}
	lock, e := parseLock(b)
	if e != nil {
		return p, e
	}
	if !goLockReady(lock, p.Version) {
		p.Reasons = append(p.Reasons, "Go 下载锁的版本、请求绑定或四平台 URL/SHA256 未就绪")
	}
	b, e = load(dir + "SHA256SUMS")
	if e != nil {
		return p, e
	}
	hashes, e := parseManifest(b)
	if e != nil || len(hashes) != 4 {
		return p, errors.New("归档 SHA256SUMS 必须包含四个平台且无重复项；请人工核对")
	}
	targets := bundleTargets
	if !all && len(p.Reasons) == 0 {
		target, e := Target(runtime.GOOS, runtime.GOARCH)
		if e != nil {
			return p, e
		}
		targets = []string{target}
	}
	for _, target := range bundleTargets {
		name := "system-sync-helper_" + p.Bundle + "_" + target + ".tar.gz"
		if hashes[name] == "" {
			return p, fmt.Errorf("归档 SHA256SUMS 缺少 %s", name)
		}
	}
	for _, target := range targets {
		name := "system-sync-helper_" + p.Bundle + "_" + target + ".tar.gz"
		b, e := load(dir + name)
		if e != nil {
			return p, e
		}
		if fmt.Sprintf("%x", sha256.Sum256(b)) != hashes[name] {
			return p, fmt.Errorf("归档损坏（SHA256 不符）：%s；请人工核对，不自动覆盖", name)
		}
	}
	return p, nil
}

// PrepareGo only offers writes on a real terminal, never in CI or pipelines.
// PATH selection is done by check.sh before this Go program can be compiled.
func PrepareGo(ctx context.Context, root string, all bool, input *os.File, out, errout io.Writer) error {
	ci := os.Getenv("CI")
	interactive := (ci == "" || ci == "false" || ci == "0") && terminal.IsTerminal(input) && terminal.IsTerminal(os.Stdout)
	return prepareGo(ctx, root, all, input, out, errout, interactive, Execute)
}

type prepareExec func(context.Context, string, []string, []string, io.Writer, io.Writer) int

func prepareGo(ctx context.Context, root string, all bool, input io.Reader, out, errout io.Writer, interactive bool, run prepareExec) error {
	plan, e := inspectGo(ctx, root, all)
	if e != nil {
		return e
	}
	if len(plan.Reasons) == 0 {
		fmt.Fprintf(out, "[+] Go %s、四平台下载锁与所选归档的构建版本一致，无需准备。\n", plan.Version)
		return nil
	}
	fmt.Fprintf(out, "[!] Go %s 需要准备：%s\n", plan.Version, strings.Join(plan.Reasons, "；"))
	fmt.Fprintf(out, "[!] 将更新 %s 中的 Go；重建 helper/prebuilt/%s/ 的四平台归档、SHA256SUMS 和 SOURCE_SHA256SUMS。\n", goLock, plan.Bundle)
	fmt.Fprintln(out, "[~] 确认后查询 Go 下载元数据，备份并更新上述文件，再按原范围继续检查；不会安装 Go。")
	if !interactive {
		return errors.New("非交互环境只检查，未更新锁文件或归档；请在交互终端运行 mise run check 并输入 yes，审阅后提交更新；CI 请先在开发机完成准备")
	}
	if e := confirmPrepare(ctx, input, out); e != nil {
		return e
	}
	lock, e := syncstate.OperationLock(root)
	if e != nil {
		return fmt.Errorf("Go 准备未开始，共享操作锁不可用：%w", e)
	}
	defer lock.Close()
	if e := unchanged(ctx, root, plan); e != nil {
		return e
	}
	for _, rel := range []string{".system-sync", ".system-sync/check"} {
		if e := safeDir(filepath.Join(root, rel)); e != nil {
			return e
		}
	}
	base := filepath.Join(root, ".system-sync/check")
	if e := os.MkdirAll(base, 0755); e != nil {
		return e
	}
	backup, e := os.MkdirTemp(base, "go-prepare-")
	if e != nil {
		return e
	}
	paths := []string{goLock}
	for rel := range plan.Files {
		if strings.HasPrefix(rel, "helper/prebuilt/") {
			paths = append(paths, rel)
		}
	}
	sort.Strings(paths)
	for _, rel := range paths {
		dst := filepath.Join(backup, "before", rel)
		if e := os.MkdirAll(filepath.Dir(dst), 0700); e != nil {
			return e
		}
		if e := os.WriteFile(dst, plan.Files[rel].Data, plan.Files[rel].Mode); e != nil {
			return e
		}
	}
	fmt.Fprintf(out, "[~] Go 准备备份与日志：%s\n", backup)
	return applyPreparation(ctx, root, backup, plan, paths, out, errout, run)
}
func confirmPrepare(ctx context.Context, input io.Reader, out io.Writer) error {
	fmt.Fprint(out, "输入 yes 确认准备（直接回车或其他输入取消）：")
	done := make(chan bool, 1)
	go func() {
		line, e := bufio.NewReader(input).ReadString('\n')
		done <- e == nil && (line == "yes\n" || line == "yes\r\n")
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case yes := <-done:
		if !yes {
			return errors.New("已取消 Go 准备；检查未完成，未修改锁文件或预编译包")
		}
		return nil
	}
}
func unchanged(ctx context.Context, root string, p preparePlan) error {
	for rel, saved := range p.Files {
		now, e := readRegular(root, rel)
		if e != nil || now.Mode != saved.Mode || !bytes.Equal(now.Data, saved.Data) {
			return fmt.Errorf("确认后文件发生变化，未开始写入：%s；请重新运行 check", rel)
		}
	}
	now, e := sourceInputs(ctx, root)
	if e != nil || !bytes.Equal(now, p.Inputs) {
		return errors.New("确认后构建输入发生变化，未开始写入；请重新运行 check")
	}
	return ctx.Err()
}
func atomicFile(path string, f savedFile) error {
	tmp, e := os.CreateTemp(filepath.Dir(path), ".go-prepare-*")
	if e != nil {
		return e
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	if e = tmp.Chmod(f.Mode); e == nil {
		_, e = tmp.Write(f.Data)
	}
	if e == nil {
		e = tmp.Sync()
	}
	if e == nil {
		e = tmp.Close()
	}
	if e != nil {
		return e
	}
	return os.Rename(tmp.Name(), path)
}

// Resolve only Go in an isolated project. Global/system configs, private env,
// task tool requests and unrelated tools cannot enter lock resolution.
func lockEnvironment(stage string) []string {
	env := []string{}
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		if !strings.HasPrefix(key, "MISE_") {
			env = append(env, item)
		}
	}
	for k, v := range map[string]string{
		"MISE_CONFIG_DIR": filepath.Join(stage, "config"), "MISE_SYSTEM_CONFIG_DIR": filepath.Join(stage, "system"),
		"MISE_DATA_DIR": filepath.Join(stage, "data"), "MISE_STATE_DIR": filepath.Join(stage, "state"), "MISE_CACHE_DIR": filepath.Join(stage, "cache"),
		"MISE_CEILING_PATHS": filepath.Dir(stage), "MISE_TRUSTED_CONFIG_PATHS": stage,
		"MISE_AUTO_INSTALL": "false", "MISE_TASK_RUN_AUTO_INSTALL": "false",
	} {
		env = append(env, k+"="+v)
	}
	if offline := os.Getenv("MISE_OFFLINE"); offline != "" {
		env = append(env, "MISE_OFFLINE="+offline)
	}
	return env
}
func applyPreparation(ctx context.Context, root, backup string, p preparePlan, paths []string, out, errout io.Writer, run prepareExec) (result error) {
	dirty := false
	var publishedLock []byte
	journal := func(phase string) error {
		return syncstate.Journal(backup, phase, map[string]any{"go_version": p.Version, "paths": paths})
	}
	if e := journal("preparing"); e != nil {
		return e
	}
	defer func() {
		if result == nil {
			return
		}
		if dirty {
			var failures []error
			for _, rel := range paths {
				// Refuse to follow new symlinks introduced by concurrent external edits.
				if _, e := regularDestination(root, rel, true); e != nil {
					failures = append(failures, e)
					continue
				}
				if rel == goLock {
					now, e := readRegular(root, rel)
					if e != nil && !errors.Is(e, os.ErrNotExist) {
						failures = append(failures, e)
						continue
					}
					if e == nil && (!bytes.Equal(now.Data, p.Files[rel].Data) && !bytes.Equal(now.Data, publishedLock) || now.Mode != p.Files[rel].Mode) {
						failures = append(failures, errors.New("Go 锁在准备期间被其他进程修改，未覆盖；请从备份人工合并 Go 记录并保留其他工具改动"))
						continue
					}
				}
				if e := atomicFile(filepath.Join(root, rel), p.Files[rel]); e != nil {
					failures = append(failures, e)
				}
			}
			if len(failures) > 0 {
				_ = journal("rollback_failed")
				result = fmt.Errorf("%w；回滚不完整：%v；原文件在 %s/before，请人工恢复", result, errors.Join(failures...), backup)
				return
			}
		}
		_ = journal("rolled_back")
		result = fmt.Errorf("%w；锁文件与预编译包已保留或恢复到准备前，检查已停止；备份与诊断：%s", result, backup)
	}()
	stage := filepath.Join(backup, "resolve")
	if e := os.MkdirAll(stage, 0700); e != nil {
		return e
	}
	config := []byte("[tools]\ngo = \"" + p.Version + "\"\n")
	for name, b := range map[string][]byte{"mise.toml": config, "mise.lock": p.Files[goLock].Data} {
		if e := os.WriteFile(filepath.Join(stage, name), b, 0600); e != nil {
			return e
		}
	}
	log, e := os.OpenFile(filepath.Join(backup, "commands.log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	defer log.Close()
	command := func(label, dir string, args, env []string) error {
		fmt.Fprintf(out, "[~] %s\n", label)
		limited, cancel := context.WithTimeout(ctx, 10*time.Minute)
		defer cancel()
		code := run(limited, dir, args, env, io.MultiWriter(out, log), io.MultiWriter(errout, log))
		if limited.Err() != nil {
			return fmt.Errorf("%s已停止（退出码 %d，单步上限 10 分钟）：%w", label, code, limited.Err())
		}
		if code != 0 {
			return fmt.Errorf("%s失败（退出码 %d）", label, code)
		}
		return nil
	}
	if e := command("解析 Go 四平台下载锁", stage, []string{"mise", "lock", "--platform", lockPlatforms, "go"}, lockEnvironment(stage)); e != nil {
		return e
	}
	b, e := os.ReadFile(filepath.Join(stage, "mise.lock"))
	if e != nil {
		return e
	}
	updated, e := parseLock(b)
	if e != nil || !goLockReady(updated, p.Version) {
		return errors.New("解析结果缺少固定 Go 版本或四平台 URL/SHA256，拒绝写入")
	}
	old, _ := parseLock(p.Files[goLock].Data)
	delete(old["tools"].(map[string]any), "go")
	delete(updated["tools"].(map[string]any), "go")
	if !reflect.DeepEqual(old, updated) {
		return errors.New("锁解析改变了 Go 以外的内容，拒绝写入")
	}
	if b, e := os.ReadFile(filepath.Join(stage, "mise.toml")); e != nil || !bytes.Equal(b, config) {
		return errors.New("锁解析意外改写版本声明，拒绝写入")
	}
	if e := unchanged(ctx, root, p); e != nil {
		return e
	}
	dirty = true
	publishedLock = b
	if e := atomicFile(filepath.Join(root, goLock), savedFile{b, p.Files[goLock].Mode}); e != nil {
		return e
	}
	if e := journal("bundling"); e != nil {
		return e
	}
	if e := command("重建四平台预编译包", root, []string{filepath.Join(root, "helper/scripts/build.sh"), "--bundle"}, os.Environ()); e != nil {
		return e
	}
	if e := command("重新校验四平台预编译包", root, []string{filepath.Join(root, "helper/scripts/verify.sh")}, os.Environ()); e != nil {
		return e
	}
	ready, e := inspectGo(ctx, root, true)
	if e != nil || len(ready.Reasons) != 0 || ready.Version != p.Version || !bytes.Equal(ready.Inputs, p.Inputs) {
		return errors.New("准备后的版本、下载锁或构建输入不一致，拒绝继续检查")
	}
	if e := journal("prepared"); e != nil {
		return e
	}
	fmt.Fprintln(out, "[+] Go 下载锁与四平台预编译包准备完成，继续原范围检查；后续检查失败时保留这些结果供修复。")
	return nil
}
