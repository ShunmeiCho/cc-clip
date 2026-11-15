//go:build darwin

package service

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shunmei/cc-clip/internal/tunnelmgr"
)

// Decode plist values, rather than pinning whitespace or launchctl spelling.
func decodePlistValue(d *xml.Decoder, start xml.StartElement) (any, error) {
	switch start.Name.Local {
	case "string", "key", "integer":
		var value string
		if err := d.DecodeElement(&value, &start); err != nil {
			return nil, err
		}
		if start.Name.Local == "integer" {
			return strconv.Atoi(value)
		}
		return value, nil
	case "true", "false":
		if err := d.Skip(); err != nil {
			return nil, err
		}
		return start.Name.Local == "true", nil
	}
	var array []any
	dict := make(map[string]any)
	key := ""
	for {
		token, err := d.Token()
		if err != nil {
			return nil, err
		}
		switch token := token.(type) {
		case xml.StartElement:
			value, err := decodePlistValue(d, token)
			if err != nil {
				return nil, err
			}
			if token.Name.Local == "key" {
				key = value.(string)
			} else if start.Name.Local == "dict" {
				dict[key] = value
			} else {
				array = append(array, value)
			}
		case xml.EndElement:
			if start.Name.Local == "dict" {
				return dict, nil
			}
			return array, nil
		}
	}
}

func TestTunnelJobInspectionSeparatesRegistrationAndProcess(t *testing.T) {
	for _, test := range []struct {
		out, process string
		pid          int
	}{
		{"state = running\n pid = 123\n last exit code = 1", "running", 123},
		{"state = not running\n last exit code = 0", "stopped", 0},
		{"unexpected output", "unknown", 0},
	} {
		job := parseTunnelJob(test.out)
		if !job.Loaded || job.Process != test.process || job.PID != test.pid {
			t.Fatalf("job = %+v", job)
		}
	}
}

func TestTunnelAuthenticationPreflightContextAndCleanup(t *testing.T) {
	for _, mode := range []string{"ok", "auth-required", "host-key-error", "invalid", "timeout", "canceled-result", "bootstrap-failure", "cleanup-failure"} {
		t.Run(mode, func(t *testing.T) {
			m, _, binary := newTestTunnelManager(t)
			t.Setenv("SSH_AUTH_SOCK", "/shell-only/agent.sock")
			t.Setenv("CC_CLIP_PORT", "18444")
			final := plistDict(t, m.plist(binary, tunnelmgr.TunnelID("host")))
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()
			var directory, label string
			m.run = func(ctx context.Context, args ...string) ([]byte, error) {
				switch args[0] {
				case "bootstrap":
					data, err := os.ReadFile(args[2])
					if err != nil {
						t.Fatal(err)
					}
					dict := plistDict(t, string(data))
					label = dict["Label"].(string)
					argv := dict["ProgramArguments"].([]any)
					directory = argv[6].(string)
					if !strings.HasPrefix(label, "com.cc-clip.tunnel-auth.") || argv[2] != "check-auth" {
						t.Fatalf("not a separate auth job: %#v", dict)
					}
					for _, key := range []string{"EnvironmentVariables", "Umask", "ExitTimeOut", "RunAtLoad"} {
						if !reflect.DeepEqual(dict[key], final[key]) {
							t.Fatalf("preflight %s differs from final service: %#v vs %#v", key, dict[key], final[key])
						}
					}
					if argv[0] != binary || args[1] != m.domain() || dict["KeepAlive"] != false {
						t.Fatal("preflight binary/domain/one-shot policy differs")
					}
					if mode == "bootstrap-failure" {
						return nil, errors.New("bootstrap rejected")
					}
					if mode != "timeout" {
						state := mode
						if mode == "cleanup-failure" || mode == "canceled-result" {
							state = "ok"
						}
						data, _ = json.Marshal(tunnelmgr.AuthResult{State: state})
						if err := os.WriteFile(filepath.Join(directory, "auth-result.json"), data, 0600); err != nil {
							t.Fatal(err)
						}
					}
					if mode == "canceled-result" {
						cancel()
					}
					return nil, nil
				case "bootout":
					if args[1] != m.domain()+"/"+label {
						t.Fatal("cleanup targeted another job")
					}
					if mode == "cleanup-failure" {
						return nil, errors.New("bootout rejected")
					}
					return nil, nil
				default:
					return nil, errors.New("unexpected operation")
				}
			}
			spec := tunnelmgr.Spec{Host: "host", Port: 18399, ExpectedInstanceID: "instance"}
			result, err := m.Preflight(ctx, binary, spec)
			if mode == "ok" || mode == "auth-required" || mode == "host-key-error" {
				if err != nil || result.State != mode {
					t.Fatalf("result=%+v error=%v", result, err)
				}
			} else if err == nil {
				t.Fatal("failed/unknown preflight reported success")
			}
			_, err = os.Stat(directory)
			if mode == "cleanup-failure" {
				if err != nil {
					t.Fatal("failed cleanup lost diagnostic files")
				}
			} else if !os.IsNotExist(err) {
				t.Fatalf("preflight artifacts retained: %v", err)
			}
		})
	}
}

