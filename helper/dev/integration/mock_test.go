//go:build integration

package integration

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func mockLoad(path string) map[string]any {
	b, e := os.ReadFile(path)
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(90)
	}
	var m map[string]any
	if e = json.Unmarshal(b, &m); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(90)
	}
	return m
}
func mockSave(p string, m any) {
	b, _ := json.Marshal(m)
	if e := os.WriteFile(p, b, 0600); e != nil {
		panic(e)
	}
}
func mockLog(p string, v any) {
	if p == "" {
		return
	}
	b, _ := json.Marshal(v)
	f, e := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if e != nil {
		panic(e)
	}
	defer f.Close()
	f.Write(append(b, '\n'))
}
func list(v any) []string {
	out := []string{}
	if a, ok := v.([]any); ok {
		for _, x := range a {
			out = append(out, x.(string))
		}
	}
	if a, ok := v.([]string); ok {
		return a
	}
	return out
}
func has(a []string, s string) bool {
	for _, x := range a {
		if x == s {
			return true
		}
	}
	return false
}
func index(a []string, s string) int {
	for i, x := range a {
		if x == s {
			return i
		}
	}
	return -1
}
func setunion(a, b []string) []string {
	out := append([]string{}, a...)
	for _, s := range b {
		if !has(out, s) {
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}
func difference(a, b []string) []string {
	out := []string{}
	for _, s := range a {
		if !has(b, s) {
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}
func printLines(a []string) {
	sort.Strings(a)
	for _, s := range a {
		fmt.Println(s)
	}
}
func truth(m map[string]any, k string) bool { return m[k] == true }
func str(m map[string]any, k, def string) string {
	if v, ok := m[k].(string); ok {
		return v
	}
	return def
}
func mockXML(a []string, group string) {
	fmt.Print("<stream><" + group + ">")
	for _, s := range a {
		fmt.Printf("<solvable name=%q kind=\"package\"/>", s)
	}
	fmt.Println("</" + group + "></stream>")
}
func mockInput(want string) bool {
	s := bufio.NewScanner(os.Stdin)
	return s.Scan() && s.Text() == want
}
func mockCommand(mode, name string, args []string) int {
	switch mode {
	case "workflows":
		return mockWorkflows(name, args)
	case "clean":
		return mockClean(args)
	case "converge":
		return mockConverge(name, args)
	case "vscode":
		return mockVSCode(args)
	case "brew":
		return mockBrew(name, args)
	case "onboard":
		return mockOnboard(name, args)
	case "audit":
		return mockAudit(name, args)
	case "feedback":
		return mockFeedback(name, args)
	case "ci":
		return mockCI(name, args)
	}
	fmt.Fprintln(os.Stderr, "unknown fixture mode:", mode, name, args)
	return 91
}
func mockConverge(name string, args []string) int {
	statePath := os.Getenv("FIXTURE_STATE")
	s := mockLoad(statePath)
	profile := str(s, "profile", "")
	manager := map[string]string{"arch": "pacman", "debian": "apt", "fedora": "dnf", "opensuse": "zypper"}[profile]
	mockLog(os.Getenv("FIXTURE_ALL_QUERIES"), append([]string{name}, args...))
	orphans := func() []string {
		key := "initial_orphans"
		if truth(s, "marked") || (profile == "opensuse" && truth(s, "did_install")) {
			key = "final_orphans"
		}
		return list(s[key])
	}
	mutate := func(kind string, installed, explicit []string) bool {
		dir := os.Getenv("SYSTEM_SYNC_RUN_LOG_DIR")
		if !exists(filepath.Join(dir, "installed.before.txt")) {
			fmt.Fprintln(os.Stderr, "mutation happened before installed-state audit")
			return false
		}
		j := mockLoad(filepath.Join(dir, "transaction.json"))
		mockLog(os.Getenv("FIXTURE_LOG"), map[string]any{"kind": kind, "args": append([]string{name}, args...), "phase": j["phase"]})
		if installed != nil {
			s["installed"] = installed
		}
		if explicit != nil {
			s["explicit"] = explicit
		}
		mockSave(statePath, s)
		return true
	}
	mark := func(dep bool, names []string) int {
		explicit := setunion(list(s["explicit"]), names)
		if dep {
			explicit = difference(list(s["explicit"]), names)
		}
		s["marked"] = true
		if !mutate("mark", nil, explicit) {
			return 92
		}
		return 0
	}
	remove := func() int {
		names := orphans()
		if name == "zypper" {
			names = []string{}
			for _, a := range args[index(args, "remove")+1:] {
				if !strings.HasPrefix(a, "-") {
					names = append(names, a)
				}
			}
		}
		if !mutate("remove", difference(list(s["installed"]), names), difference(list(s["explicit"]), names)) {
			return 92
		}
		return 0
	}
	switch name {
	case "uname":
		fmt.Println("Linux")
	case "sudo":
		if len(args) == 1 && args[0] == "-v" {
			return 0
		}
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if e := cmd.Run(); e != nil {
			if x, ok := e.(*exec.ExitError); ok {
				return x.ExitCode()
			}
			return 93
		}
	case "mise":
		mockLog(os.Getenv("FIXTURE_QUERY_LOG"), args)
		if len(args) == 1 && args[0] == "--version" {
			fmt.Println(str(s, "mise_version", "2026.10.11 fixture"))
			return 0
		}
		if len(args) < 3 {
			return 91
		}
		if args[2] == "status" {
			if truth(s, "json_error") {
				fmt.Println(`{"changed_schema":true}`)
				return 0
			}
			packages := []any{}
			for _, n := range list(s["declared"]) {
				state := "missing"
				if has(list(s["installed"]), n) {
					state = "installed"
				}
				packages = append(packages, map[string]any{"package": n, "state": str(s, "override_state", state)})
			}
			b, _ := json.Marshal(map[string]any{manager: map[string]any{"available": !truth(s, "unavailable"), "packages": packages}})
			fmt.Println(string(b))
			return 0
		}
		if args[2] == "apply" {
			if has(args, "--dry-run") {
				fmt.Println("fixture install preview")
				return 0
			}
			if truth(s, "live_progress") {
				if !has(args, "--raw") {
					return 94
				}
				fmt.Println("Fixture: native download")
				if !mockInput("download-seen") {
					return 94
				}
				fmt.Fprintln(os.Stderr, "Fixture: native install")
				if !mockInput("install-seen") {
					return 94
				}
			}
			s["did_install"] = true
			installed := setunion(list(s["installed"]), list(s["declared"]))
			if truth(s, "wrong_install_state") {
				installed = list(s["installed"])
			}
			if !mutate("install", installed, setunion(list(s["explicit"]), difference(installed, difference(installed, list(s["declared"]))))) {
				return 92
			}
			if truth(s, "install_failure") {
				return 18
			}
			return 0
		}
		return 91
	case "dpkg-query":
		for _, n := range list(s["installed"]) {
			fmt.Println("ii \t" + n)
		}
	case "apt-mark":
		if has(args, "showmanual") {
			printLines(list(s["explicit"]))
		} else if has(args, "showauto") {
			printLines(difference(list(s["installed"]), list(s["explicit"])))
		} else {
			return mark(args[0] == "auto", args[1:])
		}
	case "apt-get":
		if has(args, "-s") {
			if truth(s, "query_failure") {
				return 12
			}
			for _, n := range orphans() {
				fmt.Println("Remv " + n + " [1.0]")
			}
		} else {
			return remove()
		}
	case "dnf":
		if has(args, "--help") {
			if truth(s, "dnf4") && has(args, "dependency") {
				return 2
			}
		} else if has(args, "repoquery") {
			if truth(s, "query_failure") {
				return 12
			}
			if has(args, "--userinstalled") {
				printLines(list(s["explicit"]))
			} else {
				printLines(orphans())
			}
		} else if i := index(args, "mark"); i >= 0 {
			return mark(args[i+1] == "dependency" || args[i+1] == "remove", args[i+2:])
		} else if has(args, "autoremove") {
			return remove()
		} else {
			return 91
		}
	case "pacman":
		switch args[0] {
		case "-Qqe":
			printLines(list(s["explicit"]))
		case "-Qq":
			printLines(list(s["installed"]))
		case "-Qqd":
			printLines(difference(list(s["installed"]), list(s["explicit"])))
		case "-Qdtq":
			if truth(s, "query_failure") {
				fmt.Fprintln(os.Stderr, "query failed")
				return 1
			}
			printLines(orphans())
			if len(orphans()) == 0 {
				return 1
			}
		case "-D":
			return mark(args[1] == "--asdeps", args[2:])
		case "-Rsp":
			v := orphans()
			if x, ok := s["transaction"]; ok {
				v = list(x)
			}
			printLines(v)
		case "-Rs":
			return remove()
		default:
			return 91
		}
	case "rpm":
		printLines(list(s["installed"]))
	case "zypper":
		if has(args, "packages") {
			if truth(s, "query_failure") {
				return 12
			}
			if truth(s, "xml_error") {
				fmt.Println("<broken>")
			} else if has(args, "--userinstalled") {
				mockXML(list(s["explicit"]), "solvable-list")
			} else {
				mockXML(orphans(), "solvable-list")
			}
		} else if has(args, "--dry-run") {
			names := []string{}
			for _, a := range args[index(args, "remove")+1:] {
				if !strings.HasPrefix(a, "-") {
					names = append(names, a)
				}
			}
			if x, ok := s["transaction"]; ok {
				names = list(x)
			}
			if truth(s, "did_install") {
				if x, ok := s["final_transaction"]; ok {
					names = list(x)
				}
			}
			mockXML(names, "to-remove")
		} else if has(args, "remove") {
			return remove()
		} else {
			return 91
		}
	default:
		return 91
	}
	return 0
}
func mockVSCode(args []string) int {
	path := os.Getenv("VSCODE_TEST_STATE")
	s := mockLoad(path)
	base := filepath.Dir(path)
	mockLog(os.Getenv("VSCODE_TEST_LOG"), args)
	if i := index(args, "--install-extension"); i >= 0 {
		if exists(filepath.Join(base, "fail-install")) {
			return 7
		}
		if exists(filepath.Join(base, "wait-install")) {
			fmt.Println("private-install-token")
			deadline := time.Now().Add(5 * time.Second)
			for !exists(filepath.Join(base, "release-install")) {
				if time.Now().After(deadline) {
					return 98
				}
				time.Sleep(10 * time.Millisecond)
			}
		}
		id, v, _ := strings.Cut(args[i+1], "@")
		if !exists(filepath.Join(base, "wrong-version")) {
			s[id] = v
		}
		mockSave(path, s)
	} else if exists(filepath.Join(base, "bad-list")) {
		fmt.Println("unknown output")
	} else {
		names := []string{}
		for k := range s {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, k := range names {
			fmt.Printf("%s@%v\n", k, s[k])
		}
	}
	return 0
}
func mockBrew(name string, args []string) int {
	if name == "uname" {
		fmt.Println("Darwin")
		return 0
	}
	if name == "mise" {
		if p := os.Getenv("SYNC_TEST_VERSION_LOG"); p != "" {
			f, _ := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
			fmt.Fprintln(f, strings.Join(args, " "))
			f.Close()
		}
		v := os.Getenv("SYNC_TEST_MISE_VERSION")
		if v == "" {
			v = "2026.10.11 fixture"
		}
		fmt.Println(v)
		return 0
	}
	config := os.Getenv("XDG_CONFIG_HOME")
	real := os.Getenv("SYNC_TEST_REAL_CONFIG")
	joined := strings.Join(args, " ")
	p := os.Getenv("SYNC_TEST_LOG")
	log, _ := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	fmt.Fprintln(log, config+"|"+joined)
	log.Close()
	flag := func(k string) bool { return os.Getenv(k) == "1" }
	if len(args) == 0 {
		return 99
	}
	switch args[0] {
	case "list":
		if has(args, "--formula") {
			fmt.Println("git\nmise\nfixture/tools/tool")
		} else if flag("SYNC_TEST_INSTALLED") {
			fmt.Println("tinycast")
		}
	case "tap":
		fmt.Println("fixture/tools\nabue-ammar/tinycast")
	case "trust":
		if config == "" || config == real {
			return 91
		}
		if args[1] == "--cask" && flag("SYNC_TEST_TRUST_ERROR") {
			return 92
		}
		dir := filepath.Join(config, "homebrew")
		os.MkdirAll(dir, 0700)
		f, _ := os.OpenFile(filepath.Join(dir, "trusted-items.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
		fmt.Fprintln(f, strings.Join(args[1:3], " "))
		f.Close()
	case "bundle":
		switch args[1] {
		case "list":
			if has(args, "--formula") {
				fmt.Println("git\nmise\nfixture/tools/tool")
			} else if has(args, "--cask") {
				fmt.Println("tinycast")
			} else if has(args, "--tap") {
				fmt.Println("fixture/tools\nabue-ammar/tinycast")
			} else {
				return 90
			}
		case "check":
			if flag("SYNC_TEST_INSTALLED") {
				fmt.Println("The Brewfile's dependencies are satisfied.")
			} else {
				fmt.Println("Cask tinycast needs to be installed.")
				return 1
			}
		case "install":
			dir := os.Getenv("SYSTEM_SYNC_RUN_LOG_DIR")
			if !exists(filepath.Join(dir, "Brewfile.before")) || !exists(filepath.Join(dir, "formulae.before.txt")) {
				return 94
			}
			if flag("SYNC_TEST_UPGRADE_PROGRESS") || flag("SYNC_TEST_INSTALL_PROGRESS") {
				if !has(args, "--verbose") {
					return 97
				}
				kind := "install"
				want := "--no-upgrade"
				if flag("SYNC_TEST_UPGRADE_PROGRESS") {
					kind = "upgrade"
					want = "--upgrade"
				}
				if !has(args, want) {
					return 97
				}
				fmt.Println("Fixture: downloading " + kind)
				if !mockInput("download-seen") {
					return 98
				}
				fmt.Fprintln(os.Stderr, "Fixture: installing "+kind)
				if !mockInput("install-seen") {
					return 98
				}
			}
			if flag("SYNC_TEST_INSTALL_ERROR") {
				fmt.Fprintln(os.Stderr, "Error: fixture installation failed.")
				return 95
			}
		case "cleanup":
			if has(args, "--force") {
				return 93
			}
			if config == real {
				if !exists(filepath.Join(os.Getenv("SYSTEM_SYNC_RUN_LOG_DIR"), "cleanup.after-install.preview.txt")) {
					return 96
				}
				return 0
			}
			b, _ := os.ReadFile(filepath.Join(config, "homebrew/trusted-items.txt"))
			if !strings.Contains(string(b), "--formula fixture/tools/tool") {
				fmt.Println("Error: Untrusted formula fixture/tools/tool.")
				return 1
			}
			if flag("SYNC_TEST_INSTALLED") && !strings.Contains(string(b), "--cask abue-ammar/tinycast/tinycast") {
				fmt.Println("Error: Refusing to load cask abue-ammar/tinycast/tinycast from untrusted tap.")
				return 1
			}
			if flag("SYNC_TEST_CLEANUP_ERROR") {
				fmt.Println("Error: Simulated cleanup failure.")
				return 1
			}
			if flag("SYNC_TEST_GRAPH_WARNING") {
				fmt.Println("Formulae dependency graph sorting found a circular dependency:\nWould uninstall formulae:\nold-fixture")
			}
		default:
			return 99
		}
	default:
		return 99
	}
	return 0
}
func mockClean(args []string) int {
	real := os.Getenv("CLEAN_TEST_REAL_RM")
	run := func(a []string) int {
		c := exec.Command(real, a...)
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		if e := c.Run(); e != nil {
			return 1
		}
		return 0
	}
	target := os.Getenv("CLEAN_TEST_TARGET")
	if len(args) != 3 || args[0] != "-rf" || args[1] != "--" || args[2] != target {
		return run(args)
	}
	counter := os.Getenv("CLEAN_TEST_COUNTER")
	attempt := 1
	if b, e := os.ReadFile(counter); e == nil {
		fmt.Sscanf(string(b), "%d", &attempt)
		attempt++
	}
	os.WriteFile(counter, []byte(fmt.Sprintf("%d\n", attempt)), 0600)
	mode := os.Getenv("CLEAN_TEST_RM_MODE")
	if mode != "always" && attempt > 1 {
		return run(args)
	}
	switch mode {
	case "permission":
		fmt.Fprintf(os.Stderr, "rm: %s: Permission denied\n", target)
		return 1
	case "mixed":
		fmt.Fprintf(os.Stderr, "rm: %s: Directory not empty\nrm: %s: Permission denied\n", target, target)
		return 1
	case "link":
		if run(args) != 0 {
			return 1
		}
		os.Symlink(os.Getenv("CLEAN_TEST_OUTSIDE"), target)
		fmt.Fprintf(os.Stderr, "rm: %s: Directory not empty\n", target)
		return 1
	case "success-recreated":
		if run(args) != 0 {
			return 1
		}
		os.MkdirAll(target, 0755)
		os.WriteFile(filepath.Join(target, ".DS_Store"), []byte("metadata"), 0600)
		return 0
	}
	os.Remove(filepath.Join(target, "cached-file"))
	os.WriteFile(filepath.Join(target, ".DS_Store"), []byte("metadata"), 0600)
	fmt.Fprintf(os.Stderr, "rm: %s: Directory not empty\n", target)
	return 1
}
func mockAudit(name string, args []string) int {
	s := mockLoad(os.Getenv("AUDIT_TEST_STATE"))
	command := append([]string{name}, args...)
	if name == "policy" {
		command = []string{"/bin/bash", "--noprofile"}
	}
	if name == "packages" {
		command = append([]string{s["status_script"].(string)}, args...)
	}
	env := map[string]string{}
	for _, row := range os.Environ() {
		k, v, _ := strings.Cut(row, "=")
		env[k] = v
	}
	mockLog(s["log"].(string), map[string]any{"command": command, "environment": env})
	for _, v := range s["failures"].([]any) {
		if strings.Join(list(v), "\x00") == strings.Join(command, "\x00") {
			fmt.Println("private-query-output")
			fmt.Fprintln(os.Stderr, "private-query-diagnostic")
			return 17
		}
	}
	switch {
	case name == "policy":
		fmt.Print(s["policy"])
	case name == "packages":
		fmt.Print(s["packages"])
	case len(args) > 0 && args[0] == "ls":
		b, _ := json.Marshal(s["tools"])
		fmt.Println(string(b))
	case len(args) > 0 && args[0] == "which":
		value, ok := s["paths"].(map[string]any)[args[len(args)-1]]
		if !ok {
			return 1
		}
		fmt.Println(value)
	case strings.Join(args, " ") == "bootstrap dotfiles status --json":
		b, _ := json.Marshal(s["config"])
		fmt.Println(string(b))
	default:
		return 91
	}
	return 0
}
func mockCI(name string, args []string) int {
	if name == "mise" && len(args) > 0 && args[0] == "config" {
		c := exec.Command(os.Getenv("REAL_MISE"), args...)
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		c.Stdin = os.Stdin
		if e := c.Run(); e != nil {
			if x, ok := e.(*exec.ExitError); ok {
				return x.ExitCode()
			}
			return 91
		}
		return 0
	}
	if p := os.Getenv("CI_MARKER"); p != "" {
		os.WriteFile(p, []byte("unsafe"), 0600)
		return 99
	}
	if name == "mise" {
		f, _ := os.OpenFile(os.Getenv("CI_MISE_LOG"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		fmt.Fprintln(f, strings.Join(args, " "))
		f.Close()
		return 0
	}
	switch os.Getenv("CI_GO_MODE") {
	case "version-query-failed":
		return 17
	case "zero":
		fmt.Println("go0.0.0")
	default:
		fmt.Println("go1.26.0")
	}
	return 0
}
func mockOnboard(name string, args []string) int {
	p := os.Getenv("ONBOARD_TEST_STATE")
	s := mockLoad(p)
	calls, _ := s["calls"].([]any)
	s["calls"] = append(calls, append([]string{name}, args...))
	mockSave(p, s)
	save := func() { mockSave(p, s) }
	if name == "uname" {
		if s["profile"] == "arch" {
			fmt.Println("Linux")
		} else {
			fmt.Println("Darwin")
		}
		return 0
	}
	if name == "mise" {
		if strings.Join(args, " ") == "--version" {
			fmt.Println("2026.10.15")
			return 0
		}
		if len(args) > 4 && args[0] == "-C" && strings.Join(args[len(args)-4:], " ") == "bootstrap packages status --json" {
			packages := []any{}
			for _, n := range []string{"git", "missing-tool"} {
				state := "missing"
				if has(list(s["installed"]), n) {
					state = "installed"
				}
				packages = append(packages, map[string]any{"package": n, "state": state})
			}
			b, _ := json.Marshal(map[string]any{"pacman": map[string]any{"available": true, "packages": packages}})
			fmt.Println(string(b))
			return 0
		}
	}
	if name == "brew" {
		if len(args) > 1 && args[0] == "list" {
			if args[1] == "--formula" {
				printLines(list(s["installed"]))
			}
			return 0
		}
		if len(args) > 1 && args[0] == "bundle" && args[1] == "dump" {
			for _, a := range args {
				if strings.HasPrefix(a, "--file=") {
					text := ""
					for _, n := range list(s["roots"]) {
						text += "brew \"" + n + "\"\n"
					}
					os.WriteFile(strings.TrimPrefix(a, "--file="), []byte(text), 0600)
				}
			}
			return 0
		}
		if len(args) > 1 && args[1] == "install" {
			if os.Getenv("HOMEBREW_NO_INSTALL_CLEANUP") != "1" || os.Getenv("HOMEBREW_NO_AUTOREMOVE") != "1" || !has(args, "--no-upgrade") || !has(args, "--verbose") {
				return 95
			}
			if truth(s, "live_progress") {
				fmt.Println("Fixture: onboard download")
				if !mockInput("download-seen") {
					return 96
				}
				fmt.Fprintln(os.Stderr, "Fixture: onboard install")
				if !mockInput("install-seen") {
					return 96
				}
			}
			candidate := ""
			for _, a := range args {
				if strings.HasPrefix(a, "--file=") {
					b, _ := os.ReadFile(strings.TrimPrefix(a, "--file="))
					candidate = string(b)
				}
			}
			if !strings.Contains(candidate, `brew "missing-tool"`) {
				return 95
			}
			s["installed"] = setunion(list(s["installed"]), []string{"missing-tool"})
			s["roots"] = setunion(list(s["roots"]), []string{"missing-tool"})
			mut, _ := s["mutations"].([]any)
			s["mutations"] = append(mut, append([]string{name}, args...))
			save()
			return 0
		}
	}
	if name == "pacman" {
		switch args[0] {
		case "-Qq":
			printLines(list(s["installed"]))
			return 0
		case "-Qqen":
			printLines(list(s["roots"]))
			return 0
		case "-Qqem", "-Qdtq":
			if truth(s, "query_failure") {
				fmt.Fprintln(os.Stderr, "database-error-fixture")
			}
			return 1
		}
	}
	if name == "sudo" {
		expected := []string{"pacman", "-S", "--needed", "--noconfirm", "--", "missing-tool"}
		if strings.Join(args, " ") != strings.Join(expected, " ") {
			return 95
		}
		s["installed"] = setunion(list(s["installed"]), []string{"missing-tool"})
		s["roots"] = setunion(list(s["roots"]), []string{"missing-tool"})
		mut, _ := s["mutations"].([]any)
		s["mutations"] = append(mut, append([]string{name}, args...))
		save()
		return 0
	}
	return 91
}
func mockWorkflows(name string, args []string) int {
	base := os.Getenv("WRITE_TEST_BASE")
	if name == "uname" {
		fmt.Println("Darwin")
		return 0
	}
	if name == "mise" {
		switch {
		case strings.Join(args, " ") == "--version":
			fmt.Println("2026.10.15 fixture")
		case strings.Join(args, " ") == "bootstrap dotfiles status --json":
			b, _ := os.ReadFile(filepath.Join(base, "dotfiles-status.json"))
			fmt.Print(string(b))
		case len(args) > 2 && args[0] == "dot" && args[1] == "save":
			os.WriteFile(filepath.Join(base, "saved-by-mise"), nil, 0600)
		default:
			return 91
		}
		return 0
	}
	if name == "brew" {
		if len(args) < 2 {
			return 92
		}
		switch strings.Join(args[:2], " ") {
		case "info --json=v2":
			fmt.Println(`{"formulae":[{"full_name":"gitui","aliases":[],"oldnames":[]}]}`)
		case "list --formula":
			b, _ := os.ReadFile(filepath.Join(base, "installed"))
			fmt.Print(string(b))
		case "install --formula":
			for _, k := range []string{"HOMEBREW_NO_AUTO_UPDATE", "HOMEBREW_NO_INSTALL_CLEANUP", "HOMEBREW_NO_AUTOREMOVE", "HOMEBREW_NO_INSTALL_UPGRADE"} {
				if os.Getenv(k) != "1" {
					return 98
				}
			}
			f, _ := os.OpenFile(filepath.Join(base, "installed"), os.O_APPEND|os.O_WRONLY, 0600)
			fmt.Fprintln(f, "gitui")
			f.Close()
		default:
			return 92
		}
		return 0
	}
	return 93
}
func printlnOut(v any) { fmt.Fprintln(os.Stdout, v) }
func printlnErr(v any) { fmt.Fprintln(os.Stderr, v) }
