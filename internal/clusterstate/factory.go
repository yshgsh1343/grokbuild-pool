package clusterstate

import (
	"fmt"
	"os"
	"strings"
)

// OpenFromEnv opens Redis when REDIS_URL/SCHEME2_REDIS_URL is set; otherwise Memory.
func OpenFromEnv() (State, string, error) {
	addr := firstNonEmpty(os.Getenv("REDIS_URL"), os.Getenv("SCHEME2_REDIS_URL"))
	if strings.TrimSpace(addr) == "" {
		return NewMemory(), "memory", nil
	}
	st, err := NewRedis(addr)
	if err != nil {
		return nil, "", err
	}
	return st, "redis", nil
}

// Open opens Memory or Redis from explicit backend selector.
// backend: memory|redis
// redisAddr used when backend=redis (or when redis and addr empty, fall back to env).
func Open(backend, redisAddr string) (State, string, error) {
	backend = strings.ToLower(strings.TrimSpace(backend))
	switch backend {
	case "", "memory", "mem":
		return NewMemory(), "memory", nil
	case "redis":
		addr := firstNonEmpty(redisAddr, os.Getenv("REDIS_URL"), os.Getenv("SCHEME2_REDIS_URL"))
		if addr == "" {
			return nil, "", fmt.Errorf("clusterstate: redis backend requires --redis-url or REDIS_URL")
		}
		st, err := NewRedis(addr)
		if err != nil {
			return nil, "", err
		}
		return st, "redis", nil
	default:
		return nil, "", fmt.Errorf("clusterstate: unknown backend %q", backend)
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
