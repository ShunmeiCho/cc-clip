package shim

import (
	"encoding/json"
	"strings"
	"testing"
)

// decodeStopCommands is a test helper: pull the stop event's command list out
// of a merged document so assertions read against structure rather than
// against the exact JSON bytes.
func decodeStopCommands(t *testing.T, doc []byte) []map[string]any {
	t.Helper()
	var parsed struct {
		Hooks map[string][]map[string]any `json:"hooks"`
	}
	if err := json.Unmarshal(doc, &parsed); err != nil {
		t.Fatalf("merged document is not valid JSON: %v\n%s", err, doc)
	}
	return parsed.Hooks[cursorStopEvent]
}

// TestMergeCursorHooksCreatesFile covers the empty-remote case: no hooks.json
// yet, so the merge has to produce a complete document including the version
// field Cursor requires.
func TestMergeCursorHooksCreatesFile(t *testing.T) {
	t.Parallel()

	out, changed, err := mergeCursorHooks(nil)
	if err != nil {
		t.Fatalf("mergeCursorHooks: %v", err)
	}
	if !changed {
		t.Fatal("creating a hooks.json from nothing must report changed")
	}

	var parsed struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	if parsed.Version != cursorHooksSchemaVersion {
		t.Errorf("version = %d, want %d", parsed.Version, cursorHooksSchemaVersion)
	}

	cmds := decodeStopCommands(t, out)
	if len(cmds) != 1 {
		t.Fatalf("stop commands = %d, want 1", len(cmds))
	}
	if cmds[0]["command"] != cursorManagedHookCommand {
		t.Errorf("command = %q, want %q", cmds[0]["command"], cursorManagedHookCommand)
	}
}

// TestMergeCursorHooksAbsolutePath pins the property the whole file exists to
// guarantee. Cursor runs a user hook from ~/.cursor with the system
// environment, not from a login shell, so a bare `cc-clip` resolves only by
// luck — the reported failure mode is a hook that exits 127 while Cursor
// reports nothing at all. If this assertion is ever relaxed, the hook silently
// stops firing on exactly the setups cc-clip targets.
func TestMergeCursorHooksAbsolutePath(t *testing.T) {
	t.Parallel()

	if strings.Contains(cursorManagedHookCommand, " cc-clip ") {
		t.Fatalf("hook command resolves cc-clip from PATH: %q", cursorManagedHookCommand)
	}
	if !strings.Contains(cursorManagedHookCommand, `"$HOME/.local/bin/cc-clip"`) {
		t.Errorf("hook command must address the binary absolutely, got %q", cursorManagedHookCommand)
	}
}

// TestMergeCursorHooksPreservesUserContent asserts the merge is a merge: an
// unrelated top-level key, an unrelated event, and a user command inside the
// stop event all survive. Overwriting hooks.json would silently delete hooks
// the user wrote.
func TestMergeCursorHooksPreservesUserContent(t *testing.T) {
	t.Parallel()

	existing := []byte(`{
  "version": 1,
  "somethingElse": {"keep": "me"},
  "hooks": {
    "preToolUse": [{"command": "./audit.sh"}],
    "stop": [{"command": "./commit.sh", "timeout": 120}]
  }
}`)

	out, changed, err := mergeCursorHooks(existing)
	if err != nil {
		t.Fatalf("mergeCursorHooks: %v", err)
	}
	if !changed {
		t.Fatal("adding our command to a user file must report changed")
	}

	var parsed map[string]any
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	if _, ok := parsed["somethingElse"]; !ok {
		t.Error("unrelated top-level key was dropped")
	}
	hooks, _ := parsed["hooks"].(map[string]any)
	if _, ok := hooks["preToolUse"]; !ok {
		t.Error("unrelated event was dropped")
	}

	cmds := decodeStopCommands(t, out)
	if len(cmds) != 2 {
		t.Fatalf("stop commands = %d, want 2 (user + ours)", len(cmds))
	}
	if cmds[0]["command"] != "./commit.sh" {
		t.Errorf("user command lost or reordered: %v", cmds[0])
	}
	if cmds[1]["command"] != cursorManagedHookCommand {
		t.Errorf("our command not appended: %v", cmds[1])
	}
}

// TestMergeCursorHooksIdempotent asserts a second connect against an
// already-correct file writes nothing. Without this, every connect rewrites
// hooks.json and churns its mtime for no reason.
func TestMergeCursorHooksIdempotent(t *testing.T) {
	t.Parallel()

	first, changed, err := mergeCursorHooks(nil)
	if err != nil || !changed {
		t.Fatalf("first merge: changed=%v err=%v", changed, err)
	}
	if _, changed, err := mergeCursorHooks(first); err != nil {
		t.Fatalf("second merge: %v", err)
	} else if changed {
		t.Error("merging an already-current file must be a no-op")
	}
}

