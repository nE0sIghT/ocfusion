// SPDX-License-Identifier: GPL-3.0-or-later
"use strict";

let discoveryTargets = [];

const discoveryStages = {
  dns: "DNS lookup failed",
  vpn: "VPN unavailable",
  address: "No public IPv4 address",
  route: "Direct WAN route unavailable",
  connect: "TCP connection failed",
  tls: "TLS handshake failed",
  headers: "HTTP response failed",
  body: "Transfer interrupted",
  "http-status": "Server rejected the test",
  "short-body": "Short response; larger transfer untested",
  quic: "QUIC or HTTP/3 failed",
  stun: "No valid STUN response",
  complete: "Complete",
};

function currentDiscoveryTarget() {
  return {
    host: document.getElementById("scan-host").value.trim(),
    port: Number(document.getElementById("scan-port").value),
    protocol: document.getElementById("scan-protocol").value,
    path: document.getElementById("scan-protocol").value === "stun" ? "" : document.getElementById("scan-path").value.trim(),
  };
}

function renderDiscoveryTargets() {
  const root = document.getElementById("scan-targets");
  root.replaceChildren();

  for (const [index, target] of discoveryTargets.entries()) {
    const row = document.createElement("div");
    row.className = "ocf-discovery-target";
    const name = document.createElement("span");
    name.textContent = target.protocol.toUpperCase() + " · " + target.host + ":" + target.port + (target.path || "");
    row.appendChild(name);
    const remove = document.createElement("button");
    remove.type = "button";
    remove.className = "ext-secondary";
    remove.textContent = "Remove";
    remove.addEventListener("click", () => {
      discoveryTargets.splice(index, 1);
      renderDiscoveryTargets();
    });
    row.appendChild(remove);
    root.appendChild(row);
  }
}

function scanTargetDescription(state, index) {
  const target = state.targets?.[index];
  return target ? (index + 1) + ". " + target.host + ":" + target.port : state.host || "";
}

function appendProbeDetails(cell, entry, state) {
  const details = document.createElement("details");
  const summary = document.createElement("summary");
  summary.textContent = entry.protocol === "stun" ? "STUN response" : (entry.bytes || 0) + " B / 65536 B";
  details.appendChild(summary);

  for (const detail of entry.details || []) {
    const row = document.createElement("p");
    const name = document.createElement("span");
    name.textContent = scanTargetDescription(state, detail.target) + " · ";
    row.appendChild(name);
    if (detail.attempt) {
      const attempt = document.createElement("span");
      attempt.textContent = "Attempt";
      row.appendChild(attempt);
      row.appendChild(document.createTextNode(" " + detail.attempt + " · "));
    }

    const stage = document.createElement("span");
    stage.textContent = discoveryStages[detail.stage] || detail.stage;
    row.appendChild(stage);

    if (detail.bytes) {
      row.appendChild(document.createTextNode(" · " + detail.bytes + " B"));
    }

    details.appendChild(row);
  }

  cell.appendChild(details);
}

async function startDiscovery(extra) {
  if (!config || scanBusy || pending) {
    return;
  }

  scanBusy = true;
  renderScan(scan || {state: "IDLE"});

  try {
    const state = await parent.EXTENSIONS.call(moduleID, "scan-start", {
      revision: config.revision,
      targets: discoveryTargets.length ? discoveryTargets : [currentDiscoveryTarget()],
      ...extra,
    });
    renderScan(state);
  } catch (error) {
    document.getElementById("scan-state").textContent = error.message;
  } finally {
    scanBusy = false;
    renderScan(scan || {state: "IDLE"});
  }
}

