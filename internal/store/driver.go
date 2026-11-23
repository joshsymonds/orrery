package store

import (
	"context"
	"database/sql/driver"
	"fmt"

	sqlite3 "github.com/mattn/go-sqlite3"
)

// sqliteConnector opens connections to the database dsn names through mattn's SQLite driver, which calls the SQLite C
// library through cgo. The pre-update hook the store records epic writes with needs the build tag
// sqlite_preupdate_hook, which builds SQLite with SQLITE_ENABLE_PREUPDATE_HOOK.
type sqliteConnector struct {
	dsn    string
	driver *sqlite3.SQLiteDriver
}

// newConnector returns the connector of the database dsn names.
func newConnector(dsn string) *sqliteConnector {
	return &sqliteConnector{dsn: dsn, driver: &sqlite3.SQLiteDriver{}}
}

// Connect opens one connection. The driver's open takes no context: it opens a local file and reads its header.
func (c *sqliteConnector) Connect(context.Context) (driver.Conn, error) {
	conn, err := c.driver.Open(c.dsn)
	if err != nil {
		return nil, fmt.Errorf("%w", err)
	}
	return conn, nil
}

// Driver returns the driver the connector opens connections with.
func (c *sqliteConnector) Driver() driver.Driver {
	return c.driver
}