func plistDict(t *testing.T, content string) map[string]any {
	t.Helper()
	d := xml.NewDecoder(strings.NewReader(content))
	for {
		token, err := d.Token()
		if err == io.EOF {
			t.Fatal("missing plist dict")
		}
		if err != nil {
			t.Fatal(err)
		}
		if start, ok := token.(xml.StartElement); ok && start.Name.Local == "dict" {
			value, err := decodePlistValue(d, start)
			if err != nil {
				t.Fatal(err)
			}
			return value.(map[string]any)
		}
	}
}

type tunnelJobs struct {
	loaded    map[string]bool
	starts    int
	failStart bool
	failStop  bool
	queryErr  bool
}

func newTestTunnelManager(t *testing.T) (*tunnelLaunchd, *tunnelJobs, string) {
	t.Helper()
	m := &tunnelLaunchd{home: t.TempDir(), uid: os.Getuid()}
	jobs := &tunnelJobs{loaded: make(map[string]bool)}
	m.run = func(ctx context.Context, args ...string) ([]byte, error) {
		if _, ok := ctx.Deadline(); !ok {
			t.Error("launchctl has no deadline")
		}
		switch args[0] {
		case "print":
			if jobs.queryErr {
				return []byte("Could not access domain"), errors.New("permission denied")
			}
			if jobs.loaded[args[1]] {
				return []byte("running"), nil
			}
			return []byte("Could not find service"), errors.New("missing job")
		case "bootstrap":
			if jobs.failStart {
				return []byte("load failed"), errors.New("bootstrap")
			}
			data, err := os.ReadFile(args[2])
			if err != nil {
				t.Fatal(err)
			}
			label := plistDict(t, string(data))["Label"].(string)
			jobs.loaded[args[1]+"/"+label] = true
			jobs.starts++
			return nil, nil
		case "bootout":
			if jobs.failStop {
				return []byte("stop failed"), errors.New("bootout")
			}
			delete(jobs.loaded, args[1])
			return nil, nil
		default:
			return nil, fmt.Errorf("unexpected service operation %v", args)
		}
	}
	binary := filepath.Join(m.home, "cc-clip & <test>")
	if err := os.WriteFile(binary, []byte("fixture executable"), 0700); err != nil {
		t.Fatal(err)
	}
	return m, jobs, binary
}

