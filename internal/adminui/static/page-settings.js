/* Settings designer — save updates hint+snapshot only, not full form */
import { state } from "./state.js";
import { $, esc, toast, prefersReducedMotion } from "./util.js";
import { api, handleAuthError } from "./api.js";
import { setAuthed, stopPoll, wrapPage, pageHd } from "./shell.js";
import { field, fieldText, fieldSelect, fieldBool, fieldArea } from "./fields.js";

export function renderSettings() {
  stopPoll();
  setAuthed(true);
  var SECTIONS = [
    { id: "sel", title: "选号 / 热池" },
    { id: "lease", title: "租约 / 冷却" },
    { id: "http", title: "进程 / HTTP" },
    { id: "refresh", title: "Token 刷新" },
    { id: "token", title: "令牌模板" },
    { id: "import", title: "导入 / SSO" },
    { id: "anthropic", title: "Anthropic" },
    { id: "deploy", title: "部署 / 密钥" },
    { id: "json", title: "JSON 快照" }
  ];
  var subnav = '<nav class="settings-subnav" aria-label="设置分组">' +
    SECTIONS.map(function (s) {
      return '<button type="button" class="settings-subnav-btn" data-jump="' + s.id + '">' +
        esc(s.title) + "</button>";
    }).join("") + "</nav>";
  $("main").innerHTML = wrapPage(
    pageHd("参数设计器", "手动「保存并应用」后写入；多数项即时热更 · 标注「需重启」的项不会自动重启进程 · 密钥留空表示不修改",
      '<button type="button" class="page-action-btn" id="reloadSet">重新加载</button>' +
      '<button type="button" class="page-action-btn-primary" id="saveSet">保存并应用</button>') +
    subnav +
    '<div id="setHint"></div>' +
    '<div id="setErr"></div>' +
    '<div id="setForm" class="panel-stack"></div>' +
    '<div class="panel settings-section" id="set-sec-json">' +
    '<div class="panel-title-row">' +
    '<div class="panel-title">JSON 快照</div>' +
    '<button type="button" class="btn btn-sm btn-secondary" id="toggleJson">收起</button></div>' +
    '<pre id="setPreview" class="mono muted set-json-pre"></pre></div>'
  );

  document.querySelectorAll(".settings-subnav-btn").forEach(function (btn) {
    btn.addEventListener("click", function () {
      var id = btn.getAttribute("data-jump");
      var el = $("set-sec-" + id);
      if (el) {
        var smooth = prefersReducedMotion() ? "auto" : "smooth";
        el.scrollIntoView({ behavior: smooth, block: "start" });
      }
      document.querySelectorAll(".settings-subnav-btn").forEach(function (b) {
        b.classList.toggle("is-active", b === btn);
      });
    });
  });
  $("toggleJson").addEventListener("click", function () {
    var pre = $("setPreview");
    var btn = $("toggleJson");
    if (!pre || !btn) return;
    var hide = !pre.classList.contains("is-collapsed");
    pre.classList.toggle("is-collapsed", hide);
    btn.textContent = hide ? "展开" : "收起";
  });

  function section(id, title, note, fieldsHtml) {
    return '<div class="panel settings-section" id="set-sec-' + id + '">' +
      '<div class="section-title panel-section-title">' + title + "</div>" +
      (note ? '<p class="muted panel-note">' + note + "</p>" : "") +
      '<div class="form-row form-row-dense">' + fieldsHtml + "</div></div>";
  }

  function aliasesToText(map) {
    if (!map || typeof map !== "object") return "";
    return Object.keys(map).sort().map(function (k) {
      return k + " = " + map[k];
    }).join("\n");
  }
  function textToAliases(text) {
    var out = {};
    String(text || "").split(/\r?\n/).forEach(function (line) {
      line = line.trim();
      if (!line || line.charAt(0) === "#") return;
      var m = line.split(/\s*=\s*|\s*:\s*|\s+/);
      if (m.length >= 2) {
        var k = m[0].trim();
        var v = m.slice(1).join(" ").trim();
        if (k && v) out[k] = v;
      }
    });
    return out;
  }
  function prefixesToText(arr) {
    return (arr || []).join(", ");
  }
  function textToPrefixes(text) {
    return String(text || "").split(/[,\n]/).map(function (s) { return s.trim(); }).filter(Boolean);
  }

  function flashEl(el, cls) {
    if (!el) return;
    el.classList.remove(cls);
    void el.offsetWidth;
    el.classList.add(cls);
    window.setTimeout(function () {
      el.classList.remove(cls);
    }, 700);
  }

  function applyMeta(s, opts) {
    opts = opts || {};
    s = s || {};
    window.__settings = s;
    var path = s.persisted_path || "（内存）";
    var hint = $("setHint");
    if (hint) {
      if (s.restart_hint) {
        hint.innerHTML = '<div class="err-box" style="border-color:var(--color-warning,#c90)">' +
          esc(s.restart_hint) +
          '<div class="muted" style="margin-top:6px">提示：管理台不会自动重启服务；请在维护窗口手动重启容器/进程。</div></div>';
      } else if (opts.saved) {
        hint.innerHTML = '<div class="ok-box set-save-ok">' +
          esc(opts.persisted ? "已保存并热更新（无需重启）" : "已热更新（无需重启）") +
          '<div class="muted" style="margin-top:6px">持久化：' + esc(path) +
          " · 表单未重载 · 密钥留空=不改</div></div>";
      } else {
        hint.innerHTML = '<p class="muted">持久化：' + esc(path) +
          " · 点「保存并应用」才会写入 · 无自动保存 · 密钥 / SSO key 留空=不改</p>";
      }
      if (opts.flash) flashEl(hint, "set-meta-flash");
    }
    var pre = $("setPreview");
    if (pre) {
      pre.textContent = JSON.stringify(s, null, 2);
      if (opts.flash) flashEl(pre, "set-json-flash");
    }
    var err = $("setErr");
    if (err) err.innerHTML = "";

    if (opts.clearSecrets) {
      var ssoKey = $("sSsoKey");
      if (ssoKey) {
        ssoKey.value = "";
        ssoKey.placeholder = s.import_sso_api_key_set ? "已配置 · 留空保持" : "未配置";
      }
      var apiKeyEl = $("sApiKey");
      if (apiKeyEl) {
        apiKeyEl.value = "";
        apiKeyEl.placeholder = s.api_key_configured ? "已配置" : "未配置";
      }
      var admKey = $("sAdmKey");
      if (admKey) {
        admKey.value = "";
        admKey.placeholder = s.admin_key_configured ? "已配置" : "未配置";
      }
    }
  }

  function load() {
    api("/admin/settings").then(function (s) {
      if (s && s.settings) s = Object.assign({}, s.settings, { persisted_path: s.persisted_path });
      s = s || {};
      var html = "";
      html += section("sel", "选号 / 热池", "策略/权重/粘性即时生效；热池大小变更仅落盘，需重启后扩容",
        fieldSelect("策略", "sStrat", s.selector_strategy || "pow2_least_load", [
          { v: "pow2_least_load", l: "pow2_least_load" },
          { v: "sticky", l: "sticky" },
          { v: "random", l: "random" }
        ]) +
        field("热池大小（需重启）", "sHot", s.hot_size) +
        field("单账号最大并发", "sMaxInf", s.max_inflight_per_account) +
        field("粘性 TTL 秒", "sSticky", s.sticky_ttl_sec) +
        field("粘性 LRU 容量", "sStickyMax", s.sticky_max) +
        field("Pow2 K", "sPow2", s.pow2_k) +
        field("选号 failover", "sSelAtt", s.selector_max_attempts) +
        field("权重·优先级", "sWP", s.w_priority) +
        field("权重·inflight", "sWI", s.w_inflight) +
        field("权重·失败", "sWF", s.w_failure) +
        field("抖动幅度", "sJit", s.jitter_amp)
      );
      html += section("lease", "租约 / 防封号冷却", "429 指数退避；401/402/403 冷却与隔离",
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
      html += section("http", "进程限制 / HTTP", "全局并发、Body、超时立即生效",
        field("全局最大并发", "sGlob", s.max_concurrent) +
        field("最大 Body 字节", "sBody", s.max_body_bytes) +
        field("请求超时秒", "sTO", s.request_timeout_sec) +
        fieldSelect("日志级别", "sLog", s.logging_level || "info", [
          { v: "debug", l: "debug" }, { v: "info", l: "info" },
          { v: "warn", l: "warn" }, { v: "error", l: "error" }
        ])
      );
      html += section("refresh", "Token 刷新 workers", "QPS / Skew 保存后即时生效；Workers 数量仅落盘，需重启后调整",
        field("Workers（需重启）", "sRW", s.refresh_workers) +
        field("Refresh QPS", "sRQ", s.refresh_qps) +
        field("Skew 秒", "sRS", s.refresh_skew_sec)
      );
      html += section("token", "令牌创建默认模板", "仅影响管理台创建表单默认值",
        field("默认额度", "sTQ", s.token_default_remain_quota) +
        field("默认并发", "sTC", s.token_default_max_concurrent) +
        field("默认 RPM", "sTR", s.token_default_rpm) +
        fieldBool("默认无限额度", "sTU", !!s.token_default_unlimited)
      );
      html += section("import", "导入 / SSO 转换", "限制热更；填 SSO endpoint+key 可热重建转换器",
        fieldBool("启用导入", "sImpEn", !!s.import_enabled) +
        field("最大上传字节(0=不限)", "sImpUp", s.import_max_upload_bytes) +
        field("最大条目(默认1万)", "sImpEnt", s.import_max_entries) +
        field("并发任务数", "sImpJobs", s.import_max_concurrent_jobs) +
        field("解析 workers", "sImpW", s.import_workers) +
        field("NDJSON 行上限", "sImpNd", s.import_max_ndjson_line_bytes) +
        field("SSO 值上限", "sImpSsoB", s.import_max_sso_value_bytes) +
        field("任务超时秒", "sImpTO", s.import_job_timeout_sec) +
        field("Staging 过期秒", "sImpStale", s.import_staging_stale_after_sec) +
        fieldBool("允许服务端路径", "sImpPath", !!s.import_allow_server_path) +
        fieldText("SSO Endpoint", "sSsoEp", s.import_sso_endpoint || "", "https://…/v1/convert") +
        fieldText("SSO API Key(留空不改)", "sSsoKey", "", s.import_sso_api_key_set ? "已配置 · 留空保持" : "未配置") +
        field("SSO max_batch", "sSsoBatch", s.import_sso_max_batch) +
        field("SSO timeout 秒", "sSsoTO", s.import_sso_timeout_sec) +
        field("SSO workers", "sSsoW", s.import_sso_workers) +
        fieldBool("SSO allow_insecure", "sSsoInsec", !!s.import_sso_allow_insecure)
      );
      html += section("anthropic", "Anthropic / 模型别名", "别名每行：claude-sonnet-4 = grok-4.5",
        fieldBool("启用 Anthropic", "sAnEn", !!s.anthropic_enabled) +
        fieldBool("剥离未知 betas", "sAnStrip", !!s.anthropic_strip_unknown_betas) +
        fieldBool("count_tokens", "sAnCnt", !!s.anthropic_count_tokens) +
        fieldText("透传前缀(逗号分隔)", "sAnPre", prefixesToText(s.anthropic_passthrough_prefixes), "grok-") +
        fieldArea("模型别名映射", "sAnMap", aliasesToText(s.anthropic_model_aliases), 10)
      );
      html += section("deploy", "部署 / 上游 / 密钥", "listen/data_dir/upstream 等保存后仅落盘，需手动重启进程；始终反代真实 upstream；密钥留空不改",
        fieldText("Listen", "sListen", s.listen || "") +
        fieldBool("Allow public listen", "sPub", !!s.allow_public_listen) +
        fieldText("Data dir", "sData", s.data_dir || "") +
        fieldText("DB path", "sDB", s.db_path || "") +
        fieldText("Upstream base URL（需重启）", "sUp", s.upstream_base_url || "", "https://…/v1") +
        fieldText("OAuth refresh URL", "sOAuth", s.oauth_refresh_url || "") +
        fieldText("OAuth client_id", "sOAuthCID", s.oauth_client_id || "") +
        fieldText("API Key(留空不改)", "sApiKey", "", s.api_key_configured ? "已配置" : "未配置") +
        fieldText("Admin Key(留空不改)", "sAdmKey", "", s.admin_key_configured ? "已配置" : "未配置")
      );
      $("setForm").innerHTML = html;
      applyMeta(s, { flash: false });
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
    var n = parseFloat(el.value);
    return isNaN(n) ? 0 : n;
  }
  function numI(id) {
    var el = $(id);
    if (!el) return 0;
    return parseInt(el.value, 10) || 0;
  }
  function str(id) {
    var el = $(id);
    return el ? String(el.value || "").trim() : "";
  }
  function bool(id) {
    return str(id) === "1";
  }

  $("saveSet").addEventListener("click", function () {
    var body = {
      selector_strategy: str("sStrat"),
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
      logging_level: str("sLog"),
      refresh_workers: numI("sRW"),
      refresh_qps: num("sRQ"),
      refresh_skew_sec: numI("sRS"),
      token_default_remain_quota: numI("sTQ"),
      token_default_max_concurrent: numI("sTC"),
      token_default_rpm: numI("sTR"),
      token_default_unlimited: bool("sTU"),
      import_enabled: bool("sImpEn"),
      import_max_upload_bytes: numI("sImpUp"),
      import_max_entries: numI("sImpEnt"),
      import_max_concurrent_jobs: numI("sImpJobs"),
      import_workers: numI("sImpW"),
      import_max_ndjson_line_bytes: numI("sImpNd"),
      import_max_sso_value_bytes: numI("sImpSsoB"),
      import_job_timeout_sec: numI("sImpTO"),
      import_staging_stale_after_sec: numI("sImpStale"),
      import_allow_server_path: bool("sImpPath"),
      import_sso_endpoint: str("sSsoEp"),
      import_sso_max_batch: numI("sSsoBatch"),
      import_sso_timeout_sec: numI("sSsoTO"),
      import_sso_workers: numI("sSsoW"),
      import_sso_allow_insecure: bool("sSsoInsec"),
      anthropic_enabled: bool("sAnEn"),
      anthropic_strip_unknown_betas: bool("sAnStrip"),
      anthropic_count_tokens: bool("sAnCnt"),
      anthropic_passthrough_prefixes: textToPrefixes(str("sAnPre")),
      anthropic_model_aliases: textToAliases(($("sAnMap") && $("sAnMap").value) || ""),
      listen: str("sListen"),
      allow_public_listen: bool("sPub"),
      data_dir: str("sData"),
      db_path: str("sDB"),
      upstream_base_url: str("sUp"),
      oauth_refresh_url: str("sOAuth"),
      oauth_client_id: str("sOAuthCID")
    };
    var ssoKey = str("sSsoKey");
    if (ssoKey) body.import_sso_api_key = ssoKey;
    var apiKey = str("sApiKey");
    if (apiKey) body.api_key = apiKey;
    var admKey = str("sAdmKey");
    if (admKey) body.admin_key = admKey;

    var btn = $("saveSet");
    if (btn) {
      btn.disabled = true;
      btn.classList.add("is-busy");
    }
    api("/admin/settings", { method: "PUT", body: body }).then(function (res) {
      var s = (res && res.settings) || body;
      if (s && !s.persisted_path && window.__settings && window.__settings.persisted_path) {
        s = Object.assign({}, s, { persisted_path: window.__settings.persisted_path });
      }
      var persisted = !!(res && res.persisted);
      var restartHint = (s && s.restart_hint) || "";
      if (restartHint) {
        toast((persisted ? "已保存。" : "已应用。") + restartHint, false);
      } else {
        toast(persisted ? "已保存并热更新（无需重启）" : "已热更新（无需重启）", true);
      }
      applyMeta(s, {
        saved: true,
        persisted: persisted,
        flash: true,
        clearSecrets: true
      });
    }).catch(function (e) {
      if (handleAuthError(e)) return;
      toast(e.message || "保存失败", false);
    }).then(function () {
      if (btn) {
        btn.disabled = false;
        btn.classList.remove("is-busy");
      }
    });
  });
  $("reloadSet").addEventListener("click", load);
  load();
}
