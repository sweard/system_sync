//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

func init() {
	handlers["test_environment"] = environmentCase
	handlers["test_zsh_startup"] = zshCase
	handlers["test_task_aliases"] = aliasCase
	handlers["test_rebuild"] = rebuildCase
}
func environmentCase(f *fixture, name string) {
	t := f.t
	pathSource := filepath.Join(repo, "dotfiles/common/shell/path.sh")
	source := func(platform, extra, shell string) []string {
		r := good(t, f.run(shell, "-c", `OSTYPE="$1"; . "$2"; _system_sync_user_path; `+extra+`; printf '%s\n' "${ANDROID_HOME-}" "$PATH" "${LANG-}" "${LC_ALL-}"`, "path-test", platform, pathSource))
		equal(t, r.err, "")
		return strings.Split(strings.TrimSuffix(r.out, "\n"), "\n")
	}
	if strings.HasPrefix(name, "test_missing_") || strings.HasPrefix(name, "test_legacy_") || strings.HasPrefix(name, "test_platform_paths") || strings.HasPrefix(name, "test_custom_sdk") || strings.HasPrefix(name, "test_zsh_repeated") {
		f.env = map[string]string{"HOME": f.home, "PATH": "/usr/bin:/bin"}
		switch name {
		case "test_missing_directories_do_not_add_home_or_current_directory":
			equal(t, source("linux-gnu", ":", "bash"), []string{"", "/usr/bin:/bin", "", ""})
		case "test_legacy_flutter_directory_is_not_added_before_mise":
			mkdir(t, filepath.Join(f.home, "development/flutter/bin"))
			equal(t, source("linux-gnu", ":", "bash")[1], "/usr/bin:/bin")
		case "test_platform_paths_spaces_and_shim_priority":
			platforms := []string{"macos", "linux"}
			p := os.Getenv("SYSTEM_SYNC_TEST_PROFILE")
			if p != "all" && p != "" {
				if p == "macos" {
					platforms = []string{"macos"}
				} else {
					platforms = []string{"linux"}
				}
			}
			for _, platform := range platforms {
				g := newFixture(t)
				g.env = f.env
				g.env = map[string]string{"HOME": g.home, "PATH": "/usr/bin:/bin"}
				sdk, toolbox, ostype := "Android/Sdk", ".local/share/JetBrains/Toolbox/scripts", "linux-gnu"
				if platform == "macos" {
					sdk, toolbox, ostype = "Library/Android/sdk", "Library/Application Support/JetBrains/Toolbox/scripts", "darwin25.0"
				}
				rels := []string{".local/share/mise/shims", ".local/bin", ".git-ai/bin", ".cargo/bin", toolbox, sdk + "/emulator", sdk + "/platform-tools", sdk + "/build-tools"}
				want := []string{}
				for _, rel := range rels {
					mkdir(t, filepath.Join(g.home, rel))
					want = append(want, filepath.Join(g.home, rel))
				}
				r := good(t, g.run("bash", "-c", `OSTYPE="$1"; . "$2"; _system_sync_user_path; printf '%s\n' "$ANDROID_HOME" "$PATH"`, "fixture", ostype, pathSource))
				lines := strings.Split(strings.TrimSpace(r.out), "\n")
				equal(t, lines[0], filepath.Join(g.home, sdk))
				equal(t, strings.Split(lines[1], ":"), append(want, "/usr/bin", "/bin"))
				excludes(t, lines[1], filepath.Join(g.home, sdk, "tools"))
			}
		case "test_custom_sdk_and_cargo_locations_are_preserved":
			sdk, cargo, data := filepath.Join(f.home, "custom SDK"), filepath.Join(f.home, "cargo"), filepath.Join(f.home, "data")
			for _, p := range []string{filepath.Join(sdk, "platform-tools"), filepath.Join(cargo, "bin"), filepath.Join(data, "shims")} {
				mkdir(t, p)
			}
			f.env["ANDROID_HOME"] = sdk
			f.env["CARGO_HOME"] = cargo
			f.env["MISE_DATA_DIR"] = data
			r := source("linux-gnu", ":", "bash")
			equal(t, r[0], sdk)
			equal(t, strings.Split(r[1], ":"), []string{filepath.Join(data, "shims"), filepath.Join(cargo, "bin"), filepath.Join(sdk, "platform-tools"), "/usr/bin", "/bin"})
		case "test_zsh_repeated_startup_keeps_path_unique":
			for _, p := range []string{".config/zsh", ".local/bin", ".git-ai/bin"} {
				mkdir(t, filepath.Join(f.home, p))
			}
			symlink(f, pathSource, filepath.Join(f.home, ".config/zsh/path.sh"))
			symlink(f, filepath.Join(repo, "dotfiles/common/zsh/zshenv"), filepath.Join(f.home, ".zshenv"))
			f.env["ZDOTDIR"] = f.home
			r := good(t, f.run("zsh", "-c", `source ~/.zshenv; source ~/.zshenv; print -rl -- "${path[@]}"`))
			equal(t, r.err, "")
			paths := strings.Fields(r.out)
			equal(t, len(paths), len(setunion(paths, nil)))
			if index(paths, filepath.Join(f.home, ".git-ai/bin")) >= index(paths, "/usr/bin") {
				t.Fatal("user git precedence lost")
			}
		}
		return
	}
	config := filepath.Join(f.home, ".config/mise")
	f.env["MISE_CONFIG_DIR"] = config
	outside := filepath.Join(f.base, "outside")
	mkdir(t, outside)
	mkdir(t, config)
	f.env["PATH"] = filepath.Dir(misePath(t)) + ":/usr/bin:/bin:/usr/sbin:/sbin"
	f.env["MISE_DATA_DIR"] = filepath.Join(f.base, "data")
	definition := tomlData(f, filepath.Join(repo, "dotfiles/common/mise/config.toml"))
	raw := read(t, filepath.Join(repo, "dotfiles/common/mise/config.toml"))
	envSource := strings.SplitN(raw, "\n[env]\n", 2)[1]
	python := filepath.Join(f.home, "python")
	mkdir(t, filepath.Join(python, "bin"))
	configSource := filepath.Join(f.base, "sources/common/mise/config.toml")
	reds, _ := json.Marshal(definition["redactions"])
	write(t, configSource, "redactions = "+string(reds)+"\n[tools]\npython = "+quoteTOML("path:"+python)+"\n\n[env]\n"+envSource)
	symlink(f, configSource, filepath.Join(config, "config.toml"))
	copyTree(t, pathSource, filepath.Join(f.base, "sources/common/shell/path.sh"))
	miseAt := func(dir string, args ...string) result {
		old := f.root
		f.root = dir
		r := f.mise(args...)
		f.root = old
		return good(t, r)
	}
	resolved := func(dir string) map[string]any {
		r := miseAt(dir, "env", "--json")
		var v map[string]any
		if e := json.Unmarshal([]byte(r.out), &v); e != nil {
			t.Fatal(e)
		}
		return v
	}
	switch name {
	case "test_optional_private_file_and_project_override":
		initial := resolved(outside)
		if _, ok := initial["CASE_SUBMITTER"]; ok {
			t.Fatal("private fixture unexpectedly exists")
		}
		equal(t, initial["UV_PYTHON"], filepath.Join(python, "bin/python3"))
		equal(t, initial["UV_PYTHON_DOWNLOADS"], "never")
		secret := filepath.Join(config, "secrets.env")
		write(t, secret, "CASE_SUBMITTER='private fixture'\nDASHBOARD_HOST='https://private.invalid'\n")
		os.Chmod(secret, 0600)
		equal(t, resolved(outside)["CASE_SUBMITTER"], "private fixture")
		write(t, filepath.Join(f.root, "mise.toml"), "[tasks.private-output]\nrun = "+quoteTOML(`printf '%s\n' "$CASE_SUBMITTER" "$DASHBOARD_HOST"`)+"\n")
		masked := miseAt(f.root, "run", "--quiet", "private-output").out
		if strings.TrimSpace(masked) == "" {
			t.Fatal("missing masked output")
		}
		excludes(t, masked, "private fixture")
		excludes(t, masked, "https://private.invalid")
		write(t, filepath.Join(f.root, "mise.toml"), "[env]\nCASE_SUBMITTER = \"project fixture\"\n")
		equal(t, resolved(f.root)["CASE_SUBMITTER"], "project fixture")
		equal(t, resolved(outside)["CASE_SUBMITTER"], "private fixture")
		os.Remove(secret)
		if _, ok := resolved(outside)["CASE_SUBMITTER"]; ok {
			t.Fatal("deleted private env leaked")
		}
	case "test_exec_task_and_repeated_loading_keep_shared_path":
		for _, rel := range []string{".local/bin", ".git-ai/bin", "development/flutter/bin"} {
			mkdir(t, filepath.Join(f.home, rel))
		}
		shim := filepath.Join(f.base, "data/shims")
		mkdir(t, shim)
		f.env["PATH"] = strings.Join([]string{shim, filepath.Join(f.home, ".local/bin"), filepath.Join(f.home, ".git-ai/bin"), f.env["PATH"]}, ":")
		initial := resolved(outside)
		beforeEnv := map[string]string{}
		for k, v := range f.env {
			beforeEnv[k] = v
		}
		for k, v := range initial {
			if s, ok := v.(string); ok {
				f.env[k] = s
			}
		}
		repeated := resolved(outside)
		equal(t, repeated["PATH"], initial["PATH"])
		f.env = beforeEnv
		paths := strings.Split(initial["PATH"].(string), ":")
		equal(t, len(paths), len(setunion(paths, nil)))
		if index(paths, shim) >= index(paths, filepath.Join(f.home, ".local/bin")) || index(paths, filepath.Join(f.home, ".git-ai/bin")) >= index(paths, "/usr/bin") {
			t.Fatal("PATH order lost")
		}
		equal(t, strings.Split(strings.TrimSuffix(miseAt(outside, "exec", "--", "sh", "-c", `printf '%s\n' "$UV_PYTHON_DOWNLOADS" "$PATH"`).out, "\n"), "\n"), []string{"never", initial["PATH"].(string)})
		write(t, filepath.Join(f.root, "mise.toml"), "[tasks.capture]\nrun = 'printf \"%s\\n\" \"$UV_PYTHON_DOWNLOADS\"'\n")
		equal(t, strings.TrimSpace(miseAt(f.root, "run", "--quiet", "capture").out), "never")
	case "test_project_tool_selection_stays_ahead_of_unmanaged_commands":
		for dir, v := range map[string]string{"node-global": "global", "node-project": "project", ".local": "unmanaged"} {
			executable(t, filepath.Join(f.home, dir, "bin/node"), "#!/bin/sh\nprintf '%s\\n' "+quote(v)+"\n")
		}
		miseAt(outside, "link", "node@26.11.0", filepath.Join(f.home, "node-global"))
		miseAt(outside, "link", "node@26.12.0", filepath.Join(f.home, "node-project"))
		write(t, configSource, strings.Replace(read(t, configSource), "[tools]\n", "[tools]\nnode = \"26.11.0\"\n", 1))
		write(t, filepath.Join(f.root, "mise.toml"), "[tools]\nnode = \"26.12.0\"\n")
		miseAt(outside, "reshim")
		if !exists(filepath.Join(f.base, "data/shims/node")) {
			t.Fatal("shim missing")
		}
		f.env["PATH"] = filepath.Join(f.home, ".local/bin") + ":" + f.env["PATH"]
		for _, pair := range [][2]string{{outside, "global"}, {f.root, "project"}, {outside, "global"}} {
			equal(t, strings.TrimSpace(miseAt(pair[0], "exec", "--", "node").out), pair[1])
		}
	case "test_shell_hook_restores_project_environment_and_path":
		projectBin := filepath.Join(f.root, "bin")
		mkdir(t, projectBin)
		sdkName := "Android/Sdk"
		if runtime.GOOS == "darwin" {
			sdkName = "Library/Android/sdk"
		}
		globalSDK, projectSDK := filepath.Join(f.home, sdkName), filepath.Join(f.root, "custom SDK")
		for _, p := range []string{globalSDK, projectSDK} {
			mkdir(t, filepath.Join(p, "platform-tools"))
		}
		write(t, filepath.Join(config, "secrets.env"), "CASE_SUBMITTER='global fixture'\n")
		write(t, filepath.Join(f.root, "mise.toml"), "[env]\nCASE_SUBMITTER=\"project fixture\"\n_.path=\"bin\"\nANDROID_HOME="+quoteTOML(projectSDK)+"\n")
		script := `eval "$("$1" activate zsh)"
printf '%s|%s|%s\n' "$CASE_SUBMITTER" "$ANDROID_HOME" "$PATH"
cd "$2"
eval "$("$1" hook-env -s zsh --force)"
printf '%s|%s|%s\n' "$CASE_SUBMITTER" "$ANDROID_HOME" "$PATH"
cd "$3"
eval "$("$1" hook-env -s zsh --force)"
printf '%s|%s|%s\n' "$CASE_SUBMITTER" "$ANDROID_HOME" "$PATH"`
		old := f.root
		f.root = outside
		r := good(t, f.run("zsh", "-f", "-c", script, "hook-test", misePath(t), old, outside))
		f.root = old
		equal(t, r.err, "")
		lines := strings.Split(strings.TrimSuffix(r.out, "\n"), "\n")
		equal(t, len(lines), 3)
		before, inside, after := strings.SplitN(lines[0], "|", 3), strings.SplitN(lines[1], "|", 3), strings.SplitN(lines[2], "|", 3)
		equal(t, before[0], "global fixture")
		equal(t, before[1], globalSDK)
		equal(t, inside[0], "project fixture")
		equal(t, inside[1], projectSDK)
		contains(t, inside[2], projectBin)
		contains(t, inside[2], filepath.Join(projectSDK, "platform-tools"))
		excludes(t, inside[2], filepath.Join(globalSDK, "platform-tools"))
		equal(t, after, before)
	default:
		t.Fatal("unported", name)
	}
}
func zshCase(f *fixture, name string) {
	t := f.t
	if os.Getenv("SYSTEM_SYNC_ZSH_CTTY_PROBE") == "1" {
		var foreground int32
		_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, os.Stdin.Fd(), syscall.TIOCGPGRP, uintptr(unsafe.Pointer(&foreground)))
		if errno != 0 || int(foreground) != syscall.Getpgrp() {
			t.Fatal("regression probe does not own its foreground terminal", foreground, errno)
		}
	}
	calls := filepath.Join(f.base, "calls")
	f.env = map[string]string{"HOME": f.home, "ZDOTDIR": f.home, "PATH": f.bin + ":/usr/bin:/bin:" + f.bin, "TERM": "dumb", "ZSH_TEST_CALLS": calls}
	for _, n := range []string{"zshenv", "zshrc"} {
		symlink(f, filepath.Join(repo, "dotfiles/common/zsh", n), filepath.Join(f.home, "."+n))
	}
	symlink(f, filepath.Join(repo, "dotfiles/common/shell/path.sh"), filepath.Join(f.home, ".config/zsh/path.sh"))
	shell := func(flags, script string) []string {
		args := []string{"zsh", "-d"}
		args = append(args, strings.Fields(flags)...)
		args = append(args, script)
		r := good(t, f.run(args...))
		equal(t, r.err, "")
		return strings.Split(strings.TrimSuffix(r.out, "\n"), "\n")
	}
	switch name {
	case "test_noninteractive_startup_is_quiet_without_optional_tools":
		for _, n := range []string{"git", "brew", "mise"} {
			f.shell(n, `printf '%s\n' "$0" >> "$ZSH_TEST_CALLS"; exit 99`)
		}
		lines := shell("-c", `print -r -- ready; print -rl -- "${path[@]}"`)
		equal(t, lines[0], "ready")
		equal(t, len(lines)-1, len(setunion(lines[1:], nil)))
		if exists(calls) {
			t.Fatal("noninteractive invoked manager")
		}
	case "test_interactive_startup_keeps_local_settings_and_user_git":
		for path, s := range map[string]string{".local/share/zinit/zinit.git/zinit.zsh": "zinit() { :; }\n", ".config/zsh/platform.zsh": "typeset -g ZSH_TEST_PLATFORM=loaded\n", ".p10k.zsh": "typeset -g ZSH_TEST_THEME=loaded\n", ".zshrc.local": "export ZSH_TEST_PRIVATE='local value'\n", ".openclaw/completions/openclaw.zsh": "typeset -g ZSH_TEST_COMPLETION=loaded\n"} {
			write(t, filepath.Join(f.home, path), s)
		}
		userGit := filepath.Join(f.home, ".git-ai/bin/git")
		executable(t, userGit, "#!/bin/sh\nexit 0\n")
		for _, n := range []string{"git", "brew"} {
			f.shell(n, "exit 0")
		}
		f.shell("mise", `printf '%s\n' "$*" >> "$ZSH_TEST_CALLS"
test "$*" = 'activate zsh' || exit 99
printf '%s\n' 'export ZSH_TEST_MISE=active'`)
		lines := shell("-i -c", `print -r -- "$ZSH_TEST_PLATFORM|$ZSH_TEST_THEME|$ZSH_TEST_PRIVATE|$ZSH_TEST_COMPLETION|$ZSH_TEST_MISE|$(command -v git)|${+functions[nvm]}|${+functions[pyenv]}"`)
		equal(t, lines, []string{"loaded|loaded|local value|loaded|active|" + userGit + "|0|0"})
		equal(t, read(t, calls), "activate zsh\n")
		if os.Getenv("SYSTEM_SYNC_ZSH_CTTY_PROBE") != "1" {
			zshWithCallerTerminal(f)
		}
	case "test_login_noninteractive_loads_environment_without_prompt_hooks":
		executable(t, filepath.Join(f.home, ".local/bin/mise"), "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$ZSH_TEST_CALLS\"\ntest \"$*\" = 'env -s zsh' || exit 99\nprintf '%s\\n' 'export ZSH_TEST_MISE=environment'\n")
		platforms := []string{"macos", "linux"}
		p := os.Getenv("SYSTEM_SYNC_TEST_PROFILE")
		if p != "all" && p != "" {
			if p == "macos" {
				platforms = []string{"macos"}
			} else {
				platforms = []string{"linux"}
			}
		}
		for _, p := range platforms {
			target := filepath.Join(f.home, ".zprofile")
			os.Remove(target)
			symlink(f, filepath.Join(repo, "dotfiles", p, "zsh/zprofile"), target)
			os.Remove(calls)
			equal(t, shell("-l -c", `print -r -- "$ZSH_TEST_MISE|${+functions[mise]}"`), []string{"environment|0"})
			equal(t, read(t, calls), "env -s zsh\n")
		}
	default:
		t.Fatal("unported", name)
	}
}

