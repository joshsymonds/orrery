// Package capture declares the fixed probes that record how Claude Code behaves under stream-json
// control, and builds the isolated command that runs each one: a fixed argument vector, an
// environment limited to an allow-list, a fresh empty working directory, and a timeout.
package capture
