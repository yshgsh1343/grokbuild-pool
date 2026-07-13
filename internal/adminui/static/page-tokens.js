/* Tokens page — 创建 + 列表内联编辑并发/额度/RPM（PATCH 即时生效） */
import { state } from "./state.js";
import {
  $, esc, toast, copyText, skeletonRows,
  withButtonLoading, setControlsBusy, tokenLabel
} from "./util.js";
import { api, handleAuthError } from "./api.js";
import { setAuthed, stopPoll, wrapPage, pageHd } from "./shell.js";
import { confirmAction } from "./dialog.js";

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
  if (!keys.length && res) {
    if (res.api_key) keys.push(String(res.api_key));
    else if (res.plaintext) keys.push(String(res.plaintext));
  }
  return keys;
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
        toast("已复制 " + keys.length + " 把密钥", "success");
      }).catch(function () {
        toast("复制失败，请手动选择", "danger");
      });
    });
  }
  box.querySelectorAll("[data-copy-one]").forEach(function (btn) {
    btn.addEventListener("click", function () {
      var i = parseInt(btn.getAttribute("data-copy-one"), 10);
      if (!keys[i]) return;
      copyText(keys[i]).then(function () {
        toast("已复制第 " + (i + 1) + " 把", "success");
      }).catch(function () {
        toast("复制失败，请手动选择", "danger");
      });
    });
  });
}

function selectedTokenIds() {
  var ids = [];
  document.querySelectorAll("#tokTable input.tok-check:checked").forEach(function (cb) {
    var id = cb.getAttribute("data-id");
    if (id) ids.push(id);
  });
  return ids;
}

function selectedTokenKeys() {
  var keys = [];
  document.querySelectorAll("#tokTable input.tok-check:checked").forEach(function (cb) {
    var k = cb.getAttribute("data-key") || "";
    if (k) keys.push(k);
  });
  return keys;
}

function updateTokSelUI() {
  var n = selectedTokenIds().length;
  var label = $("tokSelCount");
  if (label) label.textContent = "已选 " + n;
  var any = document.querySelectorAll("#tokTable input.tok-check").length > 0;
  var sa = $("tokSelectAll");
  if (sa) sa.disabled = !any;
  var sn = $("tokSelectNone");
  if (sn) sn.disabled = n === 0;
  var cp = $("tokCopySelected");
  if (cp) cp.disabled = selectedTokenKeys().length === 0;
  var bd = $("tokBatchDelete");
  if (bd) bd.disabled = n === 0;
}

function fmtConc(t) {
  var max = t.max_concurrent != null ? t.max_concurrent : 0;
  var inf = t.inflight != null ? t.inflight : 0;
  if (!max) return "不限 · 占用 " + inf;
  return inf + " / " + max;
}

function fmtRpm(t) {
  return t.rpm ? String(t.rpm) : "不限";
}

function fmtQuota(t) {
  return t.unlimited_quota ? "∞" : String(t.remain_quota != null ? t.remain_quota : 0);
}

function editFormHTML(t) {
  var maxC = t.max_concurrent != null ? t.max_concurrent : 0;
  var rpm = t.rpm != null ? t.rpm : 0;
  var remain = t.remain_quota != null ? t.remain_quota : 0;
  var unlim = t.unlimited_quota ? "1" : "0";
  var name = t.name || "";
  return (
    '<div class="tok-edit" data-edit-id="' + esc(t.id) + '">' +
    '<div class="form-row form-row-token tok-edit-grid">' +
    '<div class="field"><label>名称</label>' +
    '<input class="input tok-e-name" value="' + esc(name) + '" /></div>' +
    '<div class="field"><label>并发上限 (0=不限)</label>' +
    '<input class="input tok-e-conc" type="number" min="0" value="' + maxC + '" /></div>' +
    '<div class="field"><label>RPM (0=不限)</label>' +
    '<input class="input tok-e-rpm" type="number" min="0" value="' + rpm + '" /></div>' +
    '<div class="field"><label>剩余额度</label>' +
    '<input class="input tok-e-quota" type="number" min="0" value="' + remain + '" /></div>' +
    '<div class="field"><label>无限额度</label>' +
    '<select class="input tok-e-unlim">' +
    '<option value="0"' + (unlim === "0" ? " selected" : "") + ">否</option>" +
    '<option value="1"' + (unlim === "1" ? " selected" : "") + ">是</option>" +
    "</select></div>" +
    "</div>" +
    '<p class="muted tok-edit-hint">保存后下一请求立即按新并发/RPM 限流；在途请求不中断。当前占用：' +
    esc(String(t.inflight || 0)) + "</p>" +
    '<div class="toolbar tok-edit-actions">' +
    '<button type="button" class="btn btn-primary btn-sm" data-act="save-edit" data-id="' +
    esc(t.id) + '">保存修改</button>' +
    '<button type="button" class="btn btn-sm btn-secondary" data-act="cancel-edit" data-id="' +
    esc(t.id) + '">取消</button>' +
    "</div></div>"
  );
}

