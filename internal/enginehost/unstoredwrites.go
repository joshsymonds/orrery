package enginehost

import (
	"context"
	"maps"
	"slices"
)

// unstoredWrites keeps, by key, the values a pass made whose write the store refused, a busy store among the causes, so
// the next pass stores them again in place of making them again. A key whose model call succeeded is not called for
// again, so a value lost to a failed write would stay unwritten until a restart.
type unstoredWrites[V any] map[string]V

// holds reports whether a value for key is kept, so the pass writes it again and makes no call for key.
func (u unstoredWrites[V]) holds(key string) bool {
	_, ok := u[key]
	return ok
}

// retry writes each kept value again with write, in key order, and forgets each write that lands. It stops at the first
// write the store refuses again, since the store still refuses writes, and the rest wait for the next pass. It reports
// how many writes landed.
func (u unstoredWrites[V]) retry(ctx context.Context, write func(ctx context.Context, key string, value V) error) int {
	stored := 0
	for _, key := range slices.Sorted(maps.Keys(u)) {
		if write(ctx, key, u[key]) != nil {
			return stored
		}
		delete(u, key)
		stored++
	}
	return stored
}
