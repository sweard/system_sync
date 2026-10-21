package configdetach

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/sweard/system_sync/internal/localconfig"
)

type Child struct {
	Name string
	Node *Node
}
type Node struct {
	Kind     string
	Mode     os.FileMode
	Data     []byte
	Link     string
	Children []Child
}

func snapshot(path, repository string, followRoot bool, seen map[string]bool) (*Node, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		link, err := os.Readlink(path)
		if err != nil {
			return nil, err
		}
		if repository == "" {
			return &Node{Kind: "link", Link: link}, nil
		}
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			return nil, err
		}
		if followRoot || localconfig.Within(resolved, repository) {
			return snapshot(resolved, repository, false, seen)
		}
		return &Node{Kind: "link", Link: resolved}, nil
	}
	mode := info.Mode() & (os.ModePerm | os.ModeSetuid | os.ModeSetgid | os.ModeSticky)
	if info.Mode().IsRegular() {
		data, err := os.ReadFile(path)
		return &Node{Kind: "file", Mode: mode, Data: data}, err
	}
	if info.IsDir() {
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			return nil, err
		}
		if seen[resolved] {
			return nil, fmt.Errorf("目录链接形成循环，未修改配置：%s", path)
		}
		next := map[string]bool{}
		for p := range seen {
			next[p] = true
		}
		next[resolved] = true
		entries, err := os.ReadDir(path)
		if err != nil {
			return nil, err
		}
		node := &Node{Kind: "directory", Mode: mode, Children: []Child{}}
		for _, entry := range entries {
			child, err := snapshot(filepath.Join(path, entry.Name()), repository, false, next)
			if err != nil {
				return nil, err
			}
			if child == nil {
				return nil, fmt.Errorf("配置在读取时发生变化，未修改配置")
			}
			node.Children = append(node.Children, Child{entry.Name(), child})
		}
		return node, nil
	}
	return nil, fmt.Errorf("配置中有不支持的特殊文件，未修改配置：%s", path)
}
func writeNode(node *Node, path string, private bool) error {
	switch node.Kind {
	case "link":
		return os.Symlink(node.Link, path)
	case "file":
		mode := node.Mode
		if private {
			mode = 0600
		}
		if err := os.WriteFile(path, node.Data, 0600); err != nil {
			return err
		}
		return os.Chmod(path, mode)
	case "directory":
		if err := os.Mkdir(path, 0700); err != nil {
			return err
		}
		for _, child := range node.Children {
			if err := writeNode(child.Node, filepath.Join(path, child.Name), private); err != nil {
				return err
			}
		}
		mode := node.Mode
		if private {
			mode = 0700
		}
		return os.Chmod(path, mode)
	default:
		return fmt.Errorf("不支持的配置节点")
	}
}
func removeNode(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.IsDir() {
		return os.RemoveAll(path)
	}
	return os.Remove(path)
}
func metadata(node *Node) any {
	if node == nil {
		return nil
	}
	mode := uint32(node.Mode.Perm())
	if node.Mode&os.ModeSetuid != 0 {
		mode |= 04000
	}
	if node.Mode&os.ModeSetgid != 0 {
		mode |= 02000
	}
	if node.Mode&os.ModeSticky != 0 {
		mode |= 01000
	}
	result := map[string]any{"kind": node.Kind, "mode": mode}
	if node.Kind == "directory" {
		children := map[string]any{}
		for _, child := range node.Children {
			children[child.Name] = metadata(child.Node)
		}
		result["children"] = children
	}
	return result
}
