package packagestate

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func decode(t *testing.T, text string) any {
	t.Helper()
	var data any
	if err := json.Unmarshal([]byte(text), &data); err != nil {
		t.Fatal(err)
	}
	return data
}

func TestStatusContractsAndDeduplication(t *testing.T) {
	for _, manager := range []string{"apt", "dnf", "pacman", "aur", "zypper"} {
		data := decode(t, `{"`+manager+`":{"available":true,"packages":[{"package":"git","state":"installed"},{"package":"git","state":"installed"}]}}`)
		rows, err := ParseStatus(data, []string{manager})
		if err != nil || !reflect.DeepEqual(rows, []Entry{{manager, "git", "installed"}}) {
			t.Fatalf("%s: %v %v", manager, rows, err)
		}
	}
	for _, text := range []string{`{"apt":[{"name":"git","state":"installed"}]}`, `{"packages":[{"manager":"apt","name":"git","state":"installed"}]}`, `[{"manager":"apt","name":"git","state":"installed"}]`} {
		rows, err := ParseStatus(decode(t, text), []string{"apt"})
		if err != nil || len(rows) != 1 {
			t.Fatalf("%s: %v", text, err)
		}
	}
	rows, err := ParseStatus(decode(t, `{"pacman":["zsh","git"],"aur":["git"]}`), []string{"pacman", "aur", "pacman"})
	if err != nil || !reflect.DeepEqual(rows, []Entry{{"aur", "git", "unknown"}, {"pacman", "git", "unknown"}, {"pacman", "zsh", "unknown"}}) {
		t.Fatalf("%v %v", rows, err)
	}
}

func TestStatusRejectsUnsafeIncompleteAndAbsentDeclarations(t *testing.T) {
	for _, text := range []string{`{}`, `[]`, `null`, `{"apt":{"available":false,"packages":["git"]}}`, `{"apt":{"packages":[]}}`, `{"apt":{"items":[]}}`, `{"apt":{"packages":{}}}`, `{"packages":["git"]}`} {
		if rows, err := ParseStatus(decode(t, text), []string{"apt"}); err == nil {
			t.Errorf("accepted %s as %v", text, rows)
		}
	}
	for _, name := range []string{"--help", "git;id", "../git", "git\nbase", "git*", ""} {
		data := map[string]any{"apt": []any{map[string]any{"package": name, "state": "installed"}}}
		if _, err := ParseStatus(data, []string{"apt"}); err == nil {
			t.Errorf("accepted name %q", name)
		}
	}
	for _, state := range []any{"unknown", "skipped", "absent", "unexpectedly_installed", "future-state", nil, []any{}, map[string]any{}} {
		data := map[string]any{"apt": []any{map[string]any{"package": "git", "state": state}}}
		if _, err := ParseStatus(data, []string{"apt"}); err == nil {
			t.Errorf("accepted state %#v", state)
		}
	}
	if _, err := ParseStatus(decode(t, `{"apt":[{"package":"git","state":"installed","desired_state":"absent"}]}`), []string{"apt"}); err == nil {
		t.Fatal("accepted absent")
	}
	if _, err := ParseStatus(decode(t, `{"apt":["git"]}`), []string{"--apt"}); err == nil {
		t.Fatal("accepted unsafe manager")
	}
}

func TestJSONRequiresExactlyOneCompleteDocument(t *testing.T) {
	for _, text := range []string{`{} {}`, `{} trailing`, `{"apt":`, ""} {
		var data any
		if DecodeJSON(strings.NewReader(text), &data) == nil {
			t.Errorf("accepted %q", text)
		}
	}
}

func TestZypperNamespacesKindsAndRemovalScope(t *testing.T) {
	cases := []struct {
		mode, text string
		wanted     []string
	}{
		{"solvables", `<stream xmlns="fixture"><solvable name="git"/><solvable name="desktop" kind="pattern"/></stream>`, []string{"git"}},
		{"to-remove", `<stream><to-install><solvable name="keep"/></to-install><to-remove><solvable><name>extra</name><kind>package</kind></solvable><solvable name="extra"/></to-remove></stream>`, []string{"extra"}},
		{"to-remove", `<stream><to-install><solvable name="keep"/></to-install></stream>`, []string{}},
	}
	for _, c := range cases {
		names, err := ParseZypper(strings.NewReader(c.text), c.mode)
		if err != nil || !reflect.DeepEqual(names, c.wanted) {
			t.Errorf("%s %s: %v %v", c.mode, c.text, names, err)
		}
	}
}

func TestZypperRejectsMalformedOrUnsafeTransactionsWithoutPartialResults(t *testing.T) {
	for _, text := range []string{`<broken>`, `<result/>`, `<stream/>junk`, `<stream/><stream/>`, `<stream><to-remove><solvable name="git"/><solvable name="desktop" kind="pattern"/></to-remove></stream>`, `<stream><to-remove><solvable name="--help"/></to-remove></stream>`, `<stream><to-remove><solvable/></to-remove></stream>`, `<stream><solvable name="git"></stream>`} {
		if names, err := ParseZypper(strings.NewReader(text), "to-remove"); err == nil || names != nil {
			t.Errorf("accepted %q: %v %v", text, names, err)
		}
	}
}