function detailHTML(t) {
  var key = t.api_key || t.plaintext || "";
  var keyBlock = key
    ? '<div class="tok-detail mono">' + esc(key) +
      ' <button type="button" class="btn btn-sm btn-secondary" data-act="copy" data-key="' +
      esc(key) + '">复制</button></div>'
    : '<div class="tok-detail muted">密钥明文仅在创建时返回一次；列表不再回读明文。</div>';
  return keyBlock + editFormHTML(t);
}

function tokenRowHTML(t) {
  var status = t.enabled
    ? '<span class="badge on">启用</span>'
    : '<span class="badge off">禁用</span>';
  var key = t.api_key || t.plaintext || "";
  return '<tr class="tok-row" data-id="' + esc(t.id) + '">' +
    '<td><input type="checkbox" class="tok-check" data-id="' + esc(t.id) +
      '" data-key="' + esc(key) + '" /></td>' +
    '<td><button type="button" class="btn btn-sm btn-ghost tok-expand" data-act="expand" aria-expanded="false">▸</button></td>' +
    '<td class="mono">' + esc(t.id) + "</td>" +
    "<td>" + esc(t.name) + "</td>" +
    '<td class="mono">' + esc(t.key_prefix) + "</td>" +
    "<td>" + status + "</td>" +
    "<td>" + esc(fmtQuota(t)) + "</td>" +
    '<td class="mono tok-conc-cell">' + esc(fmtConc(t)) + "</td>" +
    "<td>" + esc(fmtRpm(t)) + "</td>" +
    "<td>" + esc(String(t.used_quota || 0)) + " / " +
      esc(String(t.request_count || 0)) + "</td>" +
    '<td class="actions">' +
    '<button type="button" class="btn btn-sm btn-secondary" data-act="edit" data-id="' +
      esc(t.id) + '">编辑</button>' +
    (t.enabled
      ? '<button type="button" class="btn btn-sm btn-secondary" data-act="dis" data-id="' +
        esc(t.id) + '">禁用</button>'
      : '<button type="button" class="btn btn-sm btn-secondary" data-act="en" data-id="' +
        esc(t.id) + '">启用</button>') +
    '<button type="button" class="btn btn-sm btn-danger" data-act="del" data-id="' +
      esc(t.id) + '">删除</button>' +
    "</td></tr>" +
    '<tr class="tok-detail-row hidden" data-for="' + esc(t.id) + '"><td colspan="11">' +
    detailHTML(t) + "</td></tr>";
}

function openDetail(host, id) {
  var detailRow = host.querySelector('tr.tok-detail-row[data-for="' + id + '"]');
  var row = host.querySelector('tr.tok-row[data-id="' + id + '"]');
  if (!detailRow || !row) return;
  detailRow.classList.remove("hidden");
  var btn = row.querySelector('[data-act="expand"]');
  if (btn) {
    btn.textContent = "▾";
    btn.setAttribute("aria-expanded", "true");
  }
}

function closeDetail(host, id) {
  var detailRow = host.querySelector('tr.tok-detail-row[data-for="' + id + '"]');
  var row = host.querySelector('tr.tok-row[data-id="' + id + '"]');
  if (!detailRow || !row) return;
  detailRow.classList.add("hidden");
  var btn = row.querySelector('[data-act="expand"]');
  if (btn) {
    btn.textContent = "▸";
    btn.setAttribute("aria-expanded", "false");
  }
}

function parseIntSafe(el, fallback) {
  if (!el) return fallback;
  var n = parseInt(el.value, 10);
  return isNaN(n) ? fallback : n;
}

