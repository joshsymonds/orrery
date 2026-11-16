// Package sqlcgen holds the typed queries over orreryd's SQLite store: one method on Queries for each query in
// internal/store/queries.sql, and the row models in models.go. sqlc generate writes db.go, models.go and queries.sql.go
// from the migrations in internal/store/migrations and that query file, so change the SQL and regenerate rather than
// editing the generated files.
package sqlcgen
