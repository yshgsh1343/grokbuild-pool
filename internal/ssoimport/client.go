package ssoimport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/yshgsh1343/grokbuild2api/internal/authimport"
	"github.com/yshgsh1343/grokbuild2api/internal/catalog"
)

const (
	maxResponseBytes = int64(4 << 20)
	maxBatchItems    = 100
	maxTimeoutSec    = 300
)

// Config 控制 SSO 转换器 HTTP 客户端。
type Config struct {
	// Endpoint 为转换器基址（缺失时追加 …/v1/convert）。
	Endpoint string
	// APIKey 作为 Authorization: Bearer <key> 发送。
	APIKey string
	// MaxBatch 为每请求条目数（默认 50，最大 100）。
	MaxBatch int
	// Timeout 为单请求超时（默认 300s）。
	Timeout time.Duration
	// Workers 为 Convert 内部 batch 并行度（默认 4，最大 128）。
	Workers int
	// AllowInsecure 允许 loopback/私网/单标签主机使用 http://。
	AllowInsecure bool
	// HTTPClient 可选覆盖（测试）。
	HTTPClient *http.Client
}

// ConvertedCredential 为一条转换结果（兼容参考批量 API）。
type ConvertedCredential struct {
	Name         string
	Email        string
	UserID       string
	TeamID       string
	OIDCIssuer   string
	OIDCClientID string
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
	Error        string
}

// Converter 将 SSO cookie/token 转为 Grok 凭证（HTTP sidecar 或内置 Device Flow）。
type Converter interface {
	Convert(ctx context.Context, ssoValues []string) ([]ConvertedCredential, error)
}

// Client 调用 SSO→Grok 转换 HTTP 服务。

type Client struct {
	cfg Config
}

var _ Converter = (*Client)(nil)


// NewClient 校验配置并返回转换客户端。
func NewClient(cfg Config) (*Client, error) {
	cfg.Endpoint = strings.TrimSpace(cfg.Endpoint)
	if cfg.Endpoint == "" {
		return nil, ErrConverterRequired
	}
	endpoint, err := converterURL(cfg.Endpoint, cfg.AllowInsecure)
	if err != nil {
		return nil, err
	}
	cfg.Endpoint = endpoint
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, fmt.Errorf("ssoimport: API key is not configured")
	}
	if cfg.MaxBatch <= 0 {
		cfg.MaxBatch = 50
	}
	if cfg.MaxBatch > maxBatchItems {
		return nil, fmt.Errorf("ssoimport: max_batch must not exceed %d", maxBatchItems)
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = time.Duration(maxTimeoutSec) * time.Second
	}
	if cfg.Timeout > time.Duration(maxTimeoutSec)*time.Second {
		return nil, fmt.Errorf("ssoimport: timeout must not exceed %ds", maxTimeoutSec)
	}
	if cfg.Workers <= 0 {
		// 默认 4 路并行 batch，大文件导入时显著加速
		cfg.Workers = 4
	}
	if cfg.Workers > 128 {
		cfg.Workers = 128
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: cfg.Timeout}
	}
	// 绝不跟随重定向：请求携带 Bearer 与 SSO cookie。
	base := *cfg.HTTPClient
	base.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	if base.Timeout == 0 {
		base.Timeout = cfg.Timeout
	}
	cfg.HTTPClient = &base
	return &Client{cfg: cfg}, nil
}

