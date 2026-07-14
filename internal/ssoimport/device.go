package ssoimport

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/yshgsh1343/grokbuild2api/internal/authimport"
)

// 固定 xAI Grok CLI Device Flow 参数（与参考 Python 转换器一致）。
const (
	defaultOIDCIssuer   = "https://auth.x.ai"
	defaultOIDCClientID = "b1a00492-073a-47ea-816f-4c329264a828"
	defaultScopes       = "openid profile email offline_access grok-cli:access api:access conversations:read conversations:write"
	defaultUserAgent    = "grokbuild-pool-sso/1.0"
	maxUpstreamBody     = 1 << 20
)

// LocalConverter 在进程内用纯 Go Device Flow 把 SSO cookie 换成 Grok 凭证。
// 不依赖 Python sidecar / 外部转换服务。
var _ Converter = (*LocalConverter)(nil)

type LocalConverter struct {
	HTTPClient    *http.Client
	ClientID      string
	Issuer        string
	Scopes        string
	Timeout       time.Duration
	PollTimeout   time.Duration
	MaxRetries    int
	Workers       int
	// FlowConcurrency 限制同时进行的 verify+approve 数，降低 IP rate limit。
	FlowConcurrency int
	FlowGap         time.Duration

	flowSem chan struct{}
	flowMu  sync.Mutex
	lastFlow time.Time
}

// NewLocalConverter 返回内置 SSO→凭证转换器（默认参数）。
func NewLocalConverter() *LocalConverter {
	jar, _ := cookiejar.New(nil)
	lc := &LocalConverter{
		HTTPClient: &http.Client{
			Timeout: 45 * time.Second,
			Jar:     jar,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				// 允许 accounts/auth 跳转，但禁止跨出 x.ai
				host := strings.ToLower(req.URL.Hostname())
				if host != "accounts.x.ai" && host != "auth.x.ai" {
					return fmt.Errorf("ssoimport: redirect host not allowed: %s", host)
				}
				if len(via) >= 8 {
					return fmt.Errorf("ssoimport: too many redirects")
				}
				return nil
			},
		},
		ClientID:        defaultOIDCClientID,
		Issuer:          defaultOIDCIssuer,
		Scopes:          defaultScopes,
		Timeout:         30 * time.Second,
		PollTimeout:     90 * time.Second,
		MaxRetries:      2,
		Workers:         12,
		FlowConcurrency: 8,
		FlowGap:         80 * time.Millisecond,
	}
	lc.flowSem = make(chan struct{}, lc.FlowConcurrency)
	return lc
}

// Configure 调整并发参数（管理台热更 / 导入任务重建转换器时调用）。
func (c *LocalConverter) Configure(workers, flowConcurrency int, flowGap time.Duration) {
	if c == nil {
		return
	}
	if workers > 0 {
		if workers > 128 {
			workers = 128
		}
		c.Workers = workers
	}
	if flowConcurrency <= 0 {
		flowConcurrency = c.Workers
	}
	if flowConcurrency > 128 {
		flowConcurrency = 128
	}
	if flowConcurrency < 1 {
		flowConcurrency = 1
	}
	c.FlowConcurrency = flowConcurrency
	if flowGap > 0 {
		c.FlowGap = flowGap
	}
	c.flowSem = make(chan struct{}, c.FlowConcurrency)
}

// Convert 实现与 HTTP Client 相同的批量接口：按输入顺序返回结果。
func (c *LocalConverter) Convert(ctx context.Context, ssoValues []string) ([]ConvertedCredential, error) {
	if c == nil {
		return nil, ErrConverterRequired
	}
	if len(ssoValues) == 0 {
		return []ConvertedCredential{}, nil
	}
	workers := c.Workers
	if workers <= 0 {
		workers = 4
	}
	if workers > 128 {
		workers = 128
	}
	if c.flowSem == nil {
		n := c.FlowConcurrency
		if n <= 0 {
			n = 2
		}
		c.flowSem = make(chan struct{}, n)
	}

	out := make([]ConvertedCredential, len(ssoValues))
	type job struct{ i int; sso string }
	jobs := make(chan job)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				cred, err := c.convertOne(ctx, j.sso)
				if err != nil {
					out[j.i] = ConvertedCredential{Error: err.Error()}
					continue
				}
				out[j.i] = cred
			}
		}()
	}
	for i, s := range ssoValues {
		select {
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return nil, ctx.Err()
		case jobs <- job{i: i, sso: s}:
		}
	}
	close(jobs)
	wg.Wait()
	return out, nil
}

