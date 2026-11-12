// Package pools reads pool files: TOML files that name the pools a machine offers, their members, and the commands
// that wake, status, and admit those members. Parse reads one file; Merge joins several and refuses a pool or a
// member defined twice.
package pools

import (
	"errors"
	"fmt"
	"maps"
	"math"
	"regexp"
	"slices"
	"time"

	"github.com/BurntSushi/toml"
)

// Pool is one pool as a file defines it. Cap is the most members one project may hold from the pool at once. Each
// argv names a program and its arguments; each limit bounds how long that program may run.
type Pool struct {
	Name        string
	Host        string
	Cap         int
	Members     []string
	Wake        []string
	WakeLimit   time.Duration
	Status      []string
	StatusLimit time.Duration
	Admit       []string
	AdmitLimit  time.Duration
}

// ErrDefinedTwice reports that a pool or a member is defined by two different pools.
var ErrDefinedTwice = errors.New("defined twice")

var (
	poolNamePattern   = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	memberNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
)

// maxLimitSeconds is the largest limit whose duration fits in a time.Duration.
const maxLimitSeconds = math.MaxInt64 / int64(time.Second)

// sectionPools is the top-level table that holds every pool.
const sectionPools = "pools"

// sectionGates is the top-level table that holds every gate. internal/gates reads it, so pools leaves it alone.
const sectionGates = "gates"

// poolFile is one [pools.NAME] table as the file spells it. Whether a key is present is read from the metadata,
// since a zero value cannot tell a missing key from one set to zero.
type poolFile struct {
	Host        string   `toml:"host"`
	Cap         int      `toml:"cap"`
	Members     []string `toml:"members"`
	Wake        []string `toml:"wake"`
	WakeLimit   int64    `toml:"wake_limit"`
	Status      []string `toml:"status"`
	StatusLimit int64    `toml:"status_limit"`
	Admit       []string `toml:"admit"`
	AdmitLimit  int64    `toml:"admit_limit"`
}

type poolDocument struct {
	Pools map[string]poolFile `toml:"pools"`
}

// requiredKeys lists the keys every [pools.NAME] table carries.
func requiredKeys() []string {
	return []string{"host", "cap", "members", "wake", "wake_limit", "status", "status_limit", "admit", "admit_limit"}
}

// Parse reads one pool file. path names the file in errors; data is its contents. The pools come back sorted by name.
func Parse(path string, data []byte) ([]Pool, error) {
	var doc poolDocument
	md, err := toml.Decode(string(data), &doc)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err = checkKeys(md); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	var out []Pool
	for _, name := range slices.Sorted(maps.Keys(doc.Pools)) {
		if err = checkPresent(md, name); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		var p Pool
		p, err = toPool(name, doc.Pools[name])
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		out = append(out, p)
	}
	return out, nil
}

// checkKeys refuses any key the format does not define. Keys match exactly, so HOST is refused rather than read as
// host. The [gates] table of a project file and every key under it belong to internal/gates, so they pass here; a
// gates key that is not a table is refused.
func checkKeys(md toml.MetaData) error {
	for _, key := range md.Keys() {
		switch {
		case len(key) == 1 && key[0] == sectionGates:
			if t := md.Type(sectionGates); t != "" && t != "Hash" {
				return fmt.Errorf("%s must be a table, not %s", sectionGates, t)
			}
		case key[0] == sectionGates:
		case len(key) == 1 && key[0] == sectionPools:
		case len(key) == 2 && key[0] == sectionPools:
			if !poolNamePattern.MatchString(key[1]) {
				return fmt.Errorf("pool %q: name must match [a-z][a-z0-9_]*", key[1])
			}
		case len(key) == 3 && key[0] == sectionPools && slices.Contains(requiredKeys(), key[2]):
		default:
			return fmt.Errorf("unknown key %s", key)
		}
	}
	return nil
}

// checkPresent refuses a pool table that lacks a required key.
func checkPresent(md toml.MetaData, name string) error {
	for _, key := range requiredKeys() {
		if !md.IsDefined(sectionPools, name, key) {
			return fmt.Errorf("pool %q: missing key %q", name, key)
		}
	}
	return nil
}

