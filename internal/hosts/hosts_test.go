package hosts_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/hosts"
)

// envWith returns the environment that names config as XDG_CONFIG_HOME.
func envWith(config string) func(string) string {
	return func(key string) string {
		if key == "XDG_CONFIG_HOME" {
			return config
		}
		return ""
	}
}

// hostsEnv writes content to the hosts file under a fresh XDG_CONFIG_HOME. It returns the environment that names that
// config and the file's path.
func hostsEnv(t *testing.T, content string) (func(string) string, string) {
	t.Helper()
	config := t.TempDir()
	path := filepath.Join(config, "orrery", "hosts.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return envWith(config), path
}

// requireRefused asserts that Load refuses content, and that the error names the file and the offending entry.
func requireRefused(t *testing.T, content, offending string) {
	t.Helper()
	getenv, path := hostsEnv(t, content)
	_, err := hosts.Load(getenv)
	if err == nil {
		t.Fatalf("Load accepted %q, want a refusal", content)
	}
	for _, want := range []string{path, offending} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Load error %q does not name %q", err, want)
		}
	}
}

func TestHostsFileMissingIsEmpty(t *testing.T) {
	file, err := hosts.Load(envWith(t.TempDir()))
	if err != nil {
		t.Fatalf("Load with no hosts file: %v", err)
	}
	if !reflect.DeepEqual(file, hosts.File{}) {
		t.Errorf("Load with no hosts file = %+v, want the zero File", file)
	}
}

func TestHostsFileReadsNameAndHostsInOrder(t *testing.T) {
	getenv, _ := hostsEnv(t, `name = "desktop"

[[hosts]]
name = "workstation"
address = "workstation.example.ts.net:7443"

[[hosts]]
name = "laptop"
address = "laptop.example.ts.net:7443"
`)
	file, err := hosts.Load(getenv)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := hosts.File{Name: "desktop", Hosts: []hosts.Host{
		{Name: "workstation", Address: "workstation.example.ts.net:7443"},
		{Name: "laptop", Address: "laptop.example.ts.net:7443"},
	}}
	if !reflect.DeepEqual(file, want) {
		t.Errorf("Load = %+v, want %+v", file, want)
	}
}

func TestHostsFileRefusesUnknownKey(t *testing.T) {
	requireRefused(t, `name = "desktop"

[[hosts]]
name = "workstation"
address = "workstation.example.ts.net:7443"
adres = "workstation.example.ts.net:7443"
`, "adres")
}

func TestHostsFileRefusesDuplicateName(t *testing.T) {
	requireRefused(t, `[[hosts]]
name = "workstation"
address = "workstation.example.ts.net:7443"

[[hosts]]
name = "workstation"
address = "laptop.example.ts.net:7443"
`, "workstation")
}

func TestHostsFileRefusesControllerNamedHost(t *testing.T) {
	requireRefused(t, `name = "desktop"

[[hosts]]
name = "desktop"
address = "desktop.example.ts.net:7443"
`, "desktop")
}

func TestHostsFileRefusesBadName(t *testing.T) {
	t.Run("host", func(t *testing.T) {
		requireRefused(t, `[[hosts]]
name = "Work_station"
address = "workstation.example.ts.net:7443"
`, "Work_station")
	})
	t.Run("controller", func(t *testing.T) {
		requireRefused(t, `name = "Desk_top"
`, "Desk_top")
	})
}

func TestHostsFileRefusesAddressWithoutPort(t *testing.T) {
	requireRefused(t, `[[hosts]]
name = "workstation"
address = "workstation.example.ts.net"
`, "workstation.example.ts.net")
}

func TestHostsFileRefusesBadAddress(t *testing.T) {
	for _, address := range []string{":7443", "workstation:0", "workstation:65536", "workstation:http", "workstation:"} {
		t.Run(address, func(t *testing.T) {
			requireRefused(t, `[[hosts]]
name = "workstation"
address = "`+address+`"
`, address)
		})
	}
}