func (c *LocalConverter) convertOne(ctx context.Context, sso string) (ConvertedCredential, error) {
	sso = strings.TrimSpace(sso)
	if sso == "" {
		return ConvertedCredential{}, fmt.Errorf("missing sso cookie")
	}
	retries := c.MaxRetries
	if retries <= 0 {
		retries = 1
	}
	var last error
	for attempt := 1; attempt <= retries; attempt++ {
		if err := ctx.Err(); err != nil {
			return ConvertedCredential{}, err
		}
		cred, err := c.convertOnce(ctx, sso)
		if err == nil {
			return cred, nil
		}
		last = err
		msg := strings.ToLower(err.Error())
		retriable := strings.Contains(msg, "rate_limited") ||
			strings.Contains(msg, "timeout") ||
			strings.Contains(msg, "connection") ||
			strings.Contains(msg, "temporary") ||
			strings.Contains(msg, "eof") ||
			strings.Contains(msg, "device code") ||
			strings.Contains(msg, "token poll")
		if !retriable || attempt >= retries {
			break
		}
		wait := time.Duration(attempt) * time.Second
		if wait > 8*time.Second {
			wait = 8 * time.Second
		}
		select {
		case <-ctx.Done():
			return ConvertedCredential{}, ctx.Err()
		case <-time.After(wait):
		}
	}
	if last == nil {
		last = fmt.Errorf("conversion failed")
	}
	return ConvertedCredential{}, last
}

func (c *LocalConverter) convertOnce(ctx context.Context, sso string) (ConvertedCredential, error) {
	// 每条 SSO 使用独立 cookie jar，避免串号。
	jar, err := cookiejar.New(nil)
	if err != nil {
		return ConvertedCredential{}, err
	}
	base := c.HTTPClient
	if base == nil {
		base = http.DefaultClient
	}
	client := *base
	client.Jar = jar
	// 写入 sso cookie 到 accounts.x.ai 与 auth.x.ai
	for _, host := range []string{"https://accounts.x.ai/", "https://auth.x.ai/"} {
		u, _ := url.Parse(host)
		jar.SetCookies(u, []*http.Cookie{
			{Name: "sso", Value: sso, Path: "/"},
			{Name: "sso-rw", Value: sso, Path: "/"},
		})
	}

	// 1) 校验 SSO
	if err := c.get(ctx, &client, "https://accounts.x.ai/"); err != nil {
		return ConvertedCredential{}, fmt.Errorf("accounts.x.ai: %w", err)
	}

	// 2) device code
	sess, err := c.requestDeviceCode(ctx, &client)
	if err != nil {
		return ConvertedCredential{}, fmt.Errorf("device code: %w", err)
	}

	// 3-5) verify + approve（节流）
	if err := c.acquireFlow(ctx); err != nil {
		return ConvertedCredential{}, err
	}
	defer c.releaseFlow()

	if err := c.get(ctx, &client, sess.VerificationURIComplete); err != nil {
		return ConvertedCredential{}, fmt.Errorf("verification_uri: %w", err)
	}
	if err := c.postForm(ctx, &client, c.issuer()+"/oauth2/device/verify", url.Values{
		"user_code": {sess.UserCode},
	}); err != nil {
		return ConvertedCredential{}, fmt.Errorf("device/verify: %w", err)
	}
	if err := c.postForm(ctx, &client, c.issuer()+"/oauth2/device/approve", url.Values{
		"user_code":      {sess.UserCode},
		"action":         {"allow"},
		"principal_type": {"User"},
		"principal_id":   {""},
	}); err != nil {
		return ConvertedCredential{}, fmt.Errorf("device/approve: %w", err)
	}

	// 6) poll token
	token, err := c.pollToken(ctx, &client, sess)
	if err != nil {
		return ConvertedCredential{}, fmt.Errorf("token poll: %w", err)
	}
	return tokenToCredential(token)
}

type deviceSession struct {
	DeviceCode              string
	UserCode                string
	VerificationURI         string
	VerificationURIComplete string
	ExpiresIn               int
	Interval                int
}

func (c *LocalConverter) issuer() string {
	if strings.TrimSpace(c.Issuer) == "" {
		return defaultOIDCIssuer
	}
	return strings.TrimRight(strings.TrimSpace(c.Issuer), "/")
}

