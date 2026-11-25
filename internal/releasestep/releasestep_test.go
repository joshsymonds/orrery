package releasestep

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

// pinParams are the parameters of a well-formed pin-flake step.
func pinParams() Params {
	return Params{
		Branch: "main",
		Prefix: "v",
		Flake:  "~/nix-config",
		Input:  "orrery",
		URL:    "github:joshsymonds/orrery/" + TagSlot,
	}
}

// updateParams are the parameters of a well-formed update step.
func updateParams() Params {
	return Params{Branch: "main", Flake: "/home/owner/nix-config", Run: []string{"update"},
		Units: []string{"orreryd", "orrery-serve"}}
}

// TestProblemAcceptsEachKindAndAgentStep: an agent step with no parameters and each kind with exactly its own
// parameters are well formed.
func TestProblemAcceptsEachKindAndAgentStep(t *testing.T) {
	t.Parallel()
	cases := map[string]Params{
		"":           {},
		KindPushMain: {Branch: "main"},
		KindTag:      {Prefix: "v"},
		KindPinFlake: pinParams(),
		KindUpdate:   updateParams(),
	}
	for kind, params := range cases {
		if field, reason := Problem(kind, params); field != "" || reason != "" {
			t.Errorf("Problem(%q, %+v) = %q, %q, want none", kind, params, field, reason)
		}
	}
}

// TestProblemRefusesEachMalformedStep: each refusal names the first refused parameter and why.
func TestProblemRefusesEachMalformedStep(t *testing.T) {
	t.Parallel()
	withPin := func(change func(*Params)) Params {
		params := pinParams()
		change(&params)
		return params
	}
	withUpdate := func(change func(*Params)) Params {
		params := updateParams()
		change(&params)
		return params
	}
	cases := []struct {
		name, kind string
		params     Params
		field      string
		reason     string
	}{
		{"unknown kind", "deploy", Params{}, "kind", `unknown kind "deploy"`},
		{"agent step with a parameter", "", Params{Prefix: "v"}, ParamPrefix, reasonNoKind},
		{"missing branch", KindPushMain, Params{}, ParamBranch, "missing: a push-main step needs it"},
		{"blank branch is missing", KindPushMain, Params{Branch: "  "}, ParamBranch, "missing"},
		{"parameter of another kind", KindTag, Params{Prefix: "v", Branch: "main"}, ParamBranch,
			"not a parameter of a tag step"},
		{"glob in prefix", KindTag, Params{Prefix: "v*"}, ParamPrefix, reasonPrefix},
		{"relative flake", KindPinFlake, withPin(func(p *Params) { p.Flake = "nix-config" }), ParamFlake, reasonFlake},
		{"bad input", KindPinFlake, withPin(func(p *Params) { p.Input = "1orrery" }), ParamInput, reasonInput},
		{"url with no slot", KindPinFlake, withPin(func(p *Params) { p.URL = "github:o/r/v1" }), ParamURL, reasonURL},
		{"url with two slots", KindPinFlake, withPin(func(p *Params) { p.URL = TagSlot + TagSlot }), ParamURL,
			reasonURL},
		{"missing units", KindUpdate, withUpdate(func(p *Params) { p.Units = nil }), ParamUnits, "missing"},
		{"blank run element", KindUpdate, withUpdate(func(p *Params) { p.Run = []string{"update", " "} }), ParamRun,
			reasonBlank},
		{"blank unit", KindUpdate, withUpdate(func(p *Params) { p.Units = []string{""} }), ParamUnits, reasonBlank},
	}
	for _, c := range cases {
		field, reason := Problem(c.kind, c.params)
		if field != c.field || !strings.Contains(reason, c.reason) {
			t.Errorf("%s: Problem = %q, %q, want field %q with a reason holding %q", c.name, field, reason, c.field,
				c.reason)
		}
	}
}

// TestArgvNamesEachKindsFlags: Argv builds `release-step <kind> -candidate <sha>` and then the kind's flags, with an
// update step's units as repeated -unit flags and its run argv after `--`.
func TestArgvNamesEachKindsFlags(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		params Params
		want   []string
	}{
		KindPushMain: {Params{Branch: "main"}, []string{"-branch", "main"}},
		KindTag:      {Params{Prefix: "v"}, []string{"-prefix", "v", "-title", "the change"}},
		KindPinFlake: {pinParams(), []string{"-branch", "main", "-prefix", "v", "-flake", "~/nix-config", "-input",
			"orrery", "-url", "github:joshsymonds/orrery/{tag}"}},
		KindUpdate: {updateParams(), []string{"-branch", "main", "-flake", "/home/owner/nix-config", "-unit",
			"orreryd", "-unit", "orrery-serve", "--", "update"}},
	}
	for kind, c := range cases {
		got := Argv("/bin/orrery", kind, c.params, "abc123", "the change")
		want := append([]string{"/bin/orrery", Command, kind, "-candidate", "abc123"}, c.want...)
		if !slices.Equal(got, want) {
			t.Errorf("Argv(%s) = %q, want %q", kind, got, want)
		}
	}
}

