/* Accounts page — cursor pagination + batch ops + status */
import { state } from "./state.js";
import {
  $, esc, toast, fmtCooldown, skeletonRows,
  withButtonLoading, setControlsBusy, accountLabel
} from "./util.js";
import { api, handleAuthError, adminAuthHeaders } from "./api.js";
import { setAuthed, stopPoll, wrapPage, pageHd } from "./shell.js";
import { confirmAction } from "./dialog.js";

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
    var p = state.accPage || {};
    statsEl.innerHTML =
      '<div class="acc-metrics">' +
      '<span class="acc-stat">启用 <strong>' + esc(String(s.enabled != null ? s.enabled : "—")) + "</strong></span>" +
      '<span class="acc-stat">活跃 <strong>' + esc(String(s.active != null ? s.active : "—")) + "</strong></span>" +
      '<span class="acc-stat">冷却 <strong>' + esc(String(s.cooldown != null ? s.cooldown : "—")) + "</strong></span>" +
      '<span class="acc-stat">隔离 <strong>' + esc(String(s.quarantine != null ? s.quarantine : "—")) + "</strong></span>" +
      '<span class="acc-stat">禁用 <strong>' + esc(String(s.disabled != null ? s.disabled : "—")) + "</strong></span>" +
      (p.count != null
        ? '<span class="acc-stat">本页存活 <strong>' + esc(String(p.alive != null ? p.alive : 0)) +
          "</strong>/" + esc(String(p.count)) + "</span>" +
          '<span class="acc-stat">本页 inflight <strong>' + esc(String(p.inflight_sum != null ? p.inflight_sum : 0)) +
          "</strong></span>" +
          '<span class="acc-stat">额度快照 <strong>' + esc(String(p.with_billing != null ? p.with_billing : 0)) +
          "</strong></span>"
        : "") +
      "</div>";
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
  var bp = $("accBatchProbe");
  if (bp) bp.disabled = n === 0;
}


function fmtNum(v, digits) {
  if (v == null || v === "" || isNaN(Number(v))) return "—";
  var n = Number(v);
  if (digits == null) digits = 0;
  if (Math.abs(n) >= 1000) return n.toFixed(0);
  return n.toFixed(digits);
}


function displayEmail(a) {
  if (!a) return "—";
  var e = (a.email && String(a.email).trim()) || "";
  if (e) return e;
  var n = (a.name && String(a.name).trim()) || "";
  if (n && n.indexOf("@") >= 0) return n;
  if (n && !/^acct-|^[0-9a-f]{8}-[0-9a-f]{4}-/i.test(n) && n.length < 80) return n;
  return "—";
}

function formatBilling(a) {
  var b = a && a.billing;
  if (!b) {
    return '<span class="muted">未测活</span>';
  }
  var parts = [];
  if (b.monthly_used != null || b.monthly_limit != null) {
    parts.push("月 " + fmtNum(b.monthly_used, 0) + "/" + fmtNum(b.monthly_limit, 0));
  }
  if (b.weekly_usage_percent != null) {
    parts.push("周 " + fmtNum(b.weekly_usage_percent, 1) + "%");
  }
  if (b.grok_build_percent != null) {
    parts.push("Build " + fmtNum(b.grok_build_percent, 1) + "%");
  }
  var probe = "";
  if (b.probe_ok === true) probe = '<span class="badge on">测活OK</span>';
  else if (b.probe_ok === false) probe = '<span class="badge off">测活失败</span>';
  var when = b.probed_at ? '<div class="muted" style="font-size:11px">' + esc(fmtUnix(b.probed_at)) + "</div>" : "";
  if (!parts.length) {
    return (probe || '<span class="muted">—</span>') + when;
  }
  return '<div class="acc-billing">' + parts.map(function (p) {
    return '<div>' + esc(p) + "</div>";
  }).join("") + (probe ? "<div>" + probe + "</div>" : "") + when + "</div>";
}

function fmtUnix(sec) {
  var n = Number(sec);
  if (!n) return "—";
  try {
    return new Date(n * 1000).toLocaleString();
  } catch (e) {
    return String(sec);
  }
}

