package open

import (
	"slices"
	"strings"
	"testing"
)

// proposeClaude installs the fake claude printing reply. HOME and CLAUDE_CONFIG_DIR are set so that the run's argv
// resolves its user memory path.
func proposeClaude(t *testing.T, reply string) string {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	return installClaude(t, reply, 0)
}

// proposeHosts returns two hosts: a, with one folder, and b, with two. b is listed second and reports more load.
func proposeHosts() []HostChoice {
	return []HostChoice{
		{Name: "a", LoadPercent: 5, MemoryPercent: 30, Folders: []string{"/srv/a-one"}},
		{Name: "b", LoadPercent: 12, MemoryPercent: 40, Folders: []string{"/srv/b-one", "/srv/b-two"}},
	}
}

func TestStartProposalsOpenSendsHostsToModel(t *testing.T) {
	claude := proposeClaude(t, "a /srv/a-one\n")
	if _, err := Propose(t.Context(), claude, "example text", proposeHosts()); err != nil {
		t.Fatalf("Propose: %v", err)
	}
	runs := claudeRuns(t, claude)
	if len(runs) != 1 {
		t.Fatalf("claude ran %d times, want 1", len(runs))
	}
	want := proposeInstruction + "\nTEXT:\nexample text\n" +
		"\nHOSTS:\n" +
		"host a load 5% memory 30%\n/srv/a-one\n" +
		"host b load 12% memory 40%\n/srv/b-one\n/srv/b-two\n"
	if runs[0].stdin != want {
		t.Errorf("claude stdin = %q, want %q", runs[0].stdin, want)
	}
}

func TestStartProposalsOpenDropsUnknownHostAndFolder(t *testing.T) {
	// c is not a listed host; /srv/b-one is listed only under b, so a may not name it; /srv/nowhere is listed nowhere.
	reply := "c /srv/a-one\na /srv/b-one\nb /srv/nowhere\na /srv/a-one\n"
	claude := proposeClaude(t, reply)
	got, err := Propose(t.Context(), claude, "example text", proposeHosts())
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	want := []Proposal{{Host: "a", Dir: "/srv/a-one"}}
	if !slices.Equal(got, want) {
		t.Errorf("Propose = %v, want %v", got, want)
	}
}

func TestStartProposalsOpenRanks(t *testing.T) {
	// The reply ranks b's second folder first and repeats it last; the repeat is kept once and the order is the
	// reply's, not the hosts'.
	reply := "b /srv/b-two\na /srv/a-one\nb /srv/b-one\nb /srv/b-two\n"
	claude := proposeClaude(t, reply)
	got, err := Propose(t.Context(), claude, "example text", proposeHosts())
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	want := []Proposal{
		{Host: "b", Dir: "/srv/b-two"},
		{Host: "a", Dir: "/srv/a-one"},
		{Host: "b", Dir: "/srv/b-one"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("Propose = %v, want %v", got, want)
	}
}

func TestStartProposalsOpenNoValidProposal(t *testing.T) {
	for _, tc := range []struct {
		name  string
		reply string
	}{
		{name: "empty reply", reply: ""},
		{name: "only invalid lines", reply: "c /srv/a-one\nb /srv/a-one\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			claude := proposeClaude(t, tc.reply)
			_, err := Propose(t.Context(), claude, "example text", proposeHosts())
			if err == nil {
				t.Fatal("Propose returned nil error")
			}
			if !strings.Contains(err.Error(), "no valid proposal") {
				t.Errorf("Propose error = %v, want one saying the model chose no valid proposal", err)
			}
		})
	}
}

func TestStartProposalsOpenBlankTextOrNoHostsIsErrorBeforeModel(t *testing.T) {
	for _, tc := range []struct {
		name  string
		text  string
		hosts []HostChoice
	}{
		{name: "blank text", text: " \n\t", hosts: proposeHosts()},
		{name: "no hosts", text: "example text", hosts: nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			claude := proposeClaude(t, "a /srv/a-one\n")
			if _, err := Propose(t.Context(), claude, tc.text, tc.hosts); err == nil {
				t.Fatal("Propose returned nil error")
			}
			if runs := claudeRuns(t, claude); len(runs) != 0 {
				t.Errorf("claude ran %d times, want 0", len(runs))
			}
		})
	}
}
