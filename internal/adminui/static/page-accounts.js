/* Accounts page — cursor pagination + batch ops + status */
import { state } from "./state.js";
import { $, esc, toast, fmtCooldown, skeletonRows } from "./util.js";
import { api, handleAuthError, adminAuthHeaders } from "./api.js";
import { setAuthed, stopPoll, wrapPage, pageHd } from "./shell.js";

function updateAccPagerUI(pageCount) {
  var prev = $("accPrev");
  var next = $("accNext");
  var info = $("accPageInfo");
  if (prev) prev.disabled = state.accLoading || state.accCursorStack.length === 0;
  if (next) next.disabled = state.accLoading || !state.accNextCursor;
  if (info) {
    var pageSize = state.accPageSize > 0 ? state.accPageSize : 50;
    var total = state.accTotal > 0 ? state.accTotal : 0;
    var totalPages = total > 0 ? Math.max(1, Math.ceil(total / pageSize)) : 0;
    var bits = [];
    if (totalPages > 0) {
      bits.push("第 " + state.accPageIndex + " / " + totalPages + " 页");
    } else {
      bits.push("第 " + state.accPageIndex + " 页");
    }
    if (total > 0) bits.push("共 " + total + " 账号");
    if (pageCount != null) bits.push("本页 " + pageCount + " 条");
    if (state.accNextCursor) bits.push("有后续");
    else if (pageCount > 0 && totalPages > 0) bits.push("已到末页");
    info.textContent = bits.join(" · ");
  }
  var statsEl = $("accStats");
  if (statsEl && state.accStats) {
    var s = state.accStats;
    statsEl.innerHTML =
      '<span class="acc-stat">启用 <strong>' + esc(String(s.enabled != null ? s.enabled : "—")) + "</strong></span>" +
      '<span class="acc-stat">活跃 <strong>' + esc(String(s.active != null ? s.active : "—")) + "</strong></span>" +
      '<span class="acc-stat">冷却 <strong>' + esc(String(s.cooldown != null ? s.cooldown : "—")) + "</strong></span>" +
      '<span class="acc-stat">隔离 <strong>' + esc(String(s.quarantine != null ? s.quarantine : "—")) + "</strong></span>" +
      '<span class="acc-stat">禁用 <strong>' + esc(String(s.disabled != null ? s.disabled : "—")) + "</strong></span>";
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
  var bdel = $("accBatchDelete");
  if (bdel) bdel.disabled = n === 0;
}

function accountStatusBadges(a) {
  var now = Math.floor(Date.now() / 1000);
  var parts = [];
  if (a.alive) parts.push('<span class="badge on">存活</span>');
  else parts.push('<span class="badge off">不可用</span>');
  var life = String(a.lifecycle || "").toLowerCase();
  if (life === "quarantined") {
    parts.push('<span class="badge badge-warn">隔离</span>');
  } else if (life === "purged") {
    parts.push('<span class="badge off">已清理</span>');
  } else if (life && life !== "active") {
    parts.push('<span class="badge off">' + esc(life) + "</span>");
  }
  if (a.enabled) parts.push('<span class="badge badge-life">启用</span>');
  else parts.push('<span class="badge off">禁用</span>');
  if (a.manual_disabled) parts.push('<span class="badge off">手动关</span>');
  if (a.cooldown_until && Number(a.cooldown_until) > now) {
    parts.push('<span class="badge badge-cool">冷却 ' + esc(fmtCooldown(a.cooldown_until)) + "</span>");
  }
  if (!a.has_access && !a.has_refresh) {
    parts.push('<span class="badge off">无令牌</span>');
  } else if (!a.has_access) {
    parts.push('<span class="badge badge-soft">无 access</span>');
  }
  return '<div class="acc-status-cell">' + parts.join(" ") + "</div>";
}

function formatSuccessRate(a) {
  var ok = Number(a.success_count || 0);
  var bad = Number(a.failure_count || 0);
  var total = ok + bad;
  if (a.success_rate == null && total <= 0) {
    return '<span class="muted">—</span>';
  }
  var rate = a.success_rate != null ? Number(a.success_rate) : (total ? ok / total : 0);
  var cls = rate >= 0.9 ? "rate-ok" : rate >= 0.7 ? "rate-mid" : "rate-bad";
  return '<span class="' + cls + '">' + (rate * 100).toFixed(1) + "%</span>" +
    '<div class="muted" style="font-size:11px">' + ok + " 成功 / " + bad + " 失败</div>";
}

function runBatchAccounts(action) {
  var ids = selectedAccountIds();
  if (!ids.length) {
    toast("请先勾选账号", false);
    return;
  }
  // 无前端条数硬限；后端自动按 chunk 分块
  var btns = ["accBatchEnable", "accBatchDisable", "accBatchDelete", "accSelectAll", "accSelectNone"];
  btns.forEach(function (id) {
    var el = $(id);
    if (el) el.disabled = true;
  });
  var t0 = Date.now();
  api("/admin/accounts/batch", {
    method: "POST",
    body: { action: action, ids: ids }
  }).then(function (res) {
    var ok = res && res.ok != null ? res.ok : 0;
    var failed = res && res.failed != null ? res.failed : 0;
    var verb = action === "enable" ? "启用" : action === "delete" ? "删除" : "禁用";
    var ms = Date.now() - t0;
    toast("批量" + verb + "：成功 " + ok + (failed ? "，失败 " + failed : "") + "（" + ms + "ms）", failed === 0);
    loadAccounts();
  }).catch(function (e) {
    if (handleAuthError(e)) return;
    toast(e.message || "批量操作失败", false);
    updateAccSelUI();
  });
}

function loadAccounts() {
  var host = $("accTable");
  var errBox = $("accErr");
  if (!host) return;
  if (errBox) errBox.innerHTML = "";
  state.accLoading = true;
  updateAccPagerUI(null);
  host.innerHTML = skeletonRows(6, "加载账号");

  var limit = state.accPageSize > 0 ? state.accPageSize : 50;
  if (limit > 200) limit = 200;
  var q = "/admin/accounts?limit=" + encodeURIComponent(String(limit));
  if (state.accCursor) q += "&cursor=" + encodeURIComponent(state.accCursor);
  var st = state.accFilterStatus || "";
  var life = state.accFilterLife || "";
  var qq = state.accFilterQ || "";
  if (st) q += "&status=" + encodeURIComponent(st);
  if (life) q += "&lifecycle=" + encodeURIComponent(life);
  if (qq) q += "&q=" + encodeURIComponent(qq);

  api(q).then(function (res) {
    state.accLoading = false;
    var list = (res && res.accounts) || [];
    state.accNextCursor = (res && res.next_cursor) ? String(res.next_cursor) : "";
    if (res && res.total != null) state.accTotal = Number(res.total) || 0;
    if (res && res.stats) state.accStats = res.stats;
    if (!list.length) {
      if (state.accPageIndex > 1) {
        if (state.accCursorStack.length) {
          state.accCursor = state.accCursorStack.pop() || "";
          state.accPageIndex = Math.max(1, state.accPageIndex - 1);
          loadAccounts();
          return;
        }
      }
      host.innerHTML =
        '<div class="empty"><strong>暂无账号</strong>' +
        "请用 poolctl import / bulkimport、scripts/sso_convert.py 或管理台导入后再刷新。</div>";
      updateAccSelUI();
      updateAccPagerUI(0);
      return;
    }
    var rows = list.map(function (a) {
      var proxy = a.proxy_url ? String(a.proxy_url) : "直连";
      var life = a.lifecycle || "—";
      return "<tr>" +
        '<td><input type="checkbox" class="acc-check" data-id="' +
          esc(a.id) + '" /></td>' +
        '<td class="mono">' + esc(a.id) + "</td>" +
        "<td>" + esc(a.email || "—") + "</td>" +
        "<td>" + accountStatusBadges(a) + "</td>" +
        "<td>" + formatSuccessRate(a) + "</td>" +
        '<td class="mono muted">' + esc(life) + "</td>" +
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
      "<th>ID</th><th>Email</th><th>状态</th><th>成功率</th><th>生命周期</th>" +
      "<th>优先级</th><th>代理</th><th></th>" +
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
    state.accLoading = false;
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

export function renderAccounts() {
  stopPoll();
  setAuthed(true);
  state.accCursor = "";
  state.accNextCursor = "";
  state.accCursorStack = [];
  state.accPageIndex = 1;
  state.accTotal = 0;
  state.accStats = null;
  if (state.accFilterStatus == null) state.accFilterStatus = "";
  if (state.accFilterLife == null) state.accFilterLife = "";
  if (state.accFilterQ == null) state.accFilterQ = "";
  $("main").innerHTML = wrapPage(
    pageHd("账户管理", "冷存储脱敏列表 · 存活/成功率 · 筛选 · 批量无条数硬限（服务端自动分块）",
      '<button type="button" class="page-action-btn" id="accExport">导出 JSON</button>' +
      '<button type="button" class="page-action-btn" id="accRefresh">刷新</button>') +
    '<div class="panel">' +
    '<div id="accStats" class="acc-stats muted"></div>' +
    '<div class="acc-filters">' +
    '<div class="field"><label for="accFilterStatus">状态</label>' +
    '<select id="accFilterStatus" class="input">' +
    '<option value="">全部</option>' +
    '<option value="alive">存活</option>' +
    '<option value="dead">不可用</option>' +
    '<option value="enabled">启用</option>' +
    '<option value="disabled">禁用</option>' +
    '<option value="cooldown">冷却中</option>' +
    '<option value="quarantine">隔离</option>' +
    '<option value="no_token">无令牌</option>' +
    '</select></div>' +
    '<div class="field"><label for="accFilterLife">生命周期</label>' +
    '<select id="accFilterLife" class="input">' +
    '<option value="">全部</option>' +
    '<option value="active">active</option>' +
    '<option value="quarantined">quarantined</option>' +
    '<option value="purged">purged</option>' +
    '</select></div>' +
    '<div class="field field-grow"><label for="accFilterQ">搜索</label>' +
    '<input id="accFilterQ" class="input" placeholder="ID / Email / 名称" /></div>' +
    '<button type="button" class="btn btn-sm btn-secondary" id="accFilterApply">筛选</button>' +
    '<button type="button" class="btn btn-sm btn-ghost" id="accFilterReset">重置</button>' +
    '</div>' +
    '<div class="toolbar acc-batch-bar">' +
    '<button type="button" class="btn btn-sm btn-secondary" id="accSelectAll" disabled>全选本页</button>' +
    '<button type="button" class="btn btn-sm btn-secondary" id="accSelectNone" disabled>清空</button>' +
    '<button type="button" class="btn btn-sm btn-secondary" id="accBatchEnable" disabled>批量启用</button>' +
    '<button type="button" class="btn btn-sm btn-danger" id="accBatchDisable" disabled>批量禁用</button>' +
    '<button type="button" class="btn btn-sm btn-danger" id="accBatchDelete" disabled>批量删除</button>' +
    '<span class="muted" id="accSelCount">已选 0</span>' +
    '<span class="toolbar-spacer"></span>' +
    '<label class="acc-page-size-label" for="accPageSize">每页</label>' +
    '<select id="accPageSize" class="input acc-page-size" title="每页条数">' +
    '<option value="20">20</option>' +
    '<option value="50" selected>50</option>' +
    '<option value="100">100</option>' +
    '<option value="200">200</option>' +
    '</select></div>' +
    '<div id="accTable">' + skeletonRows(6, "加载账号") + "</div>" +
    '<div class="pager" id="accPager">' +
    '<button type="button" class="btn btn-sm btn-secondary" id="accPrev" disabled>上一页</button>' +
    '<span class="muted" id="accPageInfo">第 1 页</span>' +
    '<button type="button" class="btn btn-sm btn-secondary" id="accNext" disabled>下一页</button>' +
    '</div>' +
    '<div id="accErr"></div></div>');

  $("accRefresh").addEventListener("click", function () {
    loadAccounts();
  });
  $("accExport").addEventListener("click", function () {
    var url = "/admin/accounts/export?format=json&chunk=500";
    fetch(url, {
      method: "GET",
      headers: adminAuthHeaders()
    }).then(function (resp) {
      if (!resp.ok) {
        return resp.text().then(function (t) {
          throw new Error(t || ("export HTTP " + resp.status));
        });
      }
      var total = resp.headers.get("X-Export-Total") || "";
      return resp.blob().then(function (blob) {
        return { blob: blob, total: total };
      });
    }).then(function (pack) {
      var a = document.createElement("a");
      var obj = URL.createObjectURL(pack.blob);
      a.href = obj;
      a.download = "accounts-export.json";
      document.body.appendChild(a);
      a.click();
      a.remove();
      setTimeout(function () { URL.revokeObjectURL(obj); }, 2000);
      toast("导出完成" + (pack.total ? "（共 " + pack.total + " 条）" : ""), true);
    }).catch(function (e) {
      if (handleAuthError(e)) return;
      toast(e.message || "导出失败", false);
    });
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
  $("accBatchDelete").addEventListener("click", function () {
    runBatchAccounts("delete");
  });
  $("accPrev").addEventListener("click", function () {
    if (!state.accCursorStack.length || state.accLoading) return;
    state.accCursor = state.accCursorStack.pop() || "";
    state.accPageIndex = Math.max(1, state.accPageIndex - 1);
    loadAccounts();
  });
  $("accNext").addEventListener("click", function () {
    if (!state.accNextCursor || state.accLoading) return;
    state.accCursorStack.push(state.accCursor);
    state.accCursor = state.accNextCursor;
    state.accPageIndex += 1;
    loadAccounts();
  });
  $("accPageSize").addEventListener("change", function () {
    var n = parseInt($("accPageSize").value, 10);
    if (!n || n < 1) n = 50;
    if (n > 200) n = 200;
    state.accPageSize = n;
    state.accCursor = "";
    state.accNextCursor = "";
    state.accCursorStack = [];
    state.accPageIndex = 1;
    loadAccounts();
  });
  $("accPageSize").value = String(state.accPageSize);
  $("accFilterStatus").value = state.accFilterStatus || "";
  $("accFilterLife").value = state.accFilterLife || "";
  $("accFilterQ").value = state.accFilterQ || "";

  function resetPageAndLoad() {
    state.accCursor = "";
    state.accNextCursor = "";
    state.accCursorStack = [];
    state.accPageIndex = 1;
    loadAccounts();
  }
  $("accFilterApply").addEventListener("click", function () {
    state.accFilterStatus = $("accFilterStatus").value || "";
    state.accFilterLife = $("accFilterLife").value || "";
    state.accFilterQ = ($("accFilterQ").value || "").trim();
    resetPageAndLoad();
  });
  $("accFilterReset").addEventListener("click", function () {
    state.accFilterStatus = "";
    state.accFilterLife = "";
    state.accFilterQ = "";
    $("accFilterStatus").value = "";
    $("accFilterLife").value = "";
    $("accFilterQ").value = "";
    resetPageAndLoad();
  });
  $("accFilterQ").addEventListener("keydown", function (ev) {
    if (ev.key === "Enter") {
      ev.preventDefault();
      $("accFilterApply").click();
    }
  });
  loadAccounts();
}