func saveTestTunnelSpec(t *testing.T, m *tunnelLaunchd, host string) string {
	t.Helper()
	id := tunnelmgr.TunnelID(host)
	config := tunnelmgr.ServiceConfig{SchemaVersion: 1, ID: id, Enabled: true, Spec: tunnelmgr.Spec{Host: host, Port: 18399, ExpectedInstanceID: "instance-123"}}
	s := tunnelmgr.NewServiceStoreAt(filepath.Join(m.home, ".cache", "cc-clip", "tunnel-specs"))
	if err := s.Save(config); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestTunnelPlistSemantics(t *testing.T) {
	m, _, binary := newTestTunnelManager(t)
	id := tunnelmgr.TunnelID("host")
	dict := plistDict(t, m.plist(binary, id))
	if dict["Label"] != tunnelLabelPrefix+id {
		t.Fatal("wrong job label")
	}
	if !reflect.DeepEqual(dict["ProgramArguments"], []any{binary, "tunnel", "run", "--id", id}) {
		t.Fatalf("argv = %#v", dict["ProgramArguments"])
	}
	if dict["RunAtLoad"] != true || dict["ThrottleInterval"] != 60 || dict["ExitTimeOut"] != 10 || dict["Umask"] != 63 {
		t.Fatalf("launch policy: %#v", dict)
	}
	if !reflect.DeepEqual(dict["KeepAlive"], map[string]any{"SuccessfulExit": false}) {
		t.Fatalf("keepalive = %#v", dict["KeepAlive"])
	}
	if dict["StandardOutPath"] != m.logPath(id) || dict["StandardErrorPath"] != m.logPath(id) {
		t.Fatal("wrong log destination")
	}
	env := dict["EnvironmentVariables"].(map[string]any)
	if env["HOME"] != m.home || env["PATH"] == "" || len(env) != 2 {
		t.Fatalf("unexpected environment: %#v", env)
	}
}

func TestTunnelLaunchAgentLifecycleAndHostIsolation(t *testing.T) {
	m, jobs, binary := newTestTunnelManager(t)
	first, second := saveTestTunnelSpec(t, m, "host-a"), saveTestTunnelSpec(t, m, "host-b")
	daemon := filepath.Join(m.home, "Library", "LaunchAgents", plistFileName)
	if err := os.MkdirAll(filepath.Dir(daemon), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(daemon, []byte("existing daemon"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{first, first, second} {
		if err := m.Install(binary, id); err != nil {
			t.Fatal(err)
		}
		loaded, err := m.Status(id)
		if err != nil || !loaded {
			t.Fatalf("job not loaded: %v %v", loaded, err)
		}
		for _, path := range []string{m.plistPath(id), m.logPath(id)} {
			info, err := os.Stat(path)
			if err != nil || info.Mode().Perm() != 0600 {
				t.Fatalf("not private: %s %v %v", path, info, err)
			}
		}
	}
	if jobs.starts != 2 {
		t.Fatalf("duplicate jobs: %d starts", jobs.starts)
	}
	if err := m.Uninstall(first); err != nil {
		t.Fatal(err)
	}
	if err := m.Uninstall(first); err != nil {
		t.Fatalf("uninstall not idempotent: %v", err)
	}
	if loaded, _ := m.Status(second); !loaded {
		t.Fatal("other job stopped")
	}
	if _, err := os.Stat(m.plistPath(first)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("plist not removed")
	}
	if _, err := os.Stat(m.logPath(first)); err != nil {
		t.Fatal("diagnostic log removed")
	}
	data, err := os.ReadFile(daemon)
	if err != nil || string(data) != "existing daemon" {
		t.Fatal("daemon service changed")
	}
}

func TestTunnelLaunchAgentFailureHandling(t *testing.T) {
	for _, mode := range []string{"query", "start", "existing start", "stop"} {
		t.Run(mode, func(t *testing.T) {
			m, jobs, binary := newTestTunnelManager(t)
			id := saveTestTunnelSpec(t, m, "host")
			switch mode {
			case "query":
				jobs.queryErr = true
			case "start", "existing start":
				jobs.failStart = true
			case "stop":
				if err := m.Install(binary, id); err != nil {
					t.Fatal(err)
				}
				jobs.failStop = true
			}
			if mode == "existing start" {
				if err := os.MkdirAll(filepath.Dir(m.plistPath(id)), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(m.plistPath(id), []byte(m.plist(binary, id)), 0600); err != nil {
					t.Fatal(err)
				}
			}
			var err error
			if mode == "stop" {
				err = m.Uninstall(id)
			} else {
				err = m.Install(binary, id)
			}
			if err == nil {
				t.Fatal("failed operation reported success")
			}
			_, statErr := os.Stat(m.plistPath(id))
			if mode == "existing start" || mode == "stop" {
				if statErr != nil {
					t.Fatal("preexisting plist removed on failure")
				}
			} else if !errors.Is(statErr, os.ErrNotExist) {
				t.Fatal("failed install retained new plist")
			}
		})
	}
}

func TestTunnelLaunchAgentRequiresEnabledSpec(t *testing.T) {
	m, jobs, binary := newTestTunnelManager(t)
	id := tunnelmgr.TunnelID("missing")
	if err := m.Install(binary, id); err == nil {
		t.Fatal("installed without spec")
	}
	id = saveTestTunnelSpec(t, m, "host")
	s := tunnelmgr.NewServiceStoreAt(filepath.Join(m.home, ".cache", "cc-clip", "tunnel-specs"))
	c, _ := s.Load(id)
	c.Enabled = false
	if err := s.Save(*c); err != nil {
		t.Fatal(err)
	}
	if err := m.Install(binary, id); err == nil {
		t.Fatal("installed disabled spec")
	}
	if err := m.Install("relative/cc-clip", id); err == nil {
		t.Fatal("installed relative executable")
	}
	if _, err := m.Status("../../daemon"); err == nil {
		t.Fatal("accepted path traversal")
	}
	if jobs.starts != 0 {
		t.Fatal("invalid install started job")
	}
}

func TestTunnelLaunchAgentRejectsForeignPlistAndSymlinkLog(t *testing.T) {
	for _, mode := range []string{"foreign plist", "symlink log"} {
		t.Run(mode, func(t *testing.T) {
			m, jobs, binary := newTestTunnelManager(t)
			id := saveTestTunnelSpec(t, m, "host")
			if mode == "foreign plist" {
				if err := os.MkdirAll(filepath.Dir(m.plistPath(id)), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(m.plistPath(id), []byte("foreign job"), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.MkdirAll(filepath.Dir(m.logPath(id)), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(binary, m.logPath(id)); err != nil {
					t.Fatal(err)
				}
			}
			if err := m.Install(binary, id); err == nil {
				t.Fatal("accepted unsafe service files")
			}
			if jobs.starts != 0 {
				t.Fatal("unsafe service started")
			}
		})
	}
}

func TestTunnelRepeatedEnableRevalidatesLoadedJobLog(t *testing.T) {
	for _, mode := range []string{"file", "directory"} {
		t.Run(mode, func(t *testing.T) {
			m, jobs, binary := newTestTunnelManager(t)
			id := saveTestTunnelSpec(t, m, "host")
			if err := m.Install(binary, id); err != nil {
				t.Fatal(err)
			}
			path, perm := m.logPath(id), os.FileMode(0644)
			if mode == "directory" {
				path, perm = filepath.Dir(path), 0755
			}
			if err := os.Chmod(path, perm); err != nil {
				t.Fatal(err)
			}
			if err := m.Install(binary, id); err == nil {
				t.Fatal("repeat enable accepted public log path")
			}
			if jobs.starts != 1 {
				t.Fatal("repeat enable started a second job")
			}
			info, err := os.Stat(path)
			if err != nil || info.Mode().Perm() != perm {
				t.Fatal("validation repaired log permissions")
			}
		})
	}
}