// toPool checks one pool's values and returns it as a Pool.
func toPool(name string, raw poolFile) (Pool, error) {
	if raw.Cap < 1 {
		return Pool{}, fmt.Errorf("pool %q: cap %d is below 1", name, raw.Cap)
	}
	if err := checkMembers(name, raw.Members); err != nil {
		return Pool{}, err
	}
	if err := checkArgv(name, "wake", raw.Wake); err != nil {
		return Pool{}, err
	}
	if err := checkArgv(name, "status", raw.Status); err != nil {
		return Pool{}, err
	}
	if err := checkArgv(name, "admit", raw.Admit); err != nil {
		return Pool{}, err
	}
	wakeLimit, err := toLimit(name, "wake_limit", raw.WakeLimit)
	if err != nil {
		return Pool{}, err
	}
	statusLimit, err := toLimit(name, "status_limit", raw.StatusLimit)
	if err != nil {
		return Pool{}, err
	}
	admitLimit, err := toLimit(name, "admit_limit", raw.AdmitLimit)
	if err != nil {
		return Pool{}, err
	}
	return Pool{
		Name:        name,
		Host:        raw.Host,
		Cap:         raw.Cap,
		Members:     raw.Members,
		Wake:        raw.Wake,
		WakeLimit:   wakeLimit,
		Status:      raw.Status,
		StatusLimit: statusLimit,
		Admit:       raw.Admit,
		AdmitLimit:  admitLimit,
	}, nil
}

// checkMembers refuses an empty member list, a member outside the name pattern, and a member listed twice.
func checkMembers(name string, members []string) error {
	if len(members) == 0 {
		return fmt.Errorf("pool %q: members is empty", name)
	}
	seen := make(map[string]bool, len(members))
	for _, member := range members {
		if !memberNamePattern.MatchString(member) {
			return fmt.Errorf("pool %q: member %q in members must match [A-Za-z0-9][A-Za-z0-9._-]*", name, member)
		}
		if seen[member] {
			return fmt.Errorf("pool %q: member %q is listed twice in members", name, member)
		}
		seen[member] = true
	}
	return nil
}

// checkArgv refuses an empty argv and an empty element of one.
func checkArgv(name, key string, argv []string) error {
	if len(argv) == 0 {
		return fmt.Errorf("pool %q: %s is empty", name, key)
	}
	if slices.Contains(argv, "") {
		return fmt.Errorf("pool %q: %s has an empty element", name, key)
	}
	return nil
}

// toLimit converts a limit in whole seconds to a duration, refusing one below 1 second or too large for a duration.
func toLimit(name, key string, seconds int64) (time.Duration, error) {
	if seconds < 1 || seconds > maxLimitSeconds {
		return 0, fmt.Errorf("pool %q: %s %d is outside 1 to %d seconds", name, key, seconds, maxLimitSeconds)
	}
	return time.Duration(seconds) * time.Second, nil
}

// Merge joins the pools of several files. An identical pool in two files is kept once; a pool defined differently
// twice, or a member in two pools, wraps ErrDefinedTwice. The pools come back sorted by name.
func Merge(sets ...[]Pool) ([]Pool, error) {
	byName := make(map[string]Pool)
	for _, set := range sets {
		for _, pool := range set {
			if have, ok := byName[pool.Name]; ok {
				if !have.Equal(pool) {
					return nil, fmt.Errorf("pool %q: differing definitions: %w", pool.Name, ErrDefinedTwice)
				}
				continue
			}
			byName[pool.Name] = pool
		}
	}
	owner := make(map[string]string)
	var out []Pool
	for _, name := range slices.Sorted(maps.Keys(byName)) {
		pool := byName[name]
		for _, member := range pool.Members {
			if other, ok := owner[member]; ok {
				return nil, fmt.Errorf("member %q is in pools %q and %q: %w", member, other, name, ErrDefinedTwice)
			}
			owner[member] = name
		}
		out = append(out, pool)
	}
	return out, nil
}

// Equal reports whether p and q define the same pool.
func (p Pool) Equal(q Pool) bool {
	return p.Name == q.Name && p.Host == q.Host && p.Cap == q.Cap &&
		slices.Equal(p.Members, q.Members) && slices.Equal(p.Wake, q.Wake) && p.WakeLimit == q.WakeLimit &&
		slices.Equal(p.Status, q.Status) && p.StatusLimit == q.StatusLimit &&
		slices.Equal(p.Admit, q.Admit) && p.AdmitLimit == q.AdmitLimit
}
