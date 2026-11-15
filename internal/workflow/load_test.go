package workflow

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"reflect"
	"testing"
)

// epicLiteral is the built-in epic's canonical text, exactly as decision E2 freezes it, and epicDigest is its sha256.
const (
	epicLiteral = "name: epic\nnodes:\n  - id: plan\n    kind: plan\n    body:\n      - id: build\n        kind: build\n"
	epicDigest  = "8e56cd7c52ea852c4deb42ce575ba11e4e6c0e0ca4f201ff03cf24ba0afd8e1e"
)

// repoPath is the repository path Load reads a workflow name from.
func repoPath(name string) string {
	return ".orrery/workflows/" + name + ".yaml"
}

// repo is a Reader over the repository files in files, keyed by path. A path it does not hold is not found.
func repo(files map[string]string) Reader {
	return func(path string) ([]byte, bool, error) {
		text, found := files[path]
		return []byte(text), found, nil
	}
}

// digest is the lowercase hex sha256 of text.
func digest(text string) string {
	sum := sha256.Sum256([]byte(text))
	return fmt.Sprintf("%x", sum)
}

// inFile is a refusal at one line of the file at path.
func inFile(path string, line int, rest string) string {
	return fmt.Sprintf("%s:%d: %s", path, line, rest)
}

// stripOrigin returns nodes with File and Line cleared at every depth, so nodes read from two texts compare equal.
func stripOrigin(nodes []Node) []Node {
	out := make([]Node, 0, len(nodes))
	for _, node := range nodes {
		node.File, node.Line = "", 0
		if node.Body != nil {
			node.Body = stripOrigin(node.Body)
		}
		out = append(out, node)
	}
	return out
}