func (c *LocalConverter) clientID() string {
	if strings.TrimSpace(c.ClientID) == "" {
		return defaultOIDCClientID
	}
	return strings.TrimSpace(c.ClientID)
}

func (c *LocalConverter) scopes() string {
	if strings.TrimSpace(c.Scopes) == "" {
		return defaultScopes
	}
	return strings.TrimSpace(c.Scopes)
}

func (c *LocalConverter) requestDeviceCode(ctx context.Context, client *http.Client) (*deviceSession, error) {
	form := url.Values{
		"client_id": {c.clientID()},
		"scope":     {c.scopes()},
	}
	status, body, err := c.doForm(ctx, client, c.issuer()+"/oauth2/device/code", form)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("status %d: %s", status, trimBody(body))
	}
	var resp struct {
		DeviceCode              string `json:"device_code"`
		UserCode                string `json:"user_code"`
		VerificationURI         string `json:"verification_uri"`
		VerificationURIComplete string `json:"verification_uri_complete"`
		ExpiresIn               int    `json:"expires_in"`
		Interval                int    `json:"interval"`
		Error                   string `json:"error"`
		ErrorDescription        string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decode device code: %w", err)
	}
	if resp.Error != "" {
		return nil, fmt.Errorf("%s: %s", resp.Error, resp.ErrorDescription)
	}
	if resp.DeviceCode == "" || resp.UserCode == "" {
		return nil, fmt.Errorf("empty device/user code")
	}
	if resp.Interval <= 0 {
		resp.Interval = 5
	}
	if resp.ExpiresIn <= 0 {
		resp.ExpiresIn = 600
	}
	if resp.VerificationURIComplete == "" {
		if resp.VerificationURI == "" {
			resp.VerificationURI = "https://auth.x.ai/oauth2/device"
		}
		resp.VerificationURIComplete = resp.VerificationURI + "?user_code=" + url.QueryEscape(resp.UserCode)
	}
	return &deviceSession{
		DeviceCode:              resp.DeviceCode,
		UserCode:                resp.UserCode,
		VerificationURI:         resp.VerificationURI,
		VerificationURIComplete: resp.VerificationURIComplete,
		ExpiresIn:               resp.ExpiresIn,
		Interval:                resp.Interval,
	}, nil
}

func (c *LocalConverter) pollToken(ctx context.Context, client *http.Client, sess *deviceSession) (map[string]any, error) {
	pollTO := c.PollTimeout
	if pollTO <= 0 {
		pollTO = 90 * time.Second
	}
	deadline := time.Now().Add(pollTO)
	if int(pollTO.Seconds()) > sess.ExpiresIn && sess.ExpiresIn > 0 {
		deadline = time.Now().Add(time.Duration(sess.ExpiresIn) * time.Second)
	}
	interval := time.Duration(sess.Interval) * time.Second
	if interval < 2*time.Second {
		interval = 2 * time.Second
	}
	// 首次立即轮询
	first := true
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("deadline exceeded")
		}
		if !first {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(interval):
			}
		}
		first = false
		form := url.Values{
			"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
			"device_code": {sess.DeviceCode},
			"client_id":   {c.clientID()},
		}
		status, body, err := c.doForm(ctx, client, c.issuer()+"/oauth2/token", form)
		if err != nil {
			return nil, err
		}
		var resp map[string]any
		_ = json.Unmarshal(body, &resp)
		if status >= 200 && status < 300 {
			if at, _ := resp["access_token"].(string); strings.TrimSpace(at) != "" {
				return resp, nil
			}
		}
		errCode, _ := resp["error"].(string)
		switch errCode {
		case "authorization_pending", "slow_down":
			if errCode == "slow_down" {
				interval += 2 * time.Second
			}
			continue
		case "access_denied", "expired_token":
			return nil, fmt.Errorf("%s", errCode)
		default:
			if status == 429 || strings.Contains(strings.ToLower(string(body)), "rate") {
				return nil, fmt.Errorf("rate_limited: %s", trimBody(body))
			}
			// 继续等待，直到 deadline
			if status >= 400 {
				desc, _ := resp["error_description"].(string)
				if errCode != "" {
					return nil, fmt.Errorf("%s: %s", errCode, desc)
				}
			}
		}
	}
}

