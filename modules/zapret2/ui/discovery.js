// SPDX-License-Identifier: GPL-3.0-or-later
"use strict";

let discoveryTargets = [];

const discoveryStages = {
  dns: "DNS lookup failed",
  "rule-selection": "Target does not match this rule",
  vpn: "VPN unavailable",
  address: "No public address",
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
    family: Number(document.getElementById("scan-family").value),
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
    name.textContent = "IPv" + (target.family || 4) + " · " + target.protocol.toUpperCase() + " · " + target.host + ":" + target.port + (target.path || "");
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
  summary.textContent = "Details";
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

  const targets = [...discoveryTargets];
  const current = currentDiscoveryTarget();
  if (current.host && !targets.some((target) => JSON.stringify(target) === JSON.stringify(current))) {
    targets.push(current);
  }
  if (extra.mode === "search" && (!targets.length || targets.length > 4)) {
    document.getElementById("scan-state").textContent = "Choose up to four test targets";
    return;
  }

  scanBusy = true;
  renderScan(scan || {state: "IDLE"});

  try {
    const state = await parent.EXTENSIONS.call(moduleID, "scan-start", {
      revision: config.revision,
      targets: targets.slice(0, 4),
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

let selectedResultID = "";
let selectedResultScan = "";

function updateResultActions() {
  const entry = scan?.results?.find((item) => item.preset === selectedResultID);
  const running = ["QUEUED", "RUNNING"].includes(scan?.state);
  const disabled = pending || running || scanBusy || scan?.state !== "COMPLETE" || scan?.revision !== config?.revision;
  document.getElementById("scan-selected-actions").hidden = !entry;
  document.getElementById("scan-use").textContent = entry?.protocol === "tcp" ? "Use for TCP" : entry?.protocol === "udp" ? "Use for QUIC" : entry?.protocol === "stun" ? "Use for STUN" : "Use in rule";
  for (const id of ["scan-use", "scan-confirm", "scan-refine", "scan-simplify", "scan-scope"]) {
    document.getElementById(id).disabled = disabled || !entry || (id === "scan-use" && !entry.success);
  }
  for (const row of document.querySelectorAll("#scan-results tr")) {
    row.dataset.selected = String(row.dataset.result === selectedResultID);
  }
}

function appendDiscoveryActions(cell, entry, state, running) {
  if (entry.preset === "baseline" || entry.preset.endsWith(":baseline")) {
    return;
  }
  const label = document.createElement("label");
  const input = document.createElement("input");
  input.type = "radio";
  input.name = "scan-result";
  input.value = entry.preset;
  input.checked = selectedResultID === entry.preset;
  input.disabled = running || scanBusy || state.state !== "COMPLETE" || state.revision !== config?.revision;
  input.setAttribute("aria-label", entry.name || presetName(entry.preset));
  input.addEventListener("change", () => {
    selectedResultID = entry.preset;
    updateResultActions();
  });
  label.appendChild(input);
  cell.appendChild(label);
}

for (const [id, mode] of [["scan-confirm", "confirm"], ["scan-refine", "refine"], ["scan-simplify", "lighten"]]) {
  document.getElementById(id).addEventListener("click", () => {
    if (selectedResultID && scan?.id) {
      startDiscovery({mode, from: selectedResultID, parent: scan.id});
    }
  });
}

document.getElementById("scan-use").addEventListener("click", async () => {
  if (!scan?.id || !selectedResultID || pending || scanBusy) {
    return;
  }
  const button = document.getElementById("scan-use");
  button.disabled = true;
  try {
    const candidate = await parent.EXTENSIONS.call(moduleID, "scan-settings", {id: selectedResultID, parent: scan.id});
    let rule = draftRules.find((entry) => entry.id === document.getElementById("scan-scope").value);
    if (!rule) {
      if (draftRules.length >= 7) {
        throw new Error("at most seven destination rules");
      }
      rule = {id: "rule-" + Date.now().toString(36), name: scan.targets?.[0]?.host?.slice(0, 64) || translated("New rule"), enabled: true, action: "dpi", domains: (scan.targets || []).map((target) => target.host), networks: [], lists: [], settings: structuredClone(draftBaseStrategy)};
      draftRules.push(rule);
    }
    openRule(rule.id);
    fillStrategySettings({...candidate, id: selectedResultID}, true);
    rememberStrategyScope();
    setDirty(true);
  } catch (error) {
    document.getElementById("scan-state").textContent = error.message;
  } finally {
    updateResultActions();
  }
});

function renderScan(state) {
  if (!state) {
    return;
  }

  scan = state;
  if (selectedResultScan !== state.id) {
    selectedResultID = "";
    selectedResultScan = state.id;
  }
  const running = ["QUEUED", "RUNNING"].includes(state.state);

  for (const id of ["scan-start", "scan-host", "scan-target", "scan-port", "scan-protocol", "scan-family", "scan-path", "scan-add-target"]) {
    document.getElementById(id).disabled = running || scanBusy || !config;
  }

  for (const button of document.querySelectorAll("#scan-targets button")) {
    button.disabled = running || scanBusy;
  }

  document.getElementById("scan-cancel").disabled = !running || scanBusy;
  document.getElementById("scan-cancel").hidden = !running;
  document.getElementById("scan-result-section").hidden = !state.results?.length;
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
    row.dataset.result = entry.preset;
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
      badge.textContent = entry.history_saved || entry.remembered ? "Saved result" : "Checked again";
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
  updateResultActions();
}

document.getElementById("scan-add-target").addEventListener("click", () => {
  const target = currentDiscoveryTarget();

  if (!target.host || discoveryTargets.length >= 4) {
    document.getElementById("scan-state").textContent = "Choose up to four test targets";
    return;
  }

  if (!discoveryTargets.some((entry) => JSON.stringify(entry) === JSON.stringify(target))) {
    discoveryTargets.push(target);
  }
  document.getElementById("scan-host").value = "";
  document.getElementById("scan-target").value = "";
  renderDiscoveryTargets();
});

document.getElementById("scan-protocol").addEventListener("change", () => {
  const stun = document.getElementById("scan-protocol").value === "stun";
  const family = document.getElementById("scan-family");
  family.querySelector('[value="6"]').disabled = document.getElementById("scan-protocol").value !== "http3";
  if (document.getElementById("scan-protocol").value !== "http3") {
    family.value = "4";
  }
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
    const response = await parent.EXTENSIONS.action(moduleID, "history");
    const entries = Array.isArray(response) ? response : [];
    const root = document.getElementById("history-results");
    root.replaceChildren();

    for (const entry of entries) {
      const card = document.createElement("div");
      card.className = "ocf-service-list";
      const name = document.createElement("span");
      name.textContent = entry.name;
      card.appendChild(name);
      card.appendChild(document.createTextNode(" · " + entry.protocol.toUpperCase() + " · " + entry.targets.map((target) => target.host + ":" + target.port).join(", ")));
      card.appendChild(document.createTextNode(" · " + entry.success + "/" + entry.attempts + " · " + new Date(entry.updated * 1000).toLocaleDateString()));
      const use = document.createElement("button");
      use.type = "button";
      use.className = "ext-secondary";
      use.textContent = "Use for this rule";
      use.addEventListener("click", async () => {
        if (!config || pending) {
          return;
        }

        use.disabled = true;

        try {
          const preset = await parent.EXTENSIONS.call(moduleID, "history-settings", {id: entry.id, revision: config.revision});
          fillStrategySettings({...preset, id: entry.id}, true);
          rememberStrategyScope();
          setDirty(true);
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
      recheck.textContent = "Check again";
      recheck.addEventListener("click", () => {
        document.getElementById("scan-scope").value = strategyScope;
        selectTab("finder");
        startDiscovery({mode: "history", from: entry.id});
      });
      card.appendChild(recheck);
      root.appendChild(card);
    }

    if (entries.length === 0) {
      root.textContent = "No saved finder results";
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
