/* Promise-based confirm dialog — zero deps, CSP-safe (no unsafe innerHTML of user data) */
import { prefersReducedMotion } from "./util.js";

var active = null; // { resolve, root, previousFocus, busy }

function focusableIn(root) {
  if (!root) return [];
  return Array.prototype.slice.call(
    root.querySelectorAll(
      'button:not([disabled]), [href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'
    )
  ).filter(function (el) {
    return el.offsetParent !== null || el === document.activeElement;
  });
}

function closeActive(result) {
  if (!active) return;
  if (active.busy && result !== true) {
    // 提交中禁止取消/Escape/背景关闭
    return;
  }
  var entry = active;
  active = null;
  document.body.classList.remove("dialog-open");
  document.removeEventListener("keydown", onKeyDown, true);
  if (entry.root && entry.root.parentNode) {
    entry.root.parentNode.removeChild(entry.root);
  }
  if (entry.previousFocus && typeof entry.previousFocus.focus === "function") {
    try { entry.previousFocus.focus(); } catch (_) { /* ignore */ }
  }
  entry.resolve(!!result);
}

function onKeyDown(e) {
  if (!active) return;
  if (e.key === "Escape") {
    e.preventDefault();
    e.stopPropagation();
    closeActive(false);
    return;
  }
  if (e.key !== "Tab") return;
  var list = focusableIn(active.panel);
  if (!list.length) {
    e.preventDefault();
    return;
  }
  var first = list[0];
  var last = list[list.length - 1];
  if (e.shiftKey) {
    if (document.activeElement === first || !active.panel.contains(document.activeElement)) {
      e.preventDefault();
      last.focus();
    }
  } else if (document.activeElement === last) {
    e.preventDefault();
    first.focus();
  }
}

/**
 * confirmAction(opts) → Promise<boolean>
 * opts: title, message, confirmLabel, cancelLabel, tone("danger"|"warning"|"neutral"), requiredText
 */
export function confirmAction(opts) {
  opts = opts || {};
  // 同一时间只允许一个
  if (active) {
    closeActive(false);
  }

  var title = opts.title != null ? String(opts.title) : "确认操作";
  var message = opts.message != null ? String(opts.message) : "确定继续吗？";
  var confirmLabel = opts.confirmLabel != null ? String(opts.confirmLabel) : "确认";
  var cancelLabel = opts.cancelLabel != null ? String(opts.cancelLabel) : "取消";
  var tone = opts.tone === "danger" || opts.tone === "warning" ? opts.tone : "neutral";
  var requiredText = opts.requiredText != null ? String(opts.requiredText) : "";

  var previousFocus = document.activeElement;

  return new Promise(function (resolve) {
    var root = document.createElement("div");
    root.className = "confirm-root";
    root.setAttribute("data-tone", tone);

    var backdrop = document.createElement("div");
    backdrop.className = "confirm-backdrop";
    backdrop.tabIndex = -1;

    var panel = document.createElement("div");
    panel.className = "confirm-panel";
    panel.setAttribute("role", "dialog");
    panel.setAttribute("aria-modal", "true");
    panel.setAttribute("aria-labelledby", "confirmTitle");
    panel.setAttribute("aria-describedby", "confirmDesc");

    var head = document.createElement("div");
    head.className = "confirm-head";

    var h = document.createElement("h2");
    h.id = "confirmTitle";
    h.className = "confirm-title";
    h.textContent = title;

    var closeBtn = document.createElement("button");
    closeBtn.type = "button";
    closeBtn.className = "btn btn--icon btn--ghost confirm-close";
    closeBtn.setAttribute("aria-label", "关闭");
    closeBtn.textContent = "×";

    head.appendChild(h);
    head.appendChild(closeBtn);

    var body = document.createElement("div");
    body.className = "confirm-body";

    var desc = document.createElement("p");
    desc.id = "confirmDesc";
    desc.className = "confirm-message";
    desc.textContent = message;
    body.appendChild(desc);

    if (tone === "danger") {
      var warn = document.createElement("p");
      warn.className = "confirm-warn";
      warn.textContent = "⚠ 危险操作：请确认后再继续。";
      body.appendChild(warn);
    }

    var input = null;
    var hint = null;
    if (requiredText) {
      hint = document.createElement("label");
      hint.className = "confirm-required-label";
      hint.htmlFor = "confirmRequiredInput";
      hint.textContent = "请输入 " + requiredText + " 以确认：";
      body.appendChild(hint);

      input = document.createElement("input");
      input.id = "confirmRequiredInput";
      input.className = "input confirm-required-input";
      input.type = "text";
      input.autocomplete = "off";
      input.spellcheck = false;
      input.setAttribute("aria-required", "true");
      body.appendChild(input);
    }

    var foot = document.createElement("div");
    foot.className = "confirm-foot";

    var cancelBtn = document.createElement("button");
    cancelBtn.type = "button";
    cancelBtn.className = "btn btn-secondary";
    cancelBtn.textContent = cancelLabel;

    var okBtn = document.createElement("button");
    okBtn.type = "button";
    okBtn.className = tone === "danger" ? "btn btn-danger" : "btn btn-primary";
    okBtn.textContent = confirmLabel;
    if (requiredText) {
      okBtn.disabled = true;
    }

    foot.appendChild(cancelBtn);
    foot.appendChild(okBtn);

    panel.appendChild(head);
    panel.appendChild(body);
    panel.appendChild(foot);
    root.appendChild(backdrop);
    root.appendChild(panel);
    document.body.appendChild(root);
    document.body.classList.add("dialog-open");

    active = {
      resolve: resolve,
      root: root,
      panel: panel,
      previousFocus: previousFocus,
      busy: false
    };

    function syncRequired() {
      if (!requiredText || !input) return;
      okBtn.disabled = input.value !== requiredText;
    }

    cancelBtn.addEventListener("click", function () { closeActive(false); });
    closeBtn.addEventListener("click", function () { closeActive(false); });
    backdrop.addEventListener("click", function () { closeActive(false); });
    okBtn.addEventListener("click", function () {
      if (requiredText && input && input.value !== requiredText) return;
      closeActive(true);
    });
    if (input) {
      input.addEventListener("input", syncRequired);
      input.addEventListener("keydown", function (e) {
        if (e.key === "Enter") {
          e.preventDefault();
          if (!okBtn.disabled) closeActive(true);
        }
      });
    }

    document.addEventListener("keydown", onKeyDown, true);

    window.requestAnimationFrame(function () {
      if (!prefersReducedMotion()) {
        root.classList.add("is-open");
      } else {
        root.classList.add("is-open", "no-anim");
      }
      if (input) input.focus();
      else okBtn.focus();
    });
  });
}

/** 提交中锁定对话框（禁止 Escape/背景关闭） */
export function setConfirmBusy(busy) {
  if (!active) return;
  active.busy = !!busy;
  if (!active.panel) return;
  active.panel.querySelectorAll("button, input").forEach(function (el) {
    if (busy) {
      el.setAttribute("data-was-disabled", el.disabled ? "1" : "0");
      el.disabled = true;
    } else {
      var was = el.getAttribute("data-was-disabled");
      if (was === "0") el.disabled = false;
      el.removeAttribute("data-was-disabled");
    }
  });
}
