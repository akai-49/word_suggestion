package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

var (
	rdb      *redis.Client
	pat      = regexp.MustCompile(`^[a-z]{1,30}$`)
	writeKey = os.Getenv("WRITE_KEY")
	origin   = env("ALLOWED_ORIGINS", "*")
	rateMax  = envInt("RATE_LIMIT", 60) // requests/min/IP
	cache    = &lru{m: map[string]entry{}}
	rl       = &limiter{hits: map[string]int{}}
)

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func envInt(k string, d int) int {
	if n, err := strconv.Atoi(os.Getenv(k)); err == nil {
		return n
	}
	return d
}

// ---- in-process cache (size-capped, TTL) ----
type entry struct {
	v   []string
	exp time.Time
}

type lru struct {
	mu sync.RWMutex
	m  map[string]entry
}

func (c *lru) get(k string) ([]string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.m[k]
	if !ok || time.Now().After(e.exp) {
		return nil, false
	}
	return e.v, true
}

func (c *lru) set(k string, v []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.m) >= 20000 {
		c.m = map[string]entry{}
	}
	c.m[k] = entry{v, time.Now().Add(10 * time.Minute)}
}

func (c *lru) clear() {
	c.mu.Lock()
	c.m = map[string]entry{}
	c.mu.Unlock()
}

// ---- fixed-window rate limiter ----
type limiter struct {
	mu   sync.Mutex
	win  int64
	hits map[string]int
}

func (l *limiter) allow(ip string) bool {
	w := time.Now().Unix() / 60
	l.mu.Lock()
	defer l.mu.Unlock()
	if w != l.win {
		l.win, l.hits = w, map[string]int{}
	}
	l.hits[ip]++
	return l.hits[ip] <= rateMax
}

// ---- helpers ----
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func clean(s string) (string, bool) {
	s = strings.ToLower(strings.TrimSpace(strings.TrimLeft(s, "#")))
	return s, pat.MatchString(s)
}

func suggest(ctx context.Context, q string, n int) []string {
	key := "s:" + q + ":" + strconv.Itoa(n)
	if v, ok := cache.get(key); ok {
		return v
	}
	v, err := rdb.ZRevRange(ctx, "ac:"+q, 0, int64(n-1)).Result()
	if err != nil {
		return []string{}
	}
	cache.set(key, v)
	return v
}

func related(ctx context.Context, w string) []string {
	key := "r:" + w
	if v, ok := cache.get(key); ok {
		return v
	}
	out := []string{}
	if s, err := rdb.HGet(ctx, "rel", w).Result(); err == nil {
		json.Unmarshal([]byte(s), &out)
	}
	cache.set(key, out)
	return out
}

func middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Headers", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		ip := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-For"), ",")[0])
		if ip == "" {
			ip = r.RemoteAddr
		}
		if !rl.allow(ip) {
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"detail": "rate limit"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func main() {
	opt, err := redis.ParseURL(os.Getenv("REDIS_URL"))
	if err != nil {
		log.Fatal("bad REDIS_URL: ", err)
	}
	rdb = redis.NewClient(opt)

	mux := http.NewServeMux()

	mux.HandleFunc("GET /suggest", func(w http.ResponseWriter, r *http.Request) {
		q, ok := clean(r.URL.Query().Get("q"))
		if !ok {
			writeJSON(w, 400, map[string]string{"detail": "invalid input"})
			return
		}
		n, _ := strconv.Atoi(r.URL.Query().Get("n"))
		if n < 1 || n > 20 {
			n = 8
		}
		writeJSON(w, 200, suggest(r.Context(), q, n))
	})

	mux.HandleFunc("GET /related", func(w http.ResponseWriter, r *http.Request) {
		q, ok := clean(r.URL.Query().Get("w"))
		if !ok {
			writeJSON(w, 400, map[string]string{"detail": "invalid input"})
			return
		}
		writeJSON(w, 200, related(r.Context(), q))
	})

	mux.HandleFunc("GET /hashtags", func(w http.ResponseWriter, r *http.Request) {
		q, ok := clean(r.URL.Query().Get("q"))
		if !ok {
			writeJSON(w, 400, map[string]string{"detail": "invalid input"})
			return
		}
		seen, out := map[string]bool{}, []string{}
		for _, t := range append(suggest(r.Context(), q, 8), related(r.Context(), q)...) {
			if !seen[t] && len(out) < 10 {
				seen[t] = true
				out = append(out, "#"+t)
			}
		}
		writeJSON(w, 200, out)
	})

	mux.HandleFunc("POST /learn", func(w http.ResponseWriter, r *http.Request) {
		if writeKey == "" || r.Header.Get("X-API-Key") != writeKey {
			writeJSON(w, 401, map[string]string{"detail": "unauthorized"})
			return
		}
		word, ok := clean(r.URL.Query().Get("word"))
		if !ok {
			writeJSON(w, 400, map[string]string{"detail": "invalid input"})
			return
		}
		pipe := rdb.Pipeline()
		for i := 1; i <= len(word) && i <= 10; i++ {
			pipe.ZIncrBy(r.Context(), "ac:"+word[:i], 1, word)
		}
		if _, err := pipe.Exec(r.Context()); err != nil {
			writeJSON(w, 502, map[string]string{"detail": "redis error"})
			return
		}
		cache.clear()
		writeJSON(w, 200, map[string]bool{"ok": true})
	})

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]bool{"ok": true})
	})

	addr := ":" + env("PORT", "8000")
	log.Println("listening on", addr)
	log.Fatal((&http.Server{
		Addr:              addr,
		Handler:           middleware(mux),
		ReadHeaderTimeout: 5 * time.Second,
	}).ListenAndServe())
}
