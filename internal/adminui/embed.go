// Package adminui serves the React admin SPA (Vite build under dist/).
//
// Mount convention (pool-proxy):
//
//	/admin、/admin/  → dist/index.html（无需 admin_key）
//	/admin/ui/*      → dist 下 CSS/JS 等静态资源
//
// JSON 管理 API 仍由 internal/admin 鉴权挂载；本包只负责静态壳。
package adminui

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var distFS embed.FS

// Mount 挂载管理台静态资源（无需 admin_key；API 另鉴权）。
// 路由保持：/admin、/admin/、/admin/ui/*。
func Mount(mux *http.ServeMux) {
	if mux == nil {
		return
	}
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		return
	}
	fileServer := http.FileServer(http.FS(sub))

	mux.HandleFunc("/admin", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/admin" {
			http.NotFound(w, r)
			return
		}
		http.Redirect(w, r, "/admin/", http.StatusFound)
	})

	mux.HandleFunc("/admin/", func(w http.ResponseWriter, r *http.Request) {
		setSecurityHeaders(w)

		// SPA 壳：仅 /admin/ 返回 index.html（HashRouter，无需 path fallback）
		if r.URL.Path == "/admin/" || r.URL.Path == "/admin" {
			b, err := distFS.ReadFile("dist/index.html")
			if err != nil {
				http.Error(w, "ui missing", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			if r.Method == http.MethodHead {
				w.WriteHeader(http.StatusOK)
				return
			}
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(b)
			return
		}

		// /admin/ui/* → dist 根（Vite base: /admin/ui/）
		if strings.HasPrefix(r.URL.Path, "/admin/ui/") {
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			name := strings.TrimPrefix(r.URL.Path, "/admin/ui/")
			name = path.Clean("/" + name)
			name = strings.TrimPrefix(name, "/")
			if name == "" || name == "." || strings.Contains(name, "..") {
				http.NotFound(w, r)
				return
			}
			switch {
			case strings.HasSuffix(name, ".js"):
				w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
				// hashed assets can be cached longer; keep no-cache for safety with SPA updates
				if strings.Contains(name, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				} else {
					w.Header().Set("Cache-Control", "no-cache")
				}
			case strings.HasSuffix(name, ".css"):
				w.Header().Set("Content-Type", "text/css; charset=utf-8")
				if strings.Contains(name, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				} else {
					w.Header().Set("Cache-Control", "no-cache")
				}
			case strings.HasSuffix(name, ".svg"):
				w.Header().Set("Content-Type", "image/svg+xml")
			case strings.HasSuffix(name, ".png"):
				w.Header().Set("Content-Type", "image/png")
			case strings.HasSuffix(name, ".ico"):
				w.Header().Set("Content-Type", "image/x-icon")
			case strings.HasSuffix(name, ".woff2"):
				w.Header().Set("Content-Type", "font/woff2")
			}
			if _, err := sub.Open(name); err != nil {
				http.NotFound(w, r)
				return
			}
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/" + name
			fileServer.ServeHTTP(w, r2)
			return
		}

		http.NotFound(w, r)
	})
}

func setSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	// script 仅外部包；style 允许 React 动态宽度 + Google Fonts；字体 gstatic
	w.Header().Set("Content-Security-Policy",
		"default-src 'self'; style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; font-src 'self' https://fonts.gstatic.com data:; script-src 'self'; img-src 'self' data:; connect-src 'self'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'")
}

// ReadStatic 读取已嵌入的 dist 文件（测试/调试用）。
func ReadStatic(name string) ([]byte, error) {
	return distFS.ReadFile(path.Join("dist", name))
}
