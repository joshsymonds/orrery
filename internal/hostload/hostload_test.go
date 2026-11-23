package hostload

import (
	"fmt"
	"strings"
	"testing"
	"testing/fstest"
)

// meminfo returns a meminfo file with MemTotal and MemAvailable set, in kB, and the other lines a real file has.
func meminfo(total, available int) string {
	return fmt.Sprintf("MemTotal:       %d kB\nMemFree:        %d kB\nMemAvailable:   %d kB\nHugePages_Total:       0\n",
		total, total/2, available)
}

func TestHostLoadReadsProc(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		loadavg string
		meminfo string
		cpus    int
		load    float64
		memory  float64
	}{
		{
			name:    "under capacity",
			loadavg: "1.50 1.00 0.50 2/300 99\n",
			meminfo: meminfo(1000, 250),
			cpus:    4,
			load:    37.5,
			memory:  75,
		},
		{
			name:    "load over 100",
			loadavg: "6.00 2.00 1.00 3/400 1234\n",
			meminfo: meminfo(2000, 1500),
			cpus:    4,
			load:    150,
			memory:  25,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fsys := fstest.MapFS{
				"proc/loadavg": {Data: []byte(tc.loadavg)},
				"proc/meminfo": {Data: []byte(tc.meminfo)},
			}
			got, err := Read(fsys, tc.cpus)
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			if got.LoadPercent != tc.load {
				t.Fatalf("LoadPercent = %v, want %v", got.LoadPercent, tc.load)
			}
			if got.MemoryPercent != tc.memory {
				t.Fatalf("MemoryPercent = %v, want %v", got.MemoryPercent, tc.memory)
			}
		})
	}
}

func TestHostLoadRefusesMalformed(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		fsys fstest.MapFS
		cpus int
		want string
	}{
		{
			name: "missing loadavg",
			fsys: fstest.MapFS{"proc/meminfo": {Data: []byte(meminfo(1000, 250))}},
			cpus: 4,
			want: "proc/loadavg",
		},
		{
			name: "missing meminfo",
			fsys: fstest.MapFS{"proc/loadavg": {Data: []byte("1.00 1.00 1.00 1/1 1\n")}},
			cpus: 4,
			want: "proc/meminfo",
		},
		{
			name: "empty loadavg",
			fsys: fstest.MapFS{
				"proc/loadavg": {Data: []byte("\n")},
				"proc/meminfo": {Data: []byte(meminfo(1000, 250))},
			},
			cpus: 4,
			want: "proc/loadavg",
		},
		{
			name: "non-numeric loadavg",
			fsys: fstest.MapFS{
				"proc/loadavg": {Data: []byte("busy 1.00 1.00\n")},
				"proc/meminfo": {Data: []byte(meminfo(1000, 250))},
			},
			cpus: 4,
			want: "proc/loadavg",
		},
		{
			name: "meminfo line without a colon",
			fsys: fstest.MapFS{
				"proc/loadavg": {Data: []byte("1.00 1.00 1.00 1/1 1\n")},
				"proc/meminfo": {Data: []byte("MemTotal 1000 kB\nMemAvailable: 250 kB\n")},
			},
			cpus: 4,
			want: "proc/meminfo",
		},
		{
			name: "meminfo value not a number",
			fsys: fstest.MapFS{
				"proc/loadavg": {Data: []byte("1.00 1.00 1.00 1/1 1\n")},
				"proc/meminfo": {Data: []byte("MemTotal: lots kB\nMemAvailable: 250 kB\n")},
			},
			cpus: 4,
			want: "proc/meminfo",
		},
		{
			name: "meminfo unit not kB",
			fsys: fstest.MapFS{
				"proc/loadavg": {Data: []byte("1.00 1.00 1.00 1/1 1\n")},
				"proc/meminfo": {Data: []byte("MemTotal: 1000 MB\nMemAvailable: 250 kB\n")},
			},
			cpus: 4,
			want: "proc/meminfo",
		},
		{
			name: "no MemTotal",
			fsys: fstest.MapFS{
				"proc/loadavg": {Data: []byte("1.00 1.00 1.00 1/1 1\n")},
				"proc/meminfo": {Data: []byte("MemAvailable: 250 kB\n")},
			},
			cpus: 4,
			want: "MemTotal",
		},
		{
			name: "no MemAvailable",
			fsys: fstest.MapFS{
				"proc/loadavg": {Data: []byte("1.00 1.00 1.00 1/1 1\n")},
				"proc/meminfo": {Data: []byte("MemTotal: 1000 kB\n")},
			},
			cpus: 4,
			want: "MemAvailable",
		},
		{
			name: "MemTotal of 0",
			fsys: fstest.MapFS{
				"proc/loadavg": {Data: []byte("1.00 1.00 1.00 1/1 1\n")},
				"proc/meminfo": {Data: []byte("MemTotal: 0 kB\nMemAvailable: 0 kB\n")},
			},
			cpus: 4,
			want: "MemTotal",
		},
		{
			name: "MemAvailable above MemTotal",
			fsys: fstest.MapFS{
				"proc/loadavg": {Data: []byte("1.00 1.00 1.00 1/1 1\n")},
				"proc/meminfo": {Data: []byte("MemTotal: 1000 kB\nMemAvailable: 1001 kB\n")},
			},
			cpus: 4,
			want: "MemAvailable",
		},
		{
			name: "cpus of 0",
			fsys: fstest.MapFS{
				"proc/loadavg": {Data: []byte("1.00 1.00 1.00 1/1 1\n")},
				"proc/meminfo": {Data: []byte(meminfo(1000, 250))},
			},
			cpus: 0,
			want: "cpus",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := Read(tc.fsys, tc.cpus)
			if err == nil {
				t.Fatalf("Read succeeded, want an error naming %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Read error = %q, want it to name %q", err, tc.want)
			}
		})
	}
}
