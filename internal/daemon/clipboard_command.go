package daemon

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
)

var errClipboardOutputTooLarge = errors.New("clipboard command output exceeds limit")

type clipboardOutputLimitError struct {
	msg string
}

func (e clipboardOutputLimitError) Error() string {
	return e.msg
}

func (e clipboardOutputLimitError) Unwrap() error {
	return errClipboardOutputTooLarge
}

func limitedCommandOutput(cmd *exec.Cmd, maxBytes int, tooLargeMsg string) ([]byte, error) {
	if maxBytes < 0 {
		return nil, fmt.Errorf("invalid command output limit: %d", maxBytes)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	out, readErr := io.ReadAll(io.LimitReader(stdout, int64(maxBytes)+1))
	if readErr != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, readErr
	}
	if len(out) > maxBytes {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		if tooLargeMsg == "" {
			tooLargeMsg = fmt.Sprintf("command output exceeds %d byte limit", maxBytes)
		}
		return nil, clipboardOutputLimitError{msg: tooLargeMsg}
	}
	if err := cmd.Wait(); err != nil {
		return nil, err
	}
	return out, nil
}
