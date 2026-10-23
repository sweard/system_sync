package extensions

import (
	"bytes"
	"strings"
	"testing"
)

func TestVersionComparisonUsesNumericAndPrereleasePrecedence(t *testing.T) {
	ordered := []string{"1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-beta", "1.0.0-beta.2", "1.0.0-beta.11", "1.0.0-rc.1", "1.0.0", "1.0.1", "1.9.0", "1.10.0", "1.999999999999999999999999.0", "2.1.304", "2.1.309", "3.0.0"}
	for i, a := range ordered {
		for j, b := range ordered {
			expected := 0
			if i < j {
				expected = -1
			} else if i > j {
				expected = 1
			}
			if order := compareVersions(a, b); order != expected {
				t.Fatalf("compare(%q, %q) = %d, expected %d", a, b, order, expected)
			}
		}
	}
}

func TestSyncWarnsAboutDowngradesBeforeInstallingButKeepsManifestTarget(t *testing.T) {
	for _, action := range []string{"status", "preview", "sync"} {
		for _, current := range []string{"", "1.2.2", "1.2.3-beta.2", "1.2.3", "1.2.4", "1.10.0", "2.1.309"} {
			t.Run(action+"/"+current, func(t *testing.T) {
				_, options, fake := captureFixture(t, "[common]\n\"example.shared\" = \"1.2.3\"\n")
				fake.current = map[string]string{"extra.local": "4.5.6"}
				if current != "" {
					fake.current["example.shared"] = current
				}
				options.Action = action
				if action == "preview" {
					options.Action, options.DryRun = "sync", true
				}
				var out bytes.Buffer
				if err := Run(options.Options, fake, &out); err != nil {
					t.Fatal(err)
				}
				text := out.String()
				downgrade := current == "1.2.4" || current == "1.10.0" || current == "2.1.309"
				if strings.Contains(text, "（降级/回滚至清单固定版本）") != downgrade || strings.Contains(text, "mise run vscode-status --capture") != downgrade {
					t.Fatal("incorrect rollback warning", text)
				}
				if action == "sync" {
					if fake.current["example.shared"] != "1.2.3" || fake.current["extra.local"] != "4.5.6" {
						t.Fatal("warning changed synchronization target or extra extension", fake.current)
					}
					if downgrade && strings.Index(text, "（降级/回滚至清单固定版本）") > strings.Index(text, "安装 VS Code 扩展") {
						t.Fatal("rollback warning appeared after installation", text)
					}
				} else {
					assertCaptureQueriesOnly(t, fake)
				}
			})
		}
	}
}