// Convert 分批将 SSO 值发给转换器并返回对齐结果。
// 当 Workers>1 时按 batch 自动拆分并行请求，再按原序合并。
func (c *Client) Convert(ctx context.Context, ssoValues []string) ([]ConvertedCredential, error) {
	if c == nil {
		return nil, ErrConverterRequired
	}
	if len(ssoValues) == 0 {
		return []ConvertedCredential{}, nil
	}
	maxBatch := c.cfg.MaxBatch
	if maxBatch <= 0 {
		maxBatch = 50
	}
	// 构造 batch 区间
	type span struct{ start, end int }
	spans := make([]span, 0, (len(ssoValues)+maxBatch-1)/maxBatch)
	for start := 0; start < len(ssoValues); start += maxBatch {
		end := start + maxBatch
		if end > len(ssoValues) {
			end = len(ssoValues)
		}
		spans = append(spans, span{start, end})
	}

	output := make([]ConvertedCredential, len(ssoValues))
	workers := c.cfg.Workers
	if workers <= 1 || len(spans) == 1 {
		for _, sp := range spans {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			converted, err := c.convertBatch(ctx, ssoValues[sp.start:sp.end])
			if err != nil {
				return nil, err
			}
			copy(output[sp.start:sp.end], converted)
		}
		return output, nil
	}
	if workers > len(spans) {
		workers = len(spans)
	}

	type job struct{ sp span }
	type res struct {
		sp   span
		data []ConvertedCredential
		err  error
	}
	jobs := make(chan job, len(spans))
	results := make(chan res, len(spans))
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				if err := ctx.Err(); err != nil {
					results <- res{sp: j.sp, err: err}
					continue
				}
				converted, err := c.convertBatch(ctx, ssoValues[j.sp.start:j.sp.end])
				results <- res{sp: j.sp, data: converted, err: err}
			}
		}()
	}
	go func() {
		for _, sp := range spans {
			jobs <- job{sp: sp}
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()

	var firstErr error
	for r := range results {
		if r.err != nil {
			if firstErr == nil {
				firstErr = r.err
			}
			continue
		}
		copy(output[r.sp.start:r.sp.end], r.data)
	}
	if firstErr != nil {
		return nil, firstErr
	}
	return output, nil
}

func (c *Client) convertBatch(ctx context.Context, ssoValues []string) ([]ConvertedCredential, error) {
	type requestItem struct {
		SSO    string `json:"sso"`
		Source string `json:"source,omitempty"`
	}
	requestBody := struct {
		Items []requestItem `json:"items"`
	}{
		Items: make([]requestItem, len(ssoValues)),
	}
	for index, value := range ssoValues {
		requestBody.Items[index] = requestItem{
			SSO: strings.TrimSpace(value), Source: fmt.Sprintf("entry-%d", index+1),
		}
	}
	encoded, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("ssoimport: encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.Endpoint, bytes.NewReader(encoded))
	if err != nil {
		return nil, fmt.Errorf("ssoimport: create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(c.cfg.APIKey))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.cfg.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ssoimport: request failed: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("ssoimport: read response failed")
	}
	if int64(len(raw)) > maxResponseBytes {
		return nil, fmt.Errorf("ssoimport: response body exceeds %d bytes", maxResponseBytes)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ssoimport: service returned status %d", resp.StatusCode)
	}
	var response struct {
		Results json.RawMessage `json:"results"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, fmt.Errorf("ssoimport: invalid service response")
	}
	output := make([]ConvertedCredential, len(ssoValues))
	seen := make([]bool, len(ssoValues))
	decoder := json.NewDecoder(bytes.NewReader(response.Results))
	token, err := decoder.Token()
	if err != nil {
		return nil, fmt.Errorf("ssoimport: invalid service response")
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '[' {
		return nil, fmt.Errorf("ssoimport: results must be an array")
	}
	resultCount := 0
	for decoder.More() {
		if resultCount >= len(ssoValues) {
			return nil, fmt.Errorf("ssoimport: too many results")
		}
		var result struct {
			Index      int  `json:"index"`
			OK         bool `json:"ok"`
			Credential struct {
				SourceKey   string `json:"source_key"`
				Key         string `json:"key"`
				UserID      string `json:"user_id"`
				Email       string `json:"email"`
				PrincipalID string `json:"principal_id"`
				TeamID      string `json:"team_id"`
				Refresh     string `json:"refresh_token"`
				ExpiresAt   string `json:"expires_at"`
				Issuer      string `json:"oidc_issuer"`
				ClientID    string `json:"oidc_client_id"`
			} `json:"credential"`
		}
		if err := decoder.Decode(&result); err != nil {
			return nil, fmt.Errorf("ssoimport: invalid result")
		}
		resultCount++
		if result.Index < 0 || result.Index >= len(output) || seen[result.Index] {
			return nil, fmt.Errorf("ssoimport: invalid result index")
		}
		seen[result.Index] = true
		if !result.OK {
			output[result.Index].Error = "SSO conversion failed"
			continue
		}
		credential := result.Credential
		if strings.TrimSpace(credential.Key) == "" && strings.TrimSpace(credential.Refresh) == "" {
			output[result.Index].Error = "SSO conversion returned no tokens"
			continue
		}
		expiresAt := time.Time{}
		if strings.TrimSpace(credential.ExpiresAt) != "" {
			parsed, err := time.Parse(time.RFC3339Nano, credential.ExpiresAt)
			if err != nil {
				// 同时接受无纳秒的 RFC3339。
				parsed, err = time.Parse(time.RFC3339, credential.ExpiresAt)
				if err != nil {
					output[result.Index].Error = "SSO conversion returned an invalid expiry"
					continue
				}
			}
			expiresAt = parsed.UTC()
		}
		output[result.Index] = ConvertedCredential{
			Name: credential.Email, Email: credential.Email,
			UserID: firstNonEmpty(credential.UserID, credential.PrincipalID),
			TeamID: credential.TeamID, OIDCIssuer: credential.Issuer,
			OIDCClientID: credential.ClientID, AccessToken: credential.Key,
			RefreshToken: credential.Refresh, ExpiresAt: expiresAt,
		}
	}
	if _, err := decoder.Token(); err != nil {
		return nil, fmt.Errorf("ssoimport: invalid results array")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("ssoimport: invalid results payload")
	}
	for index := range output {
		if !seen[index] {
			output[index].Error = "SSO conversion result missing"
		}
	}
	return output, nil
}

// ToAccount 将成功转换映射为 catalog.Account。
func ToAccount(c ConvertedCredential, now time.Time) (catalog.Account, error) {
	if strings.TrimSpace(c.Error) != "" {
		return catalog.Account{}, fmt.Errorf("ssoimport: conversion error: %s", c.Error)
	}
	ic := authimport.ImportedCredential{
		SourceKey:    firstNonEmpty(c.Email, c.UserID, "sso"),
		AccessToken:  c.AccessToken,
		RefreshToken: c.RefreshToken,
		ExpiresAt:    c.ExpiresAt,
		Email:        c.Email,
		UserID:       c.UserID,
		TeamID:       c.TeamID,
		OIDCIssuer:   firstNonEmpty(c.OIDCIssuer, authimport.Issuer),
		OIDCClientID: firstNonEmpty(c.OIDCClientID, authimport.DefaultClientID),
	}
	return authimport.ToAccount(ic, now)
}

func converterURL(raw string, allowInsecure bool) (string, error) {
	raw, err := ValidateEndpoint(raw, allowInsecure)
	if err != nil {
		return "", err
	}
	parsed, _ := url.Parse(raw)
	if strings.HasSuffix(parsed.Path, "/v1/convert") {
		return raw, nil
	}
	return raw + "/v1/convert", nil
}

// ValidateEndpoint 校验 SSO 转换器基址 URL。
func ValidateEndpoint(raw string, allowInsecure bool) (string, error) {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	parsed, err := url.Parse(raw)
	if err != nil || !parsed.IsAbs() || parsed.Host == "" {
		return "", fmt.Errorf("ssoimport: endpoint must be an absolute URL")
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "https" && scheme != "http" {
		return "", fmt.Errorf("ssoimport: endpoint must use HTTPS")
	}
	if scheme == "http" {
		if !allowInsecure {
			return "", fmt.Errorf("ssoimport: endpoint must use HTTPS (or pass AllowInsecure for loopback/private)")
		}
		if !isPrivateConverterHost(parsed.Hostname()) {
			return "", fmt.Errorf("ssoimport: insecure HTTP endpoint must use loopback, a private IP, or a single-label internal hostname")
		}
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("ssoimport: endpoint must not include userinfo, query, or fragment")
	}
	return raw, nil
}

func isPrivateConverterHost(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback() || ip.IsPrivate()
	}
	if host == "" || strings.Contains(host, ".") || len(host) > 63 || host[0] == '-' || host[len(host)-1] == '-' {
		return false
	}
	hasLetter := false
	for _, char := range host {
		if char >= 'a' && char <= 'z' {
			hasLetter = true
			continue
		}
		if (char < '0' || char > '9') && char != '-' {
			return false
		}
	}
	return hasLetter
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