// TestParseArgsReadsWhatArgvWrites: the command reads back the kind, candidate, title and parameters of every argv
// Argv builds.
func TestParseArgsReadsWhatArgvWrites(t *testing.T) {
	t.Parallel()
	cases := map[string]Params{
		KindPushMain: {Branch: "main"},
		KindTag:      {Prefix: "v"},
		KindPinFlake: pinParams(),
		KindUpdate:   updateParams(),
	}
	for kind, params := range cases {
		inv, err := parseArgs(Argv("orrery", kind, params, "abc123", "a title")[2:])
		if err != nil {
			t.Errorf("%s: parseArgs: %v", kind, err)
			continue
		}
		wantTitle := ""
		if kind == KindTag {
			wantTitle = "a title"
		}
		if inv.kind != kind || inv.candidate != "abc123" || inv.title != wantTitle ||
			inv.params.Branch != params.Branch || inv.params.Prefix != params.Prefix ||
			inv.params.Flake != params.Flake || inv.params.Input != params.Input || inv.params.URL != params.URL ||
			!slices.Equal(inv.params.Run, params.Run) || !slices.Equal(inv.params.Units, params.Units) {
			t.Errorf("%s: parseArgs = %+v, want kind %s, candidate abc123, title %q and %+v", kind, inv, kind,
				wantTitle, params)
		}
	}
}

// TestMainRefusesMalformedCommandLineWithUsageStatus: a command line with no kind, an unknown kind, no candidate, a
// missing or foreign flag, a parameter of the wrong form or a stray argument exits 2 and runs nothing.
func TestMainRefusesMalformedCommandLineWithUsageStatus(t *testing.T) {
	t.Parallel()
	cases := [][]string{
		{},
		{"deploy", "-candidate", "abc"},
		{KindPushMain, "-branch", "main"},
		{KindPushMain, "-candidate", "abc"},
		{KindPushMain, "-candidate", "abc", "-branch", "main", "-prefix", "v"},
		{KindTag, "-candidate", "abc", "-prefix", "v*"},
		{KindPushMain, "-candidate", "abc", "-branch", "main", "extra"},
		{KindUpdate, "-candidate", "abc", "-branch", "main", "-flake", "/f", "-unit", "orreryd"},
	}
	for _, args := range cases {
		if _, err := parseArgs(args); !errors.Is(err, errUsage) {
			t.Errorf("parseArgs(%q) = %v, want a usage error", args, err)
		}
	}
	w := newWorld(t)
	res := w.run(KindPushMain, "-candidate", w.base)
	wantCode(t, res, exitUsage)
	wantOut(t, "stderr", res.stderr, "branch: missing", usageLine)
	if res.stdout != "" || len(w.machine.calls) != 0 {
		t.Errorf("a refused command line printed %q and ran %v", res.stdout, w.machine.calls)
	}
}

// TestParseTagListingReadsPeeledVersionTagsNewestFirst: an annotated tag's commit is its peeled line's, a lightweight
// tag's is its own line's, a tag that is not prefix then three plain numbers is left out, and versions order
// numerically.
func TestParseTagListingReadsPeeledVersionTagsNewestFirst(t *testing.T) {
	t.Parallel()
	listing := strings.Join([]string{
		"aaaa\trefs/tags/v0.9.0",
		"bbbb\trefs/tags/v0.10.0",
		"cccc\trefs/tags/v0.10.0^{}",
		"dddd\trefs/tags/v0.10.1-rc1",
		"eeee\trefs/tags/v01.0.0",
		"ffff\trefs/tags/vx.1.2",
		"1111\trefs/tags/v0.2.0^{}",
		"2222\trefs/tags/v0.2.0",
		"3333\trefs/heads/v9.9.9",
	}, "\n")
	got := parseTagListing("v", listing)
	want := []versionTag{
		{name: "v0.10.0", version: version{0, 10, 0}, commit: "cccc"},
		{name: "v0.9.0", version: version{0, 9, 0}, commit: "aaaa"},
		{name: "v0.2.0", version: version{0, 2, 0}, commit: "1111"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("parseTagListing = %+v, want %+v", got, want)
	}
}

// TestInputURLNeedsExactlyOneLine: the url line is found in either form, a follows line of the same input is not one,
// and none or two refuse.
func TestInputURLNeedsExactlyOneLine(t *testing.T) {
	t.Parallel()
	text := "  orrery.url = \"github:o/orrery/v1.0.0\";\n  orrery.inputs.nixpkgs.follows = \"nixpkgs\";\n"
	if got, err := inputURL(text, "orrery"); err != nil || got != "github:o/orrery/v1.0.0" {
		t.Errorf("inputURL = %q, %v, want the url", got, err)
	}
	if got, err := inputURL("inputs.orrery.url = \"u\";\n", "orrery"); err != nil || got != "u" {
		t.Errorf("inputURL of inputs.orrery.url = %q, %v, want u", got, err)
	}
	if _, err := inputURL("# orrery.url = \"u\";\n", "orrery"); err == nil {
		t.Error("inputURL of a commented-out line found a url")
	}
	if _, err := inputURL(text+text, "orrery"); err == nil {
		t.Error("inputURL of two url lines found a url")
	}
	got, err := setInputURL(text, "orrery", "github:o/orrery/v1.0.1")
	want := strings.Replace(text, "v1.0.0", "v1.0.1", 1)
	if err != nil || got != want {
		t.Errorf("setInputURL = %q, %v, want %q", got, err, want)
	}
}
