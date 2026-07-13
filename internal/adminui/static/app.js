/* 管理台入口（ES modules，零构建）
 * - admin_key 仅存内存（state.adminKey），绝不写入 localStorage / sessionStorage
 * - 主题偏好键：localStorage["pool-admin-theme"] = "dark" | "light"
 * - 无 HTML inline 事件；全部 addEventListener（CSP script-src 'self'）
 * - 危险删除经 dialog.confirmAction；结果用 toast（success/warning/danger）
 */
import { state } from "./state.js";
import { $, toast } from "./util.js";
import { setUnauthHandler } from "./api.js";
import {
  themeInit,
  toggleTheme,
  bindMobileMenu,
  setAuthed,
  setMobileMenu,
  stopPoll,
  swapMain
} from "./shell.js";
import { renderLogin } from "./page-login.js";
import { renderDashboard } from "./page-dashboard.js";
import { renderSettings } from "./page-settings.js";
import { renderTokens } from "./page-tokens.js";
import { renderAccounts } from "./page-accounts.js";
import { renderImportJobs } from "./page-imports.js";

function syncNav(page) {
  document.querySelectorAll(".admin-nav-link, .mobile-nav a").forEach(function (a) {
    var current = a.getAttribute("data-route") === page;
    a.classList.toggle("active", current);
    if (current) a.setAttribute("aria-current", "page");
    else a.removeAttribute("aria-current");
  });
}

function dispatchPage(page) {
  if (page === "login") return renderLogin();
  if (page === "dashboard") return renderDashboard();
  if (page === "accounts") return renderAccounts();
  if (page === "tokens") return renderTokens();
  if (page === "imports") return renderImportJobs();
  if (page === "settings") return renderSettings();
  location.hash = "#/login";
  return renderLogin();
}

function route() {
  var hash = (location.hash || "#/login").replace(/^#\/?/, "");
  var page = hash.split("?")[0] || "login";

  if (page === "config") {
    location.hash = "#/settings";
    return;
  }

  if (!state.adminKey && page !== "login") {
    location.hash = "#/login";
    // 未登录强制登录：仍走 leave/enter（若有旧页）
    var forceLogin = state.currentPage !== "login";
    state.currentPage = "login";
    setMobileMenu(false, false);
    state.dashBuilt = false;
    syncNav("login");
    swapMain(function () { renderLogin(); }, { animate: forceLogin, resetScroll: forceLogin });
    return;
  }

  var pageChanged = state.currentPage !== page;
  // 仪表盘同页软刷新：不重建、不 leave
  if (page === "dashboard" && state.dashBuilt && $("kpis") && !pageChanged) {
    syncNav(page);
    dispatchPage(page);
    return;
  }

  state.currentPage = page;
  setMobileMenu(false, false);
  if (page !== "dashboard") state.dashBuilt = false;
  syncNav(page);

  swapMain(function () {
    dispatchPage(page);
  }, {
    animate: pageChanged,
    resetScroll: pageChanged
  });
}

setUnauthHandler(function () {
  setAuthed(false);
  stopPoll();
});

$("themeToggle").addEventListener("click", toggleTheme);
$("logoutBtn").addEventListener("click", function () {
  state.adminKey = null;
  stopPoll();
  setAuthed(false);
  location.hash = "#/login";
  toast("已退出", true);
});
window.addEventListener("hashchange", route);

themeInit();
bindMobileMenu();
route();
