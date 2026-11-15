package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shunmei/cc-clip/internal/service"
	"github.com/shunmei/cc-clip/internal/tunnelmgr"
)

type fakeTunnelJobs struct {
	loaded                          map[string]bool
	installs, stops, checks         int
	checkState                      string
	checkErr                        error
	installErr, stopErr, inspectErr error
	logDir                          string
	beforeInstall                   func(string)
	beforeStop                      func(string)
}

func (j *fakeTunnelJobs) Install(_ string, id string) error {
	if j.beforeInstall != nil {
		j.beforeInstall(id)
	}
	if j.installErr != nil {
		return j.installErr
	}
	if !j.loaded[id] {
		j.installs++
	}
	j.loaded[id] = true
	return nil
}
func (j *fakeTunnelJobs) Stop(id string) error {
	if j.beforeStop != nil {
		j.beforeStop(id)
	}
	if j.stopErr != nil {
		return j.stopErr
	}
	j.stops++
	j.loaded[id] = false
	return nil
}
func (j *fakeTunnelJobs) Uninstall(id string) error      { return j.Stop(id) }
func (j *fakeTunnelJobs) Status(id string) (bool, error) { return j.loaded[id], j.inspectErr }
func (j *fakeTunnelJobs) Inspect(id string) (service.TunnelJob, error) {
	process := "stopped"
	pid := 0
	if j.loaded[id] {
		process = "running"
		pid = 123
	}
	return service.TunnelJob{Loaded: j.loaded[id], Process: process, PID: pid}, j.inspectErr
}
func (j *fakeTunnelJobs) Preflight(context.Context, string, tunnelmgr.Spec) (tunnelmgr.AuthResult, error) {
	j.checks++
	return tunnelmgr.AuthResult{State: j.checkState}, j.checkErr
}
func (j *fakeTunnelJobs) LogPath(id string) (string, error) {
	return filepath.Join(j.logDir, id+".log"), nil
}

func newTestTunnelCommands(t *testing.T) (*tunnelServiceCommands, *fakeTunnelJobs, *bytes.Buffer) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("LaunchAgent commands require Unix private files/locks")
	}
	root := t.TempDir()
	jobs := &fakeTunnelJobs{loaded: make(map[string]bool), checkState: "ok", logDir: t.TempDir()}
	out := new(bytes.Buffer)
	c := &tunnelServiceCommands{configs: tunnelmgr.NewServiceStoreAt(filepath.Join(root, "specs")), jobs: jobs, binary: "/test/cc-clip", out: out,
		localIdentity: func(int) (string, error) { return "instance-123", nil }, defaultPort: func() (int, error) { return 18399, nil },
		interactiveAuth: func(context.Context, string) error { return nil }, now: time.Now}
	return c, jobs, out
}

