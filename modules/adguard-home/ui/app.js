// SPDX-License-Identifier: GPL-3.0-or-later
"use strict";

const moduleID = "adguard-home";
const fields = document.getElementById("fields");
const result = document.getElementById("result");
let settings = null;
let pending = false;
let catalog = [];
let dirty = false;
let lastStatistics = 0;

function lines(id) {
  return document
    .getElementById(id)
    .value.split(/\r?\n/)
    .map((line) => line.trim())
    .filter(Boolean);
}

async function loadSettings() {
  if (pending) {
    return;
  }

  setPending(true);
  fields.disabled = true;

  try {
    settings = await parent.EXTENSIONS.action(moduleID, "settings");
    catalog = await parent.EXTENSIONS.action(moduleID, "catalog");
    renderCatalog();
    document.getElementById("enabled").checked = settings.enabled;
    document.getElementById("query-log").checked = settings.query_log;
    document.getElementById("upstreams").value = (settings.upstreams || [])
      .map(
        (upstream) =>
          upstream.url + (upstream.address ? " " + upstream.address : ""),
      )
      .join("\n");
    document.getElementById("filters").value = (settings.filters || [])
      .filter((url) => !catalog.some((source) => source.url === url))
      .join("\n");
    document.getElementById("rules").value = (settings.rules || []).join("\n");
    document.getElementById("clients").value = (settings.clients || [])
      .map((client) => client.address + " " + (client.filtering ? "on" : "off"))
      .join("\n");
    result.textContent = "";
    dirty = false;
  } catch (error) {
    result.textContent = error.message;
  } finally {
    setPending(false);
    fields.disabled = settings === null;
  }
}

async function updateStatus() {
  try {
    const snapshot = await parent.EXTENSIONS.status(moduleID);
    const data = snapshot.data;
    document.getElementById("state").textContent = data
      ? {
          RUNNING: "Protection enabled",
          DISABLED: "Protection disabled",
          WAIT_PROVIDER: "Waiting for DNS service",
          ERROR: "Protection unavailable",
          CONFIG_ERROR: "Check settings",
        }[data.state] || data.state
      : "Service unavailable";
    if (data?.state === "RUNNING" && data.lists_ready === false) {
      document.getElementById("state").textContent = data.list_update_failed
        ? "List download failed"
        : "Loading filter lists…";
    }

    if (
      data?.state === "RUNNING" &&
      !pending &&
      !document.getElementById("panel-protection").hidden &&
      Date.now() - lastStatistics >= 15000
    ) {
      refreshStatistics();
    }
    document.getElementById("dns-path").textContent =
      data?.dns_path === "VPN"
        ? "External DNS queries: through VPN"
        : data?.dns_path === "WAN"
          ? "External DNS queries: through your ISP"
          : "External DNS route unavailable";
  } catch (error) {
    document.getElementById("state").textContent = error.message;
    document.getElementById("dns-path").textContent =
      "External DNS route unavailable";
  }
}

document
  .getElementById("settings")
  .addEventListener("submit", async (event) => {
    event.preventDefault();

    if (pending || settings === null) {
      return;
    }

    setPending(true);
    fields.disabled = true;

    try {
      const config = {
        revision: (settings.revision || 0) + 1,
        enabled: document.getElementById("enabled").checked,
        query_log: document.getElementById("query-log").checked,
        upstreams: lines("upstreams").map((line) => {
          const parts = line.split(/\s+/);
          if (parts.length > 2) {
            throw Error("Invalid upstream entry");
          }

          return { url: parts[0], address: parts[1] || "" };
        }),
        filters: [
          ...new Set([
            ...[...document.querySelectorAll("#catalog input:checked")].map(
              (input) => input.value,
            ),
            ...lines("filters"),
          ]),
        ],
        rules: lines("rules"),
        clients: lines("clients").map((line) => {
          const parts = line.split(/\s+/);
          if (parts.length !== 2 || !["on", "off"].includes(parts[1])) {
            throw Error("Client override requires an IP or CIDR and on or off");
          }

          return { address: parts[0], filtering: parts[1] === "on" };
        }),
      };

      await parent.EXTENSIONS.configure(moduleID, config);
      settings = config;
      dirty = false;
      result.textContent = "Settings saved.";
      setTimeout(() => {
        if (
          result.textContent === "Settings saved." ||
          result.textContent === localize("Settings saved.")
        ) {
          result.textContent = "";
        }
      }, 4000);
    } catch (error) {
      result.textContent = error.message;
    } finally {
      setPending(false);
      fields.disabled = false;
    }
  });

