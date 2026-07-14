package admin

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/yshgsh1343/grokbuild2api/internal/importjobs"
)

// ListImportJobs GET /admin/import/jobs
func (h *Handlers) ListImportJobs(w http.ResponseWriter, r *http.Request) {
	if h.ImportJobs == nil {
		writeErr(w, http.StatusServiceUnavailable, "导入任务未启用")
		return
	}
	list := h.ImportJobs.List(50)
	if list == nil {
		list = []importjobs.Job{}
	}
	limits := map[string]any{
		"enabled":                  h.importEnabled(),
		"max_upload_bytes":         h.effectiveImportMaxUploadBytes(),
		"max_entries":              h.effectiveImportMaxEntries(),
		"sso_converter_configured": h.ssoConverterConfigured(),
	}
	// 回传当前 Manager 生效中的热参数，避免 UI 只见 settings 不知运行时
	if h.ImportJobs != nil {
		opts := h.ImportJobs.OptionsSnapshot()
		limits["max_concurrent_jobs"] = opts.MaxConcurrentJobs
		limits["workers"] = opts.Workers
		limits["max_ndjson_line_bytes"] = opts.MaxNDJSONLineBytes
		limits["max_sso_value_bytes"] = opts.MaxSSOValueBytes
		limits["job_timeout_sec"] = int(opts.JobTimeout.Seconds())
		limits["staging_stale_after_sec"] = int(opts.StagingStaleAfter.Seconds())
		limits["allow_server_path"] = opts.AllowServerPath
	}
	if h.Settings != nil {
		s := h.Settings.Snapshot()
		limits["import_workers"] = s.ImportWorkers
		limits["import_sso_workers"] = s.ImportSSOWorkers
		limits["import_sso_max_batch"] = s.ImportSSOMaxBatch
		limits["import_canary_hot_size"] = s.ImportCanaryHotSize
		limits["import_canary_hold_sec"] = s.ImportCanaryHoldSec
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"jobs":   list,
		"limits": limits,
	})
}

// CreateImportJob POST /admin/import/jobs。
// 浏览器使用 multipart(format,file)；旧 JSON path 协议仅在配置显式开启时接受。
func (h *Handlers) CreateImportJob(w http.ResponseWriter, r *http.Request) {
	if h.ImportJobs == nil || !h.importEnabled() {
		writeErr(w, http.StatusServiceUnavailable, "导入任务未启用")
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		writeErr(w, http.StatusUnsupportedMediaType, "Content-Type 必须是 multipart/form-data")
		return
	}
	switch mediaType {
	case "multipart/form-data":
		h.createUploadImportJob(w, r)
	case "application/json":
		if !h.importAllowServerPath() {
			writeErr(w, http.StatusUnsupportedMediaType, "服务端路径导入未启用")
			return
		}
		var req importjobs.CreateRequest
		if err := decodeJSON(r, 1<<20, &req); err != nil {
			writeErr(w, http.StatusBadRequest, "导入请求无效")
			return
		}
		job, err := h.ImportJobs.Submit(req)
		if err != nil {
			writeImportJobError(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, job)
	default:
		writeErr(w, http.StatusUnsupportedMediaType, "Content-Type 必须是 multipart/form-data")
	}
}

func (h *Handlers) createUploadImportJob(w http.ResponseWriter, r *http.Request) {
	permit, err := h.ImportJobs.ReserveUpload()
	if err != nil {
		writeImportJobError(w, err)
		return
	}
	defer permit.Release()
	// max_request_bytes / max_upload_bytes 为 0 时不限体积，只靠 max_entries。
	maxRequest := h.effectiveImportMaxRequestBytes()
	if maxRequest > 0 {
		r.Body = http.MaxBytesReader(w, r.Body, maxRequest)
	}
	reader, err := r.MultipartReader()
	if err != nil {
		writeErr(w, http.StatusBadRequest, "multipart 请求无效")
		return
	}
	var (
		format     string
		formatSeen bool
		fileSeen   bool
		upload     importjobs.StagedUpload
		partCount  int
	)
	defer func() { _ = upload.Remove() }()
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				writeErr(w, http.StatusRequestEntityTooLarge, "上传请求超过大小限制")
			} else {
				writeErr(w, http.StatusBadRequest, "multipart 请求无效")
			}
			return
		}
		partCount++
		if partCount > 3 {
			_ = part.Close()
			writeErr(w, http.StatusBadRequest, "multipart 字段过多")
			return
		}
		switch part.FormName() {
		case "format":
			if formatSeen || part.FileName() != "" {
				_ = part.Close()
				writeErr(w, http.StatusBadRequest, "format 字段重复或无效")
				return
			}
			formatSeen = true
			raw, readErr := io.ReadAll(io.LimitReader(part, 1025))
			_ = part.Close()
			if readErr != nil {
				var maxErr *http.MaxBytesError
				if errors.As(readErr, &maxErr) {
					writeErr(w, http.StatusRequestEntityTooLarge, "上传请求超过大小限制")
				} else {
					writeErr(w, http.StatusBadRequest, "format 字段无效")
				}
				return
			}
			if len(raw) > 1024 {
				writeErr(w, http.StatusBadRequest, "format 字段无效")
				return
			}
			format = strings.TrimSpace(string(raw))
		case "file":
			if fileSeen || strings.TrimSpace(part.FileName()) == "" {
				_ = part.Close()
				writeErr(w, http.StatusBadRequest, "file 字段重复或无效")
				return
			}
			fileSeen = true
			upload, err = h.ImportJobs.StageReservedUpload(part, part.FileName(), h.effectiveImportMaxUploadBytes(), permit)
			_ = part.Close()
			if err != nil {
				var maxErr *http.MaxBytesError
				if errors.Is(err, importjobs.ErrUploadTooLarge) || errors.As(err, &maxErr) {
					writeErr(w, http.StatusRequestEntityTooLarge, "文件或上传请求超过大小限制")
				} else if errors.Is(err, importjobs.ErrEmptyUpload) {
					writeErr(w, http.StatusBadRequest, "上传文件为空")
				} else {
					writeErr(w, http.StatusInternalServerError, "无法暂存上传文件")
				}
				return
			}
		default:
			_ = part.Close()
			writeErr(w, http.StatusBadRequest, "multipart 包含未知字段")
			return
		}
	}
	if format == "" {
		format = "sso" // 默认 SSO→JSON
	}
	if !fileSeen || upload.Path == "" {
		writeErr(w, http.StatusBadRequest, "缺少 file 字段")
		return
	}
	job, err := h.ImportJobs.SubmitReservedUpload(format, upload, permit)
	if err != nil {
		writeImportJobError(w, err)
		return
	}
	upload = importjobs.StagedUpload{}
	writeJSON(w, http.StatusAccepted, job)
}


