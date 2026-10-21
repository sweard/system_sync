package onboard

import (
	"archive/tar"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Local output directories are never allowed to redirect writes through links.
func localDirectory(root string, parts ...string) (string, error) {
	path := root
	for _, part := range append([]string{".system-sync"}, parts...) {
		path = filepath.Join(path, part)
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			if err = os.Mkdir(path, 0700); err != nil {
				return "", err
			}
		} else if err != nil {
			return "", err
		} else if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("本机状态路径必须是普通目录：%s", path)
		}
	}
	return path, nil
}

func (c *Capture) Save(root, kind string) (directory string, err error) {
	parent, err := localDirectory(root, kind)
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

// Archive only the exact configuration targets, retaining links and modes.
// Contents can be private: the archive and its parent are local and restricted.
func backupConfigurations(home, directory string, targets []string) (err error) {
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
