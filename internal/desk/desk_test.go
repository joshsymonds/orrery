package desk_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/desk"
	"github.com/joshsymonds/orrery/internal/deskstream"
)

const (
	deskSound = "/sounds/tick.wav"
	deskInput = 1700000123
	// deskSocket is the --socket path a test passes to the local link.
	deskSocket = "/run/user/1000/orrery/orreryd.sock"
)

// deskRig runs desk.Run on fake ssh, notify-send, pw-play and orrery. Each fake records its argv in dir, one
// invocation per line with NUL between arguments. The fakes come first on PATH and orrery is the fake at dir/orrery.
// Streams are scripts named dir/stream.NAME: the ssh fake runs the stream of the host it is given, and the orrery
// fake runs the stream named local.
type deskRig struct {
	t      *testing.T
	dir    string
	clock  *fakeClock
	cancel context.CancelFunc
	done   chan error
}

// recordEnd closes each invocation's record. The fake writes it as the last argument, so one printf writes a whole
// record, and concurrent fakes cannot interleave their records.
const recordEnd = `\n`

// rigTool is a fake named name that records its argv and then runs body.
func rigTool(dir, name, body string) string {
	return "#!/bin/sh\n" +
		"dir='" + dir + "'\n" +
		"printf '%s\\0' \"$@\" '" + recordEnd + "' >> \"$dir/" + name + ".calls\"\n" +
		body
}

func newDeskRig(t *testing.T) *deskRig {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	sshBody := "host=\nprev=\n" +
		"for a; do\n" +
		"  if [ \"$prev\" = \"--\" ]; then host=$a; fi\n" +
		"  prev=$a\n" +
		"done\n" +
		"exec /bin/sh \"$dir/stream.$host\"\n"
	writeTool(t, filepath.Join(bin, "ssh"), rigTool(dir, "ssh", sshBody))
	writeTool(t, filepath.Join(bin, "notify-send"), rigTool(dir, "notify-send",
		"if [ -e \"$dir/busy\" ]; then echo overlap >> \"$dir/overlap\"; fi\n"+
			"touch \"$dir/busy\"\nsleep 0.02\nrm -f \"$dir/busy\"\n"))
	writeTool(t, filepath.Join(bin, "pw-play"), rigTool(dir, "pw-play", ""))
	writeTool(t, filepath.Join(dir, "orrery"), rigTool(dir, "orrery",
		"exec /bin/sh \"$dir/stream.local\"\n"))
	return &deskRig{t: t, dir: dir, clock: newFakeClock(testStart()), done: make(chan error, 1)}
}

// writeTool writes an executable fake.
func writeTool(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
		t.Fatal(err)
	}
}

// script writes the stream NAME with body after its directory assignment.
func (r *deskRig) script(name, body string) {
	r.t.Helper()
	writeFile(r.t, filepath.Join(r.dir, "stream."+name), "dir='"+r.dir+"'\n"+body)
}

// live writes stream NAME: it records the activity the desk sends on its stdin, prints lines, then stays up until Run cancels it.
func (r *deskRig) live(name string, lines ...string) {
	r.script(name, "exec 3<&0\ncat <&3 > \"$dir/activity."+name+"\" &\n"+
		emit(lines...)+"while :; do sleep 0.01; done\n")
}

// exits writes stream NAME, which fails at once without printing anything.
func (r *deskRig) exits(name string) {
	r.script(name, "exit 1\n")
}

// config is the desk's Config for hosts, with a fixed input and the rig's clock.
func (r *deskRig) config(hosts ...string) desk.Config {
	return desk.Config{
		Orrery: filepath.Join(r.dir, "orrery"),
		Hosts:  hosts,
		Sound:  deskSound,
		Input: func(context.Context) (int64, bool, error) {
			return deskInput, true, nil
		},
		Clock: r.clock,
	}
}

