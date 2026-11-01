package core

import (
	"os"
	"runtime/debug"
	"strconv"
	"strings"

	"github.com/keelcore/keel/pkg/core/logging"
)

// Seams over the OS and runtime so tests can drive every branch. Production
// values are the bare library functions, so runtime behaviour is unchanged.
var (
	memReadFile       = os.ReadFile
	memGetenv         = os.Getenv
	memSetMemoryLimit = debug.SetMemoryLimit
)

// cgroupMemoryFiles lists the container memory-limit files, cgroup v2 first.
var cgroupMemoryFiles = []string{
	"/sys/fs/cgroup/memory.max",
	"/sys/fs/cgroup/memory/memory.limit_in_bytes",
}

// cgroupUnlimited is the floor of the sentinel cgroup v1 reports for "no limit".
const cgroupUnlimited = int64(1) << 62

// applyMemoryLimit sets the Go runtime soft memory limit. A configured
// backpressure.heap_max_bytes wins. Otherwise — unless the operator set
// GOMEMLIMIT — it is derived from the container (cgroup) memory limit at 90%,
// so the GC works harder before the kernel OOM-kills the process. The Go
// runtime does not do this itself. No cgroup limit leaves the runtime default.
func applyMemoryLimit(heapMax int64, log *logging.Logger) {
	if heapMax > 0 {
		memSetMemoryLimit(heapMax)
		return
	}
	if memGetenv("GOMEMLIMIT") != "" {
		return
	}
	limit, ok := cgroupMemoryLimit()
	if !ok {
		return
	}
	soft := limit / 10 * 9
	memSetMemoryLimit(soft)
	log.Info("memory_limit_from_cgroup", map[string]any{"cgroup_limit": limit, "soft_limit": soft})
}

// cgroupMemoryLimit returns the container memory limit in bytes, if one is set.
func cgroupMemoryLimit() (int64, bool) {
	for _, f := range cgroupMemoryFiles {
		b, err := memReadFile(f)
		if err != nil {
			continue
		}
		v, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
		if err != nil || v <= 0 || v >= cgroupUnlimited {
			return 0, false // "max", unparsable, or the v1 unlimited sentinel
		}
		return v, true
	}
	return 0, false
}
