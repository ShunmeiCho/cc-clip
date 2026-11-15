//go:build !windows

package tunnelmgr

import (
	"bytes"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shunmei/cc-clip/internal/daemon"
	"github.com/shunmei/cc-clip/internal/token"
	"github.com/shunmei/cc-clip/internal/tunnel"
)

func TestSupervisorTokenRepairWithRealDaemonDoesNotRenew(t *testing.T) {
	oldDir := token.TokenDirOverride
	token.TokenDirOverride = t.TempDir()
	t.Cleanup(func() { token.TokenDirOverride = oldDir })
	expiry := time.Now().Add(10 * time.Minute).Truncate(time.Second)
	localPath, err := token.WriteTokenFile("current-daemon-token", expiry)
	if err != nil {
		t.Fatal(err)
	}
	tokens := token.NewManager(time.Hour)
	if _, reused, err := tokens.LoadOrGenerate(time.Hour); err != nil || !reused {
		t.Fatalf("load daemon session: reused=%v error=%v", reused, err)
	}
	srv := daemon.NewServer("127.0.0.1:0", nil, tokens, nil)
	srv.SetInstanceID("instance-123")
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	address := strings.TrimPrefix(ts.URL, "http://")
	remotePath := filepath.Join(t.TempDir(), "session.token")
	if err := os.WriteFile(remotePath, []byte("previous-daemon-token\n"), 0600); err != nil {
		t.Fatal(err)
	}
	localBefore, err := os.ReadFile(localPath)
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_SSH_MODE", "master")
	t.Setenv("FAKE_SSH_PROBE_OUT", "cc-clip-probe:ok\n")
	t.Setenv("FAKE_SSH_IDENTITY_HELPER", executable)
	t.Setenv("FAKE_SSH_IDENTITY_ADDRESS", address)
	t.Setenv("FAKE_SSH_IDENTITY_TOKEN_FILE", remotePath)
	countPath := filepath.Join(t.TempDir(), "master-starts")
	t.Setenv("FAKE_SSH_COUNT_FILE", countPath)
	sup, store := newTestSupervisor(t, 18399)
	sup.AttentionInterval = 20 * time.Millisecond
	sup.ReadLocalToken = token.ReadTokenFile
	sup.FetchLocalIdentity = func(_ string, bearer string, timeout time.Duration) (tunnel.IdentityInfo, error) {
		return tunnel.FetchIdentity(address, bearer, timeout)
	}
	cancel, done := runSupervisor(t, sup)
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("stop supervisor: %v", err)
		}
	})
	waitUntil(t, 10*time.Second, func() bool {
		rec, err := store.Load()
		return err == nil && rec.Runtime.State == StateRemoteTokenInvalid && strings.Contains(rec.Runtime.LastError, "--token-only")
	}, "remote-token-invalid from actual daemon rejection")
	control := sup.Backend.ControlPath()
	if !sup.Backend.Running() || !sup.Backend.Forwarded() || fakeMasterStarts(countPath) != 1 {
		t.Fatal("remote token rejection discarded the SSH connection")
	}
	// Copy only the current daemon credential, as connect --token-only does.
	// Publish atomically so an in-flight helper cannot see a partial token.
	repairPath := filepath.Join(filepath.Dir(remotePath), "repaired.token")
	if err := os.WriteFile(repairPath, localBefore, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(repairPath, remotePath); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, 10*time.Second, func() bool { return sup.State() == StateHealthy }, "healthy after actual token-file repair")
	if sup.Backend.ControlPath() != control || fakeMasterStarts(countPath) != 1 {
		t.Fatal("token-file repair rebuilt SSH instead of recovering in place")
	}
	localAfter, err := os.ReadFile(localPath)
	if err != nil {
		t.Fatal(err)
	}
	if !tokens.Current().ExpiresAt.Equal(expiry) || !bytes.Equal(localBefore, localAfter) {
		t.Fatal("local or remote identity monitoring renewed the daemon token")
	}
}

// Fake SSH dispatches the test executable as a remote helper. It reads the
// copied token file on every probe and exercises the production HTTP client
// and marker protocol, with no credential in SSH arguments or test output.
func TestSupervisorDaemonIdentityHelper(t *testing.T) {
	path := os.Getenv("FAKE_SSH_IDENTITY_TOKEN_FILE")
	if path == "" {
		t.Skip("only runs as the fake SSH identity helper")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("cannot read remote token fixture")
	}
	bearer, _, _ := strings.Cut(string(data), "\n")
	identity, err := tunnel.FetchIdentity(os.Getenv("FAKE_SSH_IDENTITY_ADDRESS"), bearer, 2*time.Second)
	state := tunnel.RemoteIdentityOK
	switch {
	case errors.Is(err, tunnel.ErrIdentityUnauthorized):
		state = tunnel.RemoteIdentityTokenInvalid
	case errors.Is(err, tunnel.ErrIdentityEndpointUnavailable):
		state = tunnel.RemoteIdentityEndpointUnavailable
	case err != nil:
		state = tunnel.RemoteIdentityUnknown
	}
	fmt.Println(tunnel.RemoteIdentityProbeOutput(state, identity))
}
