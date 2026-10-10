// SPDX-License-Identifier: GPL-3.0-or-later
"use strict";

(() => {
  if (
    !/\/VPN\/vpnc\.html$|Advanced_VPNClient_Content\.asp$/.test(
      location.pathname,
    )
  ) {
    return;
  }

  const ru = typeof ui_lang !== "undefined" && ui_lang === "RU";
  const message = ru
    ? "Управляется OCFusion. Оставьте выключенным."
    : "Managed by OCFusion. Keep OFF.";

  function mark() {
    document.querySelectorAll(".svr_item_container").forEach((row) => {
      const title = row.querySelector(".main_info_text");

      if (
        !title ||
        !["OCFusion (managed)", "OpenConnect (SSH)"].includes(
          title.textContent.trim(),
        ) ||
        row.querySelector(".ocfusion-managed-note")
      ) {
        return;
      }

      const note = document.createElement("div");

      note.className = "ocfusion-managed-note";
      note.style.cssText =
        "font-size:12px;line-height:1.4;color:#ffa343;white-space:normal;margin-top:6px";
      note.textContent = message;

      const link = document.createElement("a");

      link.href = "/user/extensions.html#modules";
      link.target = "_top";
      link.textContent = ru ? "Настройки →" : "Settings →";
      link.style.cssText = "display:block;color:#ffa343;margin-top:4px";
      note.appendChild(link);
      note.addEventListener("click", (e) => e.stopPropagation());

      const text = row.querySelector(".svr_item_text_container");

      text.appendChild(note);
      text.style.height = "auto";
      row.style.height = "auto";
      row.style.minHeight = "80px";
      row.style.padding = "12px 0";
      row.title = message;

      if (typeof resize_iframe_height === "function") {
        resize_iframe_height();
      }
    });
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", mark);
  } else {
    mark();
  }

  new MutationObserver(mark).observe(document.documentElement, {
    childList: true,
    subtree: true,
  });
})();