func testEnabledTunnel(t *testing.T, c *tunnelServiceCommands, host string) tunnelmgr.ServiceConfig {
	t.Helper()
	config, err := tunnelmgr.NewServiceConfig(tunnelmgr.Spec{Host: host, Port: 18399, ExpectedInstanceID: "instance-123"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.configs.Save(config); err != nil {
		t.Fatal(err)
	}
	return config
}

func TestTunnelServiceArgumentValidation(t *testing.T) {
	for _, test := range []struct {
		command string
		args    []string
		valid   bool
	}{
		{"enable", []string{"host", "--port=18340"}, true}, {"status", nil, true}, {"status", []string{"--port", "18340", "host"}, true},
		{"logs", []string{"host", "-f", "-n", "5"}, true}, {"auth", []string{"host"}, true},
		{"enable", nil, false}, {"restart", []string{"host", "--port=18340"}, false}, {"disable", []string{"host", "--reset"}, false},
		{"enable", []string{"host", "--port=0"}, false}, {"enable", []string{"host", "--port"}, false}, {"enable", []string{"host", "--port=1", "--port=2"}, false},
		{"logs", []string{"host", "--lines=0"}, false}, {"logs", []string{"host", "--lines=10001"}, false},
		{"status", []string{"host", "other"}, false}, {"auth", []string{"--follow", "host"}, false}, {"enable", []string{"bad\x00host"}, false},
	} {
		_, err := parseTunnelServiceArgs(test.command, test.args)
		if (err == nil) != test.valid {
			t.Errorf("%s %v: error=%v, valid=%t", test.command, test.args, err, test.valid)
		}
	}
}

func TestTunnelEnablePreflightPersistenceAndIdempotence(t *testing.T) {
	c, jobs, _ := newTestTunnelCommands(t)
	jobs.beforeInstall = func(id string) {
		config, err := c.configs.Load(id)
		if err != nil || !config.Enabled || jobs.checks == 0 {
			t.Fatal("job installed before validated enabled config/preflight")
		}
	}
	for i := 0; i < 2; i++ {
		if err := c.run(context.Background(), "enable", tunnelServiceOptions{host: "host"}); err != nil {
			t.Fatal(err)
		}
	}
	if jobs.installs != 1 {
		t.Fatalf("started %d jobs", jobs.installs)
	}
	config, err := c.configs.Load(tunnelmgr.TunnelID("host"))
	if err != nil || config.Spec.Port != 18399 {
		t.Fatalf("config=%+v err=%v", config, err)
	}
	t.Setenv("CC_CLIP_PORT", "18340")
	if err := c.run(context.Background(), "enable", tunnelServiceOptions{host: "host"}); err != nil {
		t.Fatal(err)
	}
	if err := c.run(context.Background(), "enable", tunnelServiceOptions{host: "host", port: 18340}); err == nil || !strings.Contains(err.Error(), "port: persisted=18399, requested=18340") {
		t.Fatalf("port mismatch: %v", err)
	}
}

func TestTunnelConcurrentEnableAndDisableAreSerialized(t *testing.T) {
	for _, secondCommand := range []string{"enable", "disable"} {
		t.Run(secondCommand, func(t *testing.T) {
			c, jobs, _ := newTestTunnelCommands(t)
			entered := make(chan struct{})
			release := make(chan struct{})
			var once sync.Once
			jobs.beforeInstall = func(string) {
				once.Do(func() { close(entered); <-release })
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			defer func() {
				select {
				case <-release:
				default:
					close(release)
				}
			}()
			firstDone, secondDone := make(chan error, 1), make(chan error, 1)
			go func() { firstDone <- c.run(ctx, "enable", tunnelServiceOptions{host: "host"}) }()
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal("enable did not reach installation")
			}
			go func() { secondDone <- c.run(ctx, secondCommand, tunnelServiceOptions{host: "host"}) }()
			select {
			case err := <-secondDone:
				t.Fatalf("management operation crossed held lock: %v", err)
			case <-time.After(50 * time.Millisecond):
			}
			close(release)
			for _, done := range []chan error{firstDone, secondDone} {
				select {
				case err := <-done:
					if err != nil {
						t.Fatal(err)
					}
				case <-ctx.Done():
					t.Fatal("management did not complete")
				}
			}
			config, err := c.configs.Load(tunnelmgr.TunnelID("host"))
			if err != nil {
				t.Fatal(err)
			}
			wantEnabled := secondCommand == "enable"
			if config.Enabled != wantEnabled || jobs.loaded[config.ID] != wantEnabled || jobs.installs != 1 {
				t.Fatalf("desired/job state diverged: config=%+v jobs=%+v", config, jobs)
			}
		})
	}
}

func TestTunnelEnableFailureDoesNotEnableJob(t *testing.T) {
	for _, mode := range []string{"local-daemon", "auth-required", "host-key-error", "connection-error", "timeout", "unknown", "install"} {
		t.Run(mode, func(t *testing.T) {
			c, jobs, _ := newTestTunnelCommands(t)
			switch mode {
			case "local-daemon":
				c.localIdentity = func(int) (string, error) { return "", errors.New("daemon unavailable") }
			case "install":
				jobs.installErr = errors.New("bootstrap failed")
			default:
				jobs.checkState = mode
			}
			if err := c.run(context.Background(), "enable", tunnelServiceOptions{host: "host"}); err == nil {
				t.Fatal("failed enable reported success")
			}
			config, err := c.configs.Load(tunnelmgr.TunnelID("host"))
			if err == nil && config.Enabled {
				t.Fatal("failed enable left enabled desired state")
			}
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				t.Fatal(err)
			}
			if jobs.installs != 0 {
				t.Fatal("failed preflight started service")
			}
		})
	}
}

func TestTunnelRestartRecoversOnlyValidatedRuntime(t *testing.T) {
	c, jobs, _ := newTestTunnelCommands(t)
	config := testEnabledTunnel(t, c, "host")
	store, _ := c.configs.RuntimeStore(config.ID)
	rec := &tunnelmgr.Record{SchemaVersion: 1, Spec: config.Spec, Runtime: tunnelmgr.Runtime{State: tunnelmgr.StateCrashLoop, ConsecutiveStartFailures: 5}}
	if err := store.Save(rec); err != nil {
		t.Fatal(err)
	}
	jobs.loaded[config.ID] = true
	jobs.beforeInstall = func(string) {
		got, err := store.Load()
		if err != nil || got.Runtime.ConsecutiveStartFailures != 0 || got.Runtime.State != tunnelmgr.StateUnknown {
			t.Fatal("crash loop not reset before start")
		}
	}
	if err := c.run(context.Background(), "restart", tunnelServiceOptions{host: "host"}); err != nil {
		t.Fatal(err)
	}
	got, _ := c.configs.Load(config.ID)
	if *got != config {
		t.Fatal("restart changed desired state")
	}
	jobs.beforeInstall = nil
	beforeStops := jobs.stops
	if err := os.WriteFile(store.Path(), []byte(`{"schema_version":99}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := c.run(context.Background(), "restart", tunnelServiceOptions{host: "host"}); err == nil {
		t.Fatal("restart bypassed schema")
	}
	if jobs.stops != beforeStops {
		t.Fatal("invalid restart stopped live job")
	}
}

func TestTunnelRestartAuthenticationFailureLeavesExistingService(t *testing.T) {
	c, jobs, _ := newTestTunnelCommands(t)
	config := testEnabledTunnel(t, c, "host")
	jobs.loaded[config.ID] = true
	jobs.checkState = "auth-required"
	if err := c.run(context.Background(), "restart", tunnelServiceOptions{host: "host"}); err == nil {
		t.Fatal("unverified restart succeeded")
	}
	if jobs.stops != 0 || !jobs.loaded[config.ID] {
		t.Fatal("preflight failure disrupted service")
	}
}

func TestTunnelDisablePersistsBeforeStopAndIsolatesHosts(t *testing.T) {
	c, jobs, _ := newTestTunnelCommands(t)
	first := testEnabledTunnel(t, c, "first")
	second := testEnabledTunnel(t, c, "second")
	jobs.loaded[first.ID], jobs.loaded[second.ID] = true, true
	jobs.beforeStop = func(id string) {
		if id == first.ID {
			config, err := c.configs.Load(id)
			if err != nil || config.Enabled {
				t.Fatal("stop preceded disable persistence")
			}
		}
	}
	for i := 0; i < 2; i++ {
		if err := c.run(context.Background(), "disable", tunnelServiceOptions{host: "first"}); err != nil {
			t.Fatal(err)
		}
	}
	other, _ := c.configs.Load(second.ID)
	if !other.Enabled || !jobs.loaded[second.ID] {
		t.Fatal("other host was affected")
	}
	jobs.stopErr = errors.New("bootout failed")
	if err := c.run(context.Background(), "disable", tunnelServiceOptions{host: "second"}); err == nil {
		t.Fatal("failed stop hidden")
	}
	other, _ = c.configs.Load(second.ID)
	if other.Enabled {
		t.Fatal("stop failure re-enabled desired state")
	}
}

func TestTunnelDisabledEnableCanExplicitlyReconfigure(t *testing.T) {
	c, jobs, _ := newTestTunnelCommands(t)
	config := testEnabledTunnel(t, c, "host")
	config.Enabled = false
	if err := c.configs.Save(config); err != nil {
		t.Fatal(err)
	}
	store, _ := c.configs.RuntimeStore(config.ID)
	if err := store.Reset(config.Spec); err != nil {
		t.Fatal(err)
	}
	if err := c.run(context.Background(), "enable", tunnelServiceOptions{host: "host", port: 18400}); err != nil {
		t.Fatal(err)
	}
	got, err := c.configs.Load(config.ID)
	if err != nil || !got.Enabled || got.Spec.Port != 18400 || jobs.installs != 1 {
		t.Fatalf("reconfigure=%+v %v", got, err)
	}
	rec, err := c.configs.ReadRuntime(*got)
	if err != nil || rec.Spec.Port != 18400 {
		t.Fatalf("runtime=%+v %v", rec, err)
	}
}

func TestTunnelFailedReconfigurationRestoresDisabledSpecAndRuntime(t *testing.T) {
	c, jobs, _ := newTestTunnelCommands(t)
	config := testEnabledTunnel(t, c, "host")
	config.Enabled = false
	if err := c.configs.Save(config); err != nil {
		t.Fatal(err)
	}
	store, _ := c.configs.RuntimeStore(config.ID)
	record := &tunnelmgr.Record{SchemaVersion: 1, Spec: config.Spec, Runtime: tunnelmgr.Runtime{State: tunnelmgr.StateCrashLoop, ConsecutiveStartFailures: 5}}
	if err := store.Save(record); err != nil {
		t.Fatal(err)
	}
	jobs.installErr = errors.New("bootstrap failed")
	if err := c.run(context.Background(), "enable", tunnelServiceOptions{host: "host", port: 18400}); err == nil {
		t.Fatal("failed install succeeded")
	}
	got, err := c.configs.Load(config.ID)
	if err != nil || *got != config {
		t.Fatalf("old disabled configuration not restored: %+v %v", got, err)
	}
	restored, err := c.configs.ReadRuntime(config)
	if err != nil || *restored != *record {
		t.Fatalf("old runtime not restored: %+v %v", restored, err)
	}
	if jobs.loaded[config.ID] {
		t.Fatal("failed install left a job running")
	}
}

func TestTunnelAuthRequiresServiceContextAndPreservesDisabled(t *testing.T) {
	for _, mode := range []string{"enabled", "disabled", "missing", "rejected"} {
		t.Run(mode, func(t *testing.T) {
			c, jobs, _ := newTestTunnelCommands(t)
			var config tunnelmgr.ServiceConfig
			if mode != "missing" {
				config = testEnabledTunnel(t, c, "host")
				if mode == "disabled" {
					config.Enabled = false
					if err := c.configs.Save(config); err != nil {
						t.Fatal(err)
					}
				}
			}
			interactive := false
			c.interactiveAuth = func(context.Context, string) error { interactive = true; return nil }
			if mode == "rejected" {
				jobs.checkState = "auth-required"
			}
			err := c.run(context.Background(), "auth", tunnelServiceOptions{host: "host"})
			if !interactive || jobs.checks != 1 {
				t.Fatal("auth omitted terminal or service-context check")
			}
			if mode == "rejected" {
				if err == nil || jobs.installs != 0 {
					t.Fatal("terminal success bypassed service auth failure")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if mode == "enabled" {
				if jobs.installs != 1 {
					t.Fatal("enabled job not restored")
				}
			} else if jobs.installs != 0 {
				t.Fatal("auth enabled an unrequested service")
			}
		})
	}
}

func TestTunnelAuthFailurePreservesServiceAndConfiguration(t *testing.T) {
	for _, configured := range []string{"missing", "disabled", "enabled"} {
		for _, failure := range []string{"auth-required", "host-key-error", "connection-error", "timeout", "invalid", "preflight-error", "interactive-error"} {
			t.Run(configured+"/"+failure, func(t *testing.T) {
				c, jobs, out := newTestTunnelCommands(t)
				id := tunnelmgr.TunnelID("host")
				path, _ := c.configs.Path(id)
				var runtimePath string
				if configured != "missing" {
					config := testEnabledTunnel(t, c, "host")
					config.Enabled = configured == "enabled"
					if err := c.configs.Save(config); err != nil {
						t.Fatal(err)
					}
					jobs.loaded[id] = config.Enabled
					store, err := c.configs.RuntimeStore(id)
					if err != nil {
						t.Fatal(err)
					}
					if err := store.Reset(config.Spec); err != nil {
						t.Fatal(err)
					}
					runtimePath = store.Path()
				}
				beforeConfig, _ := os.ReadFile(path)
				beforeRuntime, _ := os.ReadFile(runtimePath)
				c.interactiveAuth = func(context.Context, string) error {
					if jobs.checks != 0 {
						t.Fatal("background check ran before foreground authentication")
					}
					if failure == "interactive-error" {
						return errors.New("foreground authentication rejected")
					}
					return nil
				}
				jobs.checkState = failure
				if failure == "preflight-error" {
					jobs.checkState = "ok"
					jobs.checkErr = errors.New("preflight cleanup failed")
				}
				err := c.run(context.Background(), "auth", tunnelServiceOptions{host: "host"})
				if err == nil || jobs.installs != 0 || jobs.stops != 0 || jobs.loaded[id] != (configured == "enabled") {
					t.Fatalf("failed authentication changed job: err=%v jobs=%+v", err, jobs)
				}
				if failure == "interactive-error" && jobs.checks != 0 || failure != "interactive-error" && jobs.checks != 1 {
					t.Fatalf("unexpected background checks: %d", jobs.checks)
				}
				if want := (map[string]string{"auth-required": "authentication failed", "host-key-error": "host key verification failed"})[failure]; want != "" {
					if !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "cc-clip tunnel auth host") {
						t.Fatalf("missing failure/remedy: %v", err)
					}
				}
				afterConfig, _ := os.ReadFile(path)
				afterRuntime, _ := os.ReadFile(runtimePath)
				if !bytes.Equal(beforeConfig, afterConfig) || !bytes.Equal(beforeRuntime, afterRuntime) || out.Len() != 0 {
					t.Fatal("failed authentication changed state or reported success")
				}
			})
		}
	}
}

func TestTunnelStatusIsReadOnlyAndDoesNotTrustStaleHealth(t *testing.T) {
	for _, mode := range []string{"healthy", "stale", "stopped", "disabled", "query-error"} {
		t.Run(mode, func(t *testing.T) {
			c, jobs, out := newTestTunnelCommands(t)
			config := testEnabledTunnel(t, c, "host")
			jobs.loaded[config.ID] = true
			if mode == "disabled" {
				config.Enabled = false
				if err := c.configs.Save(config); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "stopped" {
				jobs.loaded[config.ID] = false
			}
			if mode == "query-error" {
				jobs.inspectErr = errors.New("GUI domain unavailable")
			}
			updated := time.Now()
			if mode == "stale" {
				updated = updated.Add(-time.Hour)
			}
			store, _ := c.configs.RuntimeStore(config.ID)
			if err := store.Save(&tunnelmgr.Record{SchemaVersion: 1, Spec: config.Spec, Runtime: tunnelmgr.Runtime{State: tunnelmgr.StateHealthy, UpdatedAt: updated}}); err != nil {
				t.Fatal(err)
			}
			configPath, _ := c.configs.Path(config.ID)
			beforeSpec, _ := os.ReadFile(configPath)
			beforeRuntime, _ := os.ReadFile(store.Path())
			err := c.run(context.Background(), "status", tunnelServiceOptions{host: "host", port: 18400})
			if mode == "query-error" {
				if err == nil {
					t.Fatal("query error hidden")
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if mode == "healthy" {
				if !strings.Contains(out.String(), "health: healthy") {
					t.Fatal(out.String())
				}
			} else if strings.Contains(out.String(), "health: healthy") {
				t.Fatal("unverified service reported healthy")
			}
			if !strings.Contains(out.String(), "port: persisted=18399, requested=18400") {
				t.Fatal("port mismatch omitted")
			}
			afterSpec, _ := os.ReadFile(configPath)
			afterRuntime, _ := os.ReadFile(store.Path())
			if !bytes.Equal(beforeSpec, afterSpec) || !bytes.Equal(beforeRuntime, afterRuntime) || jobs.checks != 0 || jobs.stops != 0 {
				t.Fatal("status mutated config/runtime/job")
			}
		})
	}
}

func TestTunnelStatusReportsRuntimeSpecDifferencesWithoutMutation(t *testing.T) {
	c, jobs, out := newTestTunnelCommands(t)
	config := testEnabledTunnel(t, c, "host")
	store, _ := c.configs.RuntimeStore(config.ID)
	runtimeSpec := tunnelmgr.Spec{Host: "old-host", Port: 18339, ExpectedInstanceID: "old-instance"}
	if err := store.Save(&tunnelmgr.Record{SchemaVersion: 1, Spec: runtimeSpec, Runtime: tunnelmgr.Runtime{State: tunnelmgr.StateHealthy, UpdatedAt: time.Now()}}); err != nil {
		t.Fatal(err)
	}
	configPath, _ := c.configs.Path(config.ID)
	beforeSpec, _ := os.ReadFile(configPath)
	beforeRuntime, _ := os.ReadFile(store.Path())
	beforeFiles := make(map[string]os.FileInfo)
	for _, path := range []string{configPath, store.Path()} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		beforeFiles[path] = info
	}
	c.localIdentity = func(int) (string, error) { t.Fatal("status probed local identity"); return "", nil }
	c.defaultPort = func() (int, error) { t.Fatal("status resolved an implicit port"); return 0, nil }
	t.Setenv("CC_CLIP_PORT", "18400")
	jobs.loaded[config.ID] = true
	err := c.run(context.Background(), "status", tunnelServiceOptions{host: "host"})
	if err == nil || !strings.Contains(err.Error(), tunnelmgr.ServiceLocationHint) {
		t.Fatalf("missing mismatch/location error: %v", err)
	}
	for _, want := range []string{`host: persisted="old-host", requested="host"`, "port: persisted=18339, requested=18399", `expected_instance_id: persisted="old-instance", requested="instance-123"`, "requested values are the desired service configuration", "experimental", tunnelmgr.ServiceLocationHint} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("status lacks %q: %s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "health: healthy") || strings.Contains(out.String(), "18400") {
		t.Fatal("status trusted stale health or environment defaults")
	}
	afterSpec, _ := os.ReadFile(configPath)
	afterRuntime, _ := os.ReadFile(store.Path())
	if !bytes.Equal(beforeSpec, afterSpec) || !bytes.Equal(beforeRuntime, afterRuntime) || jobs.installs != 0 || jobs.stops != 0 || jobs.checks != 0 {
		t.Fatal("status changed configuration/runtime/job")
	}
	for path, before := range beforeFiles {
		after, err := os.Stat(path)
		if err != nil || !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) {
			t.Fatal("status rewrote a persisted file")
		}
	}
}

func TestTunnelServiceFailuresCarryLocalExecutionHint(t *testing.T) {
	for _, command := range []string{"enable", "status", "restart", "disable", "auth", "logs"} {
		t.Run(command, func(t *testing.T) {
			c, jobs, _ := newTestTunnelCommands(t)
			config := testEnabledTunnel(t, c, "host")
			path, _ := c.configs.Path(config.ID)
			if err := os.WriteFile(path, []byte("{broken"), 0600); err != nil {
				t.Fatal(err)
			}
			err := c.run(context.Background(), command, tunnelServiceOptions{host: "host", lines: 2})
			if err == nil || strings.Count(err.Error(), tunnelmgr.ServiceLocationHint) != 1 {
				t.Fatalf("%s missing/duplicate local-machine hint: %v", command, err)
			}
			if jobs.installs != 0 || jobs.stops != 0 || jobs.checks != 0 {
				t.Fatal("invalid config caused job side effects")
			}
		})
	}
	cause := errors.New("test failure")
	if err := tunnelServiceError(cause); !errors.Is(err, cause) {
		t.Fatal("location hint lost the original error")
	}
	if tunnelServiceError(nil) != nil {
		t.Fatal("location hint turned success into failure")
	}
}

func TestTunnelServiceIdentityMismatchNamesValuesAndLocalMachine(t *testing.T) {
	for _, command := range []string{"enable", "restart", "auth"} {
		t.Run(command, func(t *testing.T) {
			c, jobs, _ := newTestTunnelCommands(t)
			config := testEnabledTunnel(t, c, "host")
			path, _ := c.configs.Path(config.ID)
			before, _ := os.ReadFile(path)
			c.localIdentity = func(int) (string, error) { return "different-instance", nil }
			err := c.run(context.Background(), command, tunnelServiceOptions{host: "host"})
			if err == nil || !strings.Contains(err.Error(), `expected_instance_id: persisted="instance-123", requested="different-instance"`) || !strings.Contains(err.Error(), tunnelmgr.ServiceLocationHint) {
				t.Fatalf("identity mismatch: %v", err)
			}
			after, _ := os.ReadFile(path)
			if !bytes.Equal(before, after) || jobs.installs != 0 || jobs.stops != 0 {
				t.Fatal("identity mismatch changed desired state/job")
			}
		})
	}
	if got := tunnelServiceRemedy("host", tunnelmgr.StateIdentityMismatch, true, true); !strings.Contains(got, tunnelmgr.ServiceLocationHint) {
		t.Fatalf("identity remedy: %s", got)
	}
}

func TestTunnelLogsTailAndPrivateFileValidation(t *testing.T) {
	c, jobs, out := newTestTunnelCommands(t)
	config := testEnabledTunnel(t, c, "host")
	path, _ := jobs.LogPath(config.ID)
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := c.run(context.Background(), "logs", tunnelServiceOptions{host: "host", lines: 2}); err != nil {
		t.Fatal(err)
	}
	if out.String() != "two\nthree\n" {
		t.Fatalf("tail=%q", out.String())
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if err := c.run(context.Background(), "logs", tunnelServiceOptions{host: "host", lines: 2}); err == nil {
		t.Fatal("public log accepted")
	}
}

type synchronizedLogBuffer struct {
	mu      sync.Mutex
	buffer  bytes.Buffer
	written chan struct{}
}

func (b *synchronizedLogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n, err := b.buffer.Write(p)
	select {
	case b.written <- struct{}{}:
	default:
	}
	return n, err
}

func (b *synchronizedLogBuffer) content() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

func TestTunnelLogsFollowAppendsAndCancels(t *testing.T) {
	c, jobs, _ := newTestTunnelCommands(t)
	config := testEnabledTunnel(t, c, "host")
	path, _ := jobs.LogPath(config.ID)
	if err := os.WriteFile(path, []byte("old\n"), 0600); err != nil {
		t.Fatal(err)
	}
	out := &synchronizedLogBuffer{written: make(chan struct{}, 1)}
	c.out = out
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- c.run(ctx, "logs", tunnelServiceOptions{host: "host", lines: 1, follow: true}) }()
	select {
	case <-out.written:
	case <-ctx.Done():
		t.Fatal("initial tail not written")
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := f.WriteString("new\n")
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil {
		t.Fatalf("append log: %v %v", writeErr, closeErr)
	}
	select {
	case <-out.written:
	case <-ctx.Done():
		t.Fatal("appended line not followed")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("follow did not stop on cancellation")
	}
	if got := out.content(); got != "old\nnew\n" {
		t.Fatalf("follow=%q", got)
	}
}

func TestTunnelServiceHelpDocumentsExperimentalCommands(t *testing.T) {
	var out bytes.Buffer
	tunnelUsage(&out)
	for _, want := range []string{"enable <host>", "status [<host>] [--port N]", "restart <host>", "disable <host>", "auth <host>", "logs <host>", "experimental", "local machine running cc-clip serve", "No service command edits", "port: persisted=18339, requested=18340", "status does not probe local identity"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("help lacks %q", want)
		}
	}
}

func TestTunnelRemediesPreserveDistinctFailureStates(t *testing.T) {
	got := []string{tunnelServiceRemedy("host", tunnelmgr.StateAuthRequired, true, false), tunnelServiceRemedy("host", tunnelmgr.StateRemoteTokenInvalid, true, false), tunnelServiceRemedy("host", tunnelmgr.StateIdentityHelperMissing, true, false)}
	want := []string{"cc-clip tunnel auth host", "cc-clip connect host --token-only", "cc-clip connect host --force"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("remedies=%v", got)
	}
}
