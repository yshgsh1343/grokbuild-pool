// Package bulkimport 编排并发的 JSON / SSO / NDJSON catalog 导入。
package bulkimport

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yshgsh1343/grokbuild2api/internal/authimport"
	"github.com/yshgsh1343/grokbuild2api/internal/catalog"
	"github.com/yshgsh1343/grokbuild2api/internal/ssoimport"
	"github.com/yshgsh1343/grokbuild2api/internal/synth"
)

// 格式取值。
const (
	FormatAuto   = "auto"
	FormatJSON   = "json"
	FormatSSO    = "sso"
	FormatNDJSON = "ndjson"
)

// 默认值。
const (
	DefaultWorkers     = 4
	MaxWorkers         = 16
	DefaultBatch       = 500
	ProgressEvery      = 5000
	DefaultMaxRSSBytes = 5 << 30 // 5 GiB
)

// Config 控制一次批量导入运行。
type Config struct {
	// Format 为 auto|json|sso|ndjson。
	Format string
	// Workers 为并发解析/转换的工作协程数（有上限）。
	Workers int
	// Batch 为 UpsertMany 分块大小。
	Batch int
	// DryRun 仅解析；不写库。
	DryRun bool
	// MaxRSSBytes 进程 RSS 超限时中止（0 = DefaultMaxRSSBytes）。
	MaxRSSBytes uint64
	// ProgressEvery 每 N 个成功账号打进度日志（0 = 包常量 ProgressEvery）。
	ProgressEvery int
	// Converter 可选 SSO 转换器（SSO 格式必需）。
	Converter ssoimport.Converter
	// MaxEntries 限制单次导入条目数；<=0 保持兼容无界行为。
	MaxEntries int
	// MaxNDJSONLineBytes 限制 NDJSON 单行；<=0 使用 1 MiB。
	MaxNDJSONLineBytes int
	// MaxSSOValueBytes 限制单个 SSO 值；<=0 保持兼容无界行为。
	MaxSSOValueBytes int
	// OnProgress 以累计成功数回调（可选）。
	OnProgress func(ok int, elapsed time.Duration, rssMB float64)
	// OnDetailProgress 细粒度进度 total/ok/failed/phase（parsing|converting|writing）。
	OnDetailProgress func(total, ok, failed int, phase string)
	// Now 覆盖墙钟（测试用）。
	Now func() time.Time
}

// FileResult 汇总单个输入文件。
type FileResult struct {
	Path    string `json:"path"`
	Format  string `json:"format"`
	Total   int    `json:"total"`
	OK      int    `json:"ok"`
	Failed  int    `json:"failed"`
	Skipped int    `json:"skipped"`
	Error   string `json:"error,omitempty"`
}

// Report 为整体导入摘要。
type Report struct {
	Total    int           `json:"total"`
	OK       int           `json:"ok"`
	Failed   int           `json:"failed"`
	Skipped  int           `json:"skipped"`
	Duration time.Duration `json:"duration"`
	Files    []FileResult  `json:"files"`
	DryRun   bool          `json:"dry_run"`
}

// CatalogWriter 为编排器使用的最小写接口。
type CatalogWriter interface {
	UpsertMany(accounts []catalog.Account) error
}

type importedCatalogWriter interface {
	UpsertImportedMany(accounts []catalog.Account) error
}

// ImportPaths 导入 path（文件或目录）下全部支持的文件。
func ImportPaths(ctx context.Context, cat CatalogWriter, path string, cfg Config) (Report, error) {
	cfg = normalizeConfig(cfg)
	files, err := collectInputFiles(path, cfg.Format)
	if err != nil {
		return Report{}, err
	}
	if len(files) == 0 {
		return Report{}, fmt.Errorf("bulkimport: no input files found under %s", path)
	}
	return ImportFiles(ctx, cat, files, cfg)
}

// InputFile 为一个路径 + 可选强制格式。
type InputFile struct {
	Path   string
	Format string // empty → auto from cfg + extension
}

