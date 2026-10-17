// SPDX-License-Identifier: GPL-3.0-or-later
"use strict";

(() => {
  if (
    location.pathname !== "/Advanced_Wireless_Content.asp" ||
    window.OCF_RADIO_CHANNELS_LOADED
  ) {
    return;
  }

  window.OCF_RADIO_CHANNELS_LOADED = true;
  let scheduled = false;
  const changed = new WeakSet();

  document.addEventListener("change", (event) => {
    if (event.target instanceof HTMLSelectElement) {
      changed.add(event.target);
    }
  });

  function refresh() {
    scheduled = false;

    // Stock scripts may still be loading. Keep this a best-effort contribution.
    try {
      if (
        typeof systemManipulable === "undefined" ||
        typeof generateControlChannel !== "function"
      ) {
        return;
      }

      for (const prefix of ["6g1", "6g2"]) {
        const radio = systemManipulable.wlBandSeq?.[prefix];
        const select = document.getElementById(prefix + "_control_channel");
        if (!radio || !select || !Array.isArray(radio.channel)) {
          continue;
        }

        // Retain the stock channel set, including hardware and Mesh restrictions.
        // Only undo the extra SKU-based truncation of its PSC subset.
        const channels = radio.channel
          .map(String)
          .filter((channel) => /^\d+$/.test(channel))
          .filter((channel) => {
            const value = Number(channel);
            return value >= 5 && value <= 229 && (value - 5) % 16 === 0;
          });
        if (
          !channels.length ||
          JSON.stringify(channels) === JSON.stringify(radio.pscChannel)
        ) {
          continue;
        }

        const selected = select.value;
        radio.pscChannel = channels;

        // Use the stock renderer without invoking its change handlers. Those
        // also update the model and the separate 320 MHz range selector.
        const template = document.createElement("template");
        template.innerHTML = generateControlChannel(prefix);
        const rendered = template.content.querySelector("select");
        if (!rendered || rendered.id !== select.id) {
          continue;
        }

        const value = changed.has(select) ? selected : rendered.value;
        select.replaceChildren(...rendered.options);
        if ([...select.options].some((option) => option.value === value)) {
          select.value = value;
        }
      }
    } catch {
      // An unavailable stock API must not break the native settings page.
    }
  }

  function schedule() {
    if (!scheduled) {
      scheduled = true;
      requestAnimationFrame(refresh);
    }
  }

  new MutationObserver(schedule).observe(document.documentElement, {
    childList: true,
    subtree: true,
  });
  window.addEventListener("load", schedule, { once: true });
  schedule();
})();