// loadFlow loads the workflow flow from a repository holding only its file, text.
func loadFlow(t *testing.T, text string) Pinned {
	t.Helper()
	got, err := Load("flow", repo(map[string]string{repoPath("flow"): text}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return got
}

// checkPin fails unless the sha256 is the digest of the text, and the text, parsed as file and checked, gives the same
// nodes as the expanded workflow.
func checkPin(t *testing.T, got Pinned, file string) {
	t.Helper()
	if got.SHA256 != digest(got.Text) {
		t.Fatalf("SHA256 = %s, want the digest of Text %s", got.SHA256, digest(got.Text))
	}
	parsed, err := Parse(file, []byte(got.Text))
	if err != nil {
		t.Fatalf("Parse of Text: %v", err)
	}
	if err = Check(parsed); err != nil {
		t.Fatalf("Check of Text: %v", err)
	}
	if !reflect.DeepEqual(stripOrigin(parsed.Nodes), stripOrigin(got.Workflow.Nodes)) {
		t.Fatalf("Text reparses to %+v, want %+v", parsed.Nodes, got.Workflow.Nodes)
	}
}

func TestLoadBuiltInEpic(t *testing.T) {
	t.Parallel()
	got, err := Load("epic", repo(nil))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Name != "epic" || got.Text != epicLiteral || got.SHA256 != epicDigest {
		t.Fatalf("Load = {%s %q %s}, want the built-in epic {epic %q %s}", got.Name, got.Text, got.SHA256,
			epicLiteral, epicDigest)
	}
	want := []Node{{ID: "plan", Kind: "plan", Body: []Node{{ID: "build", Kind: "build"}}}}
	if !reflect.DeepEqual(stripOrigin(got.Workflow.Nodes), want) {
		t.Fatalf("nodes = %+v, want plan holding build", got.Workflow.Nodes)
	}
	checkPin(t, got, "built-in epic")
}

func TestLoadRepositoryFileReplacesBuiltIn(t *testing.T) {
	t.Parallel()
	draft := lines("name: epic", "nodes:", "  - id: draft", "    kind: plan", "    body:", "      - id: write",
		"        kind: build")
	got, err := Load("epic", repo(map[string]string{repoPath("epic"): draft}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Text != draft {
		t.Fatalf("Text = %q, want the repository file %q", got.Text, draft)
	}
	checkPin(t, got, repoPath("epic"))
}

func TestLoadExpandsIncludesInPlace(t *testing.T) {
	t.Parallel()
	files := map[string]string{
		repoPath("flow"): lines("name: flow", "nodes:",
			"  - id: first", "    kind: plan", "    body:", "      - id: one", "        kind: build",
			"  - include: mid",
			"  - include: epic"),
		repoPath("mid"): lines("name: mid", "nodes:",
			"  - id: second", "    kind: plan", "    body:", "      - id: two", "        kind: build"),
	}
	got, err := Load("flow", repo(files))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := lines("name: flow", "nodes:",
		"  - id: first", "    kind: plan", "    body:", "      - id: one", "        kind: build",
		"  - id: second", "    kind: plan", "    body:", "      - id: two", "        kind: build",
		"  - id: plan", "    kind: plan", "    body:", "      - id: build", "        kind: build")
	if got.Text != want {
		t.Fatalf("Text = %q, want %q", got.Text, want)
	}
	checkPin(t, got, repoPath("flow"))
}

func TestLoadExpandsIncludeInLoopBody(t *testing.T) {
	t.Parallel()
	files := map[string]string{
		repoPath("flow"): lines("name: flow", "nodes:",
			"  - id: rounds", "    loop:", "      max: 3", "      until: second.complete", "    body:",
			"      - include: mid"),
		repoPath("mid"): lines("name: mid", "nodes:",
			"  - id: second", "    kind: plan", "    body:", "      - id: two", "        kind: build"),
	}
	got, err := Load("flow", repo(files))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := lines("name: flow", "nodes:",
		"  - id: rounds", "    loop:", "      max: 3", "      until: second.complete", "      spent: park",
		"    body:",
		"      - id: second", "        kind: plan", "        body:", "          - id: two", "            kind: build")
	if got.Text != want {
		t.Fatalf("Text = %q, want %q", got.Text, want)
	}
	checkPin(t, got, repoPath("flow"))
}

func TestLoadRefuses(t *testing.T) {
	t.Parallel()
	flow := repoPath("flow")
	cases := []struct {
		name  string
		load  string
		files map[string]string
		want  string
	}{
		{
			name:  "include cycle in one file",
			load:  "flow",
			files: map[string]string{flow: lines("name: flow", "nodes:", "  - include: flow")},
			want:  inFile(flow, 3, "node #1: key include: include cycle flow -> flow"),
		},
		{
			name: "include cycle through a second file",
			load: "a",
			files: map[string]string{
				repoPath("a"): lines("name: a", "nodes:", "  - include: b"),
				repoPath("b"): lines("name: b", "nodes:", "  - include: a"),
			},
			want: inFile(repoPath("b"), 3, "node #1: key include: include cycle a -> b -> a"),
		},
		{
			name: "include cycle through a third file",
			load: "a",
			files: map[string]string{
				repoPath("a"): lines("name: a", "nodes:", "  - include: b"),
				repoPath("b"): lines("name: b", "nodes:", "  - include: c"),
				repoPath("c"): lines("name: c", "nodes:", "  - include: a"),
			},
			want: inFile(repoPath("c"), 3, "node #1: key include: include cycle a -> b -> c -> a"),
		},
		{
			name: "included file's refusal names that file",
			load: "flow",
			files: map[string]string{
				flow:            lines("name: flow", "nodes:", "  - include: mid"),
				repoPath("mid"): lines("name: mid", "nodes:", "  - id: x", "    kind: survey"),
			},
			want: inFile(repoPath("mid"), 4, `node x: key kind: unknown kind "survey"`),
		},
		{
			name: "duplicate id across files",
			load: "flow",
			files: map[string]string{
				flow: lines("name: flow", "nodes:",
					"  - id: p", "    kind: plan", "    body:", "      - id: one", "        kind: build",
					"  - include: other"),
				repoPath("other"): lines("name: other", "nodes:",
					"  - id: p", "    kind: plan", "    body:", "      - id: two", "        kind: build"),
			},
			want: inFile(repoPath("other"), 3, "node p: key id: already used at "+flow+":3"),
		},
		{
			name: "same file included twice",
			load: "flow",
			files: map[string]string{
				flow: lines("name: flow", "nodes:", "  - include: mid", "  - include: mid"),
				repoPath("mid"): lines("name: mid", "nodes:",
					"  - id: second", "    kind: plan", "    body:", "      - id: two", "        kind: build"),
			},
			want: inFile(flow, 4, `node #2: key include: "mid" is included more than once`),
		},
		{
			name: "loop until names no node after expansion",
			load: "flow",
			files: map[string]string{
				flow: lines("name: flow", "nodes:",
					"  - id: rounds", "    loop:", "      max: 2", "      until: nope.complete", "    body:",
					"      - include: mid"),
				repoPath("mid"): lines("name: mid", "nodes:",
					"  - id: second", "    kind: plan", "    body:", "      - id: two", "        kind: build"),
			},
			want: inFile(flow, 3, `node rounds: key loop.until: "nope" is not a node in the loop body`),
		},
		{
			name: "loop until names an outcome the included kind lacks",
			load: "flow",
			files: map[string]string{
				flow: lines("name: flow", "nodes:",
					"  - id: rounds", "    loop:", "      max: 2", "      until: second.done", "    body:",
					"      - include: mid"),
				repoPath("mid"): lines("name: mid", "nodes:",
					"  - id: second", "    kind: plan", "    body:", "      - id: two", "        kind: build"),
			},
			want: inFile(flow, 3, `node rounds: key loop.until: second has no outcome "done"`),
		},
		{
			name: "name with no file and no built-in",
			load: "flow",
			want: `workflow "flow": no file ` + flow + ` and no built-in "flow"`,
		},
		{
			name:  "include with no file and no built-in",
			load:  "flow",
			files: map[string]string{flow: lines("name: flow", "nodes:", "  - include: other")},
			want: inFile(flow, 3,
				`node #1: key include: no file `+repoPath("other")+` and no built-in "other"`),
		},
		{
			name: "bad name",
			load: "Flow",
			want: `workflow name "Flow" must match ^[a-z][a-z0-9_-]*$`,
		},
		{
			name: "empty name",
			load: "",
			want: `workflow name "" must match ^[a-z][a-z0-9_-]*$`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := Load(tc.load, repo(tc.files))
			if err == nil {
				t.Fatalf("Load accepted %q; want refusal %q", tc.load, tc.want)
			}
			if err.Error() != tc.want {
				t.Fatalf("Load refusal = %q, want %q", err.Error(), tc.want)
			}
		})
	}
}

// TestLoadRefusesDoubledIncludeChain builds files w00 to w15, each including the next file twice, and w16, a plan. Expanded
// in full, the chain holds 2^16 copies of w16; Load refuses the second include at w15, the first file to repeat one.
func TestLoadRefusesDoubledIncludeChain(t *testing.T) {
	t.Parallel()
	const depth = 16
	files := map[string]string{}
	for i := range depth {
		name := fmt.Sprintf("w%02d", i)
		next := fmt.Sprintf("w%02d", i+1)
		files[repoPath(name)] = lines("name: "+name, "nodes:", "  - include: "+next, "  - include: "+next)
	}
	last := fmt.Sprintf("w%02d", depth)
	files[repoPath(last)] = lines("name: "+last, "nodes:",
		"  - id: plan", "    kind: plan", "    body:", "      - id: build", "        kind: build")
	_, err := Load("w00", repo(files))
	want := inFile(repoPath("w15"), 4, `node #2: key include: "w16" is included more than once`)
	if err == nil || err.Error() != want {
		t.Fatalf("Load refusal = %v, want %q", err, want)
	}
}

func TestLoadReturnsReadError(t *testing.T) {
	t.Parallel()
	boom := errors.New("read failed")
	flow := repoPath("flow")
	// failing returns a Reader that fails to read failPath and reads the other paths from files.
	failing := func(failPath string, files map[string]string) Reader {
		return func(path string) ([]byte, bool, error) {
			if path == failPath {
				return nil, false, boom
			}
			text, found := files[path]
			return []byte(text), found, nil
		}
	}
	_, err := Load("flow", failing(flow, nil))
	if !errors.Is(err, boom) {
		t.Fatalf("Load error = %v, want the read error %v", err, boom)
	}
	included := map[string]string{flow: lines("name: flow", "nodes:", "  - include: other")}
	_, err = Load("flow", failing(repoPath("other"), included))
	if !errors.Is(err, boom) {
		t.Fatalf("Load error = %v, want the included file's read error %v", err, boom)
	}
}

func TestLoadCommentEditKeepsPin(t *testing.T) {
	t.Parallel()
	plain := lines("name: flow", "nodes:", "  - id: plan", "    kind: plan", "    body:", "      - id: build",
		"        kind: build")
	commented := lines("# the flow the team runs", "name: flow  # its name", "nodes:", "  # the plan",
		"  - id: plan", "    kind: plan", "    body:", "      - id: build  # compiles", "        kind: build", "",
		"# end")
	before := loadFlow(t, plain)
	checkPin(t, before, repoPath("flow"))
	after := loadFlow(t, commented)
	if after.Text != before.Text || after.SHA256 != before.SHA256 {
		t.Fatalf("comment edit moved the pin: Text %q -> %q, SHA256 %s -> %s", before.Text, after.Text,
			before.SHA256, after.SHA256)
	}
}

func TestLoadNodeEditChangesPin(t *testing.T) {
	t.Parallel()
	before := loadFlow(t, lines("name: flow", "nodes:", "  - id: plan", "    kind: plan", "    body:",
		"      - id: build", "        kind: build"))
	after := loadFlow(t, lines("name: flow", "nodes:", "  - id: plan", "    kind: plan", "    body:",
		"      - id: compile", "        kind: build"))
	if after.Text == before.Text || after.SHA256 == before.SHA256 {
		t.Fatalf("node edit left the pin: Text %q, SHA256 %s", after.Text, after.SHA256)
	}
}
