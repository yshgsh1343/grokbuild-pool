/* DOM / format / toast / loading helpers */
import { state } from "./state.js";

export function $(id) { return document.getElementById(id); }

export function esc(s) {
  return String(s == null ? "" : s)
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;");
}

/**
 * toast(msg, toneOrOk)
 * tone: true|"success" | false|"danger" | "warning" | "info" | "neutral"
 * 绝不输出密钥/凭据——调用方须先脱敏。
 */
export function toast(msg, toneOrOk) {
  var host = $("toastHost");
  if (!host) return;
  var el = $("toast");
  if (!el) {
    el = document.createElement("div");
    el.id = "toast";
    el.className = "toast hidden";
    el.setAttribute("role", "status");
    host.appendChild(el);
  }
  if (state.toastTimer) clearTimeout(state.toastTimer);

  var tone = "neutral";
  if (toneOrOk === true || toneOrOk === "success" || toneOrOk === "ok") tone = "success";
  else if (toneOrOk === false || toneOrOk === "danger" || toneOrOk === "bad" || toneOrOk === "error") tone = "danger";
  else if (toneOrOk === "warning" || toneOrOk === "warn") tone = "warning";
  else if (toneOrOk === "info") tone = "info";
  else if (toneOrOk === "neutral") tone = "neutral";

  el.textContent = sanitizeToastMessage(msg);
  el.classList.remove("hidden", "ok", "bad", "success", "danger", "warning", "info", "neutral", "is-leaving");
  void el.offsetWidth;
  el.classList.add(tone);
  if (tone === "success") el.classList.add("ok");
  if (tone === "danger") el.classList.add("bad");

  state.toastTimer = setTimeout(function () {
    el.classList.add("is-leaving");
    setTimeout(function () {
      el.classList.add("hidden");
      el.classList.remove("is-leaving");
    }, prefersReducedMotion() ? 0 : 180);
  }, tone === "danger" || tone === "warning" ? 4200 : 3000);
}

function sanitizeToastMessage(msg) {
  var s = msg == null ? "" : String(msg);
  if (s.length > 280) s = s.slice(0, 280) + "…";
  s = s.replace(/\bsk-[A-Za-z0-9_-]{12,}\b/g, "sk-…");
  s = s.replace(/\bBearer\s+[A-Za-z0-9._\-/=+]{12,}/gi, "Bearer …");
  return s;
}

export function prefersReducedMotion() {
  try {
    return !!(window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches);
  } catch (_) {
    return false;
  }
}

export function fmtBytes(n) {
  if (n == null || n === "") return "—";
  var u = ["B", "KB", "MB", "GB", "TB"];
  var i = 0;
  n = Number(n);
  if (!isFinite(n) || n < 0) return "—";
  while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
  return n.toFixed(i ? 1 : 0) + " " + u[i];
}

export function fmtCooldown(until) {
  until = Number(until) || 0;
  if (until <= 0) return "—";
  var now = Math.floor(Date.now() / 1000);
  if (until <= now) return "已过期";
  var left = until - now;
  if (left < 60) return left + "s";
  if (left < 3600) return Math.ceil(left / 60) + "m";
  return Math.ceil(left / 3600) + "h";
}

export function copyText(text) {
  if (!text) return Promise.reject(new Error("空内容"));
  if (navigator.clipboard && navigator.clipboard.writeText) {
    return navigator.clipboard.writeText(text);
  }
  return new Promise(function (resolve, reject) {
    var ta = document.createElement("textarea");
    ta.value = text;
    ta.setAttribute("readonly", "");
    ta.style.position = "fixed";
    ta.style.left = "-9999px";
    document.body.appendChild(ta);
    ta.select();
    try {
      if (document.execCommand("copy")) resolve();
      else reject(new Error("copy failed"));
    } catch (e) {
      reject(e);
    } finally {
      document.body.removeChild(ta);
    }
  });
}

export function skeletonRows(n, label) {
  n = n || 5;
  var rows = "";
  for (var i = 0; i < n; i++) {
    rows += '<div class="skeleton-row"><div class="skeleton sk-line"></div></div>';
  }
  return '<div class="skeleton-list" aria-busy="true" aria-label="' + esc(label || "加载中") + '">' +
    rows + "</div>";
}

/**
 * withButtonLoading(btn, fn, opts)
 * 防双击：同一按钮 data-loading=1 时直接 resolve。
 */
export function withButtonLoading(btn, fn, opts) {
  opts = opts || {};
  if (!btn) {
    return Promise.resolve().then(fn);
  }
  if (btn.getAttribute("data-loading") === "1") {
    return Promise.resolve();
  }
  var loadingText = opts.loadingText != null ? String(opts.loadingText) : "处理中…";
  var prevText = btn.textContent;
  var prevDisabled = !!btn.disabled;
  var hasElementChild = false;
  for (var i = 0; i < btn.childNodes.length; i++) {
    if (btn.childNodes[i].nodeType === 1) { hasElementChild = true; break; }
  }

  btn.setAttribute("data-loading", "1");
  btn.setAttribute("aria-busy", "true");
  btn.disabled = true;
  btn.classList.add("is-busy");
  if (!hasElementChild) {
    btn.textContent = loadingText;
  } else {
    btn.setAttribute("title", loadingText);
  }

  return Promise.resolve()
    .then(fn)
    .finally(function () {
      btn.removeAttribute("data-loading");
      btn.removeAttribute("aria-busy");
      btn.classList.remove("is-busy");
      if (!hasElementChild) {
        btn.textContent = prevText;
      }
      btn.disabled = prevDisabled;
      if (opts.keepDisabled) btn.disabled = true;
    });
}

export function setControlsBusy(idsOrEls, busy) {
  (idsOrEls || []).forEach(function (x) {
    var el = typeof x === "string" ? $(x) : x;
    if (!el) return;
    if (busy) {
      el.setAttribute("data-prev-disabled", el.disabled ? "1" : "0");
      el.disabled = true;
      el.setAttribute("aria-busy", "true");
    } else {
      var prev = el.getAttribute("data-prev-disabled");
      el.disabled = prev === "1";
      el.removeAttribute("data-prev-disabled");
      el.removeAttribute("aria-busy");
    }
  });
}

export function accountLabel(a) {
  if (!a) return "未知账号";
  if (a.email) return String(a.email);
  if (a.name) return String(a.name);
  var id = String(a.id || "");
  if (id.length > 18) return id.slice(0, 10) + "…" + id.slice(-4);
  return id || "未知账号";
}

export function tokenLabel(t) {
  if (!t) return "未知令牌";
  var name = t.name ? String(t.name) : "token";
  var prefix = t.key_prefix ? String(t.key_prefix) : "";
  if (!prefix && t.id) prefix = String(t.id);
  if (prefix && prefix.length > 24) prefix = prefix.slice(0, 12) + "…";
  return name + (prefix ? "（" + prefix + "）" : "");
}
