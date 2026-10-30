// Runs before the first paint: picks the saved theme, else the system one.
(function () {
  var saved = null;
  try { saved = localStorage.getItem("runova.theme"); } catch (e) { /* storage blocked */ }
  var dark = saved ? saved === "dark" : matchMedia("(prefers-color-scheme: dark)").matches;
  document.documentElement.dataset.theme = dark ? "dark" : "light";
})();
