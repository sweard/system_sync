package checkrun

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCollectRequiresEveryExpectedResult(t *testing.T) {
	for _, input := range []string{"", `{"Action":"pass","Test":"TestIntegration/parallel/a","Elapsed":0.1}` + "\n", `not-json` + "\n"} {
		if _, e := Collect(strings.NewReader(input), map[string]bool{"a": true, "b": true}, io.Discard); e == nil {
			t.Fatal("incomplete or malformed result accepted")
		}
	}
}
func TestCollectRejectsDuplicateUnexpectedFailedAndSkippedCases(t *testing.T) {
	event := func(id, action string) string {
		b, _ := json.Marshal(goEvent{Action: action, Test: "TestIntegration/parallel/" + id, Elapsed: 0.1})
		return string(b) + "\n"
	}
	for _, s := range []string{event("a", "pass") + event("a", "pass"), event("foreign", "pass"), event("a", "fail"), event("a", "skip")} {
		if _, e := Collect(strings.NewReader(s), map[string]bool{"a": true}, io.Discard); e == nil {
			t.Fatal("invalid outcome accepted", s)
		}
	}
	records, e := Collect(strings.NewReader(event("a", "pass")), map[string]bool{"a": true}, io.Discard)
	if e != nil || len(records) != 1 || records[0].Outcome != "passed" {
		t.Fatal(records, e)
	}
}
func TestFailedStageStopsAndSavesExitCode(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "tests"), 0755)
	Save(filepath.Join(root, "tests/coverage.json"), []Case{{ID: "fixture", Platforms: []string{"common"}}})
	path := filepath.Join(root, "report.json")
	var out bytes.Buffer
	code := Run(context.Background(), Config{Root: root, All: true, Jobs: 1, ReportPath: path, Out: &out, Err: &out, Stages: []Command{{Name: "failed", Args: []string{"/bin/sh", "-c", "exit 7"}}, {Name: "next", Args: []string{"/bin/false"}}}})
	if code != 7 {
		t.Fatal(code, out.String())
	}
	var r Report
	b, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(b, &r); e != nil || r.ExitCode != 7 || len(r.Stages) != 1 {
		t.Fatal(r, e)
	}
}
func TestCancelledCommandKillsProcessGroup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	root := t.TempDir()
	path := filepath.Join(root, "pid")
	done := make(chan int, 1)
	go func() {
		done <- Execute(ctx, root, []string{"/bin/sh", "-c", "echo $$ > '" + path + "'; exec /bin/sleep 30"}, os.Environ(), io.Discard, io.Discard)
	}()
	for deadline := time.Now().Add(5 * time.Second); ; {
		if _, e := os.Stat(path); e == nil {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("child never started")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	if code := <-done; code != 130 {
		t.Fatal(code)
	}
}
func TestTargetAndProfileAreHostDriven(t *testing.T) {
	for _, os := range []string{"darwin", "linux"} {
		for _, arch := range []string{"arm64", "amd64"} {
			if target, e := Target(os, arch); e != nil || target != os+"_"+arch {
				t.Fatal(target, e)
			}
		}
	}
	if _, e := Target("windows", "arm64"); e == nil {
		t.Fatal("unsupported target")
	}
	if _, e := Target("linux", "386"); e == nil {
		t.Fatal("unsupported architecture")
	}
	for _, p := range []string{"arch", "debian", "fedora", "opensuse"} {
		if !Matches(Case{Platforms: []string{"linux"}}, p) || Matches(Case{Platforms: []string{"macos"}}, p) {
			t.Fatal("wrong profile", p)
		}
	}
}
func TestResultDirectorySymlinkIsRejected(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if e := os.Symlink(outside, filepath.Join(root, ".system-sync")); e != nil {
		t.Fatal(e)
	}
	code := Run(context.Background(), Config{Root: root, All: true, Jobs: 1, Out: io.Discard, Err: io.Discard})
	if code != 2 {
		t.Fatal("linked state accepted", code)
	}
	entries, _ := os.ReadDir(outside)
	if len(entries) != 0 {
		t.Fatal("linked state modified")
	}
}