function appendDiscoveryActions(cell, entry, state, running) {
  if (entry.preset === "baseline" || entry.preset.endsWith(":baseline")) {
    return;
  }

  const actions = [
    {label: "Select", mode: "use"},
    {label: "Confirm", mode: "confirm"},
    {label: "Refine", mode: "refine"},
    {label: "Simplify", mode: "lighten"},
  ];

  for (const action of actions) {
    const button = document.createElement("button");
    button.type = "button";
    button.className = "ext-secondary";
    button.textContent = action.label;
    button.disabled = pending || running || scanBusy || state.state !== "COMPLETE" || state.revision !== config?.revision;
    button.addEventListener("click", async () => {
      if (action.mode !== "use") {
        await startDiscovery({mode: action.mode, from: entry.preset, parent: state.id});
        return;
      }

      button.disabled = true;

      try {
        const candidate = await parent.EXTENSIONS.call(moduleID, "scan-settings", {id: entry.preset, parent: state.id});
        rememberStrategyScope();
        showStrategyScope(document.getElementById("scan-scope").value);
        fillStrategySettings(candidate);
        selectTab("strategies");
        document.getElementById("preset").scrollIntoView({block: "center", behavior: "smooth"});
      } catch (error) {
        document.getElementById("scan-state").textContent = error.message;
      } finally {
        button.disabled = false;
      }
    });
    if (action.mode === "use") {
      cell.appendChild(button);
    } else {
      let more = cell.querySelector("details");
      if (!more) {
        more = document.createElement("details");
        more.className = "ocf-result-actions";
        const summary = document.createElement("summary");
        summary.textContent = "More tests";
        more.appendChild(summary);
        cell.appendChild(more);
      }
      more.appendChild(button);
    }
  }
}

function renderScan(state) {
  if (!state) {
    return;
  }

  scan = state;
  const running = ["QUEUED", "RUNNING"].includes(state.state);

  for (const id of ["scan-start", "scan-host", "scan-target", "scan-port", "scan-protocol", "scan-path", "scan-add-target"]) {
    document.getElementById(id).disabled = running || scanBusy || !config;
  }

  for (const button of document.querySelectorAll("#scan-targets button")) {
    button.disabled = running || scanBusy;
  }

  document.getElementById("scan-cancel").disabled = !running || scanBusy;
  const signature = JSON.stringify([state, config?.revision, pending, scanBusy]);

  if (signature === renderedScan) {
    return;
  }

  renderedScan = signature;
  const text = document.getElementById("scan-state");
  const labels = {QUEUED: "Waiting to start", RUNNING: "Testing", COMPLETE: "Test complete", CANCELLED: "Test cancelled", ERROR: "Test failed", IDLE: ""};
  text.replaceChildren();
  const label = document.createElement("span");
  label.textContent = labels[state.state] || "";
  text.appendChild(label);

  if (state.current || state.error) {
    text.appendChild(document.createTextNode(" · "));
    const detail = document.createElement("span");
    detail.textContent = state.error || scanTargetDescription(state, state.current_target);
    text.appendChild(detail);
  }

  if (state.state === "COMPLETE" && state.results?.some((entry) => entry.history_saved)) {
    refreshHistory();
  }

  const table = document.getElementById("scan-results");
  table.replaceChildren();

  for (const entry of state.results || []) {
    const row = document.createElement("tr");
    const name = document.createElement("td");
    const title = document.createElement("span");
    const titleParts = (entry.name || presetName(entry.preset)).split(" - ");
    title.textContent = titleParts[0];
    name.appendChild(title);

    if (titleParts.length > 1) {
      name.appendChild(document.createTextNode(" - "));
      const suffixParts = titleParts.slice(1).join(" - ").split(" / ");
      const suffix = document.createElement("span");
      suffix.textContent = suffixParts[0];
      name.appendChild(suffix);

      if (suffixParts.length > 1) {
        name.appendChild(document.createTextNode(" / " + suffixParts.slice(1).join(" / ")));
      }
    }

    name.appendChild(document.createTextNode(" · " + (entry.protocol || "tcp").toUpperCase()));
    if (entry.confirmed || entry.remembered) {
      const badge = document.createElement("span");
      badge.className = "ext-badge";
      badge.textContent = entry.confirmed ? "Confirmed" : "Previously confirmed";
      name.appendChild(badge);
    }
    if (entry.confirmed && !entry.history_saved) {
      const warning = document.createElement("p");
      warning.textContent = "History could not be saved";
      name.appendChild(warning);
    }
    row.appendChild(name);

    for (const value of [entry.success + "/" + entry.attempts, entry.success ? entry.milliseconds + " ms" : "-"]) {
      const cell = document.createElement("td");
      cell.textContent = value;
      row.appendChild(cell);
    }

    const details = document.createElement("td");
    appendProbeDetails(details, entry, state);
    row.appendChild(details);
    const actions = document.createElement("td");
    appendDiscoveryActions(actions, entry, state, running);
    row.appendChild(actions);
    table.appendChild(row);
  }
}

