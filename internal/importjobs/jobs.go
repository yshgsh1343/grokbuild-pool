// Package importjobs 提供内存中的异步 bulkimport 任务表与受控上传 staging。
package importjobs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/yshgsh1343/grokbuild2api/internal/bulkimport"
	"github.com/yshgsh1343/grokbuild2api/internal/catalog"
	"github.com/yshgsh1343/grokbuild2api/internal/ssoimport"
)

// 任务状态。
const (
	StateQueued    = "queued"
	StateRunning   = "running"
	StateSucceeded = "succeeded"
	StateFailed    = "failed"
)

var (
	ErrNotFound             = errors.New("importjobs: job not found")
	ErrInvalidPath          = errors.New("importjobs: invalid input path")
	ErrInvalidFormat        = errors.New("importjobs: invalid format")
	ErrBusy                 = errors.New("importjobs: too many concurrent jobs")
	ErrUploadTooLarge       = errors.New("importjobs: upload exceeds size limit")
	ErrEmptyUpload          = errors.New("importjobs: upload is empty")
	ErrServerPathDisabled   = errors.New("importjobs: server path import is disabled")
	ErrConverterUnavailable = errors.New("importjobs: SSO converter is not configured")
	ErrClosed               = errors.New("importjobs: manager is closed")
)

// Job 单次导入任务公开快照。绝不暴露服务端 staging 路径。
type Job struct {
	ID         string    `json:"id"`
	Format     string    `json:"format"`
	SourceName string    `json:"source_name"`
	State      string    `json:"state"`
	Total      int       `json:"total"`
	OK         int       `json:"ok"`
	Fail       int       `json:"fail"`
	Skipped    int       `json:"skipped,omitempty"`
	Phase      string    `json:"phase,omitempty"`
	Message    string    `json:"message,omitempty"`
	Error      string    `json:"error,omitempty"`
	Started    time.Time `json:"started,omitempty"`
	Finished   time.Time `json:"finished,omitempty"`
}

// CreateRequest 为旧版服务端路径任务请求。
type CreateRequest struct {
	Format string `json:"format"` // auto|json|sso|ndjson
	Path   string `json:"path"`   // 仅 allow_server_path=true 时可用
}

// Options 控制任务、解析上限和受信任的 SSO 转换器。
type Options struct {
	MaxConcurrentJobs  int
	Workers            int
	MaxEntries         int
	MaxNDJSONLineBytes int
	MaxSSOValueBytes   int
	JobTimeout         time.Duration
	StagingStaleAfter  time.Duration
	AllowServerPath    bool
	Converter          ssoimport.Converter
	AfterImport        func() error
}

// StagedUpload 是 handler 写入完成、等待任务接管的受控临时文件。
type StagedUpload struct {
	Path       string
	SourceName string
	stagingDir string
}

// UploadPermit 在读取 multipart 文件前预留一个导入槽位，限制并发磁盘写入。
type UploadPermit struct {
	manager *Manager
}

// Release 释放尚未被 SubmitReservedUpload 消费的槽位。
func (p *UploadPermit) Release() {
	if p == nil || p.manager == nil {
		return
	}
	p.manager.mu.Lock()
	defer p.manager.mu.Unlock()
	if _, ok := p.manager.permits[p]; !ok {
		return
	}
	delete(p.manager.permits, p)
	if p.manager.uploading > 0 {
		p.manager.uploading--
	}
}

