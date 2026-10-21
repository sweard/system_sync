package configops

import (
	"archive/tar"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Archive only the exact configuration targets, retaining links and modes.
// Contents can be private: the archive and its parent are local and restricted.
func BackupTargets(home, directory string, targets []string) (err error) {
	file, err := os.OpenFile(filepath.Join(directory, "config.before.tar"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	w := tar.NewWriter(file)
	defer func() {
		if closeErr := w.Close(); err == nil {
			err = closeErr
		}
	}()
	seen := map[string]bool{}
	for _, target := range targets {
		if _, err := os.Lstat(target); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return err
		}
		err = filepath.Walk(target, func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if seen[path] {
				if info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			seen[path] = true
			link := ""
			if info.Mode()&os.ModeSymlink != 0 {
				var err error
				link, err = os.Readlink(path)
				if err != nil {
					return err
				}
			}
			if !info.Mode().IsRegular() && !info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
				return fmt.Errorf("配置包含特殊文件，未应用配置：%s", path)
			}
			header, err := tar.FileInfoHeader(info, link)
			if err != nil {
				return err
			}
			relative, err := filepath.Rel(home, path)
			if err != nil || relative == ".." || strings.HasPrefix(relative, "../") {
				return fmt.Errorf("配置备份目标超出 HOME")
			}
			header.Name = filepath.ToSlash(relative)
			if err = w.WriteHeader(header); err != nil {
				return err
			}
			if info.Mode().IsRegular() {
				in, err := os.Open(path)
				if err != nil {
					return err
				}
				_, copyErr := io.Copy(w, in)
				closeErr := in.Close()
				if copyErr != nil {
					return copyErr
				}
				return closeErr
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}