// ImportFiles 并发解析/转换，并对 catalog 写操作串行化。
func ImportFiles(ctx context.Context, cat CatalogWriter, files []InputFile, cfg Config) (Report, error) {
	cfg = normalizeConfig(cfg)
	if !cfg.DryRun && cat == nil {
		return Report{}, fmt.Errorf("bulkimport: catalog is required unless dry-run")
	}
	start := time.Now()
	if cfg.Now != nil {
		start = cfg.Now()
	}

	type job struct {
		idx  int
		file InputFile
	}
	type result struct {
		idx      int
		fr       FileResult
		accounts []catalog.Account
	}

	jobs := make(chan job, len(files))
	results := make(chan result, len(files))

	workers := cfg.Workers
	// 多文件 + 总条目上限时串行，避免多文件同时预读放大内存。
	// 单文件则保持并行（SSO/JSON 内部还会再拆批）。
	if cfg.MaxEntries > 0 && len(files) > 1 {
		workers = 1
	}
	if workers > len(files) {
		workers = len(files)
	}
	if workers < 1 {
		workers = 1
	}

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				if ctx.Err() != nil {
					results <- result{idx: j.idx, fr: FileResult{
						Path: j.file.Path, Format: j.file.Format, Error: ctx.Err().Error(), Failed: 1,
					}}
					continue
				}
				fr, accounts := processFile(ctx, j.file, cfg)
				results <- result{idx: j.idx, fr: fr, accounts: accounts}
			}
		}()
	}

	go func() {
		for i, f := range files {
			jobs <- job{idx: i, file: f}
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()

	fileResults := make([]FileResult, len(files))
	var (
		totalOK, totalFail, totalSkip, totalEntries int
		writeBuf                                    []catalog.Account
		lastLogged                                  int
	)
	flush := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if cfg.DryRun || len(writeBuf) == 0 {
			writeBuf = writeBuf[:0]
			return nil
		}
		var err error
		if imported, ok := cat.(importedCatalogWriter); ok {
			err = imported.UpsertImportedMany(writeBuf)
		} else {
			err = cat.UpsertMany(writeBuf)
		}
		if err != nil {
			return err
		}
		writeBuf = writeBuf[:0]
		if err := checkRSS(cfg.MaxRSSBytes); err != nil {
			return err
		}
		return nil
	}

	for res := range results {
		fileResults[res.idx] = res.fr
		totalEntries += res.fr.Total
		totalFail += res.fr.Failed
		totalSkip += res.fr.Skipped
		if cfg.MaxEntries > 0 && len(files) > 1 && res.fr.Error != "" {
			return Report{
				Total: totalEntries, OK: totalOK, Failed: totalFail, Skipped: totalSkip,
				Duration: time.Since(start), Files: fileResults, DryRun: cfg.DryRun,
			}, fmt.Errorf("bulkimport: input validation failed: %s", res.fr.Error)
		}
		if cfg.MaxEntries > 0 && totalEntries > cfg.MaxEntries {
			return Report{}, fmt.Errorf("bulkimport: entry limit exceeded (max %d)", cfg.MaxEntries)
		}
		if res.fr.Error != "" && res.fr.OK == 0 && res.fr.Total == 0 {
			// 文件级失败已计入 Failed。
			continue
		}
		if len(res.accounts) > 0 {
			// 本文件内按 id 去重（后者覆盖）。
			seen := make(map[string]int, len(res.accounts))
			deduped := make([]catalog.Account, 0, len(res.accounts))
			for _, a := range res.accounts {
				if i, ok := seen[a.ID]; ok {
					deduped[i] = a
					totalSkip++
					fileResults[res.idx].Skipped++
					fileResults[res.idx].OK--
					continue
				}
				seen[a.ID] = len(deduped)
				deduped = append(deduped, a)
			}
			for _, a := range deduped {
				writeBuf = append(writeBuf, a)
				totalOK++
				// 始终按 batch 边解析边落库，避免万级账号攒到最后才写导致卡顿/OOM
				if len(writeBuf) >= cfg.Batch {
					if err := flush(); err != nil {
						return Report{}, err
					}
				}
				if totalOK-lastLogged >= cfg.ProgressEvery {
					elapsed := time.Since(start)
					if cfg.OnProgress != nil {
						cfg.OnProgress(totalOK, elapsed, rssMB())
					}
					lastLogged = totalOK
				}
			}
		}
	}
	if err := flush(); err != nil {
		return Report{}, err
	}

	// 去重调整后根据文件结果重算 OK。
	// totalOK 已跟踪排队写入的成功账号数。
	duration := time.Since(start)
	return Report{
		Total:    totalEntries,
		OK:       totalOK,
		Failed:   totalFail,
		Skipped:  totalSkip,
		Duration: duration,
		Files:    fileResults,
		DryRun:   cfg.DryRun,
	}, nil
}

