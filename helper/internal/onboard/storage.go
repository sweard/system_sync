package onboard

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/sweard/system_sync/helper/internal/syncstate"
)

func (c *Capture) Save(root, kind string) (directory string, err error) {
	parent, err := syncstate.LocalDirectory(root, kind)
	if err != nil {
		return "", err
	}
	directory, err = os.MkdirTemp(parent, time.Now().UTC().Format("20060102T150405Z-")+c.Profile+"-")
	if err != nil {
		return "", err
	}
	staging := directory
	defer func() {
		if err != nil {
			_ = os.RemoveAll(staging)
		}
	}()
	data, err := jsonBytes(c)
	if err != nil {
		return "", err
	}
	if err = os.WriteFile(filepath.Join(directory, "capture.json"), data, 0600); err != nil {
		return "", err
	}
	filename := "mise.toml.candidate"
	if c.Profile == "macos" {
		filename = "Brewfile.candidate"
	}
	if err = os.WriteFile(filepath.Join(directory, filename), []byte(c.Candidate.Content), 0600); err != nil {
		return "", err
	}
	data, err = jsonBytes(c.Sources)
	if err != nil {
		return "", err
	}
	err = os.WriteFile(filepath.Join(directory, "sources.before.json"), data, 0600)
	return directory, err
}

func unchanged(sources map[string]string) error {
	for path, before := range sources {
		after, err := readManifest(path)
		if err != nil || after != before {
			return fmt.Errorf("预览后清单发生变化，请重新运行：%s", path)
		}
	}
	return nil
}
