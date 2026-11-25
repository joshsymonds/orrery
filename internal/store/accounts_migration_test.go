package store

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// schemaKey returns a schema line with the SQL of a table left out, so a table is compared by its name and a column by
// its line. A table's line names the table twice, then its SQL.
func schemaKey(line string) string {
	if strings.HasPrefix(line, "table ") {
		key, _, _ := strings.Cut(line, ": ")
		return key
	}
	return line
}

// TestAccountsMigrationAddsOnlyTables checks that 00096_llm_accounts.sql creates the five tables of the LLM accounts
// and routes and changes no table that exists at version 95. It reads the schema of a store at version 95, then of a
// store the migrations take to version 96 and no further, since a later migration such as 00097_engine_hosts.sql
// changes other tables: every line of the first is in the second unchanged, and the lines that are new are exactly the
// five tables and their columns.
func TestAccountsMigrationAddsOnlyTables(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "orrery.db")

	fixture := openStoreAtVersion(t, path, 95)
	before := schemaLines(t, fixture)
	closeDB(t, fixture)

	migrated := openStoreAtVersion(t, path, 96)
	after := schemaLines(t, migrated)
	closeDB(t, migrated)

	for _, line := range before {
		if !slices.Contains(after, line) {
			t.Errorf("line at version 95 is missing or changed after Open: %s", line)
		}
	}

	var added []string
	for _, line := range after {
		if !slices.Contains(before, line) {
			added = append(added, schemaKey(line))
		}
	}
	slices.Sort(added)
	want := []string{
		"table llm_accounts on llm_accounts",
		"table route_bindings on route_bindings",
		"table route_contexts on route_contexts",
		"table route_registry on route_registry",
		"table route_seats on route_seats",
		`column llm_accounts.created INTEGER notnull=1 default="" pk=0`,
		`column llm_accounts.fingerprint TEXT notnull=1 default="" pk=0`,
		`column llm_accounts.id TEXT notnull=0 default="" pk=1`,
		`column llm_accounts.name TEXT notnull=1 default="" pk=0`,
		`column llm_accounts.plan TEXT notnull=1 default="''" pk=0`,
		`column llm_accounts.provider TEXT notnull=1 default="" pk=0`,
		`column llm_accounts.token TEXT notnull=1 default="" pk=0`,
		`column route_bindings.context TEXT notnull=1 default="" pk=1`,
		`column route_bindings.scope TEXT notnull=1 default="" pk=2`,
		`column route_bindings.seat TEXT notnull=1 default="" pk=0`,
		`column route_bindings.selector TEXT notnull=1 default="" pk=3`,
		`column route_contexts.account TEXT notnull=1 default="''" pk=0`,
		`column route_contexts.default_seat TEXT notnull=1 default="" pk=0`,
		`column route_contexts.has_subagents INTEGER notnull=1 default="0" pk=0`,
		`column route_contexts.id TEXT notnull=0 default="" pk=1`,
		`column route_contexts.subagent_default_seat TEXT notnull=1 default="''" pk=0`,
		`column route_registry.default_context TEXT notnull=1 default="" pk=0`,
		`column route_registry.id INTEGER notnull=0 default="" pk=1`,
		`column route_seats.api_key_env_file TEXT notnull=1 default="''" pk=0`,
		`column route_seats.auth_mode TEXT notnull=1 default="" pk=0`,
		`column route_seats.aws_profile TEXT notnull=1 default="''" pk=0`,
		`column route_seats.aws_region TEXT notnull=1 default="''" pk=0`,
		`column route_seats.billing TEXT notnull=1 default="''" pk=0`,
		`column route_seats.cache_ttl TEXT notnull=1 default="''" pk=0`,
		`column route_seats.id TEXT notnull=0 default="" pk=1`,
		`column route_seats.insecure INTEGER notnull=1 default="0" pk=0`,
		`column route_seats.max_input_tokens INTEGER notnull=1 default="0" pk=0`,
		`column route_seats.model TEXT notnull=1 default="''" pk=0`,
		`column route_seats.model_map_env_file TEXT notnull=1 default="''" pk=0`,
		`column route_seats.speed TEXT notnull=1 default="''" pk=0`,
		`column route_seats.strict_structured_output INTEGER notnull=1 default="0" pk=0`,
		`column route_seats.strip_betas TEXT notnull=1 default="'[]'" pk=0`,
		`column route_seats.strip_cache_control INTEGER notnull=1 default="0" pk=0`,
		`column route_seats.upstream TEXT notnull=1 default="''" pk=0`,
	}
	slices.Sort(want)
	if !slices.Equal(added, want) {
		t.Fatalf("lines 00096 adds differ from the five tables and their columns:\n%s", lineDiff(added, want))
	}
}
