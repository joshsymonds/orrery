// Package proc starts a command in its own process group, stops it by interrupt then kill, and keeps
// a bounded tail of its stderr, and on request of its stdout, with the key lines of each.
package proc
