package desk_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/desk"
)

// fakeTools puts a fake notify-send and pw-play first on PATH. Each records its argv NUL-separated
// in <name>.args under the returned directory and appends its name to order, so a test reads the
// argv and the run sequence. A tool listed in fail exits 1 after recording.
func fakeTools(t *testing.T, fail ...string) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	order := filepath.Join(dir, "order")
	for _, name := range []string{"notify-send", "pw-play"} {
		exit := "exit 0"
		if slices.Contains(fail, name) {
			exit = "exit 1"
		}
		script := "#!/bin/sh\n" +
			"echo " + name + " >> " + order + "\n" +
			"for a; do printf '%s\\0' \"$a\"; done > " + argsPath(dir, name) + "\n" +
			exit + "\n"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func argsPath(dir, name string) string {
	return filepath.Join(dir, name+".args")
}

func recordedArgs(t *testing.T, dir, name string) []string {
	t.Helper()
	data, err := os.ReadFile(argsPath(dir, name))
	if err != nil {
		t.Fatalf("%s did not run: %v", name, err)
	}
	return strings.Split(strings.TrimSuffix(string(data), "\x00"), "\x00")
}

func recordedOrder(t *testing.T, dir string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "order"))
	if err != nil {
		t.Fatalf("no tool ran: %v", err)
	}
	return strings.Fields(string(data))
}

func TestPopupNotifyArgv(t *testing.T) {
	const title, body = "Build done", "all green"
	tests := []struct {
		name    string
		waiting bool
		urgency string
	}{
		{name: "waiting is critical", waiting: true, urgency: "critical"},
		{name: "not waiting is normal", waiting: false, urgency: "normal"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := fakeTools(t)
			if err := desk.Popup(context.Background(), title, body, tt.waiting, "/sounds/tick.wav"); err != nil {
				t.Fatalf("Popup: %v", err)
			}
			want := []string{
				"-a", "Coding Agent",
				"-u", tt.urgency,
				"-h", "boolean:suppress-sound:true",
				"--", title, body,
			}
			if got := recordedArgs(t, dir, "notify-send"); !slices.Equal(got, want) {
				t.Fatalf("notify-send argv = %q, want %q", got, want)
			}
		})
	}
}

func TestPopupTitleAndBodyPassedVerbatimAfterSeparator(t *testing.T) {
	title := "--no-preserve-root $HOME"
	body := "say \"hi\" 'there' `id`\nline two $(false)"
	dir := fakeTools(t)
	if err := desk.Popup(context.Background(), title, body, false, "/sounds/tick.wav"); err != nil {
		t.Fatalf("Popup: %v", err)
	}
	got := recordedArgs(t, dir, "notify-send")
	if len(got) != 9 {
		t.Fatalf("notify-send got %d args, want 9: %q", len(got), got)
	}
	if got[6] != "--" {
		t.Fatalf("argv[6] = %q, want the option terminator", got[6])
	}
	if got[7] != title {
		t.Fatalf("title = %q, want %q", got[7], title)
	}
	if got[8] != body {
		t.Fatalf("body = %q, want %q", got[8], body)
	}
}

func TestPopupPlaysSoundAfterNotify(t *testing.T) {
	dir := fakeTools(t)
	if err := desk.Popup(context.Background(), "t", "b", true, "/sounds/tick.wav"); err != nil {
		t.Fatalf("Popup: %v", err)
	}
	if got, want := recordedOrder(t, dir), []string{"notify-send", "pw-play"}; !slices.Equal(got, want) {
		t.Fatalf("run order = %q, want %q", got, want)
	}
	if got, want := recordedArgs(t, dir, "pw-play"), []string{"/sounds/tick.wav"}; !slices.Equal(got, want) {
		t.Fatalf("pw-play argv = %q, want %q", got, want)
	}
}

func TestPopupReturnsFailures(t *testing.T) {
	tests := []struct {
		name     string
		fail     []string
		wantSubs []string
	}{
		{name: "notify-send fails", fail: []string{"notify-send"}, wantSubs: []string{"notify-send"}},
		{name: "pw-play fails", fail: []string{"pw-play"}, wantSubs: []string{"pw-play"}},
		{name: "both fail", fail: []string{"notify-send", "pw-play"}, wantSubs: []string{"notify-send", "pw-play"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := fakeTools(t, tt.fail...)
			err := desk.Popup(context.Background(), "t", "b", false, "/sounds/tick.wav")
			if err == nil {
				t.Fatal("Popup returned nil, want the failure")
			}
			for _, sub := range tt.wantSubs {
				if !strings.Contains(err.Error(), sub) {
					t.Fatalf("error %q does not name %s", err, sub)
				}
			}
			if got, want := recordedOrder(t, dir), []string{"notify-send", "pw-play"}; !slices.Equal(got, want) {
				t.Fatalf("run order = %q, want both tools to run: %q", got, want)
			}
		})
	}
}
