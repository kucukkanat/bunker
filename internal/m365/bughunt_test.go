package m365

import (
	"strconv"
	"testing"
	"time"
)

// The Graph response cache counted entries only: 2000 answers of up to
// 8 MB each could pin gigabytes of memory.
func TestGraphCacheIsBoundedByBytes(t *testing.T) {
	cacheMu.Lock()
	cache, cacheBytes = map[string]cacheEntry{}, 0
	cacheMu.Unlock()
	exp := time.Now().Add(time.Minute)
	cachePut("big", cacheEntry{status: 200, body: make([]byte, 4<<20), exp: exp})
	if _, ok := cacheGet("big"); ok {
		t.Error("a 4 MB answer was cached")
	}
	for i := 0; i < 1000; i++ {
		cachePut(strconv.Itoa(i), cacheEntry{status: 200, body: make([]byte, 400<<10), exp: exp})
	}
	cacheMu.Lock()
	total := 0
	for _, e := range cache {
		total += len(e.body)
	}
	cacheMu.Unlock()
	if total > cacheMaxBytes {
		t.Errorf("cache holds %d MB", total>>20)
	}
}