function saveEdit(host, id, btn) {
  var box = host.querySelector('.tok-edit[data-edit-id="' + id + '"]');
  if (!box) {
    toast("编辑表单不存在", "warning");
    return;
  }
  var nameEl = box.querySelector(".tok-e-name");
  var concEl = box.querySelector(".tok-e-conc");
  var rpmEl = box.querySelector(".tok-e-rpm");
  var quotaEl = box.querySelector(".tok-e-quota");
  var unlimEl = box.querySelector(".tok-e-unlim");
  var body = {
    name: (nameEl && nameEl.value || "").trim() || "client",
    max_concurrent: parseIntSafe(concEl, 0),
    rpm: parseIntSafe(rpmEl, 0),
    remain_quota: parseIntSafe(quotaEl, 0),
    unlimited_quota: unlimEl && unlimEl.value === "1"
  };
  return withButtonLoading(btn, function () {
    return api("/admin/tokens/" + encodeURIComponent(id), {
      method: "PATCH",
      body: body
    }).then(function () {
      toast("已保存：并发 " + body.max_concurrent + " · RPM " + body.rpm + "（立即生效）", "success");
      return loadTokens({ soft: true });
    }).catch(function (e) {
      if (handleAuthError(e)) return;
      toast(e.message || "保存失败", "danger");
      throw e;
    });
  }, { loadingText: "保存中…" });
}

function loadTokens(opts) {
  opts = opts || {};
  var soft = !!opts.soft;
  var host = $("tokTable");
  var cnt = $("tokCount");
  if (!host) return Promise.resolve();
  if (!soft || !host.querySelector("table")) {
    /* keep existing table on soft refresh until data arrives */
  }
  return api("/admin/tokens").then(function (res) {
    var list = res.tokens || [];
    if (cnt) cnt.textContent = String(list.length);
    if (!list.length) {
      host.innerHTML =
        '<div class="empty"><strong>暂无令牌</strong>' +
        "使用上方表单快速创建；创建后可展开编辑并发/额度/RPM。</div>";
      updateTokSelUI();
      return;
    }
    var rows = list.map(tokenRowHTML).join("");
    host.innerHTML =
      '<div class="table-wrap"><table><thead><tr>' +
      '<th><input type="checkbox" id="tokCheckAll" title="全选" /></th>' +
      "<th></th><th>ID</th><th>名称</th><th>前缀</th><th>状态</th><th>额度</th>" +
      "<th>并发(占用/上限)</th><th>RPM</th><th>已用/请求</th><th></th>" +
      "</tr></thead><tbody>" + rows + "</tbody></table></div>";

    var master = $("tokCheckAll");
    if (master) {
      master.addEventListener("change", function () {
        var on = !!master.checked;
        host.querySelectorAll("input.tok-check").forEach(function (cb) { cb.checked = on; });
        updateTokSelUI();
      });
    }
    host.querySelectorAll("input.tok-check").forEach(function (cb) {
      cb.addEventListener("change", updateTokSelUI);
    });
    updateTokSelUI();

    host.querySelectorAll("button[data-act]").forEach(function (btn) {
      btn.addEventListener("click", function () {
        var act = btn.getAttribute("data-act");
        if (act === "expand") {
          var row = btn.closest("tr");
          var eid = row && row.getAttribute("data-id");
          if (!eid) return;
          var detailRow = host.querySelector('tr.tok-detail-row[data-for="' + eid + '"]');
          if (!detailRow) return;
          var open = detailRow.classList.contains("hidden");
          if (open) openDetail(host, eid);
          else closeDetail(host, eid);
          return;
        }
        if (act === "edit") {
          var editId = btn.getAttribute("data-id");
          if (editId) openDetail(host, editId);
          return;
        }
        if (act === "cancel-edit") {
          var cid = btn.getAttribute("data-id");
          if (cid) closeDetail(host, cid);
          return;
        }
        if (act === "save-edit") {
          var sid = btn.getAttribute("data-id");
          if (sid) saveEdit(host, sid, btn);
          return;
        }
        if (act === "copy") {
          var k = btn.getAttribute("data-key") || "";
          if (!k) return;
          copyText(k).then(function () { toast("已复制密钥", "success"); })
            .catch(function () { toast("复制失败", "danger"); });
          return;
        }
        var id = btn.getAttribute("data-id");
        if (!id) return;
        if (act === "del") {
          var row = btn.closest("tr");
          var nameCell = row ? row.children[3] : null;
          var prefixCell = row ? row.children[4] : null;
          var label = tokenLabel({
            id: id,
            name: nameCell ? nameCell.textContent : "",
            key_prefix: prefixCell ? prefixCell.textContent : ""
          });
          confirmAction({
            title: "删除令牌",
            message: "将永久删除令牌「" + label + "」。完整密钥不会显示在此对话框中。此操作无法撤销。",
            confirmLabel: "永久删除",
            cancelLabel: "取消",
            tone: "danger"
          }).then(function (ok) {
            if (!ok) return;
            return withButtonLoading(btn, function () {
              return api("/admin/tokens/" + encodeURIComponent(id), { method: "DELETE" }).then(function () {
                toast("已删除令牌", "success");
                return loadTokens({ soft: true });
              }).catch(function (e) {
                if (handleAuthError(e)) return;
                toast(e.message || "删除失败", "danger");
                throw e;
              });
            }, { loadingText: "删除中…" });
          });
          return;
        }
        var path;
        var okMsg = "已更新";
        var loadingText = "处理中…";
        if (act === "dis") {
          path = "/admin/tokens/" + encodeURIComponent(id) + "/disable";
          okMsg = "已禁用";
        } else if (act === "en") {
          path = "/admin/tokens/" + encodeURIComponent(id) + "/enable";
          okMsg = "已启用";
        } else {
          return;
        }
        withButtonLoading(btn, function () {
          return api(path, { method: "POST", body: {} }).then(function () {
            toast(okMsg, "success");
            return loadTokens({ soft: true });
          }).catch(function (e) {
            if (handleAuthError(e)) return;
            toast(e.message || "操作失败", "danger");
            throw e;
          });
        }, { loadingText: loadingText });
      });
    });
  }).catch(function (e) {
    if (handleAuthError(e)) return;
    var hadTable = !!(host && host.querySelector("table"));
    if (!hadTable) {
      host.innerHTML =
        '<div class="err-box">加载令牌失败：' + esc(e.message) + "</div>";
    }
    toast(hadTable ? "刷新失败，当前显示的可能是旧数据" : (e.message || "加载失败"), "danger");
    updateTokSelUI();
  });
}

