/* Dashboard */
import { state } from "./state.js";
import { $, esc, toast } from "./util.js";
import { api, handleAuthError } from "./api.js";
import { setAuthed, stopPoll, wrapPage, pageHd } from "./shell.js";

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
  if (host.children.length !== items.length) {
    host.innerHTML = items.map(function (it) {
      return '<div class="stat-cell"><div class="stat-label">' + esc(it[0]) +
        '</div><div class="stat-value" data-k="' + esc(it[0]) + '">' + esc(String(it[1])) +
        "</div>" +
        (it[2] ? '<div class="stat-sub muted">' + esc(String(it[2])) + "</div>" : "") +
        "</div>";
    }).join("");
    return;
  }
  for (var i = 0; i < items.length; i++) {
    var cell = host.children[i];
    if (!cell) continue;
    var lab = cell.querySelector(".stat-label");
    var val = cell.querySelector(".stat-value");
    var sub = cell.querySelector(".stat-sub");
    if (lab) lab.textContent = items[i][0];
    if (val) {
      val.classList.remove("is-skeleton");
      val.textContent = String(items[i][1]);
    }
    if (items[i][2]) {
      if (!sub) {
        sub = document.createElement("div");
        sub.className = "stat-sub muted";
        cell.appendChild(sub);
      }
      sub.textContent = String(items[i][2]);
    } else if (sub) {
      sub.textContent = "";
    }
  }
}

function pct(x) {
  if (x == null || isNaN(Number(x))) return "—";
  return (Number(x) * 100).toFixed(1) + "%";
}

function loadDash(force) {
  if (document.visibilityState === "hidden" && !force) return;
  api("/admin/pool/stats").then(function (s) {
    var avail = s.accounts_available != null ? s.accounts_available : (s.catalog_active != null ? s.catalog_active : 0);
    var total = s.accounts_total != null ? s.accounts_total : (s.catalog_count != null ? s.catalog_count : 0);
    var req = s.requests_total != null ? s.requests_total : 0;
    var err = s.errors_total != null ? s.errors_total : 0;
    var ok = req - err;
    if (ok < 0) ok = 0;
    var rate = s.success_rate != null ? s.success_rate : (req > 0 ? ok / req : 1);

    // 上方小方块：去掉 503 / inflight / RSS
    setStatGrid([
      ["可用账号", avail, total ? (avail + " / " + total) : ""],
      ["请求数", req, "成功率 " + pct(rate) + (err ? " · " + err + " 失败" : "")],
      ["热池", (s.pool_hot_size != null ? s.pool_hot_size : 0) + " / " + (s.hot_cap != null ? s.hot_cap : "—"), "冷却 " + (s.pool_cooldown_size != null ? s.pool_cooldown_size : 0)],
      ["令牌", (s.tokens_enabled != null ? s.tokens_enabled : 0) + " / " + (s.tokens_total != null ? s.tokens_total : 0), "耗尽 " + (s.tokens_exhausted != null ? s.tokens_exhausted : 0)]
    ]);

    // 下方使用概览面板
    var panel = $("dashOverview");
    if (panel) {
      panel.innerHTML =
        '<div class="overview-grid">' +
        card("可用账号", String(avail), (total ? avail + " / " + total + " 可用" : "冷库 active 且启用")) +
        card("请求数", String(req), "成功率 " + pct(rate) + " · " + err + " 失败") +
        card("热池占用", String(s.pool_hot_size != null ? s.pool_hot_size : 0), "容量 " + (s.hot_cap != null ? s.hot_cap : "—") + " · 冷却 " + (s.pool_cooldown_size != null ? s.pool_cooldown_size : 0)) +
        card("账号结构", String(total),
          "启用 " + (s.catalog_enabled != null ? s.catalog_enabled : "—") +
          " · 隔离 " + (s.catalog_quarantine != null ? s.catalog_quarantine : "—") +
          " · 禁用 " + (s.catalog_disabled != null ? s.catalog_disabled : "—")) +
        card("令牌", String(s.tokens_total != null ? s.tokens_total : 0),
          "启用 " + (s.tokens_enabled != null ? s.tokens_enabled : 0) +
          " · 耗尽 " + (s.tokens_exhausted != null ? s.tokens_exhausted : 0)) +
        card("刷新", String((s.refresh_ok_total || 0) + (s.refresh_fail_total || 0)),
          "OK " + (s.refresh_ok_total != null ? s.refresh_ok_total : 0) +
          " · Fail " + (s.refresh_fail_total != null ? s.refresh_fail_total : 0)) +
        card("换号/熔断", String(s.pool_failover_total != null ? s.pool_failover_total : 0),
          "failover " + (s.pool_failover_total != null ? s.pool_failover_total : 0) +
          " · 429熔断 " + (s.pool_rate_limit_break_total != null ? s.pool_rate_limit_break_total : 0)) +
        "</div>";
    }

    var dm = $("dashMeta");
    if (dm) {
      dm.textContent = "更新于 " + new Date().toLocaleString() +
        " · uptime " + Math.round(s.uptime_seconds || 0) + "s · " +
        (s.listen || "") + " · " + (s.version || "");
    }
    var ver = $("hdVersion");
    if (ver && s.version) ver.textContent = String(s.version).indexOf("v") === 0 ? s.version : ("v" + s.version);
    var de = $("dashErr");
    if (de) de.innerHTML = "";
  }).catch(function (e) {
    if (handleAuthError(e)) return;
    var de = $("dashErr");
    if (de) de.innerHTML = '<div class="err-box">加载失败：' + esc(e.message) + "</div>";
    toast(e.message, false);
  });
}

function card(title, value, sub) {
  return '<div class="overview-card">' +
    '<div class="overview-title">' + esc(title) + "</div>" +
    '<div class="overview-value">' + esc(value) + "</div>" +
    (sub ? '<div class="overview-sub muted">' + esc(sub) + "</div>" : "") +
    "</div>";
}

export function renderDashboard() {
  setAuthed(true);
  if (!state.dashBuilt || !$("kpis")) {
    state.dashBuilt = true;
    $("main").innerHTML = wrapPage(
      pageHd("仪表盘", "运行态与池容量一览",
        '<button type="button" class="page-action-btn" id="dashRefresh">刷新</button>') +
      '<div id="dashErr"></div>' +
      '<div id="kpis" class="stat-grid">' + skeletonStats(4) + "</div>" +
      '<div class="section-head" style="margin-top:18px"><div class="section-title">使用概览</div></div>' +
      '<div id="dashOverview" class="panel">' + skeletonStats(4) + "</div>" +
      '<p class="muted dashboard-meta" id="dashMeta"></p>'
    );
    $("dashRefresh").addEventListener("click", function () { loadDash(true); });
  }
  loadDash(false);
  stopPoll();
  state.pollTimer = setInterval(function () { loadDash(false); }, 5000);
}
