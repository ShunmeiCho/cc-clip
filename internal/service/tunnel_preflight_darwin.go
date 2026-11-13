//go:build darwin

package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/shunmei/cc-clip/internal/tunnelmgr"
)

// Preflight runs the same binary in the GUI launchd domain with the same HOME,
// PATH and umask as the final job. Shell SSH_AUTH_SOCK is never copied. Its
// forwarding-free SSH master is created and cleaned up by the existing backend.
func (m *tunnelLaunchd) Preflight(ctx context.Context, binary string, spec tunnelmgr.Spec) (tunnelmgr.AuthResult, error) {
	if err := ctx.Err(); err != nil {
		return tunnelmgr.AuthResult{}, err
	}
	c, err := tunnelmgr.NewServiceConfig(spec, false)
	if err != nil {
		return tunnelmgr.AuthResult{}, err
	}
	if err := m.validateBinary(binary); err != nil {
		return tunnelmgr.AuthResult{}, err
	}
	root := filepath.Join(m.home, ".cache", "cc-clip", "tunnel-runtime", "auth")
	if err := privateTunnelDir(root); err != nil {
		return tunnelmgr.AuthResult{}, err
	}
	dir, err := os.MkdirTemp(root, "preflight-")
	if err != nil {
		return tunnelmgr.AuthResult{}, err
	}
	label := "com.cc-clip.tunnel-auth." + filepath.Base(dir)
	path := filepath.Join(dir, "job.plist")
	resultPath := filepath.Join(dir, "auth-result.json")
	configs := tunnelmgr.NewServiceStoreAt(dir)
	configPath, _ := configs.Path(c.ID)
	// Only artifacts from this invocation are removed. A cleanup failure is
	// reported and its job/artifacts retained for diagnosis, rather than lost.
	bootstrapped := false
	cleanup := func() error {
		if bootstrapped {
			out, err := m.call("bootout", m.domain()+"/"+label)
			if err != nil && (errors.Is(err, context.DeadlineExceeded) || !strings.Contains(string(out), "Could not find service")) {
				return fmt.Errorf("remove authentication preflight job: %s: %w", out, err)
			}
		}
		for _, p := range []string{path, configPath, resultPath} {
			if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		return os.Remove(dir)
	}
	// Named results ensure cleanup errors cannot be hidden from enable/auth.
	return m.runPreflight(ctx, binary, c, dir, label, path, configs, &bootstrapped, cleanup)
}

func (m *tunnelLaunchd) runPreflight(ctx context.Context, binary string, c tunnelmgr.ServiceConfig, dir, label, path string, configs *tunnelmgr.ServiceStore, bootstrapped *bool, cleanup func() error) (result tunnelmgr.AuthResult, retErr error) {
	defer func() { retErr = errors.Join(retErr, cleanup()) }()
	if err := configs.Save(c); err != nil {
		return result, err
	}
	plist := m.jobPlist(binary, label, []string{"check-auth", "--id", c.ID, "--directory", dir}, false, "")
	if err := os.WriteFile(path, []byte(plist), 0600); err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	// A launchctl failure/timeout does not prove that no job was registered.
	// Attempt targeted cleanup even if bootstrap fails before returning success.
	*bootstrapped = true
	if out, err := m.call("bootstrap", m.domain(), path); err != nil {
		return result, fmt.Errorf("start service-context authentication check: %s: %w", out, err)
	}
	ctx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return result, fmt.Errorf("service-context authentication check did not complete: %w", err)
		}
		data, err := readTunnelFile(filepath.Join(dir, "auth-result.json"))
		if err == nil {
			if err := json.Unmarshal(data, &result); err != nil || !result.Valid() {
				return result, errors.New("invalid service-context authentication result")
			}
			return result, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return result, err
		}
		select {
		case <-ctx.Done():
			return result, fmt.Errorf("service-context authentication check did not complete: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}
