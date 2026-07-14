(function () {
  try {
    var t = localStorage.getItem("pool-admin-theme");
    if (t !== "dark" && t !== "light") t = "light";
    document.documentElement.setAttribute("data-theme", t);
    document.documentElement.classList.toggle("dark", t === "dark");
  } catch (_) {
    document.documentElement.setAttribute("data-theme", "light");
  }
})();
