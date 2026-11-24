// Package hostload reads a host's load from its proc filesystem: the one-minute load average as a percent of its CPUs,
// and the memory in use as a percent of MemTotal. It is how a host fills node.Load.
package hostload

import (
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"runtime"
	"strconv"
	"strings"

	"github.com/joshsymonds/orrery/internal/node"
)

// Local reads the load of the running host, with runtime.NumCPU CPUs, from the root of its proc filesystem.
func Local() (node.Load, error) {
	return Read(os.DirFS("/"), runtime.NumCPU())
}

// Read reads proc/loadavg and proc/meminfo from fsys and returns the load of the host they describe over cpus CPUs.
// LoadPercent is the first loadavg field over cpus, times 100, uncapped. MemoryPercent is (MemTotal - MemAvailable)
// over MemTotal, times 100. Read returns an error naming the file or value if a file is missing or malformed, if
// MemTotal is 0, or if cpus is below 1.
func Read(fsys fs.FS, cpus int) (node.Load, error) {
	if cpus < 1 {
		return node.Load{}, fmt.Errorf("hostload: cpus %d, want at least 1", cpus)
	}
	loadavg, err := fs.ReadFile(fsys, "proc/loadavg")
	if err != nil {
		return node.Load{}, fmt.Errorf("hostload: read proc/loadavg: %w", err)
	}
	load1, err := parseLoadavg(loadavg)
	if err != nil {
		return node.Load{}, err
	}
	meminfo, err := fs.ReadFile(fsys, "proc/meminfo")
	if err != nil {
		return node.Load{}, fmt.Errorf("hostload: read proc/meminfo: %w", err)
	}
	total, available, err := parseMeminfo(meminfo)
	if err != nil {
		return node.Load{}, err
	}
	return node.Load{
		LoadPercent:   load1 / float64(cpus) * 100,
		MemoryPercent: float64(total-available) / float64(total) * 100,
	}, nil
}

// parseLoadavg returns the first field of a loadavg file, the one-minute load average. It must be a finite,
// non-negative number.
func parseLoadavg(data []byte) (float64, error) {
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return 0, errors.New("hostload: proc/loadavg is empty")
	}
	load1, err := strconv.ParseFloat(fields[0], 64)
	if err != nil || math.IsNaN(load1) || math.IsInf(load1, 0) || load1 < 0 {
		return 0, fmt.Errorf("hostload: proc/loadavg first field %q is not a load average", fields[0])
	}
	return load1, nil
}

// parseMeminfo returns MemTotal and MemAvailable, in kB, from a meminfo file. Every line must be a name, a colon, an
// unsigned number and an optional kB unit. MemTotal must be set and non-zero, and MemAvailable must be set and not
// above it.
func parseMeminfo(data []byte) (total, available uint64, err error) {
	values := make(map[string]uint64)
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		name, value, lineErr := parseMeminfoLine(line)
		if lineErr != nil {
			return 0, 0, lineErr
		}
		values[name] = value
	}
	total, ok := values["MemTotal"]
	if !ok {
		return 0, 0, errors.New("hostload: proc/meminfo has no MemTotal")
	}
	available, ok = values["MemAvailable"]
	if !ok {
		return 0, 0, errors.New("hostload: proc/meminfo has no MemAvailable")
	}
	if total == 0 {
		return 0, 0, errors.New("hostload: proc/meminfo MemTotal is 0")
	}
	if available > total {
		return 0, 0, fmt.Errorf("hostload: proc/meminfo MemAvailable %d is above MemTotal %d", available, total)
	}
	return total, available, nil
}

// parseMeminfoLine splits one meminfo line, such as "MemTotal:  16318956 kB", into its name and value.
func parseMeminfoLine(line string) (string, uint64, error) {
	fields := strings.Fields(line)
	if len(fields) < 2 || len(fields) > 3 || !strings.HasSuffix(fields[0], ":") ||
		(len(fields) == 3 && fields[2] != "kB") {
		return "", 0, fmt.Errorf("hostload: proc/meminfo line %q is malformed", line)
	}
	value, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return "", 0, fmt.Errorf("hostload: proc/meminfo line %q is malformed: %w", line, err)
	}
	return strings.TrimSuffix(fields[0], ":"), value, nil
}
