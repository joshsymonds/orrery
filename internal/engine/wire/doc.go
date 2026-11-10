// Package wire decodes JSON objects into Go structs, using each field's wire
// tag as its key and falling back to the json tag name when no wire tag is set.
// It also decodes Claude Code's stream-json messages into typed variants.
package wire