function formatInflight(a) {
  var n = a.inflight != null ? a.inflight : 0;
  if (!n) return '<span class="muted">0</span>';
  return '<span class="mono">' + esc(String(n)) + "</span>";
}

function accountStatusBadges(a) {
  var now = Math.floor(Date.now() / 1000);
  var parts = [];
  if (a.alive) parts.push('<span class="badge on">存活</span>');
  else parts.push('<span class="badge off">不可用</span>');
  if (a.billing && a.billing.probe_ok === true) parts.push('<span class="badge on">测活OK</span>');
  else if (a.billing && a.billing.probe_ok === false) parts.push('<span class="badge off">测活失败</span>');
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
  var coolLeft = Number(a.cooldown_remaining_sec || 0);
  if ((!coolLeft || coolLeft <= 0) && a.cooldown_until && Number(a.cooldown_until) > now) {
    coolLeft = Number(a.cooldown_until) - now;
  }
  if (coolLeft > 0) {
    parts.push('<span class="badge badge-cool">冷却剩余 ' + esc(fmtCooldown(now + coolLeft)) + "</span>");
  }
  if (!a.has_access && !a.has_refresh) {
    parts.push('<span class="badge off">无令牌</span>');
  } else if (!a.has_access) {
    parts.push('<span class="badge badge-soft">无 access</span>');
  }
  var reason = a.status_reason || a.last_error || "";
  var reasonHtml = reason
    ? '<div class="muted acc-status-reason" title="' + esc(reason) + '">' + esc(reason) + "</div>"
    : "";
  return '<div class="acc-status-cell">' + parts.join(" ") + reasonHtml + "</div>";
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

function batchControlIds() {
  return [
    "accBatchEnable", "accBatchDisable", "accBatchDelete", "accBatchProbe",
    "accSelectAll", "accSelectNone"
  ];
}

function runBatchAccounts(action) {
  var ids = selectedAccountIds();
  if (!ids.length) {
    toast("请先勾选账号", "warning");
    return;
  }
  var verb = action === "enable" ? "启用" : action === "delete" ? "删除" : "禁用";
  var confirmP;
  if (action === "delete") {
    confirmP = confirmAction({
      title: "删除 " + ids.length + " 个账号",
      message: "这些账号的凭据与相关记录将被永久删除，此操作无法撤销。不会显示任何完整密钥。",
      confirmLabel: "永久删除",
      cancelLabel: "取消",
      tone: "danger",
      requiredText: "DELETE"
    });
  } else {
    confirmP = confirmAction({
      title: "批量" + verb + " " + ids.length + " 个账号",
      message: "将对已选账号执行「" + verb + "」。",
      confirmLabel: "确认" + verb,
      cancelLabel: "取消",
      tone: action === "disable" ? "warning" : "neutral"
    });
  }
  confirmP.then(function (ok) {
    if (!ok) return;
    setControlsBusy(batchControlIds(), true);
    var t0 = Date.now();
    return api("/admin/accounts/batch", {
      method: "POST",
      body: { action: action, ids: ids }
    }).then(function (res) {
      var okN = res && res.ok != null ? res.ok : 0;
      var failed = res && res.failed != null ? res.failed : 0;
      var ms = Date.now() - t0;
      var msg = "批量" + verb + "：成功 " + okN + " 个";
      if (failed) msg += "，失败 " + failed + " 个";
      msg += "（" + ms + "ms）";
      toast(msg, failed ? "warning" : "success");
      return loadAccounts({ soft: true });
    }).catch(function (e) {
      if (handleAuthError(e)) return;
      toast(e.message || "批量操作失败", "danger");
    }).then(function () {
      setControlsBusy(batchControlIds(), false);
      updateAccSelUI();
    });
  });
}

function loadAccounts(opts) {
  opts = opts || {};
  var soft = !!opts.soft;
  var host = $("accTable");
  var errBox = $("accErr");
  if (!host) return Promise.resolve();
  if (errBox) errBox.innerHTML = "";
  state.accLoading = true;
  updateAccPagerUI(null);
  if (!soft || !host.querySelector("table")) {
    host.innerHTML = skeletonRows(6, "加载账号");
  }

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

  return api(q).then(function (res) {
    state.accLoading = false;
    var list = (res && res.accounts) || [];
    state.accNextCursor = (res && res.next_cursor) ? String(res.next_cursor) : "";
    if (res && res.total != null) state.accTotal = Number(res.total) || 0;
    if (res && res.stats) state.accStats = res.stats;
    if (res && res.page) state.accPage = res.page;
    else state.accPage = null;
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
        "<td title=\"" + esc(a.email || a.name || a.id || "") + "\">" + esc(displayEmail(a)) + "</td>" +
        "<td>" + accountStatusBadges(a) + "</td>" +
        "<td>" + formatSuccessRate(a) + "</td>" +
        "<td>" + formatInflight(a) + "</td>" +
        "<td>" + formatBilling(a) + "</td>" +
        '<td class="mono muted">' + esc(life) + "</td>" +
        "<td>" + esc(String(a.priority != null ? a.priority : 0)) + "</td>" +
        '<td class="mono" title="' + esc(proxy) + '">' + esc(proxy) + "</td>" +
        '<td class="actions">' +
        '<button type="button" class="btn btn-sm btn-secondary" data-act="detail" data-id="' +
          esc(a.id) + '">详情</button>' +
        '<button type="button" class="btn btn-sm btn-secondary" data-act="probe" data-id="' +
          esc(a.id) + '">测活</button>' +
        (a.enabled
          ? '<button type="button" class="btn btn-sm btn-secondary" data-act="dis" data-id="' +
            esc(a.id) + '">禁用</button>'
          : '<button type="button" class="btn btn-sm btn-secondary" data-act="en" data-id="' +
            esc(a.id) + '">启用</button>') +
        '<button type="button" class="btn btn-sm btn-danger" data-act="del" data-id="' +
          esc(a.id) + '" data-email="' + esc(a.email || "") + '" data-name="' +
          esc(a.name || "") + '">删除</button>' +
        "</td></tr>";
    }).join("");
    host.innerHTML =
      '<div class="table-wrap"><table><thead><tr>' +
      '<th><input type="checkbox" id="accCheckAll" title="全选本页" /></th>' +
      "<th>ID</th><th>邮箱</th><th>状态</th><th>成功率</th><th>并发</th><th>额度/测活</th><th>生命周期</th>" +
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

        if (act === "detail") {
          var row = null;
          try {
            var cached = state.accLastList || [];
            for (var i = 0; i < cached.length; i++) {
              if (cached[i] && cached[i].id === id) { row = cached[i]; break; }
            }
          } catch (_) {}
          if (!row) row = { id: id };
          openAccountDetail(row);
          return;
        }
        if (act === "probe") {
          withButtonLoading(btn, function () {
            return api("/admin/accounts/" + encodeURIComponent(id) + "/probe", {
              method: "POST", body: {}
            }).then(function (res) {
              if (res && res.probe_ok) {
                var bits = ["测活成功"];
                if (res.monthly_used != null || res.monthly_limit != null) {
                  bits.push("月 " + fmtNum(res.monthly_used, 0) + "/" + fmtNum(res.monthly_limit, 0));
                }
                if (res.weekly_usage_percent != null) {
                  bits.push("周 " + fmtNum(res.weekly_usage_percent, 1) + "%");
                }
                toast(bits.join(" · "), "success");
              } else {
                var pe = (res && res.probe_error) || "unknown";
                // 不展示过长上游 body
                if (String(pe).length > 120) pe = String(pe).slice(0, 120) + "…";
                toast("测活失败：" + pe, "danger");
              }
              return loadAccounts({ soft: true });
            }).catch(function (e) {
              if (handleAuthError(e)) return;
              toast(e.message || "测活失败", "danger");
              throw e;
            });
          }, { loadingText: "测活中…" });
          return;
        }

        if (act === "del") {
          var label = accountLabel({
            id: id,
            email: btn.getAttribute("data-email"),
            name: btn.getAttribute("data-name")
          });
          confirmAction({
            title: "删除账号",
            message: "将永久删除账号「" + label + "」及其凭据记录，此操作无法撤销。",
            confirmLabel: "永久删除",
            cancelLabel: "取消",
            tone: "danger"
          }).then(function (ok) {
            if (!ok) return;
            return withButtonLoading(btn, function () {
              return api("/admin/accounts/batch", {
                method: "POST",
                body: { action: "delete", ids: [id] }
              }).then(function (res) {
                var okN = res && res.ok != null ? res.ok : 0;
                var failed = res && res.failed != null ? res.failed : 0;
                if (failed) toast("删除失败 " + failed + " 个（成功 " + okN + "）", "warning");
                else toast("已删除账号", "success");
                return loadAccounts({ soft: true });
              }).catch(function (e) {
                if (handleAuthError(e)) return;
                toast(e.message || "删除失败", "danger");
                throw e;
              });
            }, { loadingText: "删除中…" });
          });
          return;
        }

        var path = act === "dis"
          ? "/admin/accounts/" + encodeURIComponent(id) + "/disable"
          : "/admin/accounts/" + encodeURIComponent(id) + "/enable";
        var okMsg = act === "dis" ? "已禁用账号" : "已启用账号";
        withButtonLoading(btn, function () {
          return api(path, { method: "POST", body: {} }).then(function () {
            toast(okMsg, "success");
            return loadAccounts({ soft: true });
          }).catch(function (e) {
            if (handleAuthError(e)) return;
            toast(e.message || "操作失败", "danger");
            throw e;
          });
        }, { loadingText: "处理中…" });
      });
    });
  }).catch(function (e) {
    state.accLoading = false;
    if (handleAuthError(e)) return;
    var hadTable = !!(host && host.querySelector("table"));
    if (!hadTable) {
      host.innerHTML = "";
      if (errBox) {
        errBox.innerHTML =
          '<div class="err-box">加载账号失败：' + esc(e.message) +
          '<div class="muted">请检查服务、admin_key 与 catalog 是否挂载</div></div>';
      }
    } else if (errBox) {
      errBox.innerHTML =
        '<div class="err-box">刷新失败，当前显示的可能是旧数据：' + esc(e.message) + "</div>";
    }
    toast(hadTable ? "刷新失败，当前显示的可能是旧数据" : (e.message || "加载失败"), "danger");
    updateAccSelUI();
    updateAccPagerUI(hadTable ? null : 0);
  });
}