func (c *LocalConverter) get(ctx context.Context, client *http.Client, rawURL string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", defaultUserAgent)
	req.Header.Set("Accept", "text/html,application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxUpstreamBody))
	final := ""
	if resp.Request != nil && resp.Request.URL != nil {
		final = resp.Request.URL.String()
	}
	if strings.Contains(final, "sign-in") || strings.Contains(final, "sign-up") {
		return fmt.Errorf("sso invalid (landed %s)", trimURL(final))
	}
	if resp.StatusCode == 429 {
		return fmt.Errorf("rate_limited status=429")
	}
	return nil
}

func (c *LocalConverter) postForm(ctx context.Context, client *http.Client, rawURL string, form url.Values) error {
	status, body, err := c.doForm(ctx, client, rawURL, form)
	if err != nil {
		return err
	}
	if status == 429 || strings.Contains(strings.ToLower(string(body)), "rate_limit") {
		return fmt.Errorf("rate_limited: %s", trimBody(body))
	}
	// verify/approve 常以 HTML 跳转完成；2xx/3xx 且 body 含 consent/done 即视为成功。
	if status >= 200 && status < 400 {
		return nil
	}
	return fmt.Errorf("status %d: %s", status, trimBody(body))
}

func (c *LocalConverter) doForm(ctx context.Context, client *http.Client, rawURL string, form url.Values) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, strings.NewReader(form.Encode()))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", defaultUserAgent)
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxUpstreamBody+1))
	if err != nil {
		return resp.StatusCode, nil, err
	}
	if len(body) > maxUpstreamBody {
		return resp.StatusCode, nil, fmt.Errorf("response too large")
	}
	return resp.StatusCode, body, nil
}

func (c *LocalConverter) acquireFlow(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case c.flowSem <- struct{}{}:
	}
	c.flowMu.Lock()
	gap := c.FlowGap
	if gap < 0 {
		gap = 0
	}
	wait := time.Until(c.lastFlow.Add(gap))
	c.flowMu.Unlock()
	if wait > 0 {
		select {
		case <-ctx.Done():
			<-c.flowSem
			return ctx.Err()
		case <-time.After(wait):
		}
	}
	return nil
}

func (c *LocalConverter) releaseFlow() {
	c.flowMu.Lock()
	c.lastFlow = time.Now()
	c.flowMu.Unlock()
	select {
	case <-c.flowSem:
	default:
	}
}

func tokenToCredential(token map[string]any) (ConvertedCredential, error) {
	access, _ := token["access_token"].(string)
	if access == "" {
		access, _ = token["key"].(string)
	}
	refresh, _ := token["refresh_token"].(string)
	if strings.TrimSpace(access) == "" {
		return ConvertedCredential{}, fmt.Errorf("empty access_token")
	}
	payload := decodeJWTPayload(access)
	userID, _ := payload["sub"].(string)
	if userID == "" {
		userID, _ = payload["principal_id"].(string)
	}
	email, _ := payload["email"].(string)
	var exp time.Time
	if v, ok := payload["exp"].(float64); ok && v > 0 {
		exp = time.Unix(int64(v), 0).UTC()
	} else if ei, ok := token["expires_in"].(float64); ok && ei > 0 {
		exp = time.Now().UTC().Add(time.Duration(ei) * time.Second)
	} else {
		exp = time.Now().UTC().Add(6 * time.Hour)
	}
	name := email
	if name == "" {
		name = userID
	}
	return ConvertedCredential{
		Name:         name,
		Email:        email,
		UserID:       userID,
		OIDCIssuer:   authimport.Issuer,
		OIDCClientID: defaultOIDCClientID,
		AccessToken:  access,
		RefreshToken: refresh,
		ExpiresAt:    exp,
	}, nil
}

func decodeJWTPayload(token string) map[string]any {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return map[string]any{}
	}
	seg := parts[1]
	if m := len(seg) % 4; m != 0 {
		seg += strings.Repeat("=", 4-m)
	}
	raw, err := base64.URLEncoding.DecodeString(seg)
	if err != nil {
		raw, err = base64.RawURLEncoding.DecodeString(parts[1])
		if err != nil {
			return map[string]any{}
		}
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return map[string]any{}
	}
	return out
}

func trimBody(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 240 {
		return s[:240]
	}
	return s
}

func trimURL(s string) string {
	if len(s) > 120 {
		return s[:120]
	}
	return s
}