// TestMergeCursorHooksReplacesStaleManagedCommand asserts an entry written by
// an older release is REPLACED, not duplicated. Ownership is keyed on the
// prefix rather than the whole command precisely so this case is reachable.
func TestMergeCursorHooksReplacesStaleManagedCommand(t *testing.T) {
	t.Parallel()

	existing := []byte(`{"version":1,"hooks":{"stop":[
	  {"command":"env CC_CLIP_MANAGED=1 cc-clip plugin run cursor-notify","timeout":5}
	]}}`)

	out, changed, err := mergeCursorHooks(existing)
	if err != nil {
		t.Fatalf("mergeCursorHooks: %v", err)
	}
	if !changed {
		t.Fatal("a stale managed command must be rewritten")
	}
	cmds := decodeStopCommands(t, out)
	if len(cmds) != 1 {
		t.Fatalf("stop commands = %d, want 1 (replaced, not duplicated)", len(cmds))
	}
	if cmds[0]["command"] != cursorManagedHookCommand {
		t.Errorf("command = %q, want the current one", cmds[0]["command"])
	}
}

// TestMergeCursorHooksRefusesUnparseableFile asserts cc-clip does not overwrite
// a hooks.json it could not read. The user's hooks are not ours to discard
// because the file happened to be malformed.
func TestMergeCursorHooksRefusesUnparseableFile(t *testing.T) {
	t.Parallel()

	if _, _, err := mergeCursorHooks([]byte(`{"hooks": `)); err == nil {
		t.Fatal("expected an error for malformed JSON, got nil")
	}
}

// TestRemoveCursorManagedHooks asserts the strip removes only our command and
// deletes an event it emptied, so the file returns to its pre-cc-clip shape.
func TestRemoveCursorManagedHooks(t *testing.T) {
	t.Parallel()

	t.Run("keeps user commands", func(t *testing.T) {
		merged, _, err := mergeCursorHooks([]byte(`{"version":1,"hooks":{"stop":[{"command":"./mine.sh"}]}}`))
		if err != nil {
			t.Fatalf("merge: %v", err)
		}
		out, changed, err := removeCursorManagedHooks(merged)
		if err != nil || !changed {
			t.Fatalf("remove: changed=%v err=%v", changed, err)
		}
		cmds := decodeStopCommands(t, out)
		if len(cmds) != 1 || cmds[0]["command"] != "./mine.sh" {
			t.Fatalf("stop commands = %v, want only the user command", cmds)
		}
	})

	t.Run("drops an emptied event", func(t *testing.T) {
		merged, _, err := mergeCursorHooks(nil)
		if err != nil {
			t.Fatalf("merge: %v", err)
		}
		out, changed, err := removeCursorManagedHooks(merged)
		if err != nil || !changed {
			t.Fatalf("remove: changed=%v err=%v", changed, err)
		}
		if cmds := decodeStopCommands(t, out); len(cmds) != 0 {
			t.Fatalf("stop event should be gone, got %v", cmds)
		}
	})

	t.Run("no-op when nothing is ours", func(t *testing.T) {
		_, changed, err := removeCursorManagedHooks([]byte(`{"hooks":{"stop":[{"command":"./mine.sh"}]}}`))
		if err != nil {
			t.Fatalf("remove: %v", err)
		}
		if changed {
			t.Error("removing from a file with no managed command must be a no-op")
		}
	})
}

// TestParseRemoteCursorHooksProbe asserts the sentinel parser extracts exactly
// the file body. Remote shells emit banners and rc chatter around the command
// output, which is why a bare `cat` is not enough.
func TestParseRemoteCursorHooksProbe(t *testing.T) {
	t.Parallel()

	t.Run("extracts body", func(t *testing.T) {
		out := "motd noise\n" + cursorHooksProbeBegin + "\n{\"version\":1}\n" + cursorHooksProbeEnd + "\n"
		got, err := parseRemoteCursorHooksProbe(out)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if string(got) != `{"version":1}` {
			t.Errorf("body = %q", got)
		}
	})

	t.Run("empty when the file is absent", func(t *testing.T) {
		out := cursorHooksProbeBegin + "\n\n" + cursorHooksProbeEnd + "\n"
		got, err := parseRemoteCursorHooksProbe(out)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("body = %q, want empty", got)
		}
	})

	t.Run("errors without sentinels", func(t *testing.T) {
		if _, err := parseRemoteCursorHooksProbe("just noise"); err == nil {
			t.Fatal("expected an error when sentinels are missing")
		}
	})
}