document.getElementById("scan-add-target").addEventListener("click", () => {
  const target = currentDiscoveryTarget();

  if (!target.host || discoveryTargets.length >= 4) {
    document.getElementById("scan-state").textContent = "Choose up to four test targets";
    return;
  }

  discoveryTargets.push(target);
  renderDiscoveryTargets();
});

document.getElementById("scan-protocol").addEventListener("change", () => {
  const stun = document.getElementById("scan-protocol").value === "stun";
  document.getElementById("scan-path-row").hidden = stun;
  document.getElementById("scan-port").value = stun ? "19302" : document.getElementById("scan-protocol").value === "http" ? "80" : "443";

  if (stun && !document.getElementById("scan-host").value) {
    document.getElementById("scan-host").value = "stun.l.google.com";
  }
});

document.getElementById("scan-start").addEventListener("click", () => startDiscovery({mode: "search"}));

document.getElementById("scan-cancel").addEventListener("click", async () => {
  if (!scan?.id || scanBusy) {
    return;
  }

  scanBusy = true;
  renderScan(scan);

  try {
    await parent.EXTENSIONS.call(moduleID, "scan-cancel", {id: scan.id});
  } catch (error) {
    document.getElementById("scan-state").textContent = error.message;
  } finally {
    scanBusy = false;
    renderScan(scan);
  }
});

let historyLoading = false;

async function refreshHistory() {
  if (historyLoading) {
    return;
  }

  historyLoading = true;

  try {
    const entries = await parent.EXTENSIONS.action(moduleID, "history");
    const root = document.getElementById("history-results");
    root.replaceChildren();

    for (const entry of entries) {
      const card = document.createElement("p");
      const name = document.createElement("span");
      name.textContent = entry.name;
      card.appendChild(name);
      card.appendChild(document.createTextNode(" · " + entry.protocol.toUpperCase() + " · " + entry.targets.map((target) => target.host + ":" + target.port).join(", ")));
      card.appendChild(document.createTextNode(" · " + entry.success + "/" + entry.attempts + " · " + new Date(entry.updated * 1000).toLocaleDateString()));
      const use = document.createElement("button");
      use.type = "button";
      use.className = "ext-secondary";
      use.textContent = "Use settings";
      use.addEventListener("click", async () => {
        if (!config || pending) {
          return;
        }

        use.disabled = true;

        try {
          const preset = await parent.EXTENSIONS.call(moduleID, "history-settings", {id: entry.id, revision: config.revision});
          fillStrategySettings(preset);
          selectTab("strategies");
        } catch (error) {
          document.getElementById("scan-state").textContent = error.message;
        } finally {
          use.disabled = false;
        }
      });
      card.appendChild(use);
      const recheck = document.createElement("button");
      recheck.type = "button";
      recheck.className = "ext-secondary";
      recheck.textContent = "Recheck";
      recheck.addEventListener("click", () => {
        document.getElementById("scan-scope").value = strategyScope;
        selectTab("finder");
        startDiscovery({mode: "history", from: entry.id});
      });
      card.appendChild(recheck);
      root.appendChild(card);
    }

    if (entries.length === 0) {
      root.textContent = "No confirmed strategies";
    }
  } catch (error) {
    document.getElementById("history-results").textContent = error.message;
  } finally {
    historyLoading = false;
  }
}

document.getElementById("history-refresh").addEventListener("click", refreshHistory);
document.getElementById("confirmed-history").addEventListener("toggle", () => {
  if (document.getElementById("confirmed-history").open) {
    refreshHistory();
  }
});
