// Package confighistory registers and saves manual, machine-local checkpoints.
package confighistory

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/sweard/system_sync/helper/internal/localconfig"
)

type Invoker interface {
	Invoke([]string, bool, time.Duration) (string, error)
}

func Run(action, description, home string, runner Invoker, out io.Writer) error {
	enrollment, history, candidates, err := localconfig.Locations(home)
	if err != nil {
		return err
	}
	if localconfig.IsLink(enrollment) || localconfig.IsLink(history) {
		return fmt.Errorf("历史登记文件和历史目录不能是链接")
	}
	if action == "init" {
		entries := map[string]any{}
		var text strings.Builder
		text.WriteString("# 本机登记，不部署、不上传；仅显式保存时建立检查点。\n[dotfiles]\n")
		for _, path := range candidates {
			if localconfig.IsFile(path) {
				entries[path] = map[string]any{"mode": "track-local", "autosave": false}
				fmt.Fprintf(&text, "%s = { mode = \"track-local\", autosave = false }\n", localconfig.Quoted(path))
			}
		}
		if len(entries) == 0 {
			return fmt.Errorf("没有可跟踪的目标，请先部署配置")
		}
		if _, err := os.Stat(enrollment); err == nil {
			data, err := localconfig.Read(enrollment)
			if err != nil {
				return err
			}
			if !reflect.DeepEqual(data, map[string]any{"dotfiles": entries}) {
				return fmt.Errorf("本机历史范围已有人工修改，请先核对登记文件")
			}
		} else {
			if !os.IsNotExist(err) {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(enrollment), 0755); err != nil {
				return err
			}
			file, err := os.OpenFile(enrollment, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			if err != nil {
				return err
			}
			_, writeErr := io.WriteString(file, text.String())
			if writeErr == nil {
				writeErr = file.Sync()
			}
			closeErr := file.Close()
			if writeErr != nil || closeErr != nil {
				_ = os.Remove(enrollment)
				return errors.Join(writeErr, closeErr)
			}
		}
		if err := os.Chmod(enrollment, 0600); err != nil {
			return err
		}
		if err := os.MkdirAll(history, 0700); err != nil {
			return err
		}
		if err := os.Chmod(history, 0700); err != nil {
			return err
		}
		fmt.Fprintf(out, "[+] 已登记 %d 个本机配置；自动捕获关闭。运行 mise run config-save 建立检查点。\n", len(entries))
		for _, path := range candidates {
			if _, ok := entries[path]; ok {
				fmt.Fprintf(out, "  - %s\n", path)
			}
		}
		return nil
	}
	if !localconfig.IsFile(enrollment) {
		return fmt.Errorf("请先运行 mise run config-history-init")
	}
	data, err := localconfig.Read(enrollment)
	if err != nil {
		return err
	}
	entries, ok := localconfig.LocalEntries(data, candidates)
	if !ok {
		return fmt.Errorf("历史范围或本机隔离模式不符合约定，已停止")
	}
	command := []string{"mise", "dot", "save", "--description", description, "--"}
	skipped := []string{}
	for _, path := range candidates {
		if _, ok := entries[path]; ok {
			if localconfig.IsFile(path) {
				command = append(command, path)
			} else {
				skipped = append(skipped, path)
			}
		}
	}
	if len(skipped) > 0 {
		fmt.Fprintf(out, "[!] 跳过 %d 个不可用的已登记配置：\n", len(skipped))
		for _, path := range skipped {
			fmt.Fprintf(out, "  - %s\n", path)
		}
	}
	count := len(command) - 6
	if count == 0 {
		return fmt.Errorf("已登记的目标均不存在，未建立检查点")
	}
	if _, err := runner.Invoke(command, true, time.Minute); err != nil {
		if errors.Is(err, context.Canceled) {
			return err
		}
		return fmt.Errorf("mise 保存失败，请用 mise dot --local status 核对本机历史状态")
	}
	fmt.Fprintf(out, "[+] 已手动保存 %d 个配置到本机历史。\n", count)
	for _, path := range command[6:] {
		fmt.Fprintf(out, "  - %s\n", path)
	}
	return nil
}
