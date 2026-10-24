//go:build integration

package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
)

func init() { handlers["test_onboarding"] = onboardCase }
func onboardCase(f *fixture, name string) {
	t := f.t
	f.helper()
	state := filepath.Join(f.base, "state.json")
	f.env["ONBOARD_TEST_STATE"] = state
	writeJSON(t, state, map[string]any{"profile": "macos", "installed": []string{"git", "local-tool", "orphan-lib"}, "roots": []string{"git", "local-tool"}, "calls": []any{}, "mutations": []any{}})
	f.mock("onboard", "mise", "brew", "uname", "pacman", "sudo")
	marker := filepath.Join(f.base, "unexpected-runtime")
	for _, n := range []string{"go", "python3", "python", "curl", "wget"} {
		f.shell(n, "echo called > "+quote(marker)+"\nexit 99")
	}
	f.env["PATH"] = f.bin + ":/usr/bin:/bin:/usr/sbin:/sbin"
	manifest := ""
	configure := func(p string) {
		s := jsonData(f, state)
		s["profile"] = p
		writeJSON(t, state, s)
		if p == "macos" {
			manifest = filepath.Join(f.root, "packages/macos/Brewfile")
			write(t, manifest, "# preserve options\nbrew \"git\", link: false\nbrew \"missing-tool\", link: false\n")
		} else {
			manifest = filepath.Join(f.root, "packages/linux/arch/mise.toml")
			write(t, manifest, "[bootstrap.packages]\n\"pacman:git\"=\"latest\"\n\"pacman:missing-tool\"=\"latest\"\n")
			executable(t, filepath.Join(f.root, "scripts/detect-linux-profile.sh"), "#!/bin/sh\nprintf 'arch\\n'\n")
		}
	}
	configure("macos")
	cli := func(args ...string) result {
		r := f.cli(args...)
		if exists(marker) {
			t.Fatal("runtime invoked compiler/interpreter/downloader")
		}
		return r
	}
	mutations := func() int { return len(jsonData(f, state)["mutations"].([]any)) }
	switch name {
	case "test_default_preview_and_json_never_save_or_mutate":
		before := read(t, manifest)
		contains(t, good(t, cli("package-capture")).out, "仅为预览")
		r := good(t, cli("machine-init", "--only", "packages", "--json"))
		var plan map[string]any
		json.Unmarshal([]byte(r.out), &plan)
		p := plan["packages"].(map[string]any)
		equal(t, p["missing"].([]any)[0].(map[string]any)["name"], "missing-tool")
		contains(t, p["candidate"].(map[string]any)["diff"].(string), "local-tool")
		equal(t, read(t, manifest), before)
		equal(t, mutations(), 0)
		for _, n := range []string{"capture", "history"} {
			if exists(filepath.Join(f.root, ".system-sync", n)) {
				t.Fatal("preview wrote state")
			}
		}
	case "test_capture_apply_only_saves_private_candidates":
		before := read(t, manifest)
		good(t, cli("package-capture", "--apply"))
		dirs, _ := filepath.Glob(filepath.Join(f.root, ".system-sync/capture/*"))
		equal(t, len(dirs), 1)
		candidate := filepath.Join(dirs[0], "Brewfile.candidate")
		contains(t, read(t, candidate), `brew "git", link: false`)
		contains(t, read(t, candidate), `brew "local-tool"`)
		info, _ := os.Stat(candidate)
		equal(t, info.Mode().Perm(), os.FileMode(0600))
		equal(t, read(t, manifest), before)
		equal(t, mutations(), 0)
		captureMergeCLICases(t)
	case "test_install_only_on_macos_and_arch_preserves_extras_and_is_repeatable":
		ps := []string{"macos", "arch"}
		if p := os.Getenv("SYSTEM_SYNC_TEST_PROFILE"); p != "all" && p != "" {
			ps = []string{p}
		}
		for _, p := range ps {
			configure(p)
			s := jsonData(f, state)
			s["installed"] = []string{"git", "local-tool", "orphan-lib"}
			s["roots"] = []string{"git", "local-tool"}
			s["mutations"] = []any{}
			writeJSON(t, state, s)
			before := read(t, manifest)
			good(t, cli("machine-init", "--only", "packages", "--apply", "--yes"))
			equal(t, setunion(list(jsonData(f, state)["installed"]), nil), []string{"git", "local-tool", "missing-tool", "orphan-lib"})
			equal(t, read(t, manifest), before)
			equal(t, mutations(), 1)
			good(t, cli("machine-init", "--only", "packages", "--apply", "--yes"))
			equal(t, mutations(), 1)
		}
	case "test_confirmation_and_invalid_flags_never_mutate":
		bad(t, cli("machine-init", "--only", "packages", "--apply"))
		for _, a := range [][]string{{"machine-init", "--apply", "--dry-run"}, {"machine-init", "--yes"}, {"machine-init", "--only", "unknown"}, {"package-capture", "--apply", "--json"}} {
			bad(t, cli(a...))
		}
		equal(t, mutations(), 0)
		if exists(filepath.Join(f.root, ".system-sync/history")) {
			t.Fatal("invalid flags wrote history")
		}
	case "test_machine_init_streams_homebrew_output_and_reports_verification":
		s := jsonData(f, state)
		s["live_progress"] = true
		writeJSON(t, state, s)
		brew := read(t, filepath.Join(f.bin, "brew"))
		// Model sudo's /dev/tty password input without invoking real sudo.
		f.shell("brew", `if [ "$1 $2" = 'bundle install' ]; then
  stty -echo < /dev/tty || exit 97
  printf 'Fixture password: ' > /dev/tty
  IFS= read -r password < /dev/tty
  stty echo < /dev/tty || exit 98
  printf '\n' > /dev/tty
  [ "$password" = 'fixture-password-731' ] || exit 99
fi
`+brew)
		script := filepath.Join(f.base, "interactive-init.sh")
		executable(t, script, "#!/bin/sh\n"+quote(filepath.Join(f.root, "helper/scripts/run.sh"))+" machine-init --only packages --apply || exit $?\nprintf 'Fixture after init: '\nIFS= read -r answer\n[ \"$answer\" = done ]\n")
		r := good(t, f.controllingTerminal([]string{script}, [2]string{"输入 yes 确认（其他输入取消）：", "yes"}, [2]string{"Fixture password: ", "fixture-password-731"}, [2]string{"Fixture: onboard download", "download-seen"}, [2]string{"Fixture: onboard install", "install-seen"}, [2]string{"Fixture after init: ", "done"}))
		excludes(t, r.out, "fixture-password-731")
		contains(t, r.out, "Fixture after init: done")
		contains(t, r.out, "开始接入阶段 1/1：packages")
		assertOrder(f, r.out, []string{"Fixture: onboard install", "复核补装结果"})
		contains(t, r.out, "缺失软件已补装")

		// Cancellation must restore both foreground ownership and echo, and
		// still stop descendants of the native operation's process group.
		s = jsonData(f, state)
		s["installed"] = []string{"git", "local-tool", "orphan-lib"}
		writeJSON(t, state, s)
		orphan := filepath.Join(f.base, "cancelled-child-survived")
		f.shell("brew", `if [ "$1 $2" = 'bundle install' ]; then
  stty -echo < /dev/tty || exit 97
  (sleep 0.3; printf survived > `+quote(orphan)+`) &
  kill -TERM "$PPID"
  wait
  exit 99
fi
`+brew)
		executable(t, script, "#!/bin/sh\n"+quote(filepath.Join(f.root, "helper/scripts/run.sh"))+" machine-init --only packages --apply --yes\n[ $? -eq 130 ] || exit 91\nprintf 'Fixture after cancellation: '\nIFS= read -r answer\n[ \"$answer\" = done ] || exit 92\nsleep 0.4\n[ ! -e "+quote(orphan)+" ]\n")
		r = good(t, f.controllingTerminal([]string{script}, [2]string{"Fixture after cancellation: ", "done"}))
		contains(t, r.out, "Fixture after cancellation: done")
	case "test_query_failure_does_not_publish_partial_capture":
		configure("arch")
		s := jsonData(f, state)
		s["query_failure"] = true
		writeJSON(t, state, s)
		excludes(t, bad(t, cli("package-capture", "--apply")).all(), "database-error-fixture")
		if exists(filepath.Join(f.root, ".system-sync/capture")) {
			t.Fatal("partial capture published")
		}
	case "test_shared_lock_prevents_capture_and_initialization":
		release := lock(t, f.root)
		bad(t, cli("package-capture"))
		bad(t, cli("package-capture", "--apply", "--merge"))
		bad(t, cli("machine-init", "--only", "packages", "--apply", "--yes"))
		release()
		equal(t, mutations(), 0)
	case "test_real_mise_config_deployment_preserves_private_keys_and_backs_up":
		os.Remove(filepath.Join(f.bin, "mise"))
		symlink(f, misePath(t), filepath.Join(f.bin, "mise"))
		f.env["MISE_CONFIG_DIR"] = filepath.Join(f.home, ".config/mise")
		write(t, filepath.Join(f.root, "dotfiles/common/mise/config.toml"), "# Global configuration\n")
		write(t, filepath.Join(f.root, "shared.json"), `{"shared":"new"}`)
		private := filepath.Join(f.home, ".private-app.json")
		write(t, private, `{"shared":"old","secret":"private-test-value"}`)
		write(t, filepath.Join(f.root, "mise.toml"), "[dotfiles]\n\"~/.config/mise/config.toml\"={source=\"dotfiles/common/mise/config.toml\",mode=\"symlink\"}\n\"~/.private-app.json/shared\"={source=\"shared.json\",merge=true}\n")
		before := read(t, private)
		good(t, cli("machine-init", "--only", "config"))
		equal(t, read(t, private), before)
		if exists(filepath.Join(f.home, ".config/mise/config.toml")) {
			t.Fatal("preview deployed")
		}
		good(t, cli("machine-init", "--only", "config", "--apply", "--yes"))
		equal(t, jsonData(f, private), map[string]any{"shared": "new", "secret": "private-test-value"})
		if !islink(filepath.Join(f.home, ".config/mise/config.toml")) {
			t.Fatal("missing mise link")
		}
		paths, _ := filepath.Glob(filepath.Join(f.root, ".system-sync/history/*/config.before.tar"))
		equal(t, len(paths), 1)
		equal(t, tarMember(f, paths[0], ".private-app.json"), before)
		after := read(t, private)
		good(t, cli("machine-init", "--only", "config", "--apply", "--yes"))
		equal(t, read(t, private), after)
	default:
		t.Fatal("unported", name)
	}
}
