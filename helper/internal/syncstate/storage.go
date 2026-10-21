package syncstate

import (
	"fmt"
	"os"
	"path/filepath"
)

// Local output directories are never allowed to redirect writes through links.
func LocalDirectory(root string, parts ...string) (string, error) {
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
