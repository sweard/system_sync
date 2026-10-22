package configops

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/sweard/system_sync/helper/internal/syncstate"
	"github.com/sweard/system_sync/helper/internal/taskexec"
)

// Sync is the everyday transaction. Its caller holds the shared operation lock.
// Machine setup uses the same Backup and Apply methods inside its staged journal.
func (e Engine) Sync(p *Plan, out io.Writer) (directory string, err error) {
	if len(p.Pending) > 0 {
		return "", fmt.Errorf("配置仍有待处理项，未应用配置")
	}
	if !p.Changed && p.Diff == "" {
		return "", nil
	}
	parent, err := syncstate.LocalDirectory(e.Root, "history")
	if err != nil {
		return "", err
	}
	directory, err = os.MkdirTemp(parent, time.Now().UTC().Format("20060102T150405Z-")+"config-sync-")
	if err != nil {
		return "", err
	}
	defer func() {
		if err != nil {
			_ = syncstate.Journal(directory, "needs-review", nil)
			err = fmt.Errorf("%w；请核对配置和本机记录：%s", err, directory)
		}
	}()
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return directory, err
	}
	if err = os.WriteFile(filepath.Join(directory, "plan.json"), data, 0600); err != nil {
		return directory, err
	}
	err = taskexec.Progress(out, "复核并备份受管配置", func() error { return e.Backup(p, directory) })
	if err != nil {
		return directory, err
	}
	if err = syncstate.Journal(directory, "applying", nil); err != nil {
		return directory, err
	}
	if err = e.Apply(p, out); err != nil {
		return directory, err
	}
	err = syncstate.Journal(directory, "complete", nil)
	return directory, err
}