// Remove 删除尚未由任务接管的 ready 上传文件。
func (s StagedUpload) Remove() error {
	path := filepath.Clean(strings.TrimSpace(s.Path))
	stagingDir := filepath.Clean(strings.TrimSpace(s.stagingDir))
	if path == "." || stagingDir == "." {
		return nil
	}
	if filepath.Dir(path) != stagingDir || !validReadyName(filepath.Base(path)) {
		return ErrInvalidPath
	}
	st, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !st.Mode().IsRegular() {
		return ErrInvalidPath
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

type jobInput struct {
	path       string
	sourceName string
	cleanup    bool
}

// Manager 内存任务表 + 异步 worker。
type Manager struct {
	dataDir    string
	stagingDir string
	cat        *catalog.Catalog
	opts       Options
	initErr    error

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu        sync.Mutex
	jobs      map[string]*Job
	order     []string // 旧→新；List 时倒序
	permits   map[*UploadPermit]struct{}
	maxKeep   int
	running   int
	uploading int
	closing   bool
}

// OptionsSnapshot 返回当前导入限制副本（管理台展示/编辑）。
func (m *Manager) OptionsSnapshot() Options {
	if m == nil {
		return Options{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.opts
}

// ApplyOptions 热更新导入限制（不影响已在跑的任务；新任务立即生效）。
// Converter 指针若传入非 nil 则替换；nil 表示保留原转换器。
func (m *Manager) ApplyOptions(in Options) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cur := m.opts
	if in.MaxConcurrentJobs > 0 {
		cur.MaxConcurrentJobs = in.MaxConcurrentJobs
	}
	if in.Workers > 0 {
		cur.Workers = in.Workers
	}
	if in.MaxEntries > 0 {
		cur.MaxEntries = in.MaxEntries
	}
	if in.MaxNDJSONLineBytes > 0 {
		cur.MaxNDJSONLineBytes = in.MaxNDJSONLineBytes
	}
	if in.MaxSSOValueBytes > 0 {
		cur.MaxSSOValueBytes = in.MaxSSOValueBytes
	}
	if in.JobTimeout > 0 {
		cur.JobTimeout = in.JobTimeout
	}
	if in.StagingStaleAfter > 0 {
		cur.StagingStaleAfter = in.StagingStaleAfter
	}
	// bool 与 converter 显式覆盖
	cur.AllowServerPath = in.AllowServerPath
	if in.Converter != nil {
		cur.Converter = in.Converter
	}
	if in.AfterImport != nil {
		cur.AfterImport = in.AfterImport
	}
	m.opts = cur
}

// New 保留旧调用方行为：允许 data_dir 下本地单文件路径。
func New(dataDir string, cat *catalog.Catalog) *Manager {
	m, err := newManager(dataDir, cat, Options{AllowServerPath: true})
	if m == nil {
		m = &Manager{cat: cat, jobs: make(map[string]*Job), maxKeep: 100}
	}
	m.initErr = err
	return m
}

// NewWithOptions 创建启用浏览器上传的管理器，并严格初始化 staging。
func NewWithOptions(dataDir string, cat *catalog.Catalog, opts Options) (*Manager, error) {
	return newManager(dataDir, cat, opts)
}

func newManager(dataDir string, cat *catalog.Catalog, opts Options) (*Manager, error) {
	dataDir = filepath.Clean(strings.TrimSpace(dataDir))
	if dataDir == "." || dataDir == "" {
		return nil, fmt.Errorf("importjobs: empty data dir")
	}
	if abs, err := filepath.Abs(dataDir); err == nil {
		dataDir = abs
	}
	if opts.MaxConcurrentJobs <= 0 {
		opts.MaxConcurrentJobs = 2
	}
	if opts.Workers <= 0 {
		opts.Workers = bulkimport.DefaultWorkerCount()
	}
	if opts.JobTimeout <= 0 {
		opts.JobTimeout = 2 * time.Hour
	}
	if opts.StagingStaleAfter <= 0 {
		opts.StagingStaleAfter = 24 * time.Hour
	}
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{
		dataDir:    dataDir,
		stagingDir: filepath.Join(dataDir, ".import-staging"),
		cat:        cat,
		opts:       opts,
		ctx:        ctx,
		cancel:     cancel,
		jobs:       make(map[string]*Job),
		permits:    make(map[*UploadPermit]struct{}),
		maxKeep:    100,
	}
	if err := m.initStaging(); err != nil {
		cancel()
		return nil, err
	}
	return m, nil
}

func (m *Manager) initStaging() error {
	if err := os.MkdirAll(m.dataDir, 0o700); err != nil {
		return fmt.Errorf("importjobs: create data dir: %w", err)
	}
	if st, err := os.Lstat(m.stagingDir); err == nil {
		if st.Mode()&os.ModeSymlink != 0 || !st.IsDir() {
			return fmt.Errorf("importjobs: unsafe staging directory")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("importjobs: inspect staging directory: %w", err)
	} else if err := os.Mkdir(m.stagingDir, 0o700); err != nil {
		return fmt.Errorf("importjobs: create staging directory: %w", err)
	}
	if err := os.Chmod(m.stagingDir, 0o700); err != nil {
		return fmt.Errorf("importjobs: secure staging directory: %w", err)
	}
	m.cleanupStaleUploads(time.Now())
	return nil
}

func (m *Manager) ensureStagingSafe() error {
	st, err := os.Lstat(m.stagingDir)
	if err != nil || st.Mode()&os.ModeSymlink != 0 || !st.IsDir() {
		return fmt.Errorf("importjobs: unsafe staging directory")
	}
	return nil
}

func (m *Manager) cleanupStaleUploads(now time.Time) {
	entries, err := os.ReadDir(m.stagingDir)
	if err != nil {
		return
	}
	cutoff := now.Add(-m.opts.StagingStaleAfter)
	for _, entry := range entries {
		name := entry.Name()
		if !validStagingName(name) {
			continue
		}
		path := filepath.Join(m.stagingDir, name)
		st, err := os.Lstat(path)
		if err != nil || !st.Mode().IsRegular() || !st.ModTime().Before(cutoff) {
			continue
		}
		_ = os.Remove(path)
	}
}

func validStagingName(name string) bool {
	if !strings.HasPrefix(name, "upload-") {
		return false
	}
	suffix := ""
	for _, candidate := range []string{".part", ".ready", ".work"} {
		if strings.HasSuffix(name, candidate) {
			suffix = candidate
			break
		}
	}
	if suffix == "" {
		return false
	}
	hexPart := strings.TrimSuffix(strings.TrimPrefix(name, "upload-"), suffix)
	if len(hexPart) != 32 {
		return false
	}
	_, err := hex.DecodeString(hexPart)
	return err == nil
}

func validReadyName(name string) bool {
	return strings.HasSuffix(name, ".ready") && validStagingName(name)
}

// ReserveUpload 在读取请求体前预留容量；调用方必须 defer Release。
func (m *Manager) ReserveUpload() (*UploadPermit, error) {
	if m == nil || m.initErr != nil {
		return nil, fmt.Errorf("importjobs: not configured")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing {
		return nil, ErrClosed
	}
	if m.running+m.uploading >= m.opts.MaxConcurrentJobs {
		return nil, ErrBusy
	}
	m.uploading++
	permit := &UploadPermit{manager: m}
	m.permits[permit] = struct{}{}
	return permit, nil
}

// StageUpload 兼容直接调用方，并在暂存期间自行占用上传槽位。
func (m *Manager) StageUpload(r io.Reader, sourceName string, maxBytes int64) (StagedUpload, error) {
	permit, err := m.ReserveUpload()
	if err != nil {
		return StagedUpload{}, err
	}
	defer permit.Release()
	return m.StageReservedUpload(r, sourceName, maxBytes, permit)
}

// StageReservedUpload 使用已预留的槽位把上传流写入受控 staging。
func (m *Manager) StageReservedUpload(r io.Reader, sourceName string, maxBytes int64, permit *UploadPermit) (StagedUpload, error) {
	if m == nil || m.initErr != nil || strings.TrimSpace(m.stagingDir) == "" {
		return StagedUpload{}, fmt.Errorf("importjobs: staging is not configured")
	}
	m.mu.Lock()
	_, permitOK := m.permits[permit]
	closing := m.closing
	m.mu.Unlock()
	if permit == nil || permit.manager != m || !permitOK {
		return StagedUpload{}, fmt.Errorf("importjobs: invalid upload permit")
	}
	if closing {
		return StagedUpload{}, ErrClosed
	}
	if r == nil {
		return StagedUpload{}, ErrEmptyUpload
	}
	if err := m.ensureStagingSafe(); err != nil {
		return StagedUpload{}, err
	}
	random, err := randomHex(16)
	if err != nil {
		return StagedUpload{}, fmt.Errorf("importjobs: generate upload id")
	}
	name := "upload-" + random
	part := filepath.Join(m.stagingDir, name+".part")
	ready := filepath.Join(m.stagingDir, name+".ready")
	f, err := os.OpenFile(part, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return StagedUpload{}, fmt.Errorf("importjobs: create staged upload")
	}
	cleanup := true
	defer func() {
		_ = f.Close()
		if cleanup {
			_ = os.Remove(part)
			_ = os.Remove(ready)
		}
	}()
	// maxBytes <= 0：不限体积，只按 max_entries 做业务闸门。
	var written int64
	if maxBytes <= 0 {
		written, err = io.Copy(f, r)
	} else {
		written, err = io.Copy(f, io.LimitReader(r, maxBytes+1))
	}
	if err != nil {
		return StagedUpload{}, fmt.Errorf("importjobs: write staged upload: %w", err)
	}
	if written == 0 {
		return StagedUpload{}, ErrEmptyUpload
	}
	if maxBytes > 0 && written > maxBytes {
		return StagedUpload{}, ErrUploadTooLarge
	}
	if err := f.Close(); err != nil {
		return StagedUpload{}, fmt.Errorf("importjobs: close staged upload")
	}
	if err := os.Rename(part, ready); err != nil {
		return StagedUpload{}, fmt.Errorf("importjobs: finalize staged upload")
	}
	cleanup = false
	return StagedUpload{Path: ready, SourceName: sanitizeSourceName(sourceName), stagingDir: m.stagingDir}, nil
}

// List 按创建倒序返回任务快照（最多 limit）。
func (m *Manager) List(limit int) []Job {
	if m == nil {
		return nil
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Job, 0, min(limit, len(m.order)))
	for i := len(m.order) - 1; i >= 0 && len(out) < limit; i-- {
		if j := m.jobs[m.order[i]]; j != nil {
			out = append(out, *j)
		}
	}
	return out
}

// Get 按 id 取任务。
func (m *Manager) Get(id string) (Job, error) {
	if m == nil {
		return Job{}, ErrNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	if !ok || j == nil {
		return Job{}, ErrNotFound
	}
	return *j, nil
}

// Submit 校验旧版 path/format 后入队；默认配置应关闭该能力。
func (m *Manager) Submit(req CreateRequest) (Job, error) {
	if m == nil || m.cat == nil || m.initErr != nil {
		return Job{}, fmt.Errorf("importjobs: not configured")
	}
	if !m.opts.AllowServerPath {
		return Job{}, ErrServerPathDisabled
	}
	format, err := m.validateFormat(req.Format, true)
	if err != nil {
		return Job{}, err
	}
	path, err := m.safePath(req.Path)
	if err != nil {
		return Job{}, err
	}
	return m.submit(jobInput{path: path, sourceName: sanitizeSourceName(filepath.Base(path))}, format, nil)
}

// SubmitUpload 把 staged 文件所有权转给异步任务。仅成功返回后才转移所有权。
func (m *Manager) SubmitUpload(format string, upload StagedUpload) (Job, error) {
	if m == nil || m.cat == nil || m.initErr != nil {
		return Job{}, fmt.Errorf("importjobs: not configured")
	}
	format, err := m.validateFormat(format, false)
	if err != nil {
		return Job{}, err
	}
	path, err := m.safeStagedPath(upload.Path)
	if err != nil {
		return Job{}, err
	}
	return m.submit(jobInput{path: path, sourceName: sanitizeSourceName(upload.SourceName), cleanup: true}, format, nil)
}

// SubmitReservedUpload 原子消费上传预留并把 staged 文件转为运行任务。
func (m *Manager) SubmitReservedUpload(format string, upload StagedUpload, permit *UploadPermit) (Job, error) {
	if m == nil || m.cat == nil || m.initErr != nil {
		return Job{}, fmt.Errorf("importjobs: not configured")
	}
	format, err := m.validateFormat(format, false)
	if err != nil {
		return Job{}, err
	}
	path, err := m.safeStagedPath(upload.Path)
	if err != nil {
		return Job{}, err
	}
	return m.submit(jobInput{path: path, sourceName: sanitizeSourceName(upload.SourceName), cleanup: true}, format, permit)
}

func (m *Manager) validateFormat(raw string, allowAuto bool) (string, error) {
	format := strings.ToLower(strings.TrimSpace(raw))
	if format == "" && allowAuto {
		format = bulkimport.FormatAuto
	}
	switch format {
	case bulkimport.FormatJSON, bulkimport.FormatNDJSON:
		return format, nil
	case bulkimport.FormatSSO:
		if m.opts.Converter == nil {
			return "", ErrConverterUnavailable
		}
		return format, nil
	case bulkimport.FormatAuto:
		if allowAuto {
			return format, nil
		}
	}
	return "", fmt.Errorf("%w: %s", ErrInvalidFormat, format)
}

func (m *Manager) submit(input jobInput, format string, permit *UploadPermit) (Job, error) {
	random, err := randomHex(8)
	if err != nil {
		return Job{}, fmt.Errorf("importjobs: generate job id")
	}
	id := "imp_" + random
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing {
		return Job{}, ErrClosed
	}
	if permit != nil {
		if permit.manager != m {
			return Job{}, fmt.Errorf("importjobs: invalid upload permit")
		}
		if _, ok := m.permits[permit]; !ok {
			return Job{}, fmt.Errorf("importjobs: invalid upload permit")
		}
	} else if m.running+m.uploading >= m.opts.MaxConcurrentJobs {
		return Job{}, ErrBusy
	}
	if _, exists := m.jobs[id]; exists {
		return Job{}, fmt.Errorf("importjobs: generate unique job id")
	}
	if input.cleanup {
		workPath := strings.TrimSuffix(input.path, ".ready") + ".work"
		if err := os.Rename(input.path, workPath); err != nil {
			return Job{}, ErrInvalidPath
		}
		input.path = workPath
	}
	if permit != nil {
		delete(m.permits, permit)
		if m.uploading > 0 {
			m.uploading--
		}
	}
	now := time.Now().UTC()
	j := &Job{
		ID:         id,
		Format:     format,
		SourceName: input.sourceName,
		State:      StateQueued,
		Started:    now,
	}
	m.jobs[id] = j
	m.order = append(m.order, id)
	m.trimLocked()
	m.running++
	m.wg.Add(1)
	snapshot := *j
	go m.run(id, input, format)
	return snapshot, nil
}

func (m *Manager) run(id string, input jobInput, format string) {
	defer m.wg.Done()
	completed := false
	defer func() {
		if recovered := recover(); recovered != nil && !completed {
			m.complete(id, input, func(j *Job) {
				j.State = StateFailed
				j.Error = "import task failed unexpectedly"
				j.Finished = time.Now().UTC()
			})
		}
	}()

	m.patch(id, func(j *Job) {
		j.State = StateRunning
		j.Started = time.Now().UTC()
	})

	cfg := bulkimport.Config{
		Format:             format,
		Workers:            m.opts.Workers,
		Batch:              bulkimport.DefaultBatch,
		Converter:          m.opts.Converter,
		MaxEntries:         m.opts.MaxEntries,
		MaxNDJSONLineBytes: m.opts.MaxNDJSONLineBytes,
		MaxSSOValueBytes:   m.opts.MaxSSOValueBytes,
		ProgressEvery:      25,
	}
	cfg.OnDetailProgress = func(total, ok, failed int, phase string) {
		m.patch(id, func(j *Job) {
			if total > j.Total {
				j.Total = total
			}
			if ok >= 0 {
				j.OK = ok
			}
			if failed >= 0 {
				j.Fail = failed
			}
			if phase != "" {
				j.Phase = phase
				switch phase {
				case "parsing":
					j.Message = "解析输入"
				case "converting":
					j.Message = "SSO 换票中（Device Flow）"
				case "writing":
					j.Message = "写入账号库"
				case "reloading":
					j.Message = "重建热池"
				default:
					j.Message = phase
				}
			}
		})
	}
	cfg.OnProgress = func(ok int, _ time.Duration, _ float64) {
		m.patch(id, func(j *Job) {
			if ok > j.OK {
				j.OK = ok
			}
			if j.Phase == "" || j.Phase == "converting" || j.Phase == "parsing" {
				j.Phase = "writing"
				j.Message = "写入账号库"
			}
		})
	}

	ctx, cancel := context.WithTimeout(m.ctx, m.opts.JobTimeout)
	defer cancel()
	rep, err := bulkimport.ImportPaths(ctx, m.cat, input.path, cfg)
	if err == nil && m.opts.AfterImport != nil {
		m.patch(id, func(j *Job) {
			j.Phase = "reloading"
			j.Message = "重建热池"
			if rep.Total > 0 {
				j.Total = rep.Total
			}
			j.OK = rep.OK
			j.Fail = rep.Failed
		})
		err = m.opts.AfterImport()
	}
	finished := time.Now().UTC()
	if err != nil {
		m.complete(id, input, func(j *Job) {
			j.State = StateFailed
			j.Error = safeJobError(err, input.path, m.dataDir)
			j.Total = rep.Total
			j.OK = rep.OK
			j.Fail = rep.Failed
			j.Skipped = rep.Skipped
			j.Phase = "done"
			j.Message = "失败"
			j.Finished = finished
		})
		completed = true
		return
	}
	m.complete(id, input, func(j *Job) {
		j.State = StateSucceeded
		j.Total = rep.Total
		j.OK = rep.OK
		j.Fail = rep.Failed
		j.Skipped = rep.Skipped
		j.Phase = "done"
		j.Message = "完成"
		j.Finished = finished
		if rep.Failed > 0 && rep.OK == 0 {
			j.State = StateFailed
			j.Error = "all entries failed"
			j.Message = "失败"
			if len(rep.Files) > 0 && strings.TrimSpace(rep.Files[0].Error) != "" {
				j.Error = safeJobError(errors.New(rep.Files[0].Error), input.path, m.dataDir)
			}
		}
	})
	completed = true
}

func (m *Manager) complete(id string, input jobInput, fn func(*Job)) {
	var cleanupErr error
	if input.cleanup {
		if err := os.Remove(input.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			cleanupErr = err
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.running > 0 {
		m.running--
	}
	if j := m.jobs[id]; j != nil {
		fn(j)
		if cleanupErr != nil {
			j.State = StateFailed
			j.Error = "import completed but staged upload cleanup failed"
			j.Finished = time.Now().UTC()
		}
	}
	m.trimLocked()
}

func (m *Manager) trimLocked() {
	for len(m.order) > m.maxKeep {
		removeAt := -1
		for i, oldID := range m.order {
			old := m.jobs[oldID]
			if old != nil && (old.State == StateSucceeded || old.State == StateFailed) {
				removeAt = i
				break
			}
		}
		if removeAt < 0 {
			return
		}
		oldID := m.order[removeAt]
		m.order = append(m.order[:removeAt], m.order[removeAt+1:]...)
		delete(m.jobs, oldID)
	}
}

// Close 停止接收新任务，取消运行中的导入并等待其完成。
func (m *Manager) Close(ctx context.Context) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	if !m.closing {
		m.closing = true
		if m.cancel != nil {
			m.cancel()
		}
	}
	m.mu.Unlock()
	done := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func safeJobError(err error, path, dataDir string) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if path != "" {
		msg = strings.ReplaceAll(msg, path, "input file")
	}
	if dataDir != "" {
		msg = strings.ReplaceAll(msg, dataDir, "data directory")
	}
	if len(msg) > 300 {
		msg = msg[:300]
	}
	return msg
}

func (m *Manager) patch(id string, fn func(*Job)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if j := m.jobs[id]; j != nil {
		fn(j)
	}
}

func (m *Manager) safeStagedPath(path string) (string, error) {
	if err := m.ensureStagingSafe(); err != nil {
		return "", ErrInvalidPath
	}
	path = filepath.Clean(strings.TrimSpace(path))
	if filepath.Dir(path) != m.stagingDir || !validReadyName(filepath.Base(path)) {
		return "", ErrInvalidPath
	}
	st, err := os.Lstat(path)
	if err != nil || !st.Mode().IsRegular() {
		return "", ErrInvalidPath
	}
	return path, nil
}

// safePath 校验旧 path 必须解析到 dataDir 内的普通单文件，且阻止 symlink 逃逸。
func (m *Manager) safePath(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", ErrInvalidPath
	}
	var abs string
	if filepath.IsAbs(p) {
		abs = filepath.Clean(p)
	} else {
		abs = filepath.Clean(filepath.Join(m.dataDir, p))
	}
	realRoot, err := filepath.EvalSymlinks(m.dataDir)
	if err != nil {
		return "", ErrInvalidPath
	}
	realPath, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", ErrInvalidPath
	}
	rel, err := filepath.Rel(realRoot, realPath)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", ErrInvalidPath
	}
	st, err := os.Lstat(realPath)
	if err != nil || !st.Mode().IsRegular() {
		return "", ErrInvalidPath
	}
	return realPath, nil
}

func sanitizeSourceName(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '/' || r == '\\' {
			return -1
		}
		return r
	}, name)
	if name == "" || name == "." {
		name = "浏览器上传"
	}
	runes := []rune(name)
	if len(runes) > 120 {
		name = string(runes[:120])
	}
	return name
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
