package onboard

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"

	"github.com/sweard/system_sync/helper/internal/syncstate"
	"github.com/sweard/system_sync/helper/internal/taskexec"
)

// Merge writes a freshly revalidated capture after the caller has displayed its
// diff and received confirmation while holding the shared operation lock.
func (e Engine) Merge(ctx context.Context, c *Capture, out io.Writer) (directory string, err error) {
	if err = ctx.Err(); err != nil {
		return "", err
	}
	if c.Candidate.Diff == "" {
		return "", nil
	}
	if err = unchanged(c.Sources); err != nil {
		return "", err
	}
	err = taskexec.Progress(out, "复核合并前的软件状态与清单", func() error {
		again, err := e.Capture()
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(c, again) {
			return fmt.Errorf("确认后软件状态、来源或清单已改变，请重新预览；未合并清单")
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	path := filepath.Join(e.Root, c.Candidate.Path)
	before, exists := c.Sources[path]
	if !exists {
		return "", fmt.Errorf("合并目标不在已采集的清单中")
	}
	if err = mergeDirectory(path); err != nil {
		return "", err
	}
	if err = ctx.Err(); err != nil {
		return "", err
	}
	fmt.Fprintln(out, "[~] 保存本机快照、候选与原清单备份")
	directory, err = c.Save(e.Root, "capture")
	if err != nil {
		return "", err
	}
	defer func() {
		if err != nil {
			_ = syncstate.Journal(directory, "needs-review", nil)
			err = fmt.Errorf("%w；请检查清单及本机记录：%s", err, directory)
		}
	}()
	if err = os.WriteFile(filepath.Join(directory, filepath.Base(path)+".before"), []byte(before), 0600); err != nil {
		return directory, err
	}
	if err = syncstate.Journal(directory, "merging", map[string]any{"action": "package-capture-merge", "profile": e.Profile, "manifest": c.Candidate.Path}); err != nil {
		return directory, err
	}
	fmt.Fprintln(out, "[~] 原子合并已确认的根包声明")
	if err = replaceManifest(ctx, path, c); err != nil {
		return directory, err
	}
	after, err := readManifest(path)
	if err != nil {
		return directory, err
	}
	if after != c.Candidate.Content {
		return directory, fmt.Errorf("合并后的清单发生变化，请人工核对")
	}
	err = syncstate.Journal(directory, "complete", nil)
	return directory, err
}

func mergeDirectory(path string) error {
	parent := filepath.Dir(path)
	real, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return err
	}
	if real != parent {
		return fmt.Errorf("清单目录不能通过符号链接重定向：%s", parent)
	}
	return nil
}

func replaceManifest(ctx context.Context, path string, c *Capture) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("合并目标必须是普通清单文件：%s", path)
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".package-capture-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if _, err = io.WriteString(file, c.Candidate.Content); err != nil {
		return err
	}
	if err = file.Chmod(info.Mode().Perm()); err != nil {
		return err
	}
	if err = file.Sync(); err != nil {
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = unchanged(c.Sources); err != nil {
		return err
	}
	current, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !current.Mode().IsRegular() || !os.SameFile(info, current) || current.Mode() != info.Mode() {
		return fmt.Errorf("写入前清单文件已改变，请重新预览：%s", path)
	}
	if err = mergeDirectory(path); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