var accountDetailRoot = null;

function closeAccountDetail() {
  if (!accountDetailRoot) return;
  accountDetailRoot.classList.remove("is-open");
  var root = accountDetailRoot;
  accountDetailRoot = null;
  document.body.classList.remove("dialog-open");
  setTimeout(function () {
    if (root && root.parentNode) root.parentNode.removeChild(root);
  }, 180);
}

function openAccountDetail(account) {
  if (!account || !account.id) return;
  closeAccountDetail();
  var root = document.createElement("div");
  root.className = "account-detail-root";
  root.innerHTML =
    '<div class="account-detail-backdrop" data-close="1"></div>' +
    '<aside class="account-detail-panel" role="dialog" aria-modal="true" aria-label="账号详情">' +
    '<div class="account-detail-head">' +
    '<div><h2 class="account-detail-title mono"></h2><div class="account-detail-sub"></div></div>' +
    '<button type="button" class="btn btn--icon btn--ghost" data-close="1" aria-label="关闭">×</button>' +
    '</div>' +
    '<div class="account-detail-body"></div>' +
    '</aside>';
  document.body.appendChild(root);
  document.body.classList.add("dialog-open");
  accountDetailRoot = root;
  root.querySelector(".account-detail-title").textContent = account.id;
  root.querySelector(".account-detail-sub").textContent = displayEmail(account) + " · " + (account.lifecycle || "—");
  var body = root.querySelector(".account-detail-body");
  body.innerHTML =
    '<div><div class="account-detail-section-title">状态</div>' + accountStatusBadges(account) +
    (account.status_reason ? '<div class="muted" style="margin-top:8px">' + esc(account.status_reason) + '</div>' : '') +
    '</div>' +
    '<div><div class="account-detail-section-title">基础信息</div><div class="account-detail-kv">' +
    '<div class="k">优先级</div><div class="v">' + esc(String(account.priority != null ? account.priority : 0)) + '</div>' +
    '<div class="k">成功率</div><div class="v">' + formatSuccessRate(account) + '</div>' +
    '<div class="k">并发</div><div class="v">' + formatInflight(account) + '</div>' +
    '<div class="k">代理</div><div class="v mono">' + esc(account.proxy_url || "直连") + '</div>' +
    '<div class="k">last_error</div><div class="v">' + esc(account.last_error || "—") + '</div>' +
    '</div></div>' +
    '<div><div class="account-detail-section-title">额度 / 测活</div>' + formatBilling(account) + '</div>' +
    '<div><div class="account-detail-section-title">模型冷却</div><div class="account-detail-list" id="accModelCoolList"><div class="muted">加载中…</div></div></div>';

  root.addEventListener("click", function (e) {
    var el = e.target;
    if (el && el.getAttribute && el.getAttribute("data-close") === "1") closeAccountDetail();
  });
  document.addEventListener("keydown", function onEsc(e) {
    if (e.key === "Escape") {
      document.removeEventListener("keydown", onEsc, true);
      closeAccountDetail();
    }
  }, true);
  requestAnimationFrame(function () { root.classList.add("is-open"); });

  api("/admin/accounts/" + encodeURIComponent(account.id) + "/model-cooldowns").then(function (res) {
    var list = root.querySelector("#accModelCoolList");
    if (!list) return;
    var rows = (res && res.model_cooldowns) || [];
    if (!rows.length) {
      list.innerHTML = '<div class="muted">当前无模型冷却</div>';
      return;
    }
    list.innerHTML = rows.map(function (r) {
      var left = Number(r.remaining_sec || 0);
      var unit = left < 60 ? (left + "s") : (left < 3600 ? (Math.ceil(left / 60) + "m") : (Math.ceil(left / 3600) + "h"));
      return '<div class="account-detail-item"><div class="title mono">' + esc(r.model || "?") +
        '</div><div class="meta">剩余 ' + esc(unit) +
        (r.last_error ? (" · " + esc(r.last_error)) : "") +
        (r.cooldown_until ? (" · until " + esc(fmtUnix(r.cooldown_until))) : "") +
        '</div></div>';
    }).join("");
  }).catch(function (e) {
    var list = root.querySelector("#accModelCoolList");
    if (list) list.innerHTML = '<div class="err-box">加载失败：' + esc(e.message || "unknown") + '</div>';
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
  state.accPage = null;
  if (state.accFilterStatus == null) state.accFilterStatus = "";
  if (state.accFilterLife == null) state.accFilterLife = "";
  if (state.accFilterQ == null) state.accFilterQ = "";
  $("main").innerHTML = wrapPage(
    pageHd("账户管理", "号池指标 · 额度/测活 · 存活/成功率 · 批量无条数硬限",
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
    '<option value="probe_ok">测活OK</option>' +
    '<option value="probe_fail">测活失败</option>' +
    '<option value="unprobed">未测活</option>' +
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
    '<button type="button" class="btn btn-sm btn-secondary" id="accBatchProbe" disabled>批量测活</button>' +
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
      toast(e.message || "导出失败", "danger");
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
  $("accBatchProbe").addEventListener("click", function () {
    var ids = selectedAccountIds();
    if (!ids.length) {
      toast("请先勾选账号", "warning");
      return;
    }
    if (ids.length > 100) {
      toast("单次最多测活 100 个", "warning");
      return;
    }
    var btn = $("accBatchProbe");
    setControlsBusy(batchControlIds(), true);
    withButtonLoading(btn, function () {
      var t0 = Date.now();
      return api("/admin/accounts/probe", {
        method: "POST",
        body: { ids: ids }
      }).then(function (res) {
        var ok = res && res.ok != null ? res.ok : 0;
        var failed = res && res.failed != null ? res.failed : 0;
        toast("批量测活：OK " + ok + " · 失败 " + failed + "（" + (Date.now() - t0) + "ms）",
          failed ? "warning" : "success");
        return loadAccounts({ soft: true });
      }).catch(function (e) {
        if (handleAuthError(e)) return;
        toast(e.message || "批量测活失败", "danger");
        throw e;
      });
    }, { loadingText: "测活中…" }).then(function () {
      setControlsBusy(batchControlIds(), false);
      updateAccSelUI();
    });
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
