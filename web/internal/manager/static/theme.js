"use strict";

(() => {
  let theme = "light";
  try {
    if (localStorage.getItem("mordhau-control-theme") === "dark") {
      theme = "dark";
    }
  } catch (_) {}
  document.documentElement.dataset.theme = theme;
})();
