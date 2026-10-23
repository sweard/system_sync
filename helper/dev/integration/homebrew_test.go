//go:build integration

package integration

import (
	"os"
	"path/filepath"
	"strings"
)

const trustedCask = "abue-ammar/tinycast/tinycast"

func init() { handlers["test_homebrew_preview"] = brewCase }
func (f *fixture) brew() {
	f.copy("scripts")
	f.helper()
	p := filepath.Join(f.root, "packages/macos")
	write(f.t, filepath.Join(p, "Brewfile"), "tap \"fixture/tools\"\ntap \"abue-ammar/tinycast\"\nbrew \"git\"\nbrew \"mise\"\nbrew \"fixture/tools/tool\", trusted: true\ncask \""+trustedCask+"\", trusted: true\n")
	write(f.t, filepath.Join(p, "protected-packages.txt"), "formula:git\nformula:mise\n")
	write(f.t, filepath.Join(p, "trusted-formulae.txt"), "fixture/tools/tool\n")
	write(f.t, filepath.Join(p, "trusted-casks.txt"), trustedCask+"\n")
	f.env["SYNC_TEST_LOG"] = filepath.Join(f.base, "brew.log")
	f.env["SYNC_TEST_REAL_CONFIG"] = filepath.Join(f.base, "user-config")
	f.env["XDG_CONFIG_HOME"] = f.env["SYNC_TEST_REAL_CONFIG"]
	write(f.t, filepath.Join(f.env["XDG_CONFIG_HOME"], "homebrew/trust.json"), "{\"sentinel\": true}\n")
	mkdir(f.t, filepath.Join(f.base, "tmp"))
	f.env["TMPDIR"] = filepath.Join(f.base, "tmp")
	f.env["SYNC_TEST_INSTALLED"] = "1"
	f.mock("brew", "brew", "uname", "mise")
}
func brewCase(f *fixture, name string) {
	t := f.t
	f.brew()
	p := filepath.Join(f.root, "packages/macos")
	preview := func() result {
		return f.run("bash", filepath.Join(f.root, "scripts/homebrew-converge.sh"), "--dry-run")
	}
	logs := func() string { return read(t, f.env["SYNC_TEST_LOG"]) }
	transaction := func() map[string]any { return f.journal() }
	apply := func(script, mode string, answers ...[2]string) result {
		return f.terminal([]string{"bash", filepath.Join(f.root, "scripts", script), mode}, answers)
	}
	yes := [2]string{"输入 yes 继续", "yes"}
	progress := func(mode string) result {
		kind := "install"
		if mode == "--upgrade" {
			kind = "upgrade"
			f.env["SYNC_TEST_UPGRADE_PROGRESS"] = "1"
		} else {
			f.env["SYNC_TEST_INSTALL_PROGRESS"] = "1"
		}
		return apply("homebrew-converge.sh", mode, yes, [2]string{"Fixture: downloading " + kind, "download-seen"}, [2]string{"Fixture: installing " + kind, "install-seen"})
	}
	switch name {
	case "test_before_cask_installation":
		f.env["SYNC_TEST_INSTALLED"] = "0"
		contains(t, good(t, preview()).all(), "Cask tinycast needs to be installed.")
	case "test_status_skips_cleanup_and_temporary_trust_even_when_preview_would_fail":
		f.env["SYNC_TEST_CLEANUP_ERROR"] = "1"
		f.env["SYNC_TEST_TRUST_ERROR"] = "1"
		r := good(t, f.run("bash", filepath.Join(f.root, "scripts/homebrew-converge.sh"), "--status"))
		excludes(t, logs(), "|bundle cleanup ")
		excludes(t, logs(), "|trust ")
		excludes(t, r.all(), "Homebrew cleanup 精确预演")
		contains(t, r.all(), "bundle check 退出码为 0")
		contains(t, r.all(), "mise run package-dry-run")
	case "test_installed_cask_uses_isolated_exact_trust":
		r := good(t, preview())
		commands := []string{}
		for _, line := range strings.Split(logs(), "\n") {
			if !strings.Contains(line, "|trust ") {
				continue
			}
			config, command, _ := strings.Cut(line, "|")
			if config == f.env["SYNC_TEST_REAL_CONFIG"] || exists(config) {
				t.Fatal("temporary trust not isolated/reclaimed", config)
			}
			commands = append(commands, command)
		}
		equal(t, setunion(commands, nil), []string{"trust --cask " + trustedCask, "trust --formula fixture/tools/tool"})
		equal(t, read(t, filepath.Join(f.env["XDG_CONFIG_HOME"], "homebrew/trust.json")), "{\"sentinel\": true}\n")
		contains(t, r.all(), "预览模式：未做任何修改。")
	case "test_unqualified_cask_is_rejected", "test_undeclared_cask_is_rejected", "test_cask_without_trusted_declaration_is_rejected":
		want := "必须使用完整 owner/tap/cask 名称"
		switch name {
		case "test_unqualified_cask_is_rejected":
			write(t, filepath.Join(p, "trusted-casks.txt"), "tinycast\n")
		case "test_undeclared_cask_is_rejected":
			write(t, filepath.Join(p, "trusted-casks.txt"), "other/tap/not-declared\n")
			want = "临时信任 cask 未在 Brewfile 声明"
		default:
			path := filepath.Join(p, "Brewfile")
			write(t, path, strings.ReplaceAll(read(t, path), "cask \""+trustedCask+"\", trusted: true", "cask \""+trustedCask+"\""))
			want = "cask 缺少精确 trusted: true 声明"
		}
		contains(t, bad(t, preview()).all(), want)
		if exists(f.env["SYNC_TEST_LOG"]) {
			excludes(t, logs(), "|trust ")
		}
	case "test_failed_cask_trust_stops_before_cleanup":
		f.env["SYNC_TEST_TRUST_ERROR"] = "1"
		contains(t, bad(t, preview()).all(), "无法初始化 cleanup 临时 cask 信任")
		excludes(t, logs(), "|bundle cleanup ")
	case "test_cleanup_errors_still_stop_the_preview":
		f.env["SYNC_TEST_CLEANUP_ERROR"] = "1"
		contains(t, bad(t, preview()).all(), "Homebrew cleanup 预演失败")
	case "test_wrapper_and_direct_status_each_preflight_once":
		q := filepath.Join(f.base, "versions")
		f.env["SYNC_TEST_VERSION_LOG"] = q
		for _, script := range []string{"system-sync.sh", "homebrew-converge.sh"} {
			os.Remove(q)
			good(t, f.run("bash", filepath.Join(f.root, "scripts", script), "--status"))
			equal(t, strings.TrimSpace(read(t, q)), "--version")
			os.Remove(f.env["SYNC_TEST_LOG"])
			f.env["SYNC_TEST_MISE_VERSION"] = "2026.10.10 fixture"
			bad(t, f.run("bash", filepath.Join(f.root, "scripts", script), "--status"))
			if exists(f.env["SYNC_TEST_LOG"]) {
				t.Fatal("old mise queried brew")
			}
			delete(f.env, "SYNC_TEST_MISE_VERSION")
		}
	case "test_wrapper_apply_retains_a_single_guarded_transaction":
		good(t, apply("system-sync.sh", "--apply", yes))
		equal(t, transaction()["phase"], "complete")
		equal(t, strings.Count(logs(), "|bundle install "), 1)
	case "test_system_sync_streams_brew_install_output_before_cleanup":
		f.env["SYNC_TEST_INSTALL_PROGRESS"] = "1"
		r := good(t, apply("system-sync.sh", "--apply", yes, [2]string{"Fixture: downloading install", "download-seen"}, [2]string{"Fixture: installing install", "install-seen"}))
		equal(t, transaction()["phase"], "complete")
		contains(t, logs(), "bundle install --no-upgrade --verbose")
		if strings.Index(r.out, "Fixture: installing install") >= strings.Index(r.out, "确认 Brewfile 声明均已满足") {
			t.Fatal("install progress buffered")
		}
	case "test_apply_installs_and_cleans_inside_shared_audit":
		good(t, apply("homebrew-converge.sh", "--apply", yes))
		equal(t, transaction()["phase"], "complete")
		if strings.Index(logs(), "|bundle install ") >= strings.Index(logs(), f.env["SYNC_TEST_REAL_CONFIG"]+"|bundle cleanup ") {
			t.Fatal("cleanup ran before install")
		}
	case "test_failed_install_is_audited_without_actual_cleanup":
		f.env["SYNC_TEST_INSTALL_ERROR"] = "1"
		bad(t, progress("--apply"))
		equal(t, transaction()["phase"], "failed")
		equal(t, transaction()["last_phase"], "installing")
		excludes(t, logs(), f.env["SYNC_TEST_REAL_CONFIG"]+"|bundle cleanup ")
	case "test_upgrade_is_audited_and_does_not_cleanup":
		r := good(t, progress("--upgrade"))
		equal(t, transaction()["phase"], "complete")
		contains(t, logs(), "bundle install --upgrade --verbose")
		excludes(t, logs(), "|bundle cleanup ")
		pos := -1
		for _, s := range []string{"读取本机 Homebrew 安装状态", "检查 Brewfile 声明是否已满足", "升级 Brewfile 中的软件；实时显示", "Fixture: downloading upgrade", "Fixture: installing upgrade", "记录升级后的软件状态", "升级完成。"} {
			i := strings.Index(r.out, s)
			if i <= pos {
				t.Fatal("stage missing/out of order", s, r.out)
			}
			pos = i
		}
	case "test_upgrade_streams_failure_and_preserves_failed_transaction":
		f.env["SYNC_TEST_INSTALL_ERROR"] = "1"
		r := bad(t, progress("--upgrade"))
		equal(t, r.code, 95)
		contains(t, r.out, "Error: fixture installation failed.")
		equal(t, transaction()["phase"], "failed")
		equal(t, transaction()["last_phase"], "upgrading")
		for _, s := range []string{"升级完成。", "记录升级后的软件状态"} {
			excludes(t, r.out, s)
		}
		excludes(t, logs(), "|bundle cleanup ")
	case "test_only_yes_reaches_homebrew_mutations":
		for _, mode := range []string{"--apply", "--upgrade"} {
			old := "APPLY HOMEBREW"
			if mode == "--upgrade" {
				old = "UPGRADE HOMEBREW"
			}
			for _, a := range []string{"", "no", "y", old} {
				os.Remove(f.env["SYNC_TEST_LOG"])
				r := apply("homebrew-converge.sh", mode, [2]string{"输入 yes 继续", a})
				equal(t, r.code, 2)
				excludes(t, logs(), "|bundle install ")
				excludes(t, logs(), f.env["SYNC_TEST_REAL_CONFIG"]+"|bundle cleanup ")
			}
		}
	case "test_graph_warning_uses_yes_and_retains_separate_cleanup_confirmation":
		f.env["SYNC_TEST_GRAPH_WARNING"] = "1"
		for _, response := range []string{"no", "yes"} {
			os.Remove(f.env["SYNC_TEST_LOG"])
			r := apply("homebrew-converge.sh", "--apply", [2]string{"输入 yes 继续（", "yes"}, [2]string{"输入 yes 继续 cleanup", response})
			want := 2
			if response == "yes" {
				want = 0
			}
			equal(t, r.code, want)
			contains(t, logs(), "|bundle install ")
			equal(t, strings.Contains(logs(), f.env["SYNC_TEST_REAL_CONFIG"]+"|bundle cleanup "), response == "yes")
		}
	default:
		t.Fatal("unported", name)
	}
}
