package settings

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stageChecksEnv returns the environment of a machine whose variables are vars.
func stageChecksEnv(vars map[string]string) func(string) string {
	return func(key string) string { return vars[key] }
}

// writeStageChecksFile writes content as the settings file of the config directory config, and returns its path.
func writeStageChecksFile(t *testing.T, config, content string) string {
	t.Helper()
	dir := filepath.Join(config, "orrery")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	path := filepath.Join(dir, "settings.toml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

// TestSettingsStageChecksDefaultsWhenAbsent checks that a machine with no settings file reads the default thresholds,
// 41 lines for a warning and 61 for the stronger one, with no error.
func TestSettingsStageChecksDefaultsWhenAbsent(t *testing.T) {
	t.Parallel()
	got, err := ContractLengths(stageChecksEnv(map[string]string{"XDG_CONFIG_HOME": t.TempDir()}))
	if err != nil {
		t.Fatalf("ContractLengths: %v", err)
	}
	if want := (Lengths{WarnLines: 41, StrongWarnLines: 61}); got != want {
		t.Errorf("ContractLengths = %+v, want %+v", got, want)
	}
}

// TestSettingsStageChecksReadsTheContractKeys checks that a settings file's [contract] keys warn_lines and
// strong_warn_lines set the thresholds, and that a key it does not set keeps its default.
func TestSettingsStageChecksReadsTheContractKeys(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		content string
		want    Lengths
	}{
		{
			name:    "both keys",
			content: "[contract]\nwarn_lines = 10\nstrong_warn_lines = 20\n",
			want:    Lengths{WarnLines: 10, StrongWarnLines: 20},
		},
		{
			name:    "warn key only",
			content: "[contract]\nwarn_lines = 30\n",
			want:    Lengths{WarnLines: 30, StrongWarnLines: 61},
		},
		{
			name:    "strong key only",
			content: "[contract]\nstrong_warn_lines = 70\n",
			want:    Lengths{WarnLines: 41, StrongWarnLines: 70},
		},
		{
			name:    "no contract table",
			content: "other = 1\n",
			want:    Lengths{WarnLines: 41, StrongWarnLines: 61},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			config := t.TempDir()
			writeStageChecksFile(t, config, tc.content)
			got, err := ContractLengths(stageChecksEnv(map[string]string{"XDG_CONFIG_HOME": config}))
			if err != nil {
				t.Fatalf("ContractLengths: %v", err)
			}
			if got != tc.want {
				t.Errorf("ContractLengths = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestSettingsStageChecksRefusesValuesThatAreNotPositiveIntegers checks that a threshold that is not a positive integer
// is an error naming the settings file.
func TestSettingsStageChecksRefusesValuesThatAreNotPositiveIntegers(t *testing.T) {
	t.Parallel()
	values := []string{"0", "-3", `"41"`, "4.5", "true"}
	for _, key := range []string{"warn_lines", "strong_warn_lines"} {
		for _, value := range values {
			t.Run(fmt.Sprintf("%s = %s", key, value), func(t *testing.T) {
				t.Parallel()
				config := t.TempDir()
				path := writeStageChecksFile(t, config, fmt.Sprintf("[contract]\n%s = %s\n", key, value))
				_, err := ContractLengths(stageChecksEnv(map[string]string{"XDG_CONFIG_HOME": config}))
				if err == nil {
					t.Fatalf("ContractLengths error = nil, want an error naming %s", path)
				}
				if !strings.Contains(err.Error(), path) {
					t.Errorf("error %q does not name %s", err, path)
				}
			})
		}
	}
}

// TestSettingsStageChecksRefusesAFileThatDoesNotParse checks that a settings file that does not parse is an error
// naming the file.
func TestSettingsStageChecksRefusesAFileThatDoesNotParse(t *testing.T) {
	t.Parallel()
	config := t.TempDir()
	path := writeStageChecksFile(t, config, "[contract\nwarn_lines = 1\n")
	_, err := ContractLengths(stageChecksEnv(map[string]string{"XDG_CONFIG_HOME": config}))
	if err == nil {
		t.Fatalf("ContractLengths error = nil, want an error naming %s", path)
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error %q does not name %s", err, path)
	}
}

// TestSettingsStageChecksReadsXDGBeforeHOME checks that the settings file is $XDG_CONFIG_HOME/orrery/settings.toml when
// XDG_CONFIG_HOME is set, and $HOME/.config/orrery/settings.toml when it is not.
func TestSettingsStageChecksReadsXDGBeforeHOME(t *testing.T) {
	t.Parallel()
	xdg := t.TempDir()
	home := t.TempDir()
	writeStageChecksFile(t, xdg, "[contract]\nwarn_lines = 10\n")
	writeStageChecksFile(t, filepath.Join(home, ".config"), "[contract]\nwarn_lines = 20\n")

	got, err := ContractLengths(stageChecksEnv(map[string]string{"XDG_CONFIG_HOME": xdg, "HOME": home}))
	if err != nil {
		t.Fatalf("ContractLengths with XDG_CONFIG_HOME: %v", err)
	}
	if got.WarnLines != 10 {
		t.Errorf("WarnLines with XDG_CONFIG_HOME = %d, want 10: XDG_CONFIG_HOME must win over HOME", got.WarnLines)
	}

	got, err = ContractLengths(stageChecksEnv(map[string]string{"HOME": home}))
	if err != nil {
		t.Fatalf("ContractLengths with HOME: %v", err)
	}
	if got.WarnLines != 20 {
		t.Errorf("WarnLines with only HOME = %d, want 20 from $HOME/.config", got.WarnLines)
	}
}

// TestSettingsStageChecksRefusesWhenNeitherHomeNorXDGIsSet checks that with neither XDG_CONFIG_HOME nor HOME set there
// is no settings file to read, and that is an error.
func TestSettingsStageChecksRefusesWhenNeitherHomeNorXDGIsSet(t *testing.T) {
	t.Parallel()
	_, err := ContractLengths(stageChecksEnv(map[string]string{}))
	if err == nil {
		t.Fatal("ContractLengths error = nil, want an error that neither variable is set")
	}
}
