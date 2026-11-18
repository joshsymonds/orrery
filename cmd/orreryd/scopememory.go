package main

import (
	"errors"
	"flag"
	"math"
	"strconv"
	"strings"

	"github.com/joshsymonds/orrery/internal/holder"
)

// scopeMemoryFlag defines --holder-scope-memory on flags and returns where its value lands: the cap in bytes, as
// parseScopeMemory reads it, and holder.DefaultScopeMemory when the flag is not given.
func scopeMemoryFlag(flags *flag.FlagSet) *int64 {
	size := holder.DefaultScopeMemory
	flags.Func("holder-scope-memory", "memory cap of each holder's scope", func(value string) error {
		parsed, err := parseScopeMemory(value)
		if err != nil {
			return err
		}
		size = parsed
		return nil
	})
	return &size
}

// scopeMemoryUnits gives the multiplier of each suffix --holder-scope-memory takes. Each is a power of 1024, as
// systemd reads a MemoryMax with that suffix.
func scopeMemoryUnits(suffix byte) (int64, bool) {
	switch suffix {
	case 'K':
		return 1 << 10, true
	case 'M':
		return 1 << 20, true
	case 'G':
		return 1 << 30, true
	case 'T':
		return 1 << 40, true
	}
	return 0, false
}

// errScopeMemory is the refusal of a --holder-scope-memory value that is not a positive size.
var errScopeMemory = errors.New("want a positive number of bytes, or a whole number with K, M, G or T")

// parseScopeMemory reads a --holder-scope-memory value: a positive whole number of bytes, or a positive whole number
// followed by K, M, G or T, each a power of 1024. A value whose bytes overflow an int64 is refused.
func parseScopeMemory(value string) (int64, error) {
	digits, unit := value, int64(1)
	if value != "" {
		if mult, ok := scopeMemoryUnits(value[len(value)-1]); ok {
			digits, unit = value[:len(value)-1], mult
		}
	}
	if digits == "" || strings.TrimLeft(digits, "0123456789") != "" {
		return 0, errScopeMemory
	}
	count, err := strconv.ParseInt(digits, 10, 64)
	if err != nil || count <= 0 || count > math.MaxInt64/unit {
		return 0, errScopeMemory
	}
	return count * unit, nil
}
