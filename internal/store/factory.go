package store

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/yshgsh1343/grokbuild2api/internal/catalog"
)

// OpenFromFlags opens sqlite or postgres account store.
// backend: sqlite|postgres|auto
// sqlitePath used for sqlite; databaseURL for postgres.
func OpenFromFlags(ctx context.Context, backend, sqlitePath, databaseURL string) (AccountStore, string, error) {
	backend = strings.ToLower(strings.TrimSpace(backend))
	if backend == "" || backend == "auto" {
		if strings.TrimSpace(databaseURL) != "" || strings.TrimSpace(os.Getenv("DATABASE_URL")) != "" {
			backend = "postgres"
		} else {
			backend = "sqlite"
		}
	}
	switch backend {
	case "sqlite":
		path := firstNonEmpty(sqlitePath, os.Getenv("DB_PATH"))
		if path == "" {
			return nil, "", fmt.Errorf("store: sqlite requires -db/DB_PATH")
		}
		cat, err := catalog.Open(path)
		if err != nil {
			return nil, "", err
		}
		return NewSQLiteAccountStore(cat), "sqlite", nil
	case "postgres", "pg":
		url := firstNonEmpty(databaseURL, os.Getenv("DATABASE_URL"))
		if url == "" {
			return nil, "", fmt.Errorf("store: postgres requires -database-url/DATABASE_URL")
		}
		st, err := OpenPostgres(ctx, url)
		if err != nil {
			return nil, "", err
		}
		return st, "postgres", nil
	default:
		return nil, "", fmt.Errorf("store: unknown backend %q", backend)
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
