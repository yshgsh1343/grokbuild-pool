// Command seed-sqlite inserts one smoke account into a sqlite catalog.
package main

import (
	"fmt"
	"os"
	"time"

	"github.com/yshgsh1343/grokbuild2api/internal/catalog"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: seed-sqlite <db-path>")
		os.Exit(2)
	}
	path := os.Args[1]
	c, err := catalog.Open(path)
	if err != nil {
		panic(err)
	}
	defer c.Close()
	now := time.Now().Unix()
	acc := catalog.Account{
		ID:           "smoke-1",
		Revision:     1,
		Priority:     100,
		Enabled:      true,
		Lifecycle:    catalog.LifecycleActive,
		AccessToken:  "access-smoke",
		RefreshToken: "refresh-smoke",
		ExpiresAt:    now + 3600,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := c.UpsertMany([]catalog.Account{acc}); err != nil {
		panic(err)
	}
	fmt.Println("seeded", path)
}
