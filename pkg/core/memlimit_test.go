package core

import (
	"errors"
	"io"
	"testing"

	"github.com/keelcore/keel/pkg/core/logging"
)

// stubMemLimit installs seams: files maps path→content (missing = read error),
// env is the GOMEMLIMIT value; it returns a pointer to the limit that was set
// (-1 = SetMemoryLimit never called).
func stubMemLimit(t *testing.T, files map[string]string, env string) *int64 {
	t.Helper()
	origRead, origEnv, origSet := memReadFile, memGetenv, memSetMemoryLimit
	t.Cleanup(func() { memReadFile, memGetenv, memSetMemoryLimit = origRead, origEnv, origSet })
	set := int64(-1)
	memReadFile = func(name string) ([]byte, error) {
		if c, ok := files[name]; ok {
			return []byte(c), nil
		}
		return nil, errors.New("not found")
	}
	memGetenv = func(string) string { return env }
	memSetMemoryLimit = func(v int64) int64 { set = v; return 0 }
	return &set
}

func testLogger() *logging.Logger { return logging.New(logging.Config{Out: io.Discard}) }

const (
	cgV2 = "/sys/fs/cgroup/memory.max"
	cgV1 = "/sys/fs/cgroup/memory/memory.limit_in_bytes"
)

func TestApplyMemoryLimit_HeapMaxWins(t *testing.T) {
	set := stubMemLimit(t, map[string]string{cgV2: "1000\n"}, "")
	applyMemoryLimit(4096, testLogger())
	if *set != 4096 {
		t.Fatalf("limit = %d, want 4096", *set)
	}
}

func TestApplyMemoryLimit_GOMEMLIMITRespected(t *testing.T) {
	set := stubMemLimit(t, map[string]string{cgV2: "1000\n"}, "512MiB")
	applyMemoryLimit(0, testLogger())
	if *set != -1 {
		t.Fatalf("limit set to %d despite GOMEMLIMIT", *set)
	}
}

func TestApplyMemoryLimit_FromCgroupV2(t *testing.T) {
	set := stubMemLimit(t, map[string]string{cgV2: "1000\n"}, "")
	applyMemoryLimit(0, testLogger())
	if *set != 900 {
		t.Fatalf("limit = %d, want 900 (90%% of 1000)", *set)
	}
}

func TestApplyMemoryLimit_FromCgroupV1(t *testing.T) {
	set := stubMemLimit(t, map[string]string{cgV1: "2000"}, "")
	applyMemoryLimit(0, testLogger())
	if *set != 1800 {
		t.Fatalf("limit = %d, want 1800", *set)
	}
}

func TestApplyMemoryLimit_NoLimitLeavesDefault(t *testing.T) {
	cases := map[string]map[string]string{
		"no cgroup files": {},
		"v2 max":          {cgV2: "max\n"},
		"v1 unlimited":    {cgV1: "9223372036854771712"},
		"garbage":         {cgV2: "lots"},
		"zero":            {cgV2: "0"},
	}
	for name, files := range cases {
		t.Run(name, func(t *testing.T) {
			set := stubMemLimit(t, files, "")
			applyMemoryLimit(0, testLogger())
			if *set != -1 {
				t.Fatalf("limit set to %d, want runtime default", *set)
			}
		})
	}
}