func processFile(ctx context.Context, file InputFile, cfg Config) (FileResult, []catalog.Account) {
	fr := FileResult{Path: file.Path, Format: file.Format}
	if err := ctx.Err(); err != nil {
		fr.Error = err.Error()
		fr.Failed = 1
		return fr, nil
	}
	format := resolveFormat(file, nil, cfg.Format)
	if format == FormatNDJSON {
		return processNDJSONFile(ctx, file, cfg)
	}
	data, err := os.ReadFile(file.Path)
	if err != nil {
		fr.Error = "cannot read input file"
		fr.Failed = 1
		return fr, nil
	}
	if err := ctx.Err(); err != nil {
		fr.Error = err.Error()
		fr.Failed = 1
		return fr, nil
	}
	format = resolveFormat(file, data, cfg.Format)
	fr.Format = format

	switch format {
	case FormatJSON:
		creds, _, err := authimport.ParseGrokAuthJSONDetailedLimit(data, cfg.MaxEntries)
		if err != nil {
			fr.Error = err.Error()
			fr.Failed = 1
			return fr, nil
		}
		fr.Total = len(creds)
		if err := ctx.Err(); err != nil {
			fr.Error = err.Error()
			fr.Failed = len(creds)
			return fr, nil
		}
		now := time.Now()
		if cfg.Now != nil {
			now = cfg.Now()
		}
		out := make([]catalog.Account, 0, len(creds))
		for _, c := range creds {
			if err := ctx.Err(); err != nil {
				fr.Error = err.Error()
				fr.Failed += len(creds) - len(out)
				return fr, nil
			}
			a, err := authimport.ToAccount(c, now)
			if err != nil {
				fr.Failed++
				continue
			}
			out = append(out, a)
			fr.OK++
		}
		return fr, out

	case FormatSSO:
		if cfg.OnDetailProgress != nil {
			cfg.OnDetailProgress(0, 0, 0, "parsing")
		}
		values, err := ssoimport.ParseSSOValuesBounded(data, cfg.MaxEntries, cfg.MaxSSOValueBytes)
		if err != nil {
			fr.Error = err.Error()
			fr.Failed = 1
			return fr, nil
		}
		fr.Total = len(values)
		if cfg.OnDetailProgress != nil {
			cfg.OnDetailProgress(fr.Total, 0, 0, "converting")
		}
		if cfg.Converter == nil {
			fr.Error = ssoimport.ErrConverterRequired.Error()
			fr.Failed = len(values)
			return fr, nil
		}
		if ctx.Err() != nil {
			fr.Error = ctx.Err().Error()
			fr.Failed = len(values)
			return fr, nil
		}
		// 分块换票：每块完成后立刻回调进度（SSO 远慢于 JSON）
		chunk := 16
		if cfg.Workers >= 8 {
			chunk = 24
		}
		if cfg.Workers >= 12 {
			chunk = 32
		}
		now := time.Now()
		if cfg.Now != nil {
			now = cfg.Now()
		}
		out := make([]catalog.Account, 0, len(values))
		for i := 0; i < len(values); i += chunk {
			if err := ctx.Err(); err != nil {
				fr.Error = err.Error()
				remain := len(values) - fr.OK - fr.Failed
				if remain > 0 {
					fr.Failed += remain
				}
				return fr, out
			}
			end := i + chunk
			if end > len(values) {
				end = len(values)
			}
			part, err := cfg.Converter.Convert(ctx, values[i:end])
			if err != nil {
				fr.Failed += end - i
				if fr.Error == "" {
					fr.Error = err.Error()
				}
				if cfg.OnDetailProgress != nil {
					cfg.OnDetailProgress(fr.Total, fr.OK, fr.Failed, "converting")
				}
				continue
			}
			for _, c := range part {
				if c.Error != "" {
					fr.Failed++
					continue
				}
				a, err := ssoimport.ToAccount(c, now)
				if err != nil {
					fr.Failed++
					continue
				}
				out = append(out, a)
				fr.OK++
			}
			if cfg.OnDetailProgress != nil {
				cfg.OnDetailProgress(fr.Total, fr.OK, fr.Failed, "converting")
			}
		}
		if cfg.OnDetailProgress != nil {
			cfg.OnDetailProgress(fr.Total, fr.OK, fr.Failed, "writing")
		}
		return fr, out

	case FormatNDJSON:
		return processNDJSONFile(ctx, file, cfg)

	default:
		fr.Error = fmt.Sprintf("unsupported format %q", format)
		fr.Failed = 1
		return fr, nil
	}
}

func processNDJSONFile(ctx context.Context, file InputFile, cfg Config) (FileResult, []catalog.Account) {
	fr := FileResult{Path: file.Path, Format: FormatNDJSON}
	f, err := os.Open(file.Path)
	if err != nil {
		fr.Error = "cannot read input file"
		fr.Failed = 1
		return fr, nil
	}
	defer f.Close()
	var out []catalog.Account
	n, err := synth.ReadNDJSONStreamLimit(f, cfg.MaxNDJSONLineBytes, cfg.MaxEntries, func(a catalog.Account) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		out = append(out, a)
		return nil
	})
	fr.Total = n
	if err != nil {
		fr.Error = err.Error()
		fr.Failed = 1
		return fr, nil
	}
	fr.OK = len(out)
	return fr, out
}