export function renderTokens() {
  stopPoll();
  setAuthed(true);
  var d = window.__settings || {};
  var defQ = d.token_default_remain_quota != null ? d.token_default_remain_quota : 1000;
  var defC = d.token_default_max_concurrent != null ? d.token_default_max_concurrent : 5;
  var defR = d.token_default_rpm != null ? d.token_default_rpm : 0;
  var defU = d.token_default_unlimited ? "1" : "0";
  $("main").innerHTML = wrapPage(
    pageHd("令牌", "创建 / 编辑并发·RPM·额度 · 明文仅创建时显示一次 · PATCH 立即生效", "") +
    '<div class="panel token-create-panel"><div class="panel-title">快速创建</div>' +
    '<div class="form-row form-row-token">' +
    '<div class="field"><label for="tName">名称</label><input id="tName" class="input" value="client" /></div>' +
    '<div class="field"><label for="tCount">数量 (1-100)</label><input id="tCount" class="input" type="number" value="1" min="1" max="100" /></div>' +
    '<div class="field"><label for="tQuota">剩余额度</label><input id="tQuota" class="input" type="number" value="' + defQ + '" min="0" /></div>' +
    '<div class="field"><label for="tUnlim">无限额度</label><select id="tUnlim" class="input"><option value="0"' + (defU === "0" ? " selected" : "") + '>否</option><option value="1"' + (defU === "1" ? " selected" : "") + '>是</option></select></div>' +
    '<div class="field"><label for="tConc">令牌并发上限 (0=不限)</label><input id="tConc" class="input" type="number" value="' + defC + '" min="0" /></div>' +
    '<div class="field"><label for="tRpm">RPM (0=不限)</label><input id="tRpm" class="input" type="number" value="' + defR + '" min="0" /></div>' +
    "</div>" +
    '<p class="muted" style="margin:10px 0 0">并发=该密钥同时 in-flight 请求数硬顶；0=不限（仍受全局 max_concurrent）。创建后也可在列表点「编辑」改，保存立即生效。</p>' +
    '<div class="toolbar token-create-actions">' +
    '<button class="btn btn-primary" id="createBtn" type="button">创建并复制密钥</button>' +
    '<button class="page-action-btn" id="tokRefresh" type="button">刷新列表</button></div>' +
    '<div id="onceBox"></div></div>' +
    '<div class="section-head"><div class="section-title">令牌列表<span class="section-count-badge" id="tokCount">0</span></div></div>' +
    '<div class="toolbar acc-batch-bar" style="margin-bottom:8px">' +
    '<button type="button" class="btn btn-sm btn-secondary" id="tokSelectAll" disabled>全选</button>' +
    '<button type="button" class="btn btn-sm btn-secondary" id="tokSelectNone" disabled>清空</button>' +
    '<button type="button" class="btn btn-sm btn-secondary" id="tokCopySelected" disabled>批量复制密钥</button>' +
    '<button type="button" class="btn btn-sm btn-danger" id="tokBatchDelete" disabled>批量删除</button>' +
    '<span class="muted" id="tokSelCount">已选 0</span></div>' +
    '<div id="tokTable">' + skeletonRows(6, "加载令牌") + "</div>"
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
    withButtonLoading(btn, function () {
      return api("/admin/tokens", { method: "POST", body: body }).then(function (res) {
        var keys = extractPlainKeys(res);
        bindOnceBox(keys);
        if (keys[0]) {
          return copyText(keys.join("\n")).then(function () {
            toast("已创建并复制到剪贴板（并发=" + body.max_concurrent + "）", "success");
          }).catch(function () {
            toast("已创建 " + keys.length + " 把密钥（请手动复制）", "success");
          });
        }
        toast("已创建", "success");
      }).then(function () {
        return loadTokens({ soft: true });
      }).catch(function (e) {
        if (handleAuthError(e)) return;
        toast(e.message || "创建失败", "danger");
        throw e;
      });
    }, { loadingText: "创建中…" });
  });
  $("tokRefresh").addEventListener("click", loadTokens);
  $("tokSelectAll").addEventListener("click", function () {
    document.querySelectorAll("#tokTable input.tok-check").forEach(function (cb) { cb.checked = true; });
    var master = $("tokCheckAll");
    if (master) master.checked = true;
    updateTokSelUI();
  });
  $("tokSelectNone").addEventListener("click", function () {
    document.querySelectorAll("#tokTable input.tok-check").forEach(function (cb) { cb.checked = false; });
    var master = $("tokCheckAll");
    if (master) master.checked = false;
    updateTokSelUI();
  });
  $("tokCopySelected").addEventListener("click", function () {
    var keys = selectedTokenKeys();
    if (!keys.length) {
      toast("请先勾选有明文密钥的令牌（旧令牌可能无存盘）", "warning");
      return;
    }
    copyText(keys.join("\n")).then(function () {
      toast("已复制 " + keys.length + " 把密钥", "success");
    }).catch(function () {
      toast("复制失败，请手动展开复制", "danger");
    });
  });
  $("tokBatchDelete").addEventListener("click", function () {
    var ids = selectedTokenIds();
    if (!ids.length) {
      toast("请先勾选令牌", "warning");
      return;
    }
    confirmAction({
      title: "删除 " + ids.length + " 个令牌",
      message: "这些客户端令牌将被永久删除，持有者将立即无法调用 API。完整密钥不会显示。",
      confirmLabel: "永久删除",
      cancelLabel: "取消",
      tone: "danger",
      requiredText: "DELETE"
    }).then(function (ok) {
      if (!ok) return;
      var btn = $("tokBatchDelete");
      setControlsBusy(["tokBatchDelete", "tokSelectAll", "tokSelectNone", "tokCopySelected", "createBtn"], true);
      return withButtonLoading(btn, function () {
        var t0 = Date.now();
        return api("/admin/tokens/batch", {
          method: "POST",
          body: { action: "delete", ids: ids }
        }).then(function (res) {
          var okN = res && res.ok != null ? res.ok : 0;
          var failed = res && res.failed != null ? res.failed : (ids.length - okN);
          var msg = "批量删除：成功 " + okN + " 个";
          if (failed > 0) msg += "，失败 " + failed + " 个";
          msg += "（" + (Date.now() - t0) + "ms）";
          toast(msg, failed > 0 ? "warning" : "success");
          return loadTokens({ soft: true });
        }).catch(function (e) {
          if (handleAuthError(e)) return;
          toast(e.message || "批量删除失败", "danger");
          throw e;
        });
      }, { loadingText: "删除中…" }).then(function () {
        setControlsBusy(["tokBatchDelete", "tokSelectAll", "tokSelectNone", "tokCopySelected", "createBtn"], false);
        updateTokSelUI();
      });
    });
  });
  loadTokens();
}
