// Command poolctl 为账号池运维 CLI：合成数据、导入、统计等。
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/yshgsh1343/grokbuild2api/internal/bulkimport"
	"github.com/yshgsh1343/grokbuild2api/internal/catalog"
	"github.com/yshgsh1343/grokbuild2api/internal/ssoimport"
	"github.com/yshgsh1343/grokbuild2api/internal/synth"
)

const version = "0.0.1-dev"

// 内存护栏：进程 RSS 超限则中止导入（字节）。
const maxRSSBytes = 5 << 30 // 5 GiB

// 导入过程中的进度日志间隔。
const progressEvery = 5000

// 默认导入批大小（UpsertMany 前持有的行数）。
const defaultBatch = 500

// 批量 JSON/SSO 导入的默认并发 worker 数。
const defaultWorkers = 4

func main() {
	if len(os.Args) < 2 {
		printUsage(os.Stderr)
		os.Exit(2)
	}
	cmd := os.Args[1]
	switch cmd {
	case "version", "--version", "-v":
		fmt.Printf("poolctl %s (scheme-B)\n", version)
	case "help", "-h", "--help":
		printUsage(os.Stdout)
	case "gen":
		if err := runGen(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "poolctl gen: %v\n", err)
			os.Exit(1)
		}
	case "import":
		if err := runImport(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "poolctl import: %v\n", err)
			os.Exit(1)
		}
	case "import-json":
		if err := runBulkImport(os.Args[2:], bulkimport.FormatJSON); err != nil {
			fmt.Fprintf(os.Stderr, "poolctl import-json: %v\n", err)
			os.Exit(1)
		}
	case "import-sso":
		if err := runBulkImport(os.Args[2:], bulkimport.FormatSSO); err != nil {
			fmt.Fprintf(os.Stderr, "poolctl import-sso: %v\n", err)
			os.Exit(1)
		}
	case "bulk-import":
		if err := runBulkImport(os.Args[2:], ""); err != nil {
			fmt.Fprintf(os.Stderr, "poolctl bulk-import: %v\n", err)
			os.Exit(1)
		}
	case "stats":
		if err := runStats(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "poolctl stats: %v\n", err)
			os.Exit(1)
		}
	case "prove":
		// 便捷：gen+import N 并打印 Stats.Count 与耗时。
		if err := runProve(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "poolctl prove: %v\n", err)
			os.Exit(1)
		}
	case "canary-init":
		// M12 脚手架：创建空 canary.db（不导入真号、不解锁 UNLOCK_M12）。
		if err := runCanaryInit(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "poolctl canary-init: %v\n", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", cmd)
		printUsage(os.Stderr)
		os.Exit(2)
	}
}

func printUsage(w *os.File) {
	fmt.Fprintf(w, "poolctl %s — scheme B account pool tooling\n", version)
	fmt.Fprintf(w, "usage:\n")
	fmt.Fprintf(w, "  poolctl version\n")
	fmt.Fprintf(w, "  poolctl gen --count N --out path [--seed S] [--spread-hours H]\n")
	fmt.Fprintf(w, "  poolctl import --db path --in path [--batch N]\n")
	fmt.Fprintf(w, "  poolctl import-json --db PATH --in FILE_OR_DIR [--workers N] [--batch N] [--dry-run]\n")
	fmt.Fprintf(w, "  poolctl import-sso  --db PATH --in FILE [--converter-url URL] [--api-key K] [--workers N] [--batch N] [--dry-run]\n")
	fmt.Fprintf(w, "  poolctl bulk-import --db PATH --in PATH --format auto|json|sso|ndjson [same flags]\n")
	fmt.Fprintf(w, "  poolctl stats --db path\n")
	fmt.Fprintf(w, "  poolctl prove --count N --data-dir path [--full140k]\n")
	fmt.Fprintf(w, "  poolctl canary-init --db path   # empty catalog for M12 (default data/canary.db)\n")
	fmt.Fprintf(w, "note: SSO conversion requires a converter service (--converter-url); see specs/import.md\n")
	fmt.Fprintf(w, "isolation: work only under /opt/grokbuild-pool; never touch /root/grokbuild-proxy\n")
}

