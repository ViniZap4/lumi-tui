package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestParseArgs locks in the launch behaviour of `lumi`, `lumi <dir>`,
// `lumi <file.md>`, and `lumi --help`. The most subtle case is when no
// CLI arg is supplied: splash=true only if $LUMI_NOTES_DIR is also
// unset — anyone with the env var pointing at their notes dir wants
// the file browser, not an animation.
func TestParseArgs(t *testing.T) {
	dir := t.TempDir()
	mdPath := filepath.Join(dir, "note.md")
	if err := os.WriteFile(mdPath, []byte("# x\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	nonMd := filepath.Join(dir, "thing.txt")
	if err := os.WriteFile(nonMd, []byte("x"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	type want struct {
		rootDir, initialNote string
		splash, ok           bool
		exitCode             int
	}

	cases := []struct {
		name string
		args []string
		env  map[string]string
		want want
	}{
		{
			name: "no args, no env -> splash on cwd",
			args: nil,
			want: want{rootDir: ".", splash: true, ok: true},
		},
		{
			name: "no args, LUMI_NOTES_DIR set -> tree on env dir",
			args: nil,
			env:  map[string]string{"LUMI_NOTES_DIR": dir},
			want: want{rootDir: dir, splash: false, ok: true},
		},
		{
			name: "explicit dir -> tree, no splash",
			args: []string{dir},
			want: want{rootDir: dir, splash: false, ok: true},
		},
		{
			name: "explicit file -> tree on parent + initial note",
			args: []string{mdPath},
			want: want{rootDir: dir, initialNote: mdPath, splash: false, ok: true},
		},
		{
			name: "non-md file -> error exit",
			args: []string{nonMd},
			want: want{ok: false, exitCode: 1},
		},
		{
			name: "missing path -> error exit",
			args: []string{filepath.Join(dir, "ghost.md")},
			want: want{ok: false, exitCode: 1},
		},
		{
			name: "--help -> exit 0",
			args: []string{"--help"},
			want: want{ok: false, exitCode: 0},
		},
		{
			name: "--version -> exit 0",
			args: []string{"--version"},
			want: want{ok: false, exitCode: 0},
		},
		{
			name: "unknown flag -> usage exit",
			args: []string{"--bogus"},
			want: want{ok: false, exitCode: 64},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Restore env after each case so cases don't leak.
			t.Setenv("LUMI_NOTES_DIR", "")
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			gotRoot, gotInit, gotSplash, gotExit, gotOk := parseArgs(tc.args)
			if gotOk != tc.want.ok {
				t.Fatalf("ok = %v, want %v", gotOk, tc.want.ok)
			}
			if !tc.want.ok {
				if gotExit != tc.want.exitCode {
					t.Fatalf("exitCode = %d, want %d", gotExit, tc.want.exitCode)
				}
				return
			}
			if gotRoot != tc.want.rootDir {
				t.Fatalf("rootDir = %q, want %q", gotRoot, tc.want.rootDir)
			}
			if gotInit != tc.want.initialNote {
				t.Fatalf("initialNote = %q, want %q", gotInit, tc.want.initialNote)
			}
			if gotSplash != tc.want.splash {
				t.Fatalf("splash = %v, want %v", gotSplash, tc.want.splash)
			}
		})
	}
}
