//go:build windows

package tunnelmgr

import "errors"

func lockService(string) (func(), error) {
	return nil, errors.New("background tunnel services require macOS LaunchAgents")
}
