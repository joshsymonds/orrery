package main

import (
	"strings"
	"testing"
)

// defaultHostUnknownName is a name that no hosts file of these tests lists, and that is neither the controller's host nor
// its orrery name.
const defaultHostUnknownName = "synthetic-nowhere"

// TestDefaultHostUnknownFailsStartup shows that controllerHost refuses a hosts file whose default names neither the
// controller's host, its orrery name nor a listed host, and that it accepts a default that names one of those. The
// refusal names the default.
func TestDefaultHostUnknownFailsStartup(t *testing.T) {
	t.Run("unknown default", func(t *testing.T) {
		getenv, _ := hostMigrationEnv(t, "default = \""+defaultHostUnknownName+"\"\n")
		_, _, _, err := controllerHost(options{host: hostMigrationName}, getenv)
		if err == nil {
			t.Fatalf("controllerHost accepted a default naming no host")
		}
		if !strings.Contains(err.Error(), defaultHostUnknownName) {
			t.Errorf("controllerHost error %q does not name the default", err)
		}
	})
	t.Run("the controller's host", func(t *testing.T) {
		getenv, _ := hostMigrationEnv(t, "default = \""+hostMigrationName+"\"\n")
		host, orrery, _, err := controllerHost(options{host: hostMigrationName}, getenv)
		if err != nil {
			t.Fatalf("controllerHost with the default the controller's host: %v", err)
		}
		if host != hostMigrationName || orrery != hostMigrationName {
			t.Errorf("controllerHost = %q, %q; want %q, %q", host, orrery, hostMigrationName, hostMigrationName)
		}
	})
	t.Run("the orrery name", func(t *testing.T) {
		getenv, _ := hostMigrationEnv(t,
			"name = \""+hostMigrationFileName+"\"\ndefault = \""+hostMigrationFileName+"\"\n")
		_, orrery, _, err := controllerHost(options{host: hostMigrationName}, getenv)
		if err != nil {
			t.Fatalf("controllerHost with the default the orrery name: %v", err)
		}
		if orrery != hostMigrationFileName {
			t.Errorf("controllerHost orrery = %q, want %q", orrery, hostMigrationFileName)
		}
	})
	t.Run("a listed host", func(t *testing.T) {
		getenv, _ := hostMigrationEnv(t,
			"default = \""+fleetSecretAgentName+"\"\n"+fleetSecretHostsFile("synthetic-agent.example:7443"))
		opts := options{host: hostMigrationName, fleetSecret: fleetSecretFileWith(t, fleetSecretText)}
		if _, _, _, err := controllerHost(opts, getenv); err != nil {
			t.Fatalf("controllerHost with the default a listed host: %v", err)
		}
	})
}
