//go:build integration

package integration

import (
	"os"
	"path/filepath"
	"testing"
)

// Extend the existing capture acceptance case without changing its legacy ID.
func captureMergeCLICases(t *testing.T) {
	for _, profile := range []string{"macos", "arch"} {
		t.Run("merge-"+profile, func(t *testing.T) {
			f := newFixture(t)
			f.helper()
			state := filepath.Join(f.base, "capture-state.json")
			f.env["ONBOARD_TEST_STATE"] = state
			f.env["PATH"] = f.bin + ":/usr/bin:/bin:/usr/sbin:/sbin"
			writeJSON(t, state, map[string]any{"profile": profile, "installed": []string{"git", "local-tool", "orphan-lib"}, "roots": []string{"git", "local-tool"}, "calls": []any{}, "mutations": []any{}})
			f.mock("onboard", "mise", "brew", "uname", "pacman", "sudo")
			manifest := filepath.Join(f.root, "packages/macos/Brewfile")
			before := "# original options\nbrew \"git\", link: false\nbrew \"missing-tool\", link: false\n"
			if profile == "arch" {
				manifest = filepath.Join(f.root, "packages/linux/arch/mise.toml")
				before = "# original options\n[bootstrap.packages]\n\"pacman:git\"={version=\"latest\"}\n\"pacman:missing-tool\"=\"latest\"\n[env]\nKEEP_ME=\"original\"\n"
				executable(t, filepath.Join(f.root, "scripts/detect-linux-profile.sh"), "#!/bin/sh\nprintf 'arch\\n'\n")
			}
			write(t, manifest, before)
			if err := os.Chmod(manifest, 0640); err != nil {
				t.Fatal(err)
			}
			for _, flags := range [][]string{{"--merge"}, {"--merge", "--dry-run"}, {"--merge", "--apply", "--json"}, {"--merge", "--apply", "--yes"}} {
				bad(t, f.cli(append([]string{"package-capture"}, flags...)...))
			}
			bad(t, f.cli("machine-init", "--apply", "--merge"))
			bad(t, f.cli("package-capture", "--apply", "--merge"))
			command := []string{filepath.Join(f.root, "helper/scripts/run.sh"), "package-capture", "--apply", "--merge"}
			prompt := "输入 yes 确认合并（其他输入取消）："
			for _, answer := range []string{"", "no", "y", "YES"} {
				r := bad(t, f.terminal(command, [][2]string{{prompt, answer}}))
				equal(t, r.code, 2)
				equal(t, read(t, manifest), before)
			}
			if exists(filepath.Join(f.root, ".system-sync/capture")) {
				t.Fatal("invalid, nonterminal or cancelled merge saved a snapshot")
			}
			r := good(t, f.terminal(command, [][2]string{{prompt, "yes"}}))
			assertOrder(f, r.out, []string{"+++ packages/", prompt, "原子合并已确认", "根包声明已合并"})
			after := read(t, manifest)
			contains(t, after, "local-tool")
			contains(t, after, "missing-tool")
			excludes(t, after, "orphan-lib")
			if profile == "arch" {
				contains(t, after, "KEEP_ME=\"original\"")
				contains(t, after, "\"pacman:git\"={version=\"latest\"}")
			} else {
				contains(t, after, "brew \"git\", link: false")
			}
			dirs, _ := filepath.Glob(filepath.Join(f.root, ".system-sync/capture/*"))
			equal(t, len(dirs), 1)
			equal(t, read(t, filepath.Join(dirs[0], filepath.Base(manifest)+".before")), before)
			equal(t, jsonData(f, filepath.Join(dirs[0], "transaction.json"))["phase"], "complete")
			info, err := os.Stat(manifest)
			if err != nil {
				t.Fatal(err)
			}
			equal(t, info.Mode().Perm(), os.FileMode(0640))
			stamp := stampedSnapshot(t, manifest)
			contains(t, good(t, f.cli("package-capture", "--apply", "--merge")).out, "无新增根包声明需要合并")
			equal(t, stampedSnapshot(t, manifest), stamp)
			again, _ := filepath.Glob(filepath.Join(f.root, ".system-sync/capture/*"))
			equal(t, len(again), 1)
			equal(t, len(jsonData(f, state)["mutations"].([]any)), 0)
		})
	}
}
