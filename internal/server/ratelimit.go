package server

import (
	"net/http"
	"sync"
	"time"

	"github.com/kucukkanat/bunker/internal/core"
)

// limiter is a tiny per-key token bucket used to slow down guessing on
// public endpoints (setup codes, device codes, OAuth) and abusive clients.
type limiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	rate    float64 // tokens per second
	burst   float64
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newLimiter(perMinute, burst int) *limiter {
	l := &limiter{buckets: map[string]*bucket{}, rate: float64(perMinute) / 60, burst: float64(burst)}
	go func() {
		for range time.Tick(5 * time.Minute) {
			l.mu.Lock()
			for k, b := range l.buckets {
				if time.Since(b.last) > 10*time.Minute {
					delete(l.buckets, k)
				}
			}
			l.mu.Unlock()
		}
	}()
	return l
}

func (l *limiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	b := l.buckets[key]
	if b == nil {
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}
	b.tokens += now.Sub(b.last).Seconds() * l.rate
	if b.tokens > l.burst {
		b.tokens = l.burst
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

var (
	publicLimiter = newLimiter(core.EnvInt("RATE_PUBLIC_PER_MIN", 30), 20)
	userLimiter   = newLimiter(core.EnvInt("RATE_USER_PER_MIN", 1200), 300)
)

// rateLimited reports (and answers) when the caller is over the limit.
func rateLimited(w http.ResponseWriter, r *http.Request, u *core.User, ip string) bool {
	var ok bool
	if u == nil {
		if !isUnsafe(r.Method) {
			return false
		}
		ok = publicLimiter.allow(ip)
	} else {
		ok = userLimiter.allow("u:" + itoa(u.ID))
	}
	if !ok {
		w.Header().Set("Retry-After", "10")
		core.Err(w, core.Quota("too many requests: slow down and try again in a few seconds"))
		return true
	}
	return false
}

func itoa(i int64) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	n := len(b)
	neg := i < 0
	if neg {
		i = -i
	}
	for i > 0 {
		n--
		b[n] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		n--
		b[n] = '-'
	}
	return string(b[n:])
}
