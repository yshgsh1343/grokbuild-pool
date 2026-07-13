// Command render-config 用环境变量覆盖 pool-proxy 的 config.yaml。
//
// 供 Docker entrypoint 调用；纯 Go 实现，避免 Python re 反向引用把
// HOT_SIZE=3000 写成 \13000 → "X00" 导致容器 crash loop。
package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

var placeholderAdminKeys = map[string]struct{}{
	"":                    {},
	"change-me":           {},
	"changeme":            {},
	"dev-admin-change-me": {},
	"replace-me":          {},
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintf(os.Stderr, "usage: %s /path/to/config.yaml\n", os.Args[0])
		os.Exit(2)
	}
	path := os.Args[1]
	raw, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "render-config: read: %v\n", err)
		os.Exit(1)
	}
	text := string(raw)
	text, generated, err := applyEnv(text, os.Environ())
	if err != nil {
		fmt.Fprintf(os.Stderr, "render-config: %v\n", err)
		os.Exit(1)
	}
	if generated != "" {
		// Do not log the secret; only note that config was updated.
		fmt.Fprintf(os.Stderr, "generated ADMIN_KEY written to config file (open config to save it; do not rely on container logs\n")
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "render-config: write: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "config ready: %s\n", path)
}

func applyEnv(text string, environ []string) (string, string, error) {
	env := envMap(environ)
	var generated string

	admin := strings.TrimSpace(env["ADMIN_KEY"])
	if admin == "" {
		if cur := currentScalar(text, "admin_key"); isPlaceholderAdmin(cur) {
			b := make([]byte, 24)
			if _, err := rand.Read(b); err != nil {
				return text, "", err
			}
			generated = hex.EncodeToString(b)
			admin = generated
		}
	}

	dataDir := env["POOL_DATA_DIR"]
	if dataDir == "" {
		dataDir = "/data"
	}

	set := func(key, val string) {
		if val == "" && key != "api_key" && key != "admin_key" {
			// 空值不覆盖（api_key/admin_key 允许显式清空以外由上面处理）
			if key != "data_dir" {
				return
			}
		}
		if val == "" {
			return
		}
		text = setScalar(text, key, val)
	}

	set("listen", env["LISTEN"])
	set("allow_public_listen", env["ALLOW_PUBLIC_LISTEN"])
	set("data_dir", dataDir)
	if v, ok := env["API_KEY"]; ok {
		// 允许空字符串显式写入
		text = setScalar(text, "api_key", v)
	}
	if admin != "" {
		text = setScalar(text, "admin_key", admin)
	}
	set("hot_size", env["HOT_SIZE"])
	set("mock_upstream", env["MOCK_UPSTREAM"])

	if v := env["UPSTREAM_BASE_URL"]; v != "" {
		text = setNested(text, "upstream", "base_url", v)
	}
	if v := env["MAX_CONCURRENT"]; v != "" {
		text = setNested(text, "limits", "max_concurrent", v)
	}
	if v := env["LOG_LEVEL"]; v != "" {
		text = setNested(text, "logging", "level", v)
	}
	return text, generated, nil
}

func envMap(environ []string) map[string]string {
	out := make(map[string]string, len(environ))
	for _, e := range environ {
		k, v, ok := strings.Cut(e, "=")
		if !ok {
			continue
		}
		out[k] = v
	}
	return out
}

func isPlaceholderAdmin(v string) bool {
	_, ok := placeholderAdminKeys[strings.ToLower(strings.TrimSpace(v))]
	return ok
}

func currentScalar(text, key string) string {
	re := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(key) + `\s*:\s*"?([^"\n]+)"?\s*$`)
	m := re.FindStringSubmatch(text)
	if len(m) < 2 {
		return ""
	}
	return strings.TrimSpace(m[1])
}

func render(value string) string {
	v := strings.TrimSpace(value)
	if v == "true" || v == "false" {
		return v
	}
	if _, err := strconv.ParseFloat(v, 64); err == nil && v != "" {
		// bare number (int/float)
		return v
	}
	// quoted string with escape
	escaped := strings.ReplaceAll(v, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `"`, `\"`)
	return `"` + escaped + `"`
}

func setScalar(text, key, value string) string {
	rendered := render(value)
	re := regexp.MustCompile(`(?m)^(` + regexp.QuoteMeta(key) + `\s*:\s*).*$`)
	if re.MatchString(text) {
		// 用替换函数拼接，绝不用 \1{n} 这类会触发反向引用的形式
		return re.ReplaceAllStringFunc(text, func(line string) string {
			m := re.FindStringSubmatch(line)
			if len(m) < 2 {
				return line
			}
			return m[1] + rendered
		})
	}
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	return text + key + ": " + rendered + "\n"
}

func setNested(text, parent, key, value string) string {
	rendered := render(value)
	lines := strings.Split(text, "\n")
	// 保留末尾换行语义
	endsWithNL := strings.HasSuffix(text, "\n")
	if endsWithNL && len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	out := make([]string, 0, len(lines)+1)
	parentRe := regexp.MustCompile(`^` + regexp.QuoteMeta(parent) + `\s*:\s*$`)
	keyRe := regexp.MustCompile(`^\s+` + regexp.QuoteMeta(key) + `\s*:`)
	for i := 0; i < len(lines); {
		line := lines[i]
		out = append(out, line)
		if parentRe.MatchString(line) {
			i++
			replaced := false
			for i < len(lines) && (strings.HasPrefix(lines[i], " ") || strings.HasPrefix(lines[i], "\t") || strings.TrimSpace(lines[i]) == "") {
				if keyRe.MatchString(lines[i]) {
					indent := leadingWS(lines[i])
					out = append(out, indent+key+": "+rendered)
					replaced = true
					i++
					continue
				}
				out = append(out, lines[i])
				i++
			}
			if !replaced {
				out = append(out, "  "+key+": "+rendered)
			}
			continue
		}
		i++
	}
	joined := strings.Join(out, "\n")
	if endsWithNL {
		joined += "\n"
	}
	return joined
}

func leadingWS(s string) string {
	i := 0
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	return s[:i]
}
