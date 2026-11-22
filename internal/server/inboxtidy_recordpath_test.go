package server

import "testing"

// TestInboxTidyRecordPathFromHeadWorkspace lists two record-only epics through Epics.List: one whose head names a
// workspace, which lists that workspace as its path, and one whose head names none, which lists an empty path.
func TestInboxTidyRecordPathFromHeadWorkspace(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeHead(t, root, "repoa", "named", `{"outcome":"shipped","workspace":"/synthetic/record-workspace"}`, epicsNow())
	writeHead(t, root, "repoa", "unnamed", `{"outcome":"shipped"}`, epicsNow())
	client := epicsServe(t, root, &epicsFake{})
	epics := epicsList(t, client)

	want := map[string]string{
		"repoa/named":   "/synthetic/record-workspace",
		"repoa/unnamed": "",
	}
	for id, path := range want {
		if got := epicByID(t, epics, id).GetPath(); got != path {
			t.Errorf("%s path = %q, want %q", id, got, path)
		}
	}
}