func writeImportJobError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, importjobs.ErrBusy):
		writeErr(w, http.StatusTooManyRequests, "导入任务已满，请稍后重试")
	case errors.Is(err, importjobs.ErrConverterUnavailable):
		writeErr(w, http.StatusServiceUnavailable, "SSO 转换器未配置")
	case errors.Is(err, importjobs.ErrClosed):
		writeErr(w, http.StatusServiceUnavailable, "导入任务正在关闭")
	case errors.Is(err, importjobs.ErrInvalidPath),
		errors.Is(err, importjobs.ErrInvalidFormat),
		errors.Is(err, importjobs.ErrServerPathDisabled):
		writeErr(w, http.StatusBadRequest, "导入请求无效")
	default:
		writeErr(w, http.StatusInternalServerError, "无法创建导入任务")
	}
}

// GetImportJob GET /admin/import/jobs/{id}
func (h *Handlers) GetImportJob(w http.ResponseWriter, r *http.Request) {
	if h.ImportJobs == nil {
		writeErr(w, http.StatusServiceUnavailable, "导入任务未启用")
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeErr(w, http.StatusBadRequest, "缺少 job id")
		return
	}
	job, err := h.ImportJobs.Get(id)
	if err != nil {
		if errors.Is(err, importjobs.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "任务不存在")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, job)
}


// importEnabled 优先 settings 热配置，回退 yaml Config。
func (h *Handlers) importEnabled() bool {
	if h != nil && h.Settings != nil {
		return h.Settings.Snapshot().ImportEnabled
	}
	if h == nil {
		return false
	}
	return h.Config.Imports.Enabled
}

func (h *Handlers) importAllowServerPath() bool {
	if h != nil && h.Settings != nil {
		return h.Settings.Snapshot().ImportAllowServerPath
	}
	if h == nil {
		return false
	}
	return h.Config.Imports.AllowServerPath
}

// effectiveImportMaxUploadBytes：0 = 不限体积（主闸门为 max_entries）。
func (h *Handlers) effectiveImportMaxUploadBytes() int64 {
	if h != nil && h.Settings != nil {
		return h.Settings.Snapshot().ImportMaxUploadBytes
	}
	if h == nil {
		return 0
	}
	return h.Config.Imports.MaxUploadBytes
}

func (h *Handlers) effectiveImportMaxEntries() int {
	if h != nil && h.Settings != nil {
		n := h.Settings.Snapshot().ImportMaxEntries
		if n > 0 {
			return n
		}
	}
	if h == nil {
		return 0
	}
	return h.Config.Imports.MaxEntries
}

// effectiveImportMaxRequestBytes：0 = 不限；若仅配置了 upload 上限则自动 +1MiB 开销。
func (h *Handlers) effectiveImportMaxRequestBytes() int64 {
	if h == nil {
		return 0
	}
	req := h.Config.Imports.MaxRequestBytes
	up := h.effectiveImportMaxUploadBytes()
	// settings 目前不单独热更 max_request_bytes；upload=0 时请求也不限。
	if up <= 0 {
		return 0
	}
	if req > 0 {
		return req
	}
	return up + (1 << 20)
}

func (h *Handlers) ssoConverterConfigured() bool {
	// 内置 Go Device Flow 默认可用；远程 endpoint 仅作可选覆盖。
	if h != nil && h.ImportJobs != nil {
		return true
	}
	if h != nil && h.Settings != nil {
		s := h.Settings.Snapshot()
		if strings.TrimSpace(s.ImportSSOEndpoint) != "" && s.ImportSSOAPIKeySet {
			return true
		}
	}
	if h == nil {
		return false
	}
	ep := strings.TrimSpace(h.Config.Imports.SSOConverter.Endpoint)
	key := strings.TrimSpace(h.Config.Imports.SSOConverter.APIKey)
	return ep != "" && key != ""
}
