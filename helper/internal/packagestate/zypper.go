package packagestate

import (
	"encoding/xml"
	"fmt"
	"io"
	"sort"
	"strings"
)

type xmlNode struct {
	XMLName  xml.Name
	Name     string    `xml:"name,attr"`
	Kind     string    `xml:"kind,attr"`
	Text     string    `xml:",chardata"`
	Children []xmlNode `xml:",any"`
}

func (n xmlNode) field(name, attr string) string {
	if attr != "" {
		return attr
	}
	for _, child := range n.Children {
		if child.XMLName.Local == name && child.Text != "" {
			return strings.TrimSpace(child.Text)
		}
	}
	return ""
}

func ParseZypper(input io.Reader, mode string) ([]string, error) {
	if mode != "solvables" && mode != "to-remove" {
		return nil, fmt.Errorf("未知 XML 提取模式")
	}
	d := xml.NewDecoder(input)
	var root xmlNode
	if err := d.Decode(&root); err != nil {
		return nil, err
	}
	if root.XMLName.Local != "stream" {
		return nil, fmt.Errorf("无法识别的 zypper XML 根节点")
	}
	for {
		token, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch v := token.(type) {
		case xml.CharData:
			if strings.TrimSpace(string(v)) != "" {
				return nil, fmt.Errorf("XML 根节点后含有额外内容")
			}
		case xml.Comment, xml.ProcInst:
		default:
			return nil, fmt.Errorf("XML 根节点后含有额外内容")
		}
	}
	names := make(map[string]bool)
	var walk func(xmlNode, bool) error
	walk = func(node xmlNode, removing bool) error {
		removing = removing || node.XMLName.Local == "to-remove"
		if node.XMLName.Local == "solvable" && (mode == "solvables" || removing) {
			kind := node.field("kind", node.Kind)
			if kind == "" {
				kind = "package"
			}
			if kind != "package" && mode == "to-remove" {
				return fmt.Errorf("移除事务包含非 package 对象")
			}
			if kind == "package" {
				name := node.field("name", node.Name)
				if !packageName.MatchString(name) {
					return fmt.Errorf("solvable 包名缺失或包含不安全字符")
				}
				names[name] = true
			}
		}
		for _, child := range node.Children {
			if err := walk(child, removing); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(root, false); err != nil {
		return nil, err
	}
	result := make([]string, 0, len(names))
	for name := range names {
		result = append(result, name)
	}
	sort.Strings(result)
	return result, nil
}
