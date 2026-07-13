// Command controlplane builds shard worksets for Scheme 2 workers.
package main

import (
	"context"
	"flag"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/yshgsh1343/grokbuild2api/internal/clusterstate"
	"github.com/yshgsh1343/grokbuild2api/internal/controlplane"
	"github.com/yshgsh1343/grokbuild2api/internal/store"
)

func main() {
	dbPath := flag.String("db", envOr("DB_PATH", ""), "sqlite catalog path")
	databaseURL := flag.String("database-url", envOr("DATABASE_URL", ""), "postgres url")
	storeBackend := flag.String("store", envOr("SCHEME2_STORE", "auto"), "store backend: auto|sqlite|postgres")
	stateBackend := flag.String("state", envOr("SCHEME2_STATE", "memory"), "cluster state: memory|redis")
	redisURL := flag.String("redis-url", envOr("REDIS_URL", ""), "redis url when state=redis")
	workset := flag.Int("workset", 30000, "target workset size")
	shards := flag.Int("shards", 64, "shard count")
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
	log.Printf("controlplane store=%s state=%s", storeName, stateName)

	svc := controlplane.New(st, cs, controlplane.Config{
		ShardCount:  *shards,
		WorksetSize: *workset,
	}, slog.Default())

	log.Printf("controlplane running workset=%d shards=%d", *workset, *shards)
	if err := svc.Run(ctx); err != nil && err != context.Canceled {
		log.Fatal(err)
	}
	_ = cs.Close()
	_ = st.Close()
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