func runGen(args []string) error {
	fs := flag.NewFlagSet("gen", flag.ContinueOnError)
	count := fs.Int("count", 0, "number of synthetic accounts")
	out := fs.String("out", "", "output NDJSON path")
	seed := fs.Int64("seed", 42, "RNG seed (0 → 42 for gen file)")
	spreadH := fs.Float64("spread-hours", 48, "expires_at spread window in hours")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *count <= 0 {
		return fmt.Errorf("--count must be > 0")
	}
	if *out == "" {
		return fmt.Errorf("--out is required")
	}
	if err := ensureParentDir(*out); err != nil {
		return err
	}
	start := time.Now()
	opts := synth.Options{
		Count:  *count,
		Seed:   *seed,
		Now:    time.Now(),
		Spread: time.Duration(*spreadH * float64(time.Hour)),
	}
	n, err := synth.WriteNDJSONFile(*out, opts)
	if err != nil {
		return err
	}
	st, _ := os.Stat(*out)
	size := int64(0)
	if st != nil {
		size = st.Size()
	}
	fmt.Printf("gen: wrote %d accounts to %s (%.1f MB) in %s\n",
		n, *out, float64(size)/(1<<20), time.Since(start).Round(time.Millisecond))
	return nil
}

func runImport(args []string) error {
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	dbPath := fs.String("db", "", "SQLite catalog path")
	inPath := fs.String("in", "", "input NDJSON path")
	batch := fs.Int("batch", defaultBatch, "upsert batch size")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dbPath == "" || *inPath == "" {
		return fmt.Errorf("--db and --in are required")
	}
	if *batch < 1 {
		return fmt.Errorf("--batch must be >= 1")
	}
	if err := ensureParentDir(*dbPath); err != nil {
		return err
	}

	f, err := os.Open(*inPath)
	if err != nil {
		return err
	}
	defer f.Close()

	cat, err := catalog.Open(*dbPath)
	if err != nil {
		return err
	}
	defer cat.Close()
	// DB 文件优先使用收紧权限。
	_ = os.Chmod(*dbPath, 0o600)

	start := time.Now()
	buf := make([]catalog.Account, 0, *batch)
	total := 0
	lastLogged := 0
	_, err = synth.ReadNDJSONStream(f, func(a catalog.Account) error {
		buf = append(buf, a)
		if len(buf) >= *batch {
			if err := cat.UpsertMany(buf); err != nil {
				return err
			}
			total += len(buf)
			buf = buf[:0]
			if total-lastLogged >= progressEvery {
				fmt.Fprintf(os.Stderr, "import: progress %d rows elapsed=%s rss_mb=%.1f\n",
					total, time.Since(start).Round(time.Millisecond), rssMB())
				lastLogged = total
			}
			if err := checkRSS(); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if len(buf) > 0 {
		if err := cat.UpsertMany(buf); err != nil {
			return err
		}
		total += len(buf)
	}
	elapsed := time.Since(start)
	st, err := cat.Stats()
	if err != nil {
		return err
	}
	dbSize := fileSize(*dbPath)
	fmt.Printf("import: upserted %d rows into %s in %s; Stats.Count=%d db_mb=%.2f rss_mb=%.1f batch=%d\n",
		total, *dbPath, elapsed.Round(time.Millisecond), st.Count, float64(dbSize)/(1<<20), rssMB(), *batch)
	return nil
}

func runStats(args []string) error {
	fs := flag.NewFlagSet("stats", flag.ContinueOnError)
	dbPath := fs.String("db", "", "SQLite catalog path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dbPath == "" {
		return fmt.Errorf("--db is required")
	}
	cat, err := catalog.Open(*dbPath)
	if err != nil {
		return err
	}
	defer cat.Close()
	st, err := cat.Stats()
	if err != nil {
		return err
	}
	fmt.Printf("count=%d enabled=%d active=%d cooldown=%d quarantine=%d disabled=%d\n",
		st.Count, st.EnabledCount, st.ActiveCount, st.CooldownCount, st.QuarantineCount, st.DisabledCount)
	return nil
}

// runBulkImport 处理 import-json、import-sso 与 bulk-import。
// forcedFormat 为空时读取 --format 标志（默认 auto）。
func runBulkImport(args []string, forcedFormat string) error {
	name := "bulk-import"
	switch forcedFormat {
	case bulkimport.FormatJSON:
		name = "import-json"
	case bulkimport.FormatSSO:
		name = "import-sso"
	}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	dbPath := fs.String("db", "", "SQLite catalog path")
	inPath := fs.String("in", "", "input file or directory")
	format := fs.String("format", "auto", "auto|json|sso|ndjson (bulk-import only)")
	workers := fs.Int("workers", defaultWorkers, "concurrent parse/convert workers (cap 16)")
	batch := fs.Int("batch", defaultBatch, "upsert batch size")
	dryRun := fs.Bool("dry-run", false, "parse/convert only; do not write DB")
	converterURL := fs.String("converter-url", "", "SSO converter base URL (…/v1/convert appended if missing)")
	apiKey := fs.String("api-key", strings.TrimSpace(os.Getenv("SSO_CONVERTER_API_KEY")), "SSO converter Bearer API key (or env SSO_CONVERTER_API_KEY; prefer env over argv)")
	allowInsecure := fs.Bool("allow-insecure", false, "allow http:// converter on loopback/private hosts")
	timeoutSec := fs.Int("timeout-sec", 300, "SSO converter request timeout seconds")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *inPath == "" {
		return fmt.Errorf("--in is required")
	}
	if !*dryRun && *dbPath == "" {
		return fmt.Errorf("--db is required (or pass --dry-run)")
	}
	if *batch < 1 {
		return fmt.Errorf("--batch must be >= 1")
	}
	if *workers < 1 {
		return fmt.Errorf("--workers must be >= 1")
	}
	if *workers > bulkimport.MaxWorkers {
		*workers = bulkimport.MaxWorkers
	}

	fmtFormat := forcedFormat
	if fmtFormat == "" {
		fmtFormat = strings.ToLower(strings.TrimSpace(*format))
	}

	cfg := bulkimport.Config{
		Format:        fmtFormat,
		Workers:       *workers,
		Batch:         *batch,
		DryRun:        *dryRun,
		MaxRSSBytes:   maxRSSBytes,
		ProgressEvery: progressEvery,
		OnProgress: func(ok int, elapsed time.Duration, rssMB float64) {
			fmt.Fprintf(os.Stderr, "bulk-import: progress ok=%d elapsed=%s rss_mb=%.1f\n",
				ok, elapsed.Round(time.Millisecond), rssMB)
		},
	}

	// SSO 需要转换器——纯解析的 dry-run 也不够，
	// dry-run 仍需转换器才能从 SSO cookie 产出账号。
	needsSSO := fmtFormat == bulkimport.FormatSSO || fmtFormat == bulkimport.FormatAuto
	if needsSSO || strings.TrimSpace(*converterURL) != "" {
		if strings.TrimSpace(*converterURL) != "" {
			client, err := ssoimport.NewClient(ssoimport.Config{
				Endpoint:      *converterURL,
				APIKey:        *apiKey,
				MaxBatch:      50,
				Timeout:       time.Duration(*timeoutSec) * time.Second,
				AllowInsecure: *allowInsecure,
			})
			if err != nil {
				return err
			}
			cfg.Converter = client
		} else if fmtFormat == bulkimport.FormatSSO {
			return fmt.Errorf("%w; example: --converter-url http://127.0.0.1:8091 --api-key SECRET --allow-insecure",
				ssoimport.ErrConverterRequired)
		}
	}

	var cat *catalog.Catalog
	if !*dryRun {
		if err := ensureParentDir(*dbPath); err != nil {
			return err
		}
		var err error
		cat, err = catalog.Open(*dbPath)
		if err != nil {
			return err
		}
		defer cat.Close()
		_ = os.Chmod(*dbPath, 0o600)
	}

	var writer bulkimport.CatalogWriter
	if cat != nil {
		writer = cat
	}

	start := time.Now()
	rep, err := bulkimport.ImportPaths(context.Background(), writer, *inPath, cfg)
	if err != nil {
		return err
	}

	// 全部失败时输出各文件错误。
	if rep.OK == 0 && rep.Failed > 0 {
		for _, fr := range rep.Files {
			if fr.Error != "" {
				fmt.Fprintf(os.Stderr, "bulk-import: file %s: %s\n", fr.Path, fr.Error)
			}
		}
	}

	dbCount := int64(-1)
	if cat != nil {
		if st, err := cat.Stats(); err == nil {
			dbCount = st.Count
		}
	}
	fmt.Printf("bulk-import: format=%s workers=%d dry_run=%v total=%d ok=%d failed=%d skipped=%d duration=%s",
		cfg.Format, cfg.Workers, rep.DryRun, rep.Total, rep.OK, rep.Failed, rep.Skipped,
		rep.Duration.Round(time.Millisecond))
	if dbCount >= 0 {
		fmt.Printf(" Stats.Count=%d db_mb=%.2f", dbCount, float64(fileSize(*dbPath))/(1<<20))
	}
	fmt.Printf(" rss_mb=%.1f wall=%s\n", rssMB(), time.Since(start).Round(time.Millisecond))
	for _, fr := range rep.Files {
		if fr.Error != "" || fr.Failed > 0 {
			fmt.Printf("  file %s format=%s total=%d ok=%d failed=%d skipped=%d err=%s\n",
				fr.Path, fr.Format, fr.Total, fr.OK, fr.Failed, fr.Skipped, fr.Error)
		}
	}
	if rep.OK == 0 && rep.Failed > 0 {
		return fmt.Errorf("import produced 0 ok accounts (%d failed)", rep.Failed)
	}
	return nil
}

func runProve(args []string) error {
	fs := flag.NewFlagSet("prove", flag.ContinueOnError)
	count := fs.Int("count", 10_000, "accounts to generate and import")
	dataDir := fs.String("data-dir", "/opt/grokbuild-pool/data", "directory for ndjson + db")
	full := fs.Bool("full140k", false, "set count to 140000")
	batch := fs.Int("batch", defaultBatch, "import batch size")
	seed := fs.Int64("seed", 42, "RNG seed")
	if err := fs.Parse(args); err != nil {
		return err
	}
	n := *count
	if *full {
		n = 140_000
	}
	if n <= 0 {
		return fmt.Errorf("--count must be > 0")
	}
	if err := os.MkdirAll(*dataDir, 0o755); err != nil {
		return err
	}
	ndjson := filepath.Join(*dataDir, fmt.Sprintf("synth-%d.ndjson", n))
	dbPath := filepath.Join(*dataDir, fmt.Sprintf("pool-%d.db", n))
	// 每次运行使用全新 prove DB（幂等重导亦可；计时更干净）。
	_ = os.Remove(dbPath)
	_ = os.Remove(dbPath + "-wal")
	_ = os.Remove(dbPath + "-shm")

	fmt.Printf("prove: N=%d data_dir=%s\n", n, *dataDir)
	t0 := time.Now()
	if err := runGen([]string{
		"--count", fmt.Sprintf("%d", n),
		"--out", ndjson,
		"--seed", fmt.Sprintf("%d", *seed),
	}); err != nil {
		return err
	}
	genDur := time.Since(t0)

	t1 := time.Now()
	if err := runImport([]string{
		"--db", dbPath,
		"--in", ndjson,
		"--batch", fmt.Sprintf("%d", *batch),
	}); err != nil {
		return err
	}
	importDur := time.Since(t1)
	totalDur := time.Since(t0)

	cat, err := catalog.Open(dbPath)
	if err != nil {
		return err
	}
	st, err := cat.Stats()
	_ = cat.Close()
	if err != nil {
		return err
	}
	ok := st.Count == int64(n)
	fmt.Printf("prove: Stats.Count=%d expected=%d match=%v gen=%s import=%s total=%s db_mb=%.2f rss_mb=%.1f\n",
		st.Count, n, ok, genDur.Round(time.Millisecond), importDur.Round(time.Millisecond),
		totalDur.Round(time.Millisecond), float64(fileSize(dbPath))/(1<<20), rssMB())
	if !ok {
		return fmt.Errorf("Stats.Count %d != N %d", st.Count, n)
	}
	if n == 10_000 && importDur > 60*time.Second {
		return fmt.Errorf("import 10k took %s (> 60s acceptance)", importDur)
	}
	return nil
}

// runCanaryInit 创建空 catalog SQLite（schema only），供 M12 canary 使用。
// 不导入任何真号；不修改 STATUS.md / UNLOCK_M12。
func runCanaryInit(args []string) error {
	fs := flag.NewFlagSet("canary-init", flag.ContinueOnError)
	dbPath := fs.String("db", "/opt/grokbuild-pool/data/canary.db", "empty canary catalog path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	path := strings.TrimSpace(*dbPath)
	if path == "" {
		return fmt.Errorf("--db is required")
	}
	if err := ensureParentDir(path); err != nil {
		return err
	}
	// 若已存在则打开校验 schema；不删除已有数据（安全）。
	existed := false
	if st, err := os.Stat(path); err == nil && !st.IsDir() {
		existed = true
	}
	cat, err := catalog.Open(path)
	if err != nil {
		return err
	}
	defer cat.Close()
	_ = os.Chmod(path, 0o600)
	st, err := cat.Stats()
	if err != nil {
		return err
	}
	if existed {
		fmt.Printf("canary-init: opened existing %s Stats.Count=%d (not wiped; import real creds manually ≤50)\n",
			path, st.Count)
	} else {
		fmt.Printf("canary-init: created empty catalog %s Stats.Count=%d\n", path, st.Count)
	}
	fmt.Printf("canary-init: next steps → docs/CANARY.md; do NOT set UNLOCK_M12 without human approval\n")
	return nil
}


func ensureParentDir(path string) error {
	dir := filepath.Dir(path)
	if dir == "" || dir == "." {
		return nil
	}
	return os.MkdirAll(dir, 0o755)
}

func fileSize(path string) int64 {
	st, err := os.Stat(path)
	if err != nil {
		return 0
	}
	// 若有 WAL 则计入粗略大小。
	total := st.Size()
	if w, err := os.Stat(path + "-wal"); err == nil {
		total += w.Size()
	}
	return total
}

func rssMB() float64 {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	// Sys 为 Go 堆；进程 RSS 尽量读 /proc。
	if rss, ok := procRSSBytes(); ok {
		return float64(rss) / (1 << 20)
	}
	return float64(ms.Sys) / (1 << 20)
}

func checkRSS() error {
	rss, ok := procRSSBytes()
	if !ok {
		return nil
	}
	if rss > maxRSSBytes {
		return fmt.Errorf("memory guard: RSS %.1f GiB exceeds 5 GiB; abort import (reduce --batch)",
			float64(rss)/(1<<30))
	}
	return nil
}

func procRSSBytes() (uint64, bool) {
	// Linux：/proc/self/status 中的 VmRSS（kB）。
	b, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0, false
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "VmRSS:") {
			var kb uint64
			_, err := fmt.Sscanf(line, "VmRSS: %d kB", &kb)
			if err != nil {
				return 0, false
			}
			return kb * 1024, true
		}
	}
	return 0, false
}
