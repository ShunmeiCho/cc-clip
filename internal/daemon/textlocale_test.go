package daemon

import (
	"reflect"
	"testing"
)

func TestUTF8TextEnv(t *testing.T) {
	tests := []struct {
		name string
		env  []string
		want []string
	}{
		{
			// The launchd case behind the bug: the plist sets PATH only.
			name: "no locale at all gets LC_CTYPE=UTF-8",
			env:  []string{"PATH=/usr/bin:/bin"},
			want: []string{"PATH=/usr/bin:/bin", "LC_CTYPE=UTF-8"},
		},
		{
			name: "LANG is kept and LC_CTYPE is still set",
			env:  []string{"PATH=/usr/bin", "LANG=en_US.UTF-8"},
			want: []string{"PATH=/usr/bin", "LANG=en_US.UTF-8", "LC_CTYPE=UTF-8"},
		},
		{
			// A name that only looks UTF-8 must not stop the fix: glibc's
			// "utf8" spelling is not a macOS locale.
			name: "a glibc-style LC_CTYPE is replaced",
			env:  []string{"LC_CTYPE=en_US.utf8"},
			want: []string{"LC_CTYPE=UTF-8"},
		},
		{
			name: "non-UTF-8 LC_CTYPE is replaced",
			env:  []string{"LC_CTYPE=C", "HOME=/Users/u"},
			want: []string{"HOME=/Users/u", "LC_CTYPE=UTF-8"},
		},
		{
			// LC_ALL overrides LC_CTYPE, so leaving LC_ALL=C in place would
			// keep pbcopy on the C encoding whatever LC_CTYPE says.
			name: "LC_ALL is dropped",
			env:  []string{"LC_ALL=C", "LANG=en_US.UTF-8"},
			want: []string{"LANG=en_US.UTF-8", "LC_CTYPE=UTF-8"},
		},
		{
			name: "empty LC_ALL and LC_CTYPE entries are dropped too",
			env:  []string{"LC_ALL=", "LC_CTYPE="},
			want: []string{"LC_CTYPE=UTF-8"},
		},
		{
			name: "every repeated LC_ALL and LC_CTYPE entry goes",
			env:  []string{"LC_CTYPE=C", "LANG=C", "LC_ALL=C", "LC_CTYPE=C", "LC_ALL=C"},
			want: []string{"LANG=C", "LC_CTYPE=UTF-8"},
		},
		{
			name: "keys that only share a prefix are kept",
			env:  []string{"LC_ALLX=1", "LC_CTYPES=1", "LANGUAGE=en"},
			want: []string{"LC_ALLX=1", "LC_CTYPES=1", "LANGUAGE=en", "LC_CTYPE=UTF-8"},
		},
		{
			name: "empty environment",
			env:  nil,
			want: []string{"LC_CTYPE=UTF-8"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := utf8TextEnv(tt.env)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("utf8TextEnv(%q) = %q, want %q", tt.env, got, tt.want)
			}
		})
	}
}

// The caller's slice must not change: with spare capacity, an in-place filter
// would overwrite entries the caller still holds.
func TestUTF8TextEnvDoesNotMutateInput(t *testing.T) {
	env := make([]string, 0, 8)
	env = append(env, "LC_ALL=C", "PATH=/usr/bin", "LC_CTYPE=C")
	before := append([]string(nil), env...)

	_ = utf8TextEnv(env)

	if !reflect.DeepEqual(env, before) {
		t.Fatalf("input changed: got %q, want %q", env, before)
	}
}
