package unit

import (
	"strings"
	"sync"
	"testing"
)

func TestSafeBuf_ConcurrentWritesAllLand(t *testing.T) {
	var b SafeBuf
	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if n, err := b.Write([]byte("x\n")); err != nil || n != 2 {
				t.Errorf("Write = %d, %v", n, err)
			}
		}()
	}
	wg.Wait()
	if got := strings.Count(b.String(), "x\n"); got != 50 {
		t.Errorf("String holds %d writes, want 50", got)
	}
}
