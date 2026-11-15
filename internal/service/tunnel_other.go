//go:build !darwin

package service

import (
	"errors"

	"github.com/shunmei/cc-clip/internal/tunnelmgr"
)

func NewTunnelManager() (TunnelManager, error) {
	return nil, errors.New("tunnel LaunchAgents are only supported on macOS; " + tunnelmgr.ServiceLocationHint)
}
