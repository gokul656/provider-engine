package agent

import (
	"os"
	"runtime"
	"strconv"
	"strings"

	"github.com/dcp/provider-agent/pkg/api"
)

func resources() api.ResourceInfo {
	return api.ResourceInfo{
		CPUCores: runtime.NumCPU(),
		MemoryMB: totalMemMB(),
		DiskGB:   availDiskGB(),
	}
}

func totalMemMB() int {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "MemTotal:") {
			f := strings.Fields(line)
			if len(f) >= 2 {
				kb, _ := strconv.Atoi(f[1])
				return kb / 1024
			}
		}
	}
	return 0
}

func memFreeMB() int {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "MemAvailable:") {
			f := strings.Fields(line)
			if len(f) >= 2 {
				kb, _ := strconv.Atoi(f[1])
				return kb / 1024
			}
		}
	}
	return 0
}

func cpuLoad() float64 {
	// Read /proc/loadavg — first field is 1-min load average
	b, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0
	}
	f := strings.Fields(string(b))
	if len(f) == 0 {
		return 0
	}
	v, _ := strconv.ParseFloat(f[0], 64)
	return v
}

func availDiskGB() int {
	// Rough estimate via /proc/mounts — not critical for MVP
	return 0
}
