// Command worker owns local hot pool shards and serves protocol + internal endpoints.
package main

import (
	"context"
	"flag"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/yshgsh1343/grokbuild2api/internal/clusterstate"
	"github.com/yshgsh1343/grokbuild2api/internal/store"
	"github.com/yshgsh1343/grokbuild2api/internal/worker"
)

func main() {
	listen := flag.String("listen", envOr("WORKER_LISTEN", "0.0.0.0:8081"), "listen address")
	workerID := flag.String("worker-id", envOr("WORKER_ID", "worker-0"), "worker id")
	dbPath := flag.String("db", envOr("DB_PATH", ""), "sqlite catalog path")
	databaseURL := flag.String("database-url", envOr("DATABASE_URL", ""), "postgres url")
	storeBackend := flag.String("store", envOr("SCHEME2_STORE", "auto"), "store backend: auto|sqlite|postgres")
	stateBackend := flag.String("state", envOr("SCHEME2_STATE", "memory"), "cluster state: memory|redis")
	redisURL := flag.String("redis-url", envOr("REDIS_URL", ""), "redis url when state=redis")
	hotSize := flag.Int("hot-size", 5000, "local hot size")
	shardCount := flag.Int("shards", 64, "global shard count")
	apiKey := flag.String("api-key", os.Getenv("API_KEY"), "optional static api key")
	upstream := flag.String("upstream", envOr("UPSTREAM_BASE_URL", ""), "upstream base url")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	st, storeName, err := store.OpenFromFlags(ctx, *storeBackend, *dbPath, *databaseURL)
	if err != nil {
		log.Fatal(err)
	}
	cs, stateName, err := clusterstate.Open(*stateBackend, *redisURL)
	if err != nil {
		_ = st.Close()
		log.Fatal(err)
	}
	log.Printf("worker store=%s state=%s", storeName, stateName)

	svc := worker.New(worker.Config{
		WorkerID:        *workerID,
		Listen:          *listen,
		ShardCount:      *shardCount,
		HotSize:         *hotSize,
		APIKey:          *apiKey,
		UpstreamBaseURL: *upstream,
	}, st, cs, slog.Default())

	go func() {
		if err := svc.Run(ctx); err != nil && err != context.Canceled {
			log.Printf("worker run ended: %v", err)
		}
	}()

	httpSrv := &http.Server{Addr: *listen, Handler: svc.Handler()}
	go func() {
		log.Printf("worker %s listening on %s", *workerID, *listen)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()

	<-ctx.Done()
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutdown)
	_ = cs.Close()
	_ = st.Close()
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