// run starts desk.Run with cfg in its own goroutine.
func (r *deskRig) run(cfg desk.Config) {
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	r.t.Cleanup(cancel)
	go func() { r.done <- desk.Run(ctx, cfg) }()
}

// finish cancels Run and checks that it returned nil.
func (r *deskRig) finish() {
	r.t.Helper()
	r.cancel()
	select {
	case err := <-r.done:
		if err != nil {
			r.t.Fatalf("Run returned %v, want nil", err)
		}
	case <-time.After(ioTimeout):
		r.t.Fatal("Run did not return after ctx ended")
	}
}

// calls returns the argv of each invocation of the fake named name, in the order they were recorded, without the
// program name.
func (r *deskRig) calls(name string) [][]string {
	data, err := os.ReadFile(filepath.Join(r.dir, name+".calls"))
	if err != nil || len(data) == 0 {
		return nil
	}
	var out [][]string
	var record []string
	for field := range strings.SplitSeq(string(data), "\x00") {
		if field == recordEnd {
			out = append(out, record)
			record = nil
			continue
		}
		record = append(record, field)
	}
	return out
}

// notifyArgv is the notify-send argv for one popup.
func notifyArgv(urgency, title, body string) []string {
	return []string{
		"-a", "Coding Agent",
		"-u", urgency,
		"-h", "boolean:suppress-sound:true",
		"--", title, body,
	}
}

// hello is the line a stream prints first.
func hello(host string) string {
	return marshalLine(deskstream.Line{Kind: deskstream.KindHello, Host: host})
}

// notice is a notice line from host in state.
func notice(host, state, title, body string) string {
	return marshalLine(deskstream.Line{Kind: deskstream.KindNotice, Host: host, State: state, Title: title, Body: body})
}

