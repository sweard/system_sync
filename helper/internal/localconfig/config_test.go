package localconfig

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRealPathFollowsBrokenRepoLinkAndRejectsCycle(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "missing")
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	got, err := RealPath(link)
	want, _ := filepath.EvalSymlinks(dir)
	if err != nil || got != filepath.Join(want, "missing") {
		t.Fatalf("broken link resolved to %q: %v", got, err)
	}
	_ = os.Remove(link)
	_ = os.Symlink(link, link)
	if _, err := RealPath(link); err == nil {
		t.Fatal("link cycle accepted")
	}
}
func TestTOMLValidationAndLocalOnlyScope(t *testing.T) {
	if _, err := Decode([]byte("[dotfiles]\n\"/private\"={mode=\"track-local\",autosave=false}\n[dotfiles]\n")); err == nil {
		t.Fatal("duplicate TOML accepted")
	}
	for _, text := range []string{"[dotfiles]\n\"/private\"={mode=\"track\",autosave=false}\n", "[dotfiles]\n\"/private\"={mode=\"track-local\",autosave=true}\n", "[dotfiles]\n\"/unknown\"={mode=\"track-local\",autosave=false}\n"} {
		data, err := Decode([]byte(text))
		if err != nil {
			t.Fatal(err)
		}
		if _, valid := LocalEntries(data, []string{"/private"}); valid {
			t.Fatal("unsafe local history accepted")
		}
	}
}
