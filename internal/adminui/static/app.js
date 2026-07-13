/* 管理台前端：深浅色 + 令牌发放 + 仪表盘
 * - admin_key 仅存内存（变量 adminKey），绝不写入 localStorage / sessionStorage
 * - 主题偏好键：localStorage["pool-admin-theme"] = "dark" | "light"
 * - KPI 历史：localStorage["pool-admin-kpi-history"]（仅 success_rate/requests，供 sparkline）
 * - 无 HTML inline 事件；全部 addEventListener / 属性赋值（CSP script-src 'self'）
 * - 禁用 / 删除等危险操作以 toast 反馈，不弹 confirm
 */
(function () {
  "use strict";

  var adminKey = null; // 仅内存
  var currentPage = "";
  var dashBuilt = false;
  var pollTimer = null;
  var toastTimer = null;
  var KPI_HISTORY_KEY = "pool-admin-kpi-history";
  var KPI_HISTORY_MAX = 60; // 约 5 分钟（5s 轮询）

  // 账号列表游标分页状态（API: GET /admin/accounts?cursor=&limit=）
  var accPageSize = 50;
  var accCursor = "";          // 当前页请求 cursor（首页为空）
  var accNextCursor = "";      // 服务端返回的下一页 cursor
  var accCursorStack = [];     // 历史 cursor，用于上一页
  var accPageIndex = 1;        // 1-based 展示页码
  var accLoading = false;

  function $(id) { return document.getElementById(id); }

  /** 轻量 toast；ok=true 成功边框，否则错误边框 */
  function toast(msg, ok) {
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
    el.textContent = msg == null ? "" : String(msg);
    el.classList.remove("hidden", "ok", "bad");
    el.classList.add(ok ? "ok" : "bad");
    if (toastTimer) clearTimeout(toastTimer);
    toastTimer = setTimeout(function () {
      el.classList.add("hidden");
    }, 3200);
  }

  function themeInit() {
    var saved = null;
    try {
      saved = localStorage.getItem("pool-admin-theme");
    } catch (_) { /* 隐私模式等 */ }
    if (saved !== "light" && saved !== "dark") {
      saved = "light"; // 对齐 grok2api 默认浅色纸感
    }
    document.documentElement.setAttribute("data-theme", saved);
    syncThemeBtn();
  }

  function toggleTheme() {
    var cur = document.documentElement.getAttribute("data-theme") === "light" ? "dark" : "light";
    document.documentElement.setAttribute("data-theme", cur);
    try {
      localStorage.setItem("pool-admin-theme", cur);
    } catch (_) { /* ignore */ }
    syncThemeBtn();
  }

  function syncThemeBtn() {
    var btn = $("themeToggle");
    if (!btn) return;
    var light = document.documentElement.getAttribute("data-theme") !== "dark";
    btn.textContent = light ? "深色" : "浅色";
    btn.title = light ? "切换到深色" : "切换到浅色";
  }

  function headers(body) {
    var h = { "Accept": "application/json" };
    var isForm = typeof FormData !== "undefined" && body instanceof FormData;
    if (!isForm) h["Content-Type"] = "application/json";
    if (adminKey) h["Authorization"] = "Bearer " + adminKey;
    return h;
  }

  function api(path, opts) {
    opts = opts || {};
    var body = opts.body;
    var isForm = typeof FormData !== "undefined" && body instanceof FormData;
    return fetch(path, {
      method: opts.method || "GET",
      headers: headers(body),
      body: body !== undefined ? (isForm ? body : JSON.stringify(body)) : undefined
    }).then(function (r) {
      return r.text().then(function (text) {
        var j = {};
        if (text) {
          try { j = JSON.parse(text); } catch (_) { j = {}; }
        }
        if (!r.ok) {
          var msg = (j && j.error) || ("HTTP " + r.status);
          if (typeof msg !== "string") msg = "HTTP " + r.status;
          var err = new Error(msg);
          err.status = r.status;
          throw err;
        }
        return j;
      });
    });
  }

  function handleAuthError(e) {
    if (e && e.status === 401) {
      adminKey = null;
      setAuthed(false);
      location.hash = "#/login";
      toast("鉴权失效，请重新登录", false);
      return true;
    }
    return false;
  }

  function mobileMenuIsOpen() {
    var drawer = $("mobileDrawer");
    return !!(drawer && drawer.classList.contains("is-open"));
  }

  function mobileMenuFocusable() {
    var drawer = $("mobileDrawer");
    if (!drawer) return [];
    return Array.prototype.slice.call(
      drawer.querySelectorAll('a[href], button:not([disabled]), [tabindex]:not([tabindex="-1"])')
    );
  }

  function setMobileMenu(open, restoreFocus) {
    var btn = $("menuBtn");
    var drawer = $("mobileDrawer");
    var overlay = $("mobileOverlay");
    if (!btn || !drawer || !overlay) return;

    open = !!open && !!adminKey && window.innerWidth < 768;
    btn.setAttribute("aria-expanded", open ? "true" : "false");
    drawer.setAttribute("aria-hidden", open ? "false" : "true");
    drawer.classList.toggle("is-open", open);
    overlay.hidden = !open;
    overlay.classList.toggle("hidden", !open);
    document.body.classList.toggle("nav-open", open);
    if (open) drawer.removeAttribute("inert");
    else drawer.setAttribute("inert", "");

    if (open) {
      window.requestAnimationFrame(function () {
        var current = drawer.querySelector('[aria-current="page"]');
        var target = current || $("menuClose") || mobileMenuFocusable()[0];
        if (target) target.focus();
      });
    } else if (restoreFocus && !btn.classList.contains("hidden")) {
      btn.focus();
    }
  }

  function bindMobileMenu() {
    var btn = $("menuBtn");
    var close = $("menuClose");
    var overlay = $("mobileOverlay");
    var nav = $("mobileNav");
    if (!btn || !close || !overlay || !nav) return;

    btn.addEventListener("click", function () { setMobileMenu(true, false); });
    close.addEventListener("click", function () { setMobileMenu(false, true); });
    overlay.addEventListener("click", function () { setMobileMenu(false, true); });
    nav.addEventListener("click", function (ev) {
      if (ev.target.closest("a[href]")) setMobileMenu(false, false);
    });
    document.addEventListener("keydown", function (ev) {
      if (!mobileMenuIsOpen()) return;
      if (ev.key === "Escape") {
        ev.preventDefault();
        setMobileMenu(false, true);
        return;
      }
      if (ev.key !== "Tab") return;
      var focusable = mobileMenuFocusable();
      if (!focusable.length) return;
      var first = focusable[0];
      var last = focusable[focusable.length - 1];
      if (focusable.indexOf(document.activeElement) === -1) {
        ev.preventDefault();
        first.focus();
      } else if (ev.shiftKey && document.activeElement === first) {
        ev.preventDefault();
        last.focus();
      } else if (!ev.shiftKey && document.activeElement === last) {
        ev.preventDefault();
        first.focus();
      }
    });
    window.addEventListener("resize", function () {
      if (window.innerWidth >= 768 && mobileMenuIsOpen()) setMobileMenu(false, false);
    });
  }

  function setAuthed(on) {
    var nav = $("nav");
    var lo = $("logoutBtn");
    var menu = $("menuBtn");
    // grok2api 风格：导航用 visibility 占位，避免登录后顶栏「抽一下」
    if (nav) {
      nav.classList.toggle("is-locked", !on);
      nav.classList.remove("hidden");
    }
    if (lo) lo.classList.toggle("hidden", !on);
    if (menu) menu.classList.toggle("hidden", !on);
    if (!on) setMobileMenu(false, false);
  }

  function route() {
    var hash = (location.hash || "#/login").replace(/^#\/?/, "");
    var page = hash.split("?")[0] || "login";
    if (!adminKey && page !== "login") {
      location.hash = "#/login";
      return renderLogin();
    }
    var pageChanged = currentPage !== page;
    currentPage = page;
    setMobileMenu(false, false);
    if (page !== "dashboard") dashBuilt = false;
    document.querySelectorAll(".admin-nav-link, .mobile-nav a").forEach(function (a) {
      var current = a.getAttribute("data-route") === page;
      a.classList.toggle("active", current);
      if (current) a.setAttribute("aria-current", "page");
      else a.removeAttribute("aria-current");
    });
    window.__pageEnter = pageChanged;
    if (page === "login") return renderLogin();
    if (page === "dashboard") return renderDashboard();
    if (page === "accounts") return renderAccounts();
    if (page === "tokens") return renderTokens();
    if (page === "imports") return renderImportJobs();
    if (page === "settings") return renderSettings();
    if (page === "config") return renderConfig();
    location.hash = "#/login";
    renderLogin();
  }


  function wrapPage(html) {
    var enter = window.__pageEnter ? " page-enter" : "";
    return '<div class="page' + enter + '">' + html + "</div>";
  }

  function pageHd(title, sub, actionsHtml) {
    return (
      '<div class="page-hd">' +
      "<div><div class=\"page-title\">" + title + "</div>" +
      (sub ? '<div class="page-sub">' + sub + "</div>" : "") +
      "</div>" +
      (actionsHtml ? '<div class="page-actions">' + actionsHtml + "</div>" : "") +
      "</div>"
    );
  }

  function renderLogin() {
    stopPoll();
    setAuthed(false);
    dashBuilt = false;
    $("main").innerHTML = wrapPage(
      '<div class="login-body-wrap">' +
      '<div class="login-shell"><div class="login-card">' +
      '<div class="login-brand">GROKBUILD-POOL</div>' +
      '<div class="login-title">管理后台</div>' +
      '<div class="login-subtitle">请输入 admin_key 以继续（仅保存在本页内存）</div>' +
      '<div class="login-form">' +
      '<input type="password" id="keyInput" class="input" placeholder="后台密钥" autocomplete="off" spellcheck="false" />' +
      '<button class="btn btn-primary" id="loginBtn" type="button">继续</button>' +
      "</div></div></div></div>"
    );

    function doLogin() {
      var k = ($("keyInput").value || "").trim();
      if (!k) return toast("请输入密钥", false);
      var btn = $("loginBtn");
      if (btn) btn.disabled = true;
      adminKey = k;
      Promise.all([api("/admin/pool/stats"), api("/admin/settings")]).then(function (arr) {
        window.__settings = arr[1] || {};
        setAuthed(true);
        location.hash = "#/dashboard";
        toast("登录成功", true);
      }).catch(function (e) {
        adminKey = null;
        toast(e.message || "登录失败", false);
      }).then(function () {
        if (btn) btn.disabled = false;
      });
    }

    $("loginBtn").addEventListener("click", doLogin);
    $("keyInput").addEventListener("keydown", function (ev) {
      if (ev.key === "Enter") {
        ev.preventDefault();
        doLogin();
      }
    });
    setTimeout(function () {
      var inp = $("keyInput");
      if (inp) inp.focus();
    }, 0);
  }

  function fmtBytes(n) {
    if (n == null || n === "") return "—";
    var u = ["B", "KB", "MB", "GB", "TB"];
    var i = 0;
    n = Number(n);
    if (!isFinite(n) || n < 0) return "—";
    while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
    return n.toFixed(i ? 1 : 0) + " " + u[i];
  }

  function kpi(label, value) {
    return '<div class="kpi"><div class="label">' + esc(label) +
      '</div><div class="value">' + esc(String(value)) + "</div></div>";
  }

  /** 读取 KPI 历史（localStorage，失败则空数组） */
  function loadKpiHistory() {
    try {
      var raw = localStorage.getItem(KPI_HISTORY_KEY);
      if (!raw) return [];
      var arr = JSON.parse(raw);
      return Array.isArray(arr) ? arr : [];
    } catch (_) {
      return [];
    }
  }

  /** 追加一次采样并截断 */
  function pushKpiSample(successRate, requests) {
    var hist = loadKpiHistory();
    hist.push({
      t: Date.now(),
      success_rate: Number(successRate) || 0,
      requests: Number(requests) || 0
    });
    if (hist.length > KPI_HISTORY_MAX) {
      hist = hist.slice(hist.length - KPI_HISTORY_MAX);
    }
    try {
      localStorage.setItem(KPI_HISTORY_KEY, JSON.stringify(hist));
    } catch (_) { /* 配额满等 */ }
    return hist;
  }

  /**
   * 在 canvas 上画简易折线 sparkline（成功率 0–1）。
   * 纯前端、无依赖；空数据时画基线。
   */
  function drawSparkline(canvas, hist) {
    if (!canvas || !canvas.getContext) return;
    var dpr = window.devicePixelRatio || 1;
    var cssW = canvas.clientWidth || 240;
    var cssH = canvas.clientHeight || 48;
    canvas.width = Math.max(1, Math.floor(cssW * dpr));
    canvas.height = Math.max(1, Math.floor(cssH * dpr));
    var ctx = canvas.getContext("2d");
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    ctx.clearRect(0, 0, cssW, cssH);

    var rates = (hist || []).map(function (p) {
      var v = Number(p.success_rate);
      if (!isFinite(v)) v = 0;
      if (v < 0) v = 0;
      if (v > 1) v = 1;
      return v;
    });
    var theme = getComputedStyle(document.documentElement);
    // 背景
    ctx.fillStyle = theme.getPropertyValue("--bg-card").trim();
    ctx.fillRect(0, 0, cssW, cssH);

    // 参考线 100% / 50%
    ctx.strokeStyle = theme.getPropertyValue("--border").trim();
    ctx.lineWidth = 1;
    ctx.beginPath();
    ctx.moveTo(0, cssH * 0.15);
    ctx.lineTo(cssW, cssH * 0.15);
    ctx.moveTo(0, cssH * 0.5);
    ctx.lineTo(cssW, cssH * 0.5);
    ctx.stroke();

    if (rates.length < 2) {
      ctx.fillStyle = theme.getPropertyValue("--fg-muted").trim();
      ctx.font = "11px ui-sans-serif, system-ui, sans-serif";
      ctx.fillText("采样中…", 8, cssH / 2 + 4);
      return;
    }

    var pad = 4;
    var w = cssW - pad * 2;
    var h = cssH - pad * 2;
    var n = rates.length;
    var muted = theme.getPropertyValue("--fg-muted").trim();
    var success = theme.getPropertyValue("--success").trim();
    var error = theme.getPropertyValue("--error").trim();
    // 最近值决定线色：<0.95 用 error
    var last = rates[n - 1];
    var stroke = last >= 0.99 ? success : (last >= 0.95 ? muted : error);

    ctx.beginPath();
    for (var i = 0; i < n; i++) {
      var x = pad + (w * i) / (n - 1);
      // y：1 → 顶，0 → 底
      var y = pad + h * (1 - rates[i]);
      if (i === 0) ctx.moveTo(x, y);
      else ctx.lineTo(x, y);
    }
    ctx.strokeStyle = stroke;
    ctx.lineWidth = 1.75;
    ctx.lineJoin = "round";
    ctx.stroke();

    // 终点圆点
    var ex = pad + w;
    var ey = pad + h * (1 - last);
    ctx.beginPath();
    ctx.arc(ex, ey, 2.5, 0, Math.PI * 2);
    ctx.fillStyle = stroke;
    ctx.fill();
  }

  function renderDashboard() {
    setAuthed(true);
    if (!dashBuilt || !$("kpis")) {
      dashBuilt = true;
      $("main").innerHTML = wrapPage(
        pageHd("仪表盘", "运行态与池容量一览（对齐 grok2api 统计卡片）",
          '<button type="button" class="page-action-btn" id="dashRefresh">刷新</button>') +
        '<div id="dashErr"></div>' +
        '<div id="kpis" class="stat-grid">' + skeletonStats(9) + "</div>" +
        '<div class="spark-wrap">' +
        '<div class="spark-head"><span>请求成功率</span><span class="muted" id="sparkMeta"></span></div>' +
        '<canvas id="rateSpark" width="640" height="56" aria-label="成功率"></canvas>' +
        '<p class="muted dashboard-note">本机采样 · localStorage · 约 5s/点</p></div>' +
        '<p class="muted dashboard-meta" id="dashMeta"></p>'
      );
      $("dashRefresh").addEventListener("click", function () { loadDash(true); });
      drawSparkline($("rateSpark"), loadKpiHistory());
    }
    loadDash(false);
    stopPoll();
    pollTimer = setInterval(function () { loadDash(false); }, 5000);
  }

  function skeletonStats(n) {
    var html = "";
    for (var i = 0; i < n; i++) {
      html += '<div class="stat-cell"><div class="stat-label">—</div><div class="stat-value is-skeleton">0</div></div>';
    }
    return html;
  }

  function setStatGrid(items) {
    var host = $("kpis");
    if (!host) return;
    // 稳定结构：只改 text，避免整表销毁
    if (host.children.length !== items.length) {
      host.innerHTML = items.map(function (it) {
        return '<div class="stat-cell"><div class="stat-label">' + esc(it[0]) +
          '</div><div class="stat-value" data-k="' + esc(it[0]) + '">' + esc(String(it[1])) +
          "</div></div>";
      }).join("");
      return;
    }
    for (var i = 0; i < items.length; i++) {
      var cell = host.children[i];
      if (!cell) continue;
      var lab = cell.querySelector(".stat-label");
      var val = cell.querySelector(".stat-value");
      if (lab) lab.textContent = items[i][0];
      if (val) {
        val.classList.remove("is-skeleton");
        val.textContent = String(items[i][1]);
      }
    }
  }

  function loadDash(force) {
    if (document.visibilityState === "hidden" && !force) return;
    api("/admin/pool/stats").then(function (s) {
      var rate = s.success_rate != null
        ? (100 * Number(s.success_rate)).toFixed(1) + "%" : "—";
      setStatGrid([
        ["请求总数", s.requests_total != null ? s.requests_total : 0],
        ["成功率", rate],
        ["热池", (s.pool_hot_size != null ? s.pool_hot_size : 0) + " / " + (s.hot_cap != null ? s.hot_cap : "—")],
        ["冷却", s.pool_cooldown_size != null ? s.pool_cooldown_size : 0],
        ["Inflight", s.proxy_inflight != null ? s.proxy_inflight : 0],
        ["拒绝 503", s.proxy_reject_total != null ? s.proxy_reject_total : 0],
        ["RSS", fmtBytes(s.process_rss_bytes)],
        ["令牌启用", (s.tokens_enabled != null ? s.tokens_enabled : 0) + " / " + (s.tokens_total != null ? s.tokens_total : 0)],
        ["额度耗尽", s.tokens_exhausted != null ? s.tokens_exhausted : 0]
      ]);
      var dm = $("dashMeta");
      if (dm) {
        dm.textContent = "uptime " + Math.round(s.uptime_seconds || 0) + "s · " +
          (s.listen || "") + " · " + (s.version || "");
      }
      var ver = $("hdVersion");
      if (ver && s.version) ver.textContent = String(s.version).indexOf("v") === 0 ? s.version : ("v" + s.version);
      var de = $("dashErr");
      if (de) de.innerHTML = "";
      var hist = pushKpiSample(
        s.success_rate != null ? s.success_rate : 1,
        s.requests_total != null ? s.requests_total : 0
      );
      drawSparkline($("rateSpark"), hist);
      var meta = $("sparkMeta");
      if (meta) meta.textContent = hist.length + " 点 · " + rate;
    }).catch(function (e) {
      if (handleAuthError(e)) return;
      var de = $("dashErr");
      if (de) de.innerHTML = '<div class="err-box">加载失败：' + esc(e.message) + "</div>";
      toast(e.message, false);
    });
  }


  function stopPoll() {
    if (pollTimer) {
      clearInterval(pollTimer);
      pollTimer = null;
    }
  }

  function extractPlainKeys(res) {
    var keys = [];
    var list = (res && res.tokens) || [];
    list.forEach(function (x) {
      if (!x) return;
      var k = x.api_key || x.plaintext;
      if (!k && x.token && typeof x.token === "object") {
        /* 嵌套 token 无明文 */
      }
      if (k) keys.push(String(k));
    });
    // 单条兼容字段
    if (!keys.length && res) {
      if (res.api_key) keys.push(String(res.api_key));
      else if (res.plaintext) keys.push(String(res.plaintext));
    }
    return keys;
  }

  function copyText(text) {
    if (!text) return Promise.reject(new Error("空内容"));
    if (navigator.clipboard && navigator.clipboard.writeText) {
      return navigator.clipboard.writeText(text);
    }
    // 降级
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

  function bindOnceBox(keys) {
    var box = $("onceBox");
    if (!box) return;
    if (!keys.length) {
      box.innerHTML =
        '<div class="once-box"><p class="muted">已创建，但响应中未包含明文密钥。</p></div>';
      return;
    }
    var rows = keys.map(function (k, i) {
      return '<div class="once-row">' +
        '<div class="once-key mono">' + esc(k) + "</div>" +
        '<button type="button" class="btn btn-sm btn-secondary" data-copy-one="' + i + '">复制</button>' +
        "</div>";
    }).join("");
    box.innerHTML =
      '<div class="once-box">' +
      '<div class="once-head">' +
      '<p class="muted once-hint">明文密钥仅显示一次（共 ' + keys.length +
      " 把），请立即复制：</p>" +
      '<button type="button" class="btn sm primary" id="copyAllKeys">全部复制</button>' +
      "</div>" +
      '<div class="once-list">' + rows + "</div></div>";

    var all = $("copyAllKeys");
    if (all) {
      all.addEventListener("click", function () {
        copyText(keys.join("\n")).then(function () {
          toast("已复制 " + keys.length + " 把密钥", true);
        }).catch(function () {
          toast("复制失败，请手动选择", false);
        });
      });
    }
    box.querySelectorAll("[data-copy-one]").forEach(function (btn) {
      btn.addEventListener("click", function () {
        var i = parseInt(btn.getAttribute("data-copy-one"), 10);
        if (!keys[i]) return;
        copyText(keys[i]).then(function () {
          toast("已复制第 " + (i + 1) + " 把", true);
        }).catch(function () {
          toast("复制失败，请手动选择", false);
        });
      });
    });
  }

  function field(label, id, val, type) {
    type = type || "number";
    return '<div><label for="' + id + '">' + label + '</label><input id="' + id +
      '" class="input" type="' + type + '" value="' + (val == null ? "" : val) + '" /></div>';
  }


  function renderSettings() {
    stopPoll();
    setAuthed(true);
    $("main").innerHTML = wrapPage(
      pageHd("配置管理", "全部运行参数 · 分组编辑 · 热更新并持久化到 settings.json",
        '<button type="button" class="page-action-btn" id="reloadSet">重新加载</button>' +
        '<button type="button" class="page-action-btn-primary" id="saveSet">保存并应用</button>') +
      '<div id="setErr"></div>' +
      '<div id="setForm" class="panel-stack"></div>' +
      '<div class="panel"><div class="panel-title">当前 JSON 快照</div>' +
      '<pre id="setPreview" class="mono muted"></pre></div>'
    );

    function section(title, note, fieldsHtml) {
      return '<div class="panel">' +
        '<div class="section-title panel-section-title">' + title + "</div>" +
        (note ? '<p class="muted panel-note">' + note + "</p>" : "") +
        '<div class="form-row">' + fieldsHtml + "</div></div>";
    }

    function ro(label, val) {
      return '<div><label>' + label + '</label>' +
        '<input class="input" type="text" value="' + esc(val == null ? "" : String(val)) +
        '" readonly style="opacity:.75;cursor:default" /></div>';
    }

    function load() {
      api("/admin/settings").then(function (s) {
        // Snapshot may nest under settings or be flat
        if (s && s.settings) s = Object.assign({}, s.settings, { persisted_path: s.persisted_path });
        window.__settings = s || {};
        var path = s.persisted_path || "（内存）";
        var html = "";
        html += section("选号 / 热池", "max_inflight 为硬上限；权重影响 pow2 打分",
          field("热池大小(展示)", "sHot", s.hot_size) +
          field("单账号最大并发", "sMaxInf", s.max_inflight_per_account) +
          field("粘性 TTL 秒", "sSticky", s.sticky_ttl_sec) +
          field("粘性 LRU 容量", "sStickyMax", s.sticky_max) +
          field("Pow2 K", "sPow2", s.pow2_k) +
          field("选号 failover 建议", "sSelAtt", s.selector_max_attempts) +
          field("权重·优先级", "sWP", s.w_priority) +
          field("权重·inflight 惩罚", "sWI", s.w_inflight) +
          field("权重·失败惩罚", "sWF", s.w_failure) +
          field("选号抖动幅度", "sJit", s.jitter_amp)
        );
        html += section("租约 / 防封号冷却", "429 指数退避；403 可配置更长冷却与隔离",
          field("Lease failover 次数", "sAtt", s.max_attempts) +
          field("429 冷却基数秒", "sCB", s.cooldown_base_sec) +
          field("冷却上限秒", "sCC", s.cooldown_cap_sec) +
          field("429 指数上限", "sCE", s.cooldown_exp_max) +
          field("冷却抖动 %", "sCJ", s.cooldown_jitter_pct) +
          field("401 冷却秒", "sC401", s.unauthorized_cooldown_sec) +
          field("402 冷却秒", "sC402", s.payment_required_cooldown_sec) +
          field("401 隔离阈值", "sQ401", s.unauthorized_quarantine_after) +
          field("403 冷却秒", "sC403", s.forbidden_cooldown_sec) +
          field("403 隔离阈值(0=关)", "sQ403", s.forbidden_quarantine_after)
        );
        html += section("进程限制", "全局并发立即生效；body/超时写入配置快照",
          field("全局最大并发", "sGlob", s.max_concurrent) +
          field("最大 Body 字节", "sBody", s.max_body_bytes) +
          field("请求超时秒", "sTO", s.request_timeout_sec)
        );
        html += section("Token 刷新 workers", "QPS/Skew 热更新；worker 数仅影响后续逻辑",
          field("Workers (2-4)", "sRW", s.refresh_workers) +
          field("Refresh QPS", "sRQ", s.refresh_qps) +
          field("Skew 秒 (提前刷新)", "sRS", s.refresh_skew_sec)
        );
        html += section("令牌创建默认模板", "仅影响管理台创建表单默认值",
          field("默认额度", "sTQ", s.token_default_remain_quota) +
          field("默认并发", "sTC", s.token_default_max_concurrent) +
          field("默认 RPM", "sTR", s.token_default_rpm) +
          '<div><label>默认无限额度</label><select id="sTU" class="input">' +
          '<option value="0"' + (!s.token_default_unlimited ? " selected" : "") + ">否</option>" +
          '<option value="1"' + (s.token_default_unlimited ? " selected" : "") + ">是</option></select></div>"
        );
        html += section("只读 · 进程/部署", "修改 listen/上游/密钥请改 runtime YAML 后重启",
          ro("Listen", s.listen) +
          ro("Allow public listen", s.allow_public_listen) +
          ro("Data dir", s.data_dir) +
          ro("DB path", s.db_path) +
          ro("Mock upstream", s.mock_upstream) +
          ro("Upstream base URL", s.upstream_base_url || "（空）") +
          ro("OAuth refresh URL", s.oauth_refresh_url || "（默认/空）") +
          ro("API key 已配置", s.api_key_configured) +
          ro("Admin key 已配置", s.admin_key_configured) +
          ro("日志级别", s.logging_level) +
          ro("持久化路径", path)
        );
        $("setForm").innerHTML = html;
        $("setPreview").textContent = JSON.stringify(s, null, 2);
        var err = $("setErr");
        if (err) err.innerHTML = "";
      }).catch(function (e) {
        if (handleAuthError(e)) return;
        var err = $("setErr");
        if (err) err.innerHTML = '<div class="err-box">' + esc(e.message) + "</div>";
        toast(e.message, false);
      });
    }

    function num(id) {
      var el = $(id);
      if (!el) return 0;
      return parseFloat(el.value);
    }
    function numI(id) {
      var el = $(id);
      if (!el) return 0;
      return parseInt(el.value, 10) || 0;
    }

    $("saveSet").addEventListener("click", function () {
      var body = {
        hot_size: numI("sHot"),
        max_inflight_per_account: numI("sMaxInf"),
        sticky_ttl_sec: numI("sSticky"),
        sticky_max: numI("sStickyMax"),
        pow2_k: numI("sPow2"),
        selector_max_attempts: numI("sSelAtt"),
        w_priority: num("sWP"),
        w_inflight: num("sWI"),
        w_failure: num("sWF"),
        jitter_amp: num("sJit"),
        max_attempts: numI("sAtt"),
        cooldown_base_sec: numI("sCB"),
        cooldown_cap_sec: numI("sCC"),
        unauthorized_cooldown_sec: numI("sC401"),
        payment_required_cooldown_sec: numI("sC402"),
        unauthorized_quarantine_after: numI("sQ401"),
        forbidden_cooldown_sec: numI("sC403"),
        forbidden_quarantine_after: numI("sQ403"),
        cooldown_jitter_pct: numI("sCJ"),
        cooldown_exp_max: numI("sCE"),
        max_concurrent: numI("sGlob"),
        max_body_bytes: numI("sBody"),
        request_timeout_sec: numI("sTO"),
        refresh_workers: numI("sRW"),
        refresh_qps: num("sRQ"),
        refresh_skew_sec: numI("sRS"),
        token_default_remain_quota: numI("sTQ"),
        token_default_max_concurrent: numI("sTC"),
        token_default_rpm: numI("sTR"),
        token_default_unlimited: $("sTU") && $("sTU").value === "1"
      };
      var btn = $("saveSet");
      if (btn) btn.disabled = true;
      api("/admin/settings", { method: "PUT", body: body }).then(function (res) {
        var s = (res && res.settings) || body;
        window.__settings = s;
        $("setPreview").textContent = JSON.stringify(s, null, 2);
        toast(res && res.persisted ? "参数已应用（已持久化）" : "参数已应用", true);
        load();
      }).catch(function (e) {
        if (handleAuthError(e)) return;
        toast(e.message || "保存失败", false);
      }).then(function () {
        if (btn) btn.disabled = false;
      });
    });
    $("reloadSet").addEventListener("click", load);
    load();
  }


  function renderTokens() {
    stopPoll();
    setAuthed(true);
    var d = window.__settings || {};
    var defQ = d.token_default_remain_quota != null ? d.token_default_remain_quota : 1000;
    var defC = d.token_default_max_concurrent != null ? d.token_default_max_concurrent : 5;
    var defR = d.token_default_rpm != null ? d.token_default_rpm : 0;
    var defU = d.token_default_unlimited ? "1" : "0";
    $("main").innerHTML = wrapPage(
      pageHd("令牌", "new-api 风格发放 · 明文仅创建时可见", "") +
      '<div class="panel"><div class="panel-title">快速创建</div>' +
      '<p class="muted" style="margin-bottom:12px">默认值来自「参数」页模板，可覆盖。</p>' +
      '<div class="form-row">' +
      '<div><label for="tName">名称</label><input id="tName" class="input" value="client" /></div>' +
      '<div><label for="tCount">数量 (1-100)</label><input id="tCount" class="input" type="number" value="1" min="1" max="100" /></div>' +
      '<div><label for="tQuota">剩余额度</label><input id="tQuota" class="input" type="number" value="' + defQ + '" /></div>' +
      '<div><label for="tUnlim">无限额度</label><select id="tUnlim" class="input"><option value="0"' + (defU === "0" ? " selected" : "") + '>否</option><option value="1"' + (defU === "1" ? " selected" : "") + '>是</option></select></div>' +
      '<div><label for="tConc">令牌并发上限 (0=不限)</label><input id="tConc" class="input" type="number" value="' + defC + '" min="0" /></div>' +
      '<div><label for="tRpm">RPM (0=不限)</label><input id="tRpm" class="input" type="number" value="' + defR + '" min="0" /></div>' +
      "</div>" +
      '<div class="toolbar" style="margin-top:12px">' +
      '<button class="btn btn-primary" id="createBtn" type="button">创建并复制密钥</button>' +
      '<button class="page-action-btn" id="tokRefresh" type="button">刷新列表</button></div>' +
      '<div id="onceBox"></div></div>' +
      '<div class="section-head"><div class="section-title">令牌列表<span class="section-count-badge" id="tokCount">0</span></div></div>' +
      '<div id="tokTable"><div class="empty">加载中…</div></div>'
    );

    $("createBtn").addEventListener("click", function () {
      var body = {
        name: ($("tName").value || "").trim() || "client",
        count: parseInt($("tCount").value, 10) || 1,
        remain_quota: parseInt($("tQuota").value, 10) || 0,
        unlimited_quota: $("tUnlim").value === "1",
        max_concurrent: parseInt($("tConc").value, 10) || 0,
        rpm: parseInt($("tRpm").value, 10) || 0
      };
      var btn = $("createBtn");
      if (btn) btn.disabled = true;
      api("/admin/tokens", { method: "POST", body: body }).then(function (res) {
        var keys = extractPlainKeys(res);
        var html = '<p class="muted">明文密钥仅显示一次，请立即复制：</p>';
        keys.forEach(function (k) {
          html += '<div class="once-key">' + esc(k) + "</div>";
        });
        var box = $("onceBox");
        if (box) box.innerHTML = html;
        if (keys[0]) {
          copyText(keys.join("\n")).then(function () {
            toast("已创建并复制到剪贴板", true);
          }).catch(function () {
            toast("已创建 " + keys.length + " 把密钥（请手动复制）", true);
          });
        } else {
          toast("已创建", true);
        }
        loadTokens();
      }).catch(function (e) {
        if (handleAuthError(e)) return;
        toast(e.message || "创建失败", false);
      }).then(function () {
        if (btn) btn.disabled = false;
      });
    });
    $("tokRefresh").addEventListener("click", loadTokens);
    loadTokens();
  }


  function loadTokens() {
    var host = $("tokTable");
    var cnt = $("tokCount");
    if (!host) return;
    api("/admin/tokens").then(function (res) {
      var list = res.tokens || [];
      if (!list.length) {
        host.innerHTML =
          '<div class="empty"><strong>暂无令牌</strong>' +
          "使用上方表单快速创建，明文密钥仅显示一次。</div>";
        return;
      }
      var rows = list.map(function (t) {
        var status = t.enabled
          ? '<span class="badge on">启用</span>'
          : '<span class="badge off">禁用</span>';
        var quota = t.unlimited_quota ? "∞" : String(t.remain_quota != null ? t.remain_quota : 0);
        return "<tr>" +
          '<td class="mono">' + esc(t.id) + "</td>" +
          "<td>" + esc(t.name) + "</td>" +
          '<td class="mono">' + esc(t.key_prefix) + "</td>" +
          "<td>" + status + "</td>" +
          "<td>" + esc(quota) + "</td>" +
          "<td>" + esc(t.max_concurrent ? String(t.max_concurrent) : "—") + "</td>" +
          "<td>" + esc(t.rpm ? String(t.rpm) : "—") + "</td>" +
          "<td>" + esc(String(t.used_quota || 0)) + " / " +
            esc(String(t.request_count || 0)) + "</td>" +
          '<td class="actions">' +
          (t.enabled
            ? '<button type="button" class="btn btn-sm btn-secondary" data-act="dis" data-id="' +
              esc(t.id) + '">禁用</button>'
            : '<button type="button" class="btn btn-sm btn-secondary" data-act="en" data-id="' +
              esc(t.id) + '">启用</button>') +
          '<button type="button" class="btn btn-sm btn-danger" data-act="del" data-id="' +
            esc(t.id) + '">删除</button>' +
          "</td></tr>";
      }).join("");
      host.innerHTML =
        '<div class="table-wrap"><table><thead><tr>' +
        "<th>ID</th><th>名称</th><th>前缀</th><th>状态</th><th>额度</th>" +
        "<th>并发</th><th>RPM</th><th>已用/请求</th><th></th>" +
        "</tr></thead><tbody>" + rows + "</tbody></table></div>";

      host.querySelectorAll("button[data-act]").forEach(function (btn) {
        btn.addEventListener("click", function () {
          var id = btn.getAttribute("data-id");
          var act = btn.getAttribute("data-act");
          if (!id) return;
          btn.disabled = true;
          var p;
          var okMsg = "已更新";
          if (act === "del") {
            p = api("/admin/tokens/" + encodeURIComponent(id), { method: "DELETE" });
            okMsg = "已删除";
          } else if (act === "dis") {
            p = api("/admin/tokens/" + encodeURIComponent(id) + "/disable", {
              method: "POST", body: {}
            });
            okMsg = "已禁用";
          } else {
            p = api("/admin/tokens/" + encodeURIComponent(id) + "/enable", {
              method: "POST", body: {}
            });
            okMsg = "已启用";
          }
          p.then(function () {
            toast(okMsg, true);
            loadTokens();
          }).catch(function (e) {
            if (handleAuthError(e)) return;
            toast(e.message || "操作失败", false);
            btn.disabled = false;
          });
        });
      });
    }).catch(function (e) {
      if (handleAuthError(e)) return;
      host.innerHTML =
        '<div class="err-box">加载令牌失败：' + esc(e.message) + "</div>";
      toast(e.message, false);
    });
  }

  /** 账号池列表：多选 + 批量启停 + 单行启停 + 游标翻页 */
  function renderAccounts() {
    stopPoll();
    setAuthed(true);
    // 进入页面时重置到第一页
    accCursor = "";
    accNextCursor = "";
    accCursorStack = [];
    accPageIndex = 1;
    $("main").innerHTML = wrapPage(
      pageHd("账户管理", "冷存储脱敏列表 · 启停同步热池 · 批量最多 500",
        '<button type="button" class="page-action-btn" id="accRefresh">刷新</button>') +
      '<div class="panel">' +
      '<div class="toolbar acc-batch-bar">' +
      '<button type="button" class="btn btn-sm btn-secondary" id="accSelectAll" disabled>全选本页</button>' +
      '<button type="button" class="btn btn-sm btn-secondary" id="accSelectNone" disabled>清空</button>' +
      '<button type="button" class="btn btn-sm btn-secondary" id="accBatchEnable" disabled>批量启用</button>' +
      '<button type="button" class="btn btn-sm btn-danger" id="accBatchDisable" disabled>批量禁用</button>' +
      '<span class="muted" id="accSelCount">已选 0</span>' +
      '<span class="toolbar-spacer"></span>' +
      '<label class="acc-page-size-label" for="accPageSize">每页</label>' +
      '<select id="accPageSize" class="input acc-page-size" title="每页条数">' +
      '<option value="20">20</option>' +
      '<option value="50" selected>50</option>' +
      '<option value="100">100</option>' +
      '<option value="200">200</option>' +
      '</select></div>' +
      '<div id="accTable"><div class="empty">加载中…</div></div>' +
      '<div class="pager" id="accPager">' +
      '<button type="button" class="btn btn-sm btn-secondary" id="accPrev" disabled>上一页</button>' +
      '<span class="muted" id="accPageInfo">第 1 页</span>' +
      '<button type="button" class="btn btn-sm btn-secondary" id="accNext" disabled>下一页</button>' +
      '</div>' +
      '<div id="accErr"></div></div>');

    $("accRefresh").addEventListener("click", function () {
      // 刷新保持当前页 cursor
      loadAccounts();
    });
    $("accSelectAll").addEventListener("click", function () {
      document.querySelectorAll("#accTable input.acc-check").forEach(function (cb) {
        cb.checked = true;
      });
      var master = $("accCheckAll");
      if (master) master.checked = true;
      updateAccSelUI();
    });
    $("accSelectNone").addEventListener("click", function () {
      document.querySelectorAll("#accTable input.acc-check").forEach(function (cb) {
        cb.checked = false;
      });
      var master = $("accCheckAll");
      if (master) master.checked = false;
      updateAccSelUI();
    });
    $("accBatchEnable").addEventListener("click", function () {
      runBatchAccounts("enable");
    });
    $("accBatchDisable").addEventListener("click", function () {
      runBatchAccounts("disable");
    });
    $("accPrev").addEventListener("click", function () {
      if (!accCursorStack.length || accLoading) return;
      accCursor = accCursorStack.pop() || "";
      accPageIndex = Math.max(1, accPageIndex - 1);
      loadAccounts();
    });
    $("accNext").addEventListener("click", function () {
      if (!accNextCursor || accLoading) return;
      accCursorStack.push(accCursor);
      accCursor = accNextCursor;
      accPageIndex += 1;
      loadAccounts();
    });
    $("accPageSize").addEventListener("change", function () {
      var n = parseInt($("accPageSize").value, 10);
      if (!n || n < 1) n = 50;
      if (n > 200) n = 200;
      accPageSize = n;
      // 改每页条数后回到首页
      accCursor = "";
      accNextCursor = "";
      accCursorStack = [];
      accPageIndex = 1;
      loadAccounts();
    });
    // 同步 select 与状态
    $("accPageSize").value = String(accPageSize);
    loadAccounts();
  }

  function updateAccPagerUI(pageCount) {
    var prev = $("accPrev");
    var next = $("accNext");
    var info = $("accPageInfo");
    if (prev) prev.disabled = accLoading || accCursorStack.length === 0;
    if (next) next.disabled = accLoading || !accNextCursor;
    if (info) {
      var bits = ["第 " + accPageIndex + " 页"];
      if (pageCount != null) bits.push("本页 " + pageCount + " 条");
      if (accNextCursor) bits.push("有后续");
      else if (pageCount > 0) bits.push("已到末页");
      info.textContent = bits.join(" · ");
    }
  }

  function selectedAccountIds() {
    var ids = [];
    document.querySelectorAll("#accTable input.acc-check:checked").forEach(function (cb) {
      var id = cb.getAttribute("data-id");
      if (id) ids.push(id);
    });
    return ids;
  }

  function updateAccSelUI() {
    var n = selectedAccountIds().length;
    var label = $("accSelCount");
    if (label) label.textContent = "已选 " + n;
    var any = document.querySelectorAll("#accTable input.acc-check").length > 0;
    var sa = $("accSelectAll");
    if (sa) sa.disabled = !any;
    var sn = $("accSelectNone");
    if (sn) sn.disabled = n === 0;
    var be = $("accBatchEnable");
    if (be) be.disabled = n === 0;
    var bd = $("accBatchDisable");
    if (bd) bd.disabled = n === 0;
  }

  function runBatchAccounts(action) {
    var ids = selectedAccountIds();
    if (!ids.length) {
      toast("请先勾选账号", false);
      return;
    }
    if (ids.length > 500) {
      toast("单次最多 500 个", false);
      return;
    }
    var btns = ["accBatchEnable", "accBatchDisable", "accSelectAll", "accSelectNone"];
    btns.forEach(function (id) {
      var el = $(id);
      if (el) el.disabled = true;
    });
    api("/admin/accounts/batch", {
      method: "POST",
      body: { action: action, ids: ids }
    }).then(function (res) {
      var ok = res && res.ok != null ? res.ok : 0;
      var failed = res && res.failed != null ? res.failed : 0;
      var verb = action === "enable" ? "启用" : "禁用";
      toast("批量" + verb + "：成功 " + ok + (failed ? "，失败 " + failed : ""), failed === 0);
      loadAccounts();
    }).catch(function (e) {
      if (handleAuthError(e)) return;
      toast(e.message || "批量操作失败", false);
      updateAccSelUI();
    });
  }

  function fmtCooldown(until) {
    until = Number(until) || 0;
    if (until <= 0) return "—";
    var now = Math.floor(Date.now() / 1000);
    if (until <= now) return "已过期";
    var left = until - now;
    if (left < 60) return left + "s";
    if (left < 3600) return Math.ceil(left / 60) + "m";
    return Math.ceil(left / 3600) + "h";
  }

  function loadAccounts() {
    var host = $("accTable");
    var errBox = $("accErr");
    if (!host) return;
    if (errBox) errBox.innerHTML = "";
    accLoading = true;
    updateAccPagerUI(null);
    host.innerHTML = '<div class="empty">加载中…</div>';

    var limit = accPageSize > 0 ? accPageSize : 50;
    if (limit > 200) limit = 200;
    var q = "/admin/accounts?limit=" + encodeURIComponent(String(limit));
    if (accCursor) q += "&cursor=" + encodeURIComponent(accCursor);

    api(q).then(function (res) {
      accLoading = false;
      var list = (res && res.accounts) || [];
      accNextCursor = (res && res.next_cursor) ? String(res.next_cursor) : "";
      if (!list.length) {
        if (accPageIndex > 1) {
          // 当前页空了（例如尾页被删光）：回退上一页
          if (accCursorStack.length) {
            accCursor = accCursorStack.pop() || "";
            accPageIndex = Math.max(1, accPageIndex - 1);
            loadAccounts();
            return;
          }
        }
        host.innerHTML =
          '<div class="empty"><strong>暂无账号</strong>' +
          "请用 poolctl import / bulkimport 或管理台导入后再刷新。</div>";
        updateAccSelUI();
        updateAccPagerUI(0);
        return;
      }
      var rows = list.map(function (a) {
        var status = a.enabled
          ? '<span class="badge on">启用</span>'
          : '<span class="badge off">禁用</span>';
        var proxy = a.proxy_url ? String(a.proxy_url) : "直连";
        return "<tr>" +
          '<td><input type="checkbox" class="acc-check" data-id="' +
            esc(a.id) + '" /></td>' +
          '<td class="mono">' + esc(a.id) + "</td>" +
          "<td>" + esc(a.email || "—") + "</td>" +
          "<td>" + esc(a.lifecycle || "—") + "</td>" +
          "<td>" + status + "</td>" +
          "<td>" + esc(fmtCooldown(a.cooldown_until)) + "</td>" +
          "<td>" + esc(String(a.priority != null ? a.priority : 0)) + "</td>" +
          '<td class="mono" title="' + esc(proxy) + '">' + esc(proxy) + "</td>" +
          '<td class="actions">' +
          (a.enabled
            ? '<button type="button" class="btn btn-sm btn-secondary" data-act="dis" data-id="' +
              esc(a.id) + '">禁用</button>'
            : '<button type="button" class="btn btn-sm btn-secondary" data-act="en" data-id="' +
              esc(a.id) + '">启用</button>') +
          "</td></tr>";
      }).join("");
      host.innerHTML =
        '<div class="table-wrap"><table><thead><tr>' +
        '<th><input type="checkbox" id="accCheckAll" title="全选本页" /></th>' +
        "<th>ID</th><th>Email</th><th>生命周期</th><th>状态</th>" +
        "<th>冷却</th><th>优先级</th><th>代理</th><th></th>" +
        "</tr></thead><tbody>" + rows + "</tbody></table></div>";

      var master = $("accCheckAll");
      if (master) {
        master.addEventListener("change", function () {
          var on = !!master.checked;
          host.querySelectorAll("input.acc-check").forEach(function (cb) {
            cb.checked = on;
          });
          updateAccSelUI();
        });
      }
      host.querySelectorAll("input.acc-check").forEach(function (cb) {
        cb.addEventListener("change", updateAccSelUI);
      });
      updateAccSelUI();
      updateAccPagerUI(list.length);

      host.querySelectorAll("button[data-act]").forEach(function (btn) {
        btn.addEventListener("click", function () {
          var id = btn.getAttribute("data-id");
          var act = btn.getAttribute("data-act");
          if (!id) return;
          btn.disabled = true;
          var path = act === "dis"
            ? "/admin/accounts/" + encodeURIComponent(id) + "/disable"
            : "/admin/accounts/" + encodeURIComponent(id) + "/enable";
          var okMsg = act === "dis" ? "已禁用账号" : "已启用账号";
          api(path, { method: "POST", body: {} }).then(function () {
            toast(okMsg, true);
            loadAccounts();
          }).catch(function (e) {
            if (handleAuthError(e)) return;
            toast(e.message || "操作失败", false);
            btn.disabled = false;
          });
        });
      });
    }).catch(function (e) {
      accLoading = false;
      if (handleAuthError(e)) return;
      host.innerHTML = "";
      if (errBox) {
        errBox.innerHTML =
          '<div class="err-box">加载账号失败：' + esc(e.message) +
          '<div class="muted">请检查服务、admin_key 与 catalog 是否挂载</div></div>';
      }
      toast(e.message, false);
      updateAccSelUI();
      updateAccPagerUI(0);
    });
  }


  function renderImportJobs() {
    stopPoll();
    setAuthed(true);
    $("main").innerHTML = wrapPage(
      pageHd("导入任务", "从本地浏览器安全上传，后台异步导入",
        '<button type="button" class="page-action-btn" id="impRefresh">刷新</button>') +
      '<div class="panel">' +
      '<div id="impErr"></div>' +
      '<div class="form-row">' +
      '<div><label for="impFormat">格式</label>' +
      '<select id="impFormat" class="input"><option value="json">JSON</option><option value="ndjson">NDJSON</option><option value="sso">SSO</option></select></div>' +
      '<div><label for="impFile">本地文件</label>' +
      '<input id="impFile" class="input file-input" type="file" accept=".json,application/json" /></div></div>' +
      '<p class="muted panel-note import-limit-note" id="impLimits">单文件上传，正在读取服务端限制…</p>' +
      '<div class="toolbar form-actions">' +
      '<button type="button" class="btn btn-primary" id="impSubmit">上传并创建任务</button></div></div>' +
      '<div class="section-head"><div class="section-title">任务列表</div></div>' +
      '<div id="impTable"><div class="empty">加载中…</div></div>'
    );
    var importMaxUpload = 32 * 1024 * 1024;

    function setImportError(message) {
      var host = $("impErr");
      if (!host) return;
      host.innerHTML = message ? '<div class="err-box">' + esc(message) + "</div>" : "";
    }

    function scheduleJobs(list) {
      stopPoll();
      var active = (list || []).some(function (j) {
        return j && (j.state === "queued" || j.state === "running");
      });
      if (active && currentPage === "imports") {
        pollTimer = setTimeout(loadJobs, 2500);
      }
    }

    function loadJobs() {
      stopPoll();
      return api("/admin/import/jobs").then(function (res) {
        var list = (res && res.jobs) || [];
        var limits = (res && res.limits) || {};
        var note = $("impLimits");
        if (limits.max_upload_bytes) importMaxUpload = Number(limits.max_upload_bytes) || importMaxUpload;
        if (note) {
          var maxMB = Math.round(importMaxUpload / 1048576);
          note.textContent = "最大 " + maxMB + " MiB · 最多 " + (limits.max_entries || 10000) +
            " 条 · SSO 转换器" + (limits.sso_converter_configured ? "已配置" : "未配置");
        }
        var host = $("impTable");
        if (!host) return;
        if (!list.length) {
          host.innerHTML = '<div class="empty"><strong>暂无任务</strong>选择本地文件后创建导入任务。</div>';
          scheduleJobs(list);
          return;
        }
        var rows = list.map(function (j) {
          return "<tr><td class=\"mono\">" + esc(j.id) + "</td><td>" + esc(j.source_name || "浏览器上传") +
            "</td><td>" + esc(j.format) + "</td><td>" + esc(j.state) + "</td><td>" +
            esc(String(j.ok != null ? j.ok : "—")) + "/" + esc(String(j.total != null ? j.total : "—")) +
            "</td><td>" + esc(j.error || "—") + "</td></tr>";
        }).join("");
        host.innerHTML = '<div class="table-wrap"><table><thead><tr><th>ID</th><th>来源</th><th>格式</th><th>状态</th><th>OK/Total</th><th>错误</th></tr></thead><tbody>' +
          rows + "</tbody></table></div>";
        scheduleJobs(list);
      }).catch(function (e) {
        if (handleAuthError(e)) return;
        setImportError("加载任务失败：" + (e.message || "未知错误"));
        stopPoll();
        if (currentPage === "imports") {
          pollTimer = setTimeout(loadJobs, 2500);
        }
      });
    }

    function syncFileAccept() {
      var format = $("impFormat").value;
      var input = $("impFile");
      if (!input) return;
      if (format === "ndjson") input.accept = ".ndjson,.jsonl,application/x-ndjson";
      else if (format === "sso") input.accept = ".txt,.json,text/plain,application/json";
      else input.accept = ".json,application/json";
    }

    $("impFormat").addEventListener("change", syncFileAccept);
    $("impRefresh").addEventListener("click", loadJobs);
    $("impSubmit").addEventListener("click", function () {
      var input = $("impFile");
      var file = input && input.files && input.files[0];
      if (!file) {
        setImportError("请选择要上传的本地文件");
        return;
      }
      if (file.size > importMaxUpload) {
        setImportError("文件超过 " + Math.round(importMaxUpload / 1048576) + " MiB；服务端也会再次校验");
        return;
      }
      setImportError("");
      var data = new FormData();
      data.append("format", $("impFormat").value);
      data.append("file", file, file.name);
      var btn = $("impSubmit");
      btn.disabled = true;
      btn.textContent = "上传中…";
      api("/admin/import/jobs", { method: "POST", body: data }).then(function () {
        input.value = "";
        toast("导入任务已创建", true);
        return loadJobs();
      }).catch(function (e) {
        if (handleAuthError(e)) return;
        var message = e.status === 413 ? "文件超过上传大小限制" :
          e.status === 429 ? "导入任务已满，请稍后重试" :
          e.status === 503 ? "SSO 转换器未配置或导入服务不可用" :
          (e.message || "提交失败");
        setImportError(message);
        toast(message, false);
      }).then(function () {
        btn.disabled = false;
        btn.textContent = "上传并创建任务";
      });
    });
    syncFileAccept();
    loadJobs();
  }


  function renderConfig() {
    stopPoll();
    setAuthed(true);
    $("main").innerHTML = wrapPage(
      pageHd("系统配置", "只读安全视图 · 不含密钥明文", 
        '<button type="button" class="page-action-btn" id="cfgRefresh">刷新</button>') +
      '<div class="panel"><pre id="cfg" class="mono">加载中…</pre></div>'
    );
    function load() {
      api("/admin/config").then(function (c) {
        var pre = $("cfg");
        if (pre) pre.textContent = JSON.stringify(c, null, 2);
      }).catch(function (e) {
        if (handleAuthError(e)) return;
        var pre = $("cfg");
        if (pre) pre.textContent = "加载失败：" + (e.message || "");
        toast(e.message, false);
      });
    }
    $("cfgRefresh").addEventListener("click", load);
    load();
  }

  function esc(s) {
    return String(s == null ? "" : s)
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;")
      .replace(/'/g, "&#39;");
  }

  // 顶栏绑定（无 HTML onclick）
  $("themeToggle").addEventListener("click", toggleTheme);
  $("logoutBtn").addEventListener("click", function () {
    adminKey = null;
    stopPoll();
    setAuthed(false);
    location.hash = "#/login";
    toast("已退出", true);
  });
  window.addEventListener("hashchange", route);
  document.addEventListener("visibilitychange", function () {
    // 仪表盘轮询在 load 内自行判断 hidden
  });

  themeInit();
  bindMobileMenu();
  route();
})();
