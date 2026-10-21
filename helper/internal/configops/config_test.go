package configops

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fixtureRunner struct {
	rows                   []any
	diff                   string
	applyError, leaveDrift bool
	applied                []string
}

func (r *fixtureRunner) Run(command []string, capture bool) (string, error) {
	switch command[3] {
	case "status":
		data, _ := json.Marshal(map[string]any{"files": r.rows, "edits": []any{}})
		return string(data), nil
	case "diff":
		return r.diff, nil
	case "apply":
		r.applied = append([]string{}, command[5:]...)
		if r.applyError {
			return "", fmt.Errorf("fixture apply failed")
		}
		if !r.leaveDrift {
			for _, value := range r.rows {
				value.(map[string]any)["state"] = "applied"
			}
			r.diff = ""
		}
		return "", nil
	}
	return "", fmt.Errorf("unexpected command: %v", command)
}

func fixture(t *testing.T) (Engine, *fixtureRunner, string) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root, home := filepath.Join(base, "repo"), filepath.Join(base, "home")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(home, ".example")
	if err := os.WriteFile(target, []byte("original-private-value"), 0600); err != nil {
		t.Fatal(err)
	}
	r := &fixtureRunner{rows: []any{map[string]any{
		"target": target, "mode": "copy", "state": "differs",
		"origin": map[string]any{"config": filepath.Join(root, "mise.toml")},
	}}, diff: "--- target\n+++ source\n+shared-value\n"}
	return Engine{Root: root, Home: home, Runner: r}, r, target
}

func phase(t *testing.T, directory string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(directory, "transaction.json"))
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err = json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	return value["phase"].(string)
}

func TestSyncBacksUpExactTargetsAndVerifiesResult(t *testing.T) {
	e, r, target := fixture(t)
	p, err := e.Plan(nil)
	if err != nil {
		t.Fatal(err)
	}
	directory, err := e.Sync(p, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.applied) != 1 || r.applied[0] != target {
		t.Fatal(r.applied)
	}
	for _, name := range []string{"config.before.tar", "plan.json", "transaction.json"} {
		info, err := os.Stat(filepath.Join(directory, name))
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("%s: %v %v", name, info, err)
		}
	}
	if got := phase(t, directory); got != "complete" {
		t.Fatal(got)
	}
}

func TestApplyFailureAndPostApplyDriftKeepBackupAndJournal(t *testing.T) {
	for _, leaveDrift := range []bool{false, true} {
		t.Run(fmt.Sprint(leaveDrift), func(t *testing.T) {
			e, r, _ := fixture(t)
			r.applyError, r.leaveDrift = !leaveDrift, leaveDrift
			p, err := e.Plan(nil)
			if err != nil {
				t.Fatal(err)
			}
			directory, err := e.Sync(p, io.Discard)
			if err == nil {
				t.Fatal("failure accepted")
			}
			if _, err := os.Stat(filepath.Join(directory, "config.before.tar")); err != nil {
				t.Fatal(err)
			}
			if got := phase(t, directory); got != "needs-review" {
				t.Fatal(got)
			}
		})
	}
}

func TestNoChangeCreatesNoHistoryAndChangedPlanStopsBeforeApply(t *testing.T) {
	e, r, _ := fixture(t)
	r.rows[0].(map[string]any)["state"] = "applied"
	r.diff = ""
	p, err := e.Plan(nil)
	if err != nil {
		t.Fatal(err)
	}
	directory, err := e.Sync(p, io.Discard)
	if err != nil || directory != "" || len(r.applied) != 0 {
		t.Fatal(directory, err, r.applied)
	}
	if _, err := os.Stat(filepath.Join(e.Root, ".system-sync")); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	r.rows[0].(map[string]any)["state"] = "differs"
	p, err = e.Plan(nil)
	if err != nil {
		t.Fatal(err)
	}
	r.diff = "+changed-after-preview\n"
	if _, err := e.Sync(p, io.Discard); err == nil || !strings.Contains(err.Error(), "状态已改变") {
		t.Fatal(err)
	}
	if len(r.applied) != 0 {
		t.Fatal(r.applied)
	}
}

func TestInitializationPolicyKeepsTakeoverAndDeletionGuards(t *testing.T) {
	for _, mode := range []string{"symlink", "absent"} {
		t.Run(mode, func(t *testing.T) {
			e, r, _ := fixture(t)
			r.rows[0].(map[string]any)["mode"] = mode
			daily, err := e.Plan(nil)
			if err != nil || len(daily.Pending) != 0 {
				t.Fatal(daily, err)
			}
			e.Initializing = true
			initial, err := e.Plan(nil)
			if err != nil || len(initial.Pending) == 0 {
				t.Fatal(initial, err)
			}
			if err = e.Apply(initial, io.Discard); err == nil || len(r.applied) != 0 {
				t.Fatal(err, r.applied)
			}
		})
	}
}

func TestRepositoryScopeAndLocalHistoryCollisionRules(t *testing.T) {
	e, r, target := fixture(t)
	other := filepath.Join(e.Home, ".other")
	r.rows = append(r.rows, map[string]any{"target": other, "mode": "copy", "state": "missing", "origin": map[string]any{"config": filepath.Join(e.Home, "global.toml")}})
	r.rows = append(r.rows, map[string]any{"target": target, "mode": "track-local", "state": "tracked", "origin": map[string]any{"config": filepath.Join(e.Home, "history.toml")}})
	p, err := e.Plan([]string{target})
	if err != nil || len(p.Pending) != 0 || len(p.Targets) != 1 {
		t.Fatal(p, err)
	}
	if _, err := e.Plan([]string{other}); err == nil {
		t.Fatal("foreign target selected")
	}
	r.rows[1].(map[string]any)["target"] = target
	p, err = e.Plan(nil)
	if err != nil || len(p.Pending) == 0 {
		t.Fatal(p, err)
	}
	if err = e.Apply(p, io.Discard); err == nil || len(r.applied) != 0 {
		t.Fatal(err, r.applied)
	}
}