func marshalLine(line deskstream.Line) string {
	b, err := json.Marshal(line)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// containsArgv reports whether argvs has want.
func containsArgv(argvs [][]string, want []string) bool {
	return slices.ContainsFunc(argvs, func(argv []string) bool { return slices.Equal(argv, want) })
}

func TestSSHCommandArgv(t *testing.T) {
	want := []string{
		"ssh", "-T",
		"-o", "BatchMode=yes",
		"-o", "ControlMaster=no",
		"-o", "ControlPath=none",
		"-o", "ServerAliveInterval=15",
		"-o", "ServerAliveCountMax=3",
		"--", "box", "orrery", "notices",
	}
	if got := desk.SSHCommand("box"); !slices.Equal(got, want) {
		t.Fatalf("SSHCommand(box) = %q, want %q", got, want)
	}
}

func TestRunLocalArgv(t *testing.T) {
	tests := []struct {
		name   string
		socket string
		want   []string
	}{
		{name: "default socket", socket: "", want: []string{"notices"}},
		{name: "named socket", socket: deskSocket, want: []string{"--socket", deskSocket, "notices"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rig := newDeskRig(t)
			rig.live("local", hello("local"))
			cfg := rig.config()
			cfg.Socket = tt.socket
			rig.run(cfg)

			waitFor(t, "local orrery run", func() bool { return len(rig.calls("orrery")) == 1 })
			if got := rig.calls("orrery")[0]; !slices.Equal(got, tt.want) {
				t.Fatalf("local argv = %q, want %q", got, tt.want)
			}
			rig.finish()
		})
	}
}

func TestRunSSHArgvPerHost(t *testing.T) {
	rig := newDeskRig(t)
	rig.live("local", hello("local"))
	rig.live("alpha", hello("alpha"))
	rig.live("beta", hello("beta"))
	rig.run(rig.config("alpha", "beta"))

	waitFor(t, "two ssh runs", func() bool { return len(rig.calls("ssh")) == 2 })
	for _, host := range []string{"alpha", "beta"} {
		if !containsArgv(rig.calls("ssh"), desk.SSHCommand(host)[1:]) {
			t.Fatalf("no ssh run with argv %q; ssh ran %q", desk.SSHCommand(host)[1:], rig.calls("ssh"))
		}
	}
	rig.finish()
}

func TestRunPopsUpEachStreamsNotices(t *testing.T) {
	rig := newDeskRig(t)
	rig.live("local", hello("local"), notice("local", "done", "local done", "all green"))
	rig.live("box", hello("box"), notice("box", "waiting", "box waiting", "which branch?"))
	rig.run(rig.config("box"))

	waitFor(t, "two popups", func() bool { return len(rig.calls("notify-send")) == 2 })
	notifies := rig.calls("notify-send")
	for _, want := range [][]string{
		notifyArgv("normal", "local done", "all green"),
		notifyArgv("critical", "box waiting", "which branch?"),
	} {
		if !containsArgv(notifies, want) {
			t.Fatalf("no popup with argv %q; notify-send ran %q", want, notifies)
		}
	}
	waitFor(t, "two sounds", func() bool { return len(rig.calls("pw-play")) == 2 })
	for _, argv := range rig.calls("pw-play") {
		if !slices.Equal(argv, []string{deskSound}) {
			t.Fatalf("pw-play argv = %q, want %q", argv, []string{deskSound})
		}
	}
	rig.finish()
}

func TestRunPopsUpNoticesInOrderOneAtATime(t *testing.T) {
	rig := newDeskRig(t)
	rig.live("local", hello("local"))
	rig.live("box", hello("box"),
		notice("box", "done", "first", "1"),
		notice("box", "done", "second", "2"),
		notice("box", "done", "third", "3"))
	rig.run(rig.config("box"))

	waitFor(t, "three popups", func() bool { return len(rig.calls("notify-send")) == 3 })
	calls := rig.calls("notify-send")
	titles := make([]string, 0, len(calls))
	for _, argv := range calls {
		titles = append(titles, argv[7])
	}
	if want := []string{"first", "second", "third"}; !slices.Equal(titles, want) {
		t.Fatalf("popup titles = %q, want %q", titles, want)
	}
	if _, err := os.Stat(filepath.Join(rig.dir, "overlap")); err == nil {
		t.Fatal("two popups were shown at once")
	}
	waitFor(t, "three sounds", func() bool { return len(rig.calls("pw-play")) == 3 })
	rig.finish()
}

func TestRunPopsUpLostHost(t *testing.T) {
	rig := newDeskRig(t)
	hostname, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	rig.exits("local")
	rig.exits("gone")
	rig.run(rig.config("gone"))

	waitFor(t, "both lost waits", func() bool { return rig.clock.callCount(lostWait) == 2 })
	rig.clock.Advance(lostWait)
	waitFor(t, "two lost popups", func() bool { return len(rig.calls("notify-send")) == 2 })
	notifies := rig.calls("notify-send")
	for _, host := range []string{hostname, "gone"} {
		want := notifyArgv("critical", "orrery desk", host+" unreachable for 5 minutes")
		if !containsArgv(notifies, want) {
			t.Fatalf("no lost popup with argv %q; notify-send ran %q", want, notifies)
		}
	}
	waitFor(t, "two lost sounds", func() bool { return len(rig.calls("pw-play")) == 2 })
	rig.finish()
}

func TestRunSendsActivityAtHello(t *testing.T) {
	rig := newDeskRig(t)
	rig.live("local", hello("local"))
	rig.live("box", hello("box"))
	rig.run(rig.config("box"))

	want := marshalActivity(t, deskInput)
	for _, name := range []string{"local", "box"} {
		waitFor(t, "activity from "+name, func() bool { return len(readLines(rig.dir, "activity."+name)) > 0 })
		if got := readLines(rig.dir, "activity."+name)[0]; got != want {
			t.Fatalf("%s activity = %q, want %q", name, got, want)
		}
	}
	rig.finish()
}

func marshalActivity(t *testing.T, input int64) string {
	t.Helper()
	b, err := json.Marshal(deskstream.Activity{Input: input})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
