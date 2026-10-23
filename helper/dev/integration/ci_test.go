//go:build integration

package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

func init() { handlers["test_ci_environment"] = ciCase }
func workflowCommands(f *fixture, name string) string {
	lines := strings.Split(read(f.t, filepath.Join(repo, ".github/workflows/check.yml")), "\n")
	found, run := false, false
	out := []string{}
	for _, line := range lines {
		if line == "      - name: "+name {
			found = true
			continue
		}
		if !found {
			continue
		}
		if strings.HasPrefix(line, "      - ") {
			break
		}
		if line == "        run: |" {
			run = true
			continue
		}
		if run && strings.HasPrefix(line, "          ") {
			out = append(out, strings.TrimPrefix(line, "          "))
		}
	}
	if !run || len(out) == 0 {
		f.t.Fatal("workflow step has no run script", name)
	}
	return strings.Join(out, "\n")
}
func (f *fixture) toolchain() {
	f.goConfig()
	f.copy("helper/scripts/go-version.sh", "helper/scripts/common.sh", "helper/scripts/build.sh", "helper/scripts/test.sh", "scripts/task-output.sh", "tests/check.sh")
	f.env["REAL_MISE"] = misePath(f.t)
}
func (f *fixture) restrictedTools() {
	for _, n := range []string{"bash", "dirname", "cat"} {
		p, e := exec.LookPath(n)
		if e != nil {
			f.t.Fatal(e)
		}
		symlink(f, p, filepath.Join(f.bin, n))
	}
	symlink(f, misePath(f.t), filepath.Join(f.bin, "mise"))
	f.env["PATH"] = f.bin
	f.env["LC_ALL"] = "C"
	f.env["GO_VERSION"] = "99.0.0"
	f.env["REQUIRED_GO_VERSION"] = "99.0.0"
}
func ciCase(f *fixture, name string) {
	t := f.t
	f.toolchain()
	config := filepath.Join(f.root, "dotfiles/common/mise/config.toml")
	version := goVersion(t)
	script := func(file string, args ...string) result {
		return f.run(append([]string{filepath.Join(f.root, "helper/scripts", file)}, args...)...)
	}
	steps := []string{"Prepare pinned Go compiler for explicit builds", "Run isolated tests", "Build four platform archives from public source only"}
	runStep := func(s string) result { return f.run("/bin/bash", "-e", "-o", "pipefail", "-c", workflowCommands(f, s)) }
	variants := []struct {
		file string
		args []string
		task string
	}{{"build.sh", []string{"--native"}, "helper-build"}, {"build.sh", []string{"--dist"}, "helper-dist"}, {"build.sh", []string{"--bundle"}, "helper-bundle"}, {"test.sh", nil, "check"}, {"../../tests/check.sh", nil, "check"}}
	if name == "test_workflow_selects_installed_go_without_global_configuration" {
		workflow := read(t, filepath.Join(repo, ".github/workflows/check.yml"))
		for _, obsolete := range []string{"setup-python", "matrix.python", "PYTHONDONTWRITEBYTECODE"} {
			excludes(t, workflow, obsolete)
		}
		contains(t, workflow, "os: [ubuntu-24.04, macos-15]")
		query := exec.Command(misePath(t), "where", "go@"+version)
		query.Dir = repo
		compiler, err := query.CombinedOutput()
		if err != nil {
			t.Fatal(err, string(compiler))
		}
		installed := strings.TrimSpace(string(compiler))
		installed = resolved(installed)
		for _, inherited := range []string{"no-go", "managed-go-on-path", "old-system-go"} {
			g := newFixture(t)
			g.toolchain()
			tool := filepath.Join(g.env["MISE_DATA_DIR"], "installs/go", version)
			symlink(g, installed, tool)
			raw := read(t, filepath.Join(repo, "mise.toml"))
			re := regexp.MustCompile(`(?ms)^\[tasks\.check\]\n.*?\n\[`)
			replacement := "[tasks.check]\nrun = \"command -v go; test $(go env GOVERSION) = go" + version + "; go env GOVERSION; :\"\n\n["
			raw = re.ReplaceAllStringFunc(raw, func(string) string { return replacement })
			write(t, filepath.Join(g.root, "mise.toml"), raw)
			paths := []string{filepath.Dir(misePath(t)), "/usr/bin", "/bin", "/usr/sbin", "/sbin"}
			if inherited == "managed-go-on-path" {
				paths = append([]string{filepath.Join(tool, "bin")}, paths...)
			}
			if inherited == "old-system-go" {
				g.shell("go", "printf 'go1.26.0\\n'")
				paths = append([]string{g.bin}, paths...)
			}
			g.env["PATH"] = strings.Join(paths, ":")
			g.env["GOTOOLCHAIN"] = "local"
			g.env["GOENV"] = "off"
			g.env["GOCACHE"] = filepath.Join(g.base, "go-cache")
			before := read(t, filepath.Join(g.root, "mise.toml"))
			r := good(t, g.run("/bin/bash", "--noprofile", "--norc", "-e", "-o", "pipefail", "-c", workflowCommands(g, "Run isolated tests")))
			lines := strings.Split(strings.TrimSpace(r.out), "\n")
			equal(t, lines[len(lines)-1], "go"+version)
			equal(t, resolved(lines[0]), resolved(filepath.Join(installed, "bin/go")))
			equal(t, read(t, filepath.Join(g.root, "mise.toml")), before)
			if exists(filepath.Join(g.env["MISE_CONFIG_DIR"], "config.toml")) {
				t.Fatal("CI wrote global tool config")
			}
		}
		return
	}
	f.restrictedTools()
	switch name {
	case "test_build_and_test_errors_recommend_the_matching_task_and_explicit_tool":
		for _, state := range []string{"missing", "wrong-version", "version-query-failed"} {
			if state != "missing" {
				f.env["CI_GO_MODE"] = state
				f.mock("ci", "go")
			}
			for _, v := range variants {
				r := bad(t, script(v.file, v.args...))
				contains(t, r.err, "mise install go@"+version)
				contains(t, r.err, "mise run --tool go@"+version+" "+v.task)
				excludes(t, r.err, "mise exec")
				expected := map[string]string{"missing": "当前 PATH 中没有 go", "wrong-version": "当前为 go1.26.0", "version-query-failed": "无法读取当前 Go 版本"}[state]
				contains(t, r.err, expected)
				if exists(filepath.Join(f.root, ".system-sync")) {
					t.Fatal("failed preflight wrote state")
				}
			}
		}
	case "test_reader_and_build_help_follow_only_stored_go_without_a_compiler_or_environment_loading":
		marker := filepath.Join(f.root, "loaded-private-environment")
		private := filepath.Join(f.root, "private.sh")
		write(t, private, "touch "+quote(marker)+"\n")
		for _, p := range []string{filepath.Join(f.env["MISE_CONFIG_DIR"], "config.toml"), filepath.Join(f.root, "mise.toml"), filepath.Join(filepath.Dir(config), "config.local.toml")} {
			write(t, p, "[tools]\ngo=\"99.0.0\"\n")
		}
		for _, v := range []string{version, "9.8.7"} {
			write(t, config, "[tools]\nnode=\"latest\"\ngo="+quoteTOML(v)+"\n[env]\n_.source="+quoteTOML(private)+"\n_.file=\"missing-private.env\"\n")
			r := good(t, script("go-version.sh"))
			equal(t, r.out, v+"\n")
			equal(t, r.err, "")
			contains(t, good(t, script("build.sh", "--help")).out, "需要 Go "+v)
		}
		if exists(filepath.Join(f.root, ".system-sync")) || exists(marker) {
			t.Fatal("version read loaded environment or tools")
		}
	case "test_invalid_or_missing_config_stops_before_querying_go_or_installing_tools":
		marker := filepath.Join(f.root, "queried")
		f.env["CI_MARKER"] = marker
		os.Remove(filepath.Join(f.bin, "mise"))
		f.mock("ci", "mise", "go")
		values := []string{"<missing>", "", "[tools]\nnode=\"latest\"\n", "[env]\nsecret=\"private-parser-diagnostic\"\n[tools\n"}
		for _, literal := range []string{`"latest"`, `"1.2"`, `"^1.2.3"`, `"go1.2.3"`, `"1.2.3\n9.8.7"`, `"1.2.3\n"`, `"1.2.3;id"`, `["1.2.3"]`, `{version="1.2.3"}`, `true`, `123`, `"{{env.GO_VERSION}}"`} {
			values = append(values, "[tools]\ngo="+literal+"\n")
		}
		for _, v := range values {
			if v == "<missing>" {
				os.Remove(config)
			} else {
				write(t, config, v)
			}
			for _, s := range []string{"go-version.sh", "build.sh", "test.sh", "../../tests/check.sh"} {
				r := bad(t, script(s))
				equal(t, r.out, "")
				contains(t, r.err, "dotfiles/common/mise/config.toml")
				contains(t, r.err, "tools.go")
				excludes(t, r.err, "private-parser-diagnostic")
			}
			for _, s := range steps {
				r := bad(t, runStep(s))
				contains(t, r.err, "tools.go")
				equal(t, r.out, "")
				excludes(t, r.err, "private-parser-diagnostic")
			}
			if exists(marker) || exists(filepath.Join(f.root, ".system-sync")) {
				t.Fatal("invalid config queried tool or wrote state")
			}
		}
	case "test_missing_mise_reports_the_parser_requirement_before_build_or_test":
		os.Remove(filepath.Join(f.bin, "mise"))
		for _, s := range []string{"go-version.sh", "build.sh", "test.sh", "../../tests/check.sh"} {
			r := bad(t, script(s))
			equal(t, r.out, "")
			contains(t, r.err, "tools.go 需要 mise")
			contains(t, r.err, "PATH")
		}
		if exists(filepath.Join(f.root, ".system-sync")) {
			t.Fatal("missing parser wrote state")
		}
	case "test_changed_go_controls_build_and_test_mismatch_diagnostics":
		write(t, config, "[tools]\ngo=\"9.8.7\"\n")
		f.env["CI_GO_MODE"] = "zero"
		f.mock("ci", "go")
		for _, v := range variants {
			r := bad(t, script(v.file, v.args...))
			contains(t, r.err, "Go 9.8.7，当前为 go0.0.0")
			contains(t, r.err, "mise install go@9.8.7")
			contains(t, r.err, "mise run --tool go@9.8.7 "+v.task)
			if exists(filepath.Join(f.root, ".system-sync")) {
				t.Fatal("mismatched version wrote state")
			}
		}
	case "test_workflow_steps_read_changed_go_for_every_go_invocation":
		write(t, config, "[tools]\ngo=\"9.8.7\"\n")
		log := filepath.Join(f.base, "mise.log")
		f.env["CI_MISE_LOG"] = log
		os.Remove(filepath.Join(f.bin, "mise"))
		f.mock("ci", "mise")
		expected := [][]string{{"trust --all", "install go@9.8.7", "exec go@9.8.7 -- go version"}, {"run --tool go@9.8.7 check --all-platforms"}, {"trust --all", "install go@9.8.7", "exec go@9.8.7 -- ./helper/scripts/build.sh --dist"}}
		for i, s := range steps {
			os.Remove(log)
			good(t, runStep(s))
			equal(t, strings.Split(strings.TrimSpace(read(t, log)), "\n"), expected[i])
		}
	default:
		t.Fatal("unported", name)
	}
}
