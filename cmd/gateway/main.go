// Command gateway is the Scheme 2 client entry (auth, limit, sticky route).
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/yshgsh1343/grokbuild2api/internal/clusterstate"
	"github.com/yshgsh1343/grokbuild2api/internal/gateway"
)

func main() {
	listen := flag.String("listen", envOr("GATEWAY_LISTEN", "0.0.0.0:8080"), "listen address")
	workers := flag.String("workers", envOr("GATEWAY_WORKERS", "http://127.0.0.1:8081"), "comma-separated worker base URLs")
	apiKey := flag.String("api-key", os.Getenv("API_KEY"), "optional static API key")
	adminKey := flag.String("admin-key", os.Getenv("ADMIN_KEY"), "admin key for status")
	maxConcurrent := flag.Int64("max-concurrent", 2000, "global concurrent requests")
	stateBackend := flag.String("state", envOr("SCHEME2_STATE", "memory"), "cluster state backend: memory|redis")
	redisURL := flag.String("redis-url", envOr("REDIS_URL", ""), "redis url when state=redis")
	flag.Parse()

	state, backend, err := clusterstate.Open(*stateBackend, *redisURL)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("gateway state backend=%s", backend)

	cfg := gateway.Config{
		Listen:         *listen,
		WorkerBaseURLs: splitCSV(*workers),
		MaxConcurrent:  *maxConcurrent,
		APIKey:         *apiKey,
		AdminKey:       *adminKey,
	}
	srv := gateway.New(cfg, state)
	httpSrv := &http.Server{Addr: cfg.Listen, Handler: srv.Handler()}
	go func() {
		log.Printf("gateway listening on %s workers=%v", cfg.Listen, cfg.WorkerBaseURLs)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutdown)
	_ = state.Close()
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
