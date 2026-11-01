// SPDX-License-Identifier: GPL-3.0-or-later
"use strict";

let monitoringTargets = [];
let healthBusy = false;
let healthDetailsSignature = "";
let healthDetailsBusy = false;
let latestHealth = null;

function showMonitoringTargets() {
  document.getElementById("monitor-target-count").textContent = "";
  document.getElementById("monitor-targets-input").value = monitoringTargets.map((target) => target.protocol + "://" + target.host + ":" + target.port + (target.path || "")).join("\n");
}

function loadMonitoringForm(saved) {
  healthDetailsSignature = "";
  document.getElementById("health-results").replaceChildren();
  document.getElementById("monitor-result").textContent = "";
  document.getElementById("monitor-enabled").checked = saved?.enabled || false;
  document.getElementById("monitor-interval").value = saved?.interval_minutes || 15;
  monitoringTargets = structuredClone(saved?.targets || []);
  showMonitoringTargets();
}

function monitoringFormConfig() {
  const targets = document.getElementById("monitor-targets-input").value.split(/\r?\n/).map((line) => line.trim()).filter(Boolean).map((line) => {
    const url = new URL(line.includes("://") ? line : "https://" + line);
    const protocol = url.protocol.slice(0, -1);
    if (!["https", "https12", "http", "http3", "stun"].includes(protocol) || url.username || url.password || url.hash) {
      throw new Error("Invalid test address");
    }
    return {host: url.hostname, port: Number(url.port || (protocol === "http" ? 80 : protocol === "stun" ? 3478 : 443)), protocol, path: protocol === "stun" ? "" : url.pathname + url.search || "/"};
  });
  return {
    enabled: document.getElementById("monitor-enabled").checked,
    interval_minutes: Number(document.getElementById("monitor-interval").value),
    targets,
  };
}

async function renderHealth(state) {
  latestHealth = state || {state: "IDLE"};
  if (!editingRule) {
    return;
  }
  const selectedHealth = latestHealth.rule_id === editingRule ? latestHealth : latestHealth.rules?.[editingRule] || {state: "IDLE"};
  document.getElementById("health-time").textContent = selectedHealth.checked ? new Date(selectedHealth.checked * 1000).toLocaleString() : "-";
  const labels = {IDLE: "Not checked", QUEUED: "Waiting to start", CHECKING: "Checking", HEALTHY: "Available", DEGRADED: "Availability problems", ERROR: "Check failed", PAUSED: "Check paused"};
  document.getElementById("health-state").textContent = labels[selectedHealth.state] || "Not checked";
  document.getElementById("health-check").disabled = healthBusy || ["QUEUED", "CHECKING"].includes(selectedHealth.state) || !config?.rules?.find((rule) => rule.id === editingRule && rule.enabled)?.monitor?.targets?.length || settingsDirty;
  const signature = JSON.stringify([editingRule, selectedHealth.state, selectedHealth.checked, selectedHealth.revision]);

  if (healthDetailsBusy || signature === healthDetailsSignature || document.getElementById("rule-checks").closest("[hidden]")) {
    return;
  }

  healthDetailsBusy = true;
  const checkedRuleID = editingRule;

  try {
    const details = await parent.EXTENSIONS.action(moduleID, "health-status");
    if (checkedRuleID !== editingRule) {
      return;
    }
    const selected = details.rule_id === editingRule ? details : details.rules?.[editingRule] || {};
    const root = document.getElementById("health-results");
    root.replaceChildren();

    for (const row of selected.results || []) {
      const card = document.createElement("p");
      card.appendChild(document.createTextNode(row.target.host + ":" + row.target.port + " · "));
      const status = document.createElement("span");
      status.textContent = row.observation.success && row.observation.stage === "complete" ? "Available" : discoveryStages[row.observation.stage] || "Unavailable";
      card.appendChild(status);
      card.appendChild(document.createTextNode(" · " + (row.observation.milliseconds || 0) + " ms · "));
      if (row.path) {
        const path = document.createElement("span");
        path.className = "ext-badge";
        path.textContent = {VPN: "VPN", WAN: "WAN", DPI: "DPI bypass", BLOCKED: "Blocked", NORMAL: "Without processing"}[row.path] || "Unavailable";
        card.appendChild(path);
      }
      const strategy = document.createElement("span");
      strategy.textContent = row.strategy || "Not tested";
      card.appendChild(strategy);
      card.appendChild(document.createTextNode(" · "));
      const failures = document.createElement("span");
      failures.textContent = "Consecutive failures";
      card.appendChild(failures);
      card.appendChild(document.createTextNode(": " + row.consecutive_failures));
      root.appendChild(card);
    }

    healthDetailsSignature = signature;
  } catch (error) {
    document.getElementById("health-state").textContent = error.message;
  } finally {
    healthDetailsBusy = false;
  }
}

document.getElementById("health-check").addEventListener("click", async () => {
  if (!config || pending || healthBusy) {
    return;
  }

  healthBusy = true;
  document.getElementById("health-check").disabled = true;

  try {
    await parent.EXTENSIONS.call(moduleID, "health-check", {revision: config.revision, rule_id: editingRule});
    healthDetailsSignature = "";
    document.getElementById("health-state").textContent = "Waiting to start";
  } catch (error) {
    document.getElementById("health-state").textContent = error.message;
  } finally {
    healthBusy = false;
  }
});

for (const id of ["monitor-enabled", "monitor-interval", "monitor-targets-input"]) {
  document.getElementById(id).addEventListener("input", () => {
    const rule = draftRules.find((entry) => entry.id === editingRule);
    if (!rule) {
      return;
    }
    try {
      rule.monitor = monitoringFormConfig();
      document.getElementById("monitor-result").textContent = "";
    } catch (error) {
      document.getElementById("monitor-result").textContent = error.message;
    }
    setDirty(true);
    document.getElementById("health-check").disabled = true;
  });
}
