// Command refresher drains token refresh due queue for Scheme 2.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/yshgsh1343/grokbuild2api/internal/catalog"
	"github.com/yshgsh1343/grokbuild2api/internal/clusterstate"
	"github.com/yshgsh1343/grokbuild2api/internal/refresher"
	"github.com/yshgsh1343/grokbuild2api/internal/store"
)

type noopOAuth struct{}

func (noopOAuth) Refresh(ctx context.Context, acc catalog.Account) (catalog.TokenSet, error) {
	_ = ctx
	_ = acc
	return catalog.TokenSet{}, fmt.Errorf("oauth disabled in bootstrap refresher; wire real TokenRefresher")
}

func main() {
	dbPath := flag.String("db", envOr("DB_PATH", ""), "sqlite catalog path")
	databaseURL := flag.String("database-url", envOr("DATABASE_URL", ""), "postgres url")
	storeBackend := flag.String("store", envOr("SCHEME2_STORE", "auto"), "store backend: auto|sqlite|postgres")
	stateBackend := flag.String("state", envOr("SCHEME2_STATE", "memory"), "cluster state: memory|redis")
	redisURL := flag.String("redis-url", envOr("REDIS_URL", ""), "redis url when state=redis")
	id := flag.String("id", envOr("REFRESHER_ID", "refresher-0"), "refresher id")
	enqueue := flag.Bool("enqueue-expiring", true, "scan expiring accounts into due queue on start")
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
	log.Printf("refresher store=%s state=%s", storeName, stateName)

	svc := refresher.New(refresher.Config{RefresherID: *id}, st, cs, noopOAuth{}, slog.Default())
	if *enqueue {
		n, err := svc.EnqueueExpiring(ctx, 15*time.Minute, 1000)
		if err != nil {
			log.Printf("enqueue expiring failed: %v", err)
		} else {
			log.Printf("enqueued expiring accounts: %d", n)
		}
	}

	log.Printf("refresher %s running", *id)
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
