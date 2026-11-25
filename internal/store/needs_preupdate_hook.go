//go:build !sqlite_preupdate_hook

package store

// The store records the epics each write moves from SQLite's pre-update hook, which mattn/go-sqlite3 builds only under
// the build tag sqlite_preupdate_hook; without it the driver's RegisterPreUpdateHook does nothing, and epic revisions
// would go unrecorded with no error. This file stops a build without the tag. Build with -tags=sqlite_preupdate_hook,
// as the dev shell's GOFLAGS and the flake's package do.
var _ = buildWithTagSqlitePreupdateHook