// A PTY on stdin alone does not reproduce a caller's controlling terminal.
// Re-run this case as a foreground session leader with a real controlling PTY;
// fixture subprocesses must not wait for foreground job-control ownership.
func zshWithCallerTerminal(f *fixture) {
	t := f.t
	master, slave, e := openPTY()
	if e != nil {
		t.Fatal(e)
	}
	defer master.Close()
	defer slave.Close()
	pattern := []string{}
	for _, part := range strings.Split(t.Name(), "/") {
		pattern = append(pattern, "^"+regexp.QuoteMeta(part)+"$")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 55*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run="+strings.Join(pattern, "/"), "-test.parallel=1", "-test.timeout=50s")
	cmd.Dir = f.root
	cmd.Env = append(f.environment(), "SYSTEM_SYNC_ZSH_CTTY_PROBE=1")
	cmd.Stdin = slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM) }
	cmd.WaitDelay = 3 * time.Second
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if e := runTracked(cmd); e != nil {
		t.Fatalf("interactive zsh under caller terminal failed: %v\n%s", e, output.String())
	}
}
func aliasCase(f *fixture, name string) {
	t := f.t
	equal(t, name, "test_compatibility_aliases_forward_arguments_to_one_canonical_task")
	f.copy("mise.toml")
	r := good(t, f.mise("tasks", "ls", "--json"))
	var tasks []map[string]any
	if e := json.Unmarshal([]byte(r.out), &tasks); e != nil {
		t.Fatal(e)
	}
	byName := map[string]map[string]any{}
	for _, task := range tasks {
		parts := strings.Split(task["name"].(string), ":")
		byName[parts[len(parts)-1]] = task
	}
	for _, x := range []struct {
		old, canonical string
		args           []string
		command        string
	}{{"status", "package-status", nil, "./scripts/system-sync.sh --status"}, {"dry-run", "package-dry-run", nil, "./scripts/system-sync.sh --dry-run"}, {"vscode-dry-run", "vscode-status", []string{"--code", "/fixture/code"}, "./helper/scripts/run.sh vscode status --code /fixture/code"}} {
		if byName[x.old] != nil {
			t.Fatal("alias duplicates task")
		}
		if !has(list(byName[x.canonical]["aliases"]), x.old) {
			t.Fatal("missing alias")
		}
		a := good(t, f.mise(append([]string{"run", "--dry-run", x.canonical}, x.args...)...))
		b := good(t, f.mise(append([]string{"run", "--dry-run", x.old}, x.args...)...))
		equal(t, a.all(), b.all())
		contains(t, a.all(), x.command)
	}
	if exists(filepath.Join(f.root, ".system-sync")) {
		t.Fatal("alias dry-run executed task")
	}
}
func rebuildCase(f *fixture, name string) {
	t := f.t
	if name == "test_locks_cover_declared_versions_and_keep_dependency_graphs_in_repository" {
		configs := [][2]string{{"common/mise/config.toml", "common/mise/mise.lock"}, {"macos/mise/config.toml", "macos/mise/mise.macos.lock"}}
		for _, pair := range configs {
			p := os.Getenv("SYSTEM_SYNC_TEST_PROFILE")
			if strings.HasPrefix(pair[0], "macos") && p != "all" && p != "" && p != "macos" {
				continue
			}
			tools := tomlData(f, filepath.Join(repo, "dotfiles", pair[0]))["tools"].(map[string]any)
			lockPath := filepath.Join(repo, "dotfiles", pair[1])
			locked := tomlData(f, lockPath)["tools"].(map[string]any)
			equal(t, len(tools), len(locked))
			for n, spec := range tools {
				version, ok := spec.(string)
				if !ok {
					version = spec.(map[string]any)["version"].(string)
				}
				entries := locked[n].([]any)
				equal(t, len(entries), 1)
				entry := entries[0].(map[string]any)
				equal(t, entry["version"], version)
				for k, v := range entry {
					target := strings.ReplaceAll(strings.ReplaceAll(runtime.GOOS+"_"+runtime.GOARCH, "darwin_", "macos-"), "linux_", "linux-")
					target = strings.ReplaceAll(target, "amd64", "x64")
					if p != "all" && p != "" && strings.HasPrefix(k, "platforms.") && k != "platforms."+target {
						continue
					}
					if m, ok := v.(map[string]any); ok {
						if url, ok := m["url"].(string); ok {
							if !strings.HasPrefix(url, "https://") || m["checksum"] == nil {
								t.Fatal("invalid locked download", n, k)
							}
						}
					}
				}
				if a, ok := entry["aube"].(map[string]any); ok {
					sidecar := resolved(filepath.Join(filepath.Dir(lockPath), a["path"].(string)))
					if !strings.HasPrefix(sidecar, repo+string(os.PathSeparator)) || !exists(filepath.Join(sidecar, "aube-lock.yaml")) {
						t.Fatal("missing vendored lock graph")
					}
				}
			}
		}
		return
	}
	f.copy("mise.toml", "mise.macos.toml", "mise.linux.toml", ".miserc.toml", "dotfiles")
	f.env["MISE_CONFIG_DIR"] = filepath.Join(f.home, ".config/mise")
	settings := filepath.Join(f.home, ".config/Code/User/settings.json")
	if runtime.GOOS == "darwin" {
		settings = filepath.Join(f.home, "Library/Application Support/Code/User/settings.json")
	}
	switch name {
	case "test_empty_home_rebuild_and_second_apply_are_idempotent":
		good(t, f.mise("bootstrap", "dotfiles", "apply"))
		config := filepath.Join(f.home, ".config/mise")
		for _, p := range []string{filepath.Join(f.home, ".zshrc"), filepath.Join(config, "config.toml"), filepath.Join(config, "mise.lock")} {
			if !islink(p) {
				t.Fatal("missing link", p)
			}
		}
		lockPath := filepath.Join(config, "mise.lock")
		entry := tomlData(f, lockPath)["tools"].(map[string]any)["npm:yarn"].([]any)[0].(map[string]any)
		graph := entry["aube"].(map[string]any)["path"].(string)
		if !exists(filepath.Join(filepath.Dir(resolved(lockPath)), graph, "aube-lock.yaml")) {
			t.Fatal("missing dependency graph")
		}
		if runtime.GOOS == "darwin" && !islink(filepath.Join(config, "mise.macos.lock")) {
			t.Fatal("missing platform lock")
		}
		for _, p := range []string{filepath.Join(config, "secrets.env"), filepath.Join(f.home, ".zshrc.local")} {
			if exists(p) {
				t.Fatal("private file created")
			}
		}
		if !exists(settings) {
			t.Fatal("settings missing")
		}
		good(t, f.mise("bootstrap", "dotfiles", "apply"))
		equal(t, strings.TrimSpace(good(t, f.mise("bootstrap", "dotfiles", "diff")).out), "")
		if exists(filepath.Join(f.env["MISE_DATA_DIR"], "installs")) {
			t.Fatal("deployment installed tools")
		}
	case "test_rebuild_preserves_local_keys_without_importing_them_to_sources":
		codex := filepath.Join(f.home, ".codex/config.toml")
		write(t, codex, "private_fixture=\"local-canary\"\n")
		write(t, settings, `{"private.fixture":"local-canary"}`)
		good(t, f.mise("bootstrap", "dotfiles", "apply"))
		equal(t, tomlData(f, codex)["private_fixture"], "local-canary")
		equal(t, jsonData(f, settings)["private.fixture"], "local-canary")
		for _, item := range snapshot(t, filepath.Join(f.root, "dotfiles")) {
			excludes(t, item.Content, "local-canary")
		}
	default:
		t.Fatal("unported", name)
	}
}
