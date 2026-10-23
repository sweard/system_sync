//go:build integration

package integration

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Exercise the real check bootstrap and runner with one synthetic integration
// case. Only expensive build/test work is replaced; lock resolution uses mise.
func checkGoPreparationCLI(parent *fixture) {
	for _, scenario := range []string{"installed-selection", "pipeline", "ci-terminal", "cancel", "prepare", "build-failure"} {
		parent.t.Run(scenario, func(t *testing.T) {
			f := newFixture(t)
			f.toolchain()
			f.copy("helper/go.mod", "helper/go.sum", "helper/vendor", "helper/dev/check", "helper/dev/checkrun", "helper/internal/syncstate", "helper/internal/taskexec", "helper/internal/terminal", "helper/scripts/verify.sh", "helper/VERSION", "dotfiles/common/mise/mise.lock")
			version := goVersion(t)
			common := filepath.Join(f.root, "helper/scripts/common.sh")
			write(t, common, read(t, common)+"\nhelper_source_manifest() { cat \"$REPO_ROOT/current-inputs\"; }\n")
			manifest := func(v string) string {
				return fmt.Sprintf("%x  ../dotfiles/common/mise/config.toml#tools.go\n", sha256.Sum256([]byte(v+"\n")))
			}
			write(t, filepath.Join(f.root, "current-inputs"), manifest(version))
			dir := filepath.Join(f.root, "helper/prebuilt", strings.TrimSpace(read(t, filepath.Join(f.root, "helper/VERSION"))))
			previous := "0.0.0"
			if scenario == "installed-selection" {
				previous = version
			}
			write(t, filepath.Join(dir, "SOURCE_SHA256SUMS"), manifest(previous))
			var hashes strings.Builder
			for _, target := range []string{"darwin_arm64", "darwin_amd64", "linux_arm64", "linux_amd64"} {
				name := "system-sync-helper_" + filepath.Base(dir) + "_" + target + ".tar.gz"
				write(t, filepath.Join(dir, name), "archive-"+target)
				fmt.Fprintf(&hashes, "%x  %s\n", sha256.Sum256([]byte("archive-"+target)), name)
			}
			write(t, filepath.Join(dir, "SHA256SUMS"), hashes.String())
			write(t, filepath.Join(f.root, "mise.toml"), "[tasks.check]\nraw=true\nrun='./tests/check.sh'\n")
			executable(t, filepath.Join(f.root, "helper/scripts/run.sh"), "#!/bin/sh\nexit 0\n")
			executable(t, filepath.Join(f.root, "helper/scripts/test.sh"), "#!/bin/sh\nexit 0\n")
			build := "#!/bin/sh\nif [ \"${1-}\" = --bundle ]; then\n  echo bundle >> " + quote(filepath.Join(f.root, "bundle-called")) + "\n  cp " + quote(filepath.Join(f.root, "current-inputs")) + " " + quote(filepath.Join(dir, "SOURCE_SHA256SUMS")) + "\n"
			if scenario == "build-failure" {
				build += "  exit 7\n"
			}
			executable(t, filepath.Join(f.root, "helper/scripts/build.sh"), build+"fi\n")
			f.shell("git", "exit 0")
			executable(t, filepath.Join(f.root, "scripts/detect-linux-profile.sh"), "#!/bin/sh\necho arch\n")
			write(t, filepath.Join(f.root, "helper/dev/integration/fixture_test.go"), "package integration\nimport \"testing\"\nfunc TestIntegration(t *testing.T){t.Run(\"parallel\",func(t *testing.T){t.Run(\"fixture\",func(t *testing.T){})})}\n")
			writeJSON(t, filepath.Join(f.root, "tests/coverage.json"), []map[string]any{{"id": "fixture", "platforms": []string{"common"}}})
			// Share only the compiler cache, not mise state or user configuration.
			cache := os.Getenv("GOCACHE")
			if cache == "" {
				cache = filepath.Join(repo, ".system-sync/go/build")
			}
			symlink(f, cache, filepath.Join(f.root, ".system-sync/go/build"))
			before := snapshot(t, filepath.Join(f.root, "dotfiles"))
			archives := snapshot(t, dir)
			args := []string{filepath.Join(f.root, "tests/check.sh")}
			var r result
			switch scenario {
			case "installed-selection":
				symlink(f, runtime.GOROOT(), filepath.Join(f.env["MISE_DATA_DIR"], "installs/go", version))
				f.shell("go", "printf 'go0.0.0\\n'")
				r = good(t, f.run(args...))
				contains(t, r.all(), "选择已安装的固定 Go "+version)
			case "pipeline":
				r = bad(t, f.input("yes\n", args...))
				contains(t, r.all(), "非交互环境只检查")
			case "ci-terminal":
				f.env["CI"] = "true"
				r = bad(t, f.ttyOutput(args))
				contains(t, r.all(), "非交互环境只检查")
			case "cancel":
				r = bad(t, f.ttyOutput(args, [2]string{"输入 yes 确认准备", "yes "}))
				contains(t, r.all(), "已取消 Go 准备")
			case "prepare":
				r = good(t, f.ttyOutput(args, [2]string{"输入 yes 确认准备", "yes"}))
				contains(t, r.all(), "继续原范围检查")
			case "build-failure":
				r = bad(t, f.ttyOutput(args, [2]string{"输入 yes 确认准备", "yes"}))
				contains(t, r.all(), "已保留或恢复到准备前")
			}
			report := jsonData(f, filepath.Join(f.root, ".system-sync/check/latest.json"))
			equal(t, report["scope"], "current")
			equal(t, report["target"], runtime.GOOS+"_"+runtime.GOARCH)
			if scenario == "prepare" || scenario == "installed-selection" {
				equal(t, report["exit_code"], float64(0))
				equal(t, len(report["stages"].([]any)), 10)
				equal(t, report["tests_run"], float64(1))
			} else {
				equal(t, report["exit_code"], float64(1))
				equal(t, len(report["stages"].([]any)), 1)
				equal(t, snapshot(t, filepath.Join(f.root, "dotfiles")), before)
				equal(t, snapshot(t, dir), archives)
			}
		})
	}
}