func resolveFormat(file InputFile, data []byte, cfgFormat string) string {
	if f := strings.ToLower(strings.TrimSpace(file.Format)); f != "" && f != FormatAuto {
		return f
	}
	cfgFormat = strings.ToLower(strings.TrimSpace(cfgFormat))
	if cfgFormat != "" && cfgFormat != FormatAuto {
		return cfgFormat
	}
	ext := strings.ToLower(filepath.Ext(file.Path))
	switch ext {
	case ".ndjson", ".jsonl":
		return FormatNDJSON
	case ".json":
		return FormatJSON
	case ".txt":
		return FormatSSO
	}
	trimmed := strings.TrimSpace(string(data))
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		// 启发式：若像 SSO 字符串 JSON 列表，仅在强制时优先 SSO；
		// 可能是凭证的对象/数组默认按 JSON。
		return FormatJSON
	}
	return FormatSSO
}

func collectInputFiles(path string, format string) ([]InputFile, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	format = strings.ToLower(strings.TrimSpace(format))
	if !st.IsDir() {
		return []InputFile{{Path: path, Format: format}}, nil
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	var out []InputFile
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		ext := strings.ToLower(filepath.Ext(name))
		full := filepath.Join(path, name)
		switch format {
		case FormatJSON:
			if ext == ".json" {
				out = append(out, InputFile{Path: full, Format: FormatJSON})
			}
		case FormatSSO:
			if ext == ".txt" || ext == ".json" {
				out = append(out, InputFile{Path: full, Format: FormatSSO})
			}
		case FormatNDJSON:
			if ext == ".ndjson" || ext == ".jsonl" {
				out = append(out, InputFile{Path: full, Format: FormatNDJSON})
			}
		default: // auto
			switch ext {
			case ".json":
				out = append(out, InputFile{Path: full, Format: FormatAuto})
			case ".txt":
				out = append(out, InputFile{Path: full, Format: FormatSSO})
			case ".ndjson", ".jsonl":
				out = append(out, InputFile{Path: full, Format: FormatNDJSON})
			}
		}
	}
	return out, nil
}

func normalizeConfig(cfg Config) Config {
	cfg.Format = strings.ToLower(strings.TrimSpace(cfg.Format))
	if cfg.Format == "" {
		cfg.Format = FormatAuto
	}
	if cfg.Workers <= 0 {
		cfg.Workers = DefaultWorkers
	}
	// 小机器限制 worker（4C 已有 max 16；再按 GOMAXPROCS*4 封顶）。
	cap := MaxWorkers
	if n := runtime.NumCPU() * 4; n > 0 && n < cap {
		// 按规格 4C 也允许到 16；保留 MaxWorkers。
		_ = n
	}
	if cfg.Workers > MaxWorkers {
		cfg.Workers = MaxWorkers
	}
	if cfg.Batch < 1 {
		cfg.Batch = DefaultBatch
	}
	if cfg.MaxRSSBytes == 0 {
		cfg.MaxRSSBytes = DefaultMaxRSSBytes
	}
	if cfg.ProgressEvery <= 0 {
		cfg.ProgressEvery = ProgressEvery
	}
	if cfg.MaxNDJSONLineBytes <= 0 {
		cfg.MaxNDJSONLineBytes = 1024 * 1024
	}
	return cfg
}

func checkRSS(max uint64) error {
	if max == 0 {
		return nil
	}
	rss, ok := procRSSBytes()
	if !ok {
		return nil
	}
	if rss > max {
		return fmt.Errorf("bulkimport: memory guard: RSS %.1f GiB exceeds limit; abort import (reduce --batch)",
			float64(rss)/(1<<30))
	}
	return nil
}

// RSSMB 返回进程 RSS（MiB，供 CLI 进度）。
func RSSMB() float64 { return rssMB() }

func rssMB() float64 {
	if rss, ok := procRSSBytes(); ok {
		return float64(rss) / (1 << 20)
	}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return float64(ms.Sys) / (1 << 20)
}

func procRSSBytes() (uint64, bool) {
	b, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0, false
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "VmRSS:") {
			var kb uint64
			if _, err := fmt.Sscanf(line, "VmRSS: %d kB", &kb); err != nil {
				return 0, false
			}
			return kb * 1024, true
		}
	}
	return 0, false
}

// DefaultWorkerCount 返回本机合理的默认 worker 数。
func DefaultWorkerCount() int {
	n := runtime.NumCPU()
	if n < 1 {
		n = 1
	}
	if n > DefaultWorkers {
		// 典型 4C 机器优先 4，除非用户覆盖。
		if n >= 4 {
			return DefaultWorkers
		}
	}
	if n > MaxWorkers {
		return MaxWorkers
	}
	if n < 1 {
		return 1
	}
	return n
}

// 原子计数辅助，供测试/进度（可选）。
type Counters struct {
	OK, Failed, Skipped atomic.Int64
}
