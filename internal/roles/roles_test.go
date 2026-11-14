package roles

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeEnv returns a getenv that reads vars and reports every other name as unset.
func fakeEnv(vars map[string]string) func(string) string {
	return func(name string) string { return vars[name] }
}

// writeFile creates path with text, along with any missing parent directories.
func writeFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), os.ModePerm); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestAgentReadsXDGConfigHome(t *testing.T) {
	xdg := t.TempDir()
	home := t.TempDir()
	writeFile(t, filepath.Join(xdg, "orrery", "roles.toml"), `planner = "opus-high-ro"`+"\n")
	writeFile(t, filepath.Join(home, ".config", "orrery", "roles.toml"), `planner = "sonnet-ro"`+"\n")

	got, err := Agent(fakeEnv(map[string]string{"XDG_CONFIG_HOME": xdg, "HOME": home}), "planner")
	if err != nil {
		t.Fatalf("Agent: %v", err)
	}
	if got != "opus-high-ro" {
		t.Fatalf("Agent = %q, want %q: XDG_CONFIG_HOME must win over HOME", got, "opus-high-ro")
	}
}

func TestAgentFallsBackToHomeWhenXDGConfigHomeEmpty(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, ".config", "orrery", "roles.toml"), `planner = "  opus-high-ro  "`+"\n")

	got, err := Agent(fakeEnv(map[string]string{"XDG_CONFIG_HOME": "", "HOME": home}), "planner")
	if err != nil {
		t.Fatalf("Agent: %v", err)
	}
	if got != "opus-high-ro" {
		t.Fatalf("Agent = %q, want the trimmed %q", got, "opus-high-ro")
	}
}

func TestAgentMissingFileNamesPathAndDoesNotCreateIt(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".config", "orrery", "roles.toml")

	_, err := Agent(fakeEnv(map[string]string{"HOME": home}), "planner")
	if err == nil {
		t.Fatal("Agent with no role file returned no error")
	}
	if want := "no role file " + path; !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %q, want it to contain %q", err, want)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("role file was created or stat failed: %v", statErr)
	}
}

func TestAgentFileWithoutRoleNamesPathAndRole(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".config", "orrery", "roles.toml")
	writeFile(t, path, `worker = "sonnet-ro"`+"\n")

	_, err := Agent(fakeEnv(map[string]string{"HOME": home}), "planner")
	if err == nil {
		t.Fatal("Agent for an absent role returned no error")
	}
	if want := "role file " + path + " names no planner agent"; err.Error() != want {
		t.Fatalf("error = %q, want %q", err, want)
	}
}

func TestAgentEmptyAgentNamesPathAndRole(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".config", "orrery", "roles.toml")
	writeFile(t, path, `planner = "   "`+"\n")

	_, err := Agent(fakeEnv(map[string]string{"HOME": home}), "planner")
	if err == nil {
		t.Fatal("Agent for a blank agent returned no error")
	}
	if want := "role file " + path + " names no planner agent"; err.Error() != want {
		t.Fatalf("error = %q, want %q", err, want)
	}
}

func TestAgentNonStringValueNamesPathAndCause(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".config", "orrery", "roles.toml")
	writeFile(t, path, "planner = 3\n")

	_, err := Agent(fakeEnv(map[string]string{"HOME": home}), "planner")
	if err == nil {
		t.Fatal("Agent for a non-string value returned no error")
	}
	for _, want := range []string{path, "planner", "must be a string"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want it to contain %q", err, want)
		}
	}
}

func TestAgentInvalidTOMLNamesPath(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".config", "orrery", "roles.toml")
	writeFile(t, path, "planner = \n")

	_, err := Agent(fakeEnv(map[string]string{"HOME": home}), "planner")
	if err == nil {
		t.Fatal("Agent for invalid TOML returned no error")
	}
	if !strings.Contains(err.Error(), path) {
		t.Fatalf("error = %q, want it to contain %q", err, path)
	}
}

func TestAgentNeitherXDGNorHomeIsAnError(t *testing.T) {
	_, err := Agent(fakeEnv(map[string]string{}), "planner")
	if err == nil {
		t.Fatal("Agent with neither XDG_CONFIG_HOME nor HOME returned no error")
	}
	if want := "neither XDG_CONFIG_HOME nor HOME is set"; !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %q, want it to contain %q", err, want)
	}
}