document.getElementById("reload").addEventListener("click", () => {
  if (!dirty || confirm(localize("Discard unsaved changes?"))) {
    loadSettings();
  }
});
async function refreshStatistics() {
  if (pending) {
    return;
  }

  setPending(true);
  lastStatistics = Date.now();

  try {
    const data = await parent.EXTENSIONS.action(moduleID, "statistics");
    document.getElementById("queries").textContent = String(
      data.num_dns_queries || 0,
    );
    document.getElementById("blocked").textContent = String(
      data.num_blocked_filtering || 0,
    );
    document.getElementById("response-time").textContent =
      ((data.avg_processing_time || 0) * 1000).toFixed(1) + " ms";
  } catch (error) {
    result.textContent = error.message;
  } finally {
    setPending(false);
  }
}

document.getElementById("stats").addEventListener("click", refreshStatistics);

loadSettings();
updateStatus();
setInterval(updateStatus, 3000);
setInterval(updateFilters, 5000);

function localize(text) {
  return window.OCF_I18N
    ? window.OCF_I18N.translate(text, window.OCF_I18N.language)
    : text;
}

function renderCatalog() {
  const container = document.getElementById("catalog");
  container.replaceChildren();

  for (const source of catalog) {
    const row = document.createElement("div");
    row.className = "agh-list";
    const label = document.createElement("label");
    label.className = "ext-check";
    const input = document.createElement("input");
    input.type = "checkbox";
    input.value = source.url;
    input.checked = (settings.filters || []).includes(source.url);
    label.append(input, document.createTextNode(source.name));
    if (source.recommended) {
      const badge = document.createElement("span");
      badge.className = "ext-badge";
      badge.textContent = "Recommended";
      label.append(badge);
    }
    const description = document.createElement("p");
    description.textContent = source.description;
    const status = document.createElement("p");
    status.className = "agh-list-status";
    status.dataset.url = source.url;
    status.dataset.id = String(source.id);
    status.textContent = "Not downloaded";
    const link = document.createElement("a");
    link.href = source.homepage;
    link.target = "_blank";
    link.rel = "noopener noreferrer";
    link.textContent = "About this list ↗";
    row.append(label, description, status, link);
    container.append(row);
  }
}

async function updateFilters() {
  if (pending || !settings || document.getElementById("panel-lists").hidden) {
    return;
  }

  setPending(true);
  try {
    const data = await parent.EXTENSIONS.action(moduleID, "filter-status");
    for (const node of document.querySelectorAll(".agh-list-status")) {
      const filter = (data.filters || []).find(
        (filter) => String(filter.id) === node.dataset.id,
      );
      node.textContent = !(settings.filters || []).includes(node.dataset.url)
        ? "Not selected"
        : filter?.update_failed
          ? filter.rules_count
            ? "Update failed. Previous copy retained."
            : "Download failed. No list loaded."
          : filter?.last_updated
            ? localize("Rules") +
              ": " +
              filter.rules_count +
              (filter.last_updated
                ? " · " + new Date(filter.last_updated).toLocaleString()
                : "")
            : "Not downloaded";
    }
    document.getElementById("filter-result").textContent = data.updating
      ? "Updating lists…"
      : "";
  } catch (error) {
    document.getElementById("filter-result").textContent = error.message;
  } finally {
    setPending(false);
  }
}

document
  .getElementById("refresh-filters")
  .addEventListener("click", async () => {
    if (pending) {
      return;
    }
    if (dirty) {
      document.getElementById("filter-result").textContent =
        "Save changes before updating lists.";
      return;
    }

    setPending(true);
    const button = document.getElementById("refresh-filters");
    button.disabled = true;
    try {
      await parent.EXTENSIONS.action(moduleID, "refresh-filters");
      document.getElementById("filter-result").textContent = "Updating lists…";
    } catch (error) {
      document.getElementById("filter-result").textContent = error.message;
    } finally {
      setPending(false);
      button.disabled = false;
    }
  });

const tabs = [...document.querySelectorAll('[role="tab"]')];
for (const tab of tabs) {
  tab.addEventListener("keydown", (event) => {
    const offset =
      event.key === "ArrowRight" ? 1 : event.key === "ArrowLeft" ? -1 : 0;
    if (!offset) {
      return;
    }

    event.preventDefault();
    const next = tabs[(tabs.indexOf(tab) + offset + tabs.length) % tabs.length];
    next.click();
    next.focus();
  });
  tab.addEventListener("click", () => {
    for (const item of document.querySelectorAll('[role="tab"]')) {
      const active = item === tab;
      item.className = active ? "tabClicked" : "tab";
      item.setAttribute("aria-selected", String(active));
      item.tabIndex = active ? 0 : -1;
      document.getElementById(item.getAttribute("aria-controls")).hidden =
        !active;
    }
    updateFilters();
  });
}

document.getElementById("settings").addEventListener("input", () => {
  dirty = true;
});

window.addEventListener("beforeunload", (event) => {
  if (dirty) {
    event.preventDefault();
    event.returnValue = "";
  }
});

function setPending(value) {
  pending = value;
  for (const id of ["save", "reload", "stats", "refresh-filters"]) {
    document.getElementById(id).disabled = value;
  }
}
