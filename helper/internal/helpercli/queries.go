package helpercli

import (
	"fmt"
	"io"

	"github.com/sweard/system_sync/helper/internal/taskexec"
)

// Status/list commands keep native stdout, including JSON, separate from progress.
func nativeQuery(runner taskexec.Runner, command []string, label, retry string, out, progress io.Writer) (int, error) {
	var output string
	err := taskexec.Progress(progress, label, func() error {
		var err error
		output, err = runner.Run(command, true)
		return err
	})
	if err != nil {
		return 1, fmt.Errorf("%s失败：%w；请单独运行 %s 核对", label, taskexec.Redacted(err), retry)
	}
	_, err = io.WriteString(out, output)
	if err != nil {
		return 1, err
	}
	return 0, nil
}
