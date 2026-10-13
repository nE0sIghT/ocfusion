// SPDX-License-Identifier: GPL-3.0-or-later
(function () {
  "use strict";

  if (window.OCF_HEADER_LOADED) {
    return;
  }

  window.OCF_HEADER_LOADED = true;

  function insertIcon() {
    const target = document.getElementById("status_block");

    if (!target || document.getElementById("asus_extensions_link")) {
      return;
    }

    const link = document.createElement("a");
    link.id = "asus_extensions_link";
    link.href = "/user/extensions.html";
    link.title = "OCFusion";
    link.setAttribute("aria-label", "OCFusion");
    link.style.cssText = "display:flex;align-items:center;justify-content:center;width:30px;height:30px;margin-right:16px;flex-shrink:0";

    const icon = document.createElement("img");
    icon.src = "/user/ocfusion-icon.png";
    icon.width = 28;
    icon.height = 28;
    icon.alt = "";
    icon.style.objectFit = "contain";
    link.appendChild(icon);

    target.insertBefore(link, target.firstChild);
  }

  function start() {
    insertIcon();

    const observer = new MutationObserver(insertIcon);
    observer.observe(document.documentElement, {
      childList: true,
      subtree: true,
    });

    const script = document.createElement("script");
    script.src = "/user/ocfusion-contributions.js";
    document.head.appendChild(script);
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", start, { once: true });
  } else {
    start();
  }
})();
