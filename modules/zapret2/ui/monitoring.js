// SPDX-License-Identifier: GPL-3.0-or-later
"use strict";

let monitoringTargets = [];
let healthBusy = false;
let healthDetailsSignature = "";
let healthDetailsBusy = false;
let latestHealth = null;

function showMonitoringTargets() {
  document.getElementById("monitor-target-count").textContent = monitoringTargets.map((target) => target.protocol.toUpperCase() + " · " + target.host + ":" + target.port).join("; ");
}

function loadMonitoringForm(saved) {
  document.getElementById("monitor-enabled").checked = saved?.enabled || false;
  document.getElementById("monitor-interval").value = saved?.interval_minutes || 15;
  monitoringTargets = structuredClone(saved?.targets || []);
  showMonitoringTargets();
}

function monitoringFormConfig() {
  return {
    enabled: document.getElementById("monitor-enabled").checked,
    interval_minutes: Number(document.getElementById("monitor-interval").value),
    targets: structuredClone(monitoringTargets),
  };
}

async function renderHealth(state) {
  latestHealth = state || {state: "IDLE"};
  document.getElementById("health-time").textContent = latestHealth.checked ? new Date(latestHealth.checked * 1000).toLocaleString() : "-";
  const labels = {IDLE: "Not checked", QUEUED: "Waiting to start", CHECKING: "Checking", HEALTHY: "Available", DEGRADED: "Availability problems", ERROR: "Check failed", PAUSED: "Check paused"};
  document.getElementById("health-state").textContent = labels[latestHealth.state] || "Not checked";
  document.getElementById("health-check").disabled = healthBusy || ["QUEUED", "CHECKING"].includes(latestHealth.state) || !config?.monitor?.targets?.length;
  const signature = JSON.stringify([latestHealth.state, latestHealth.checked, latestHealth.revision]);

  if (healthDetailsBusy || signature === healthDetailsSignature || document.getElementById("panel-diagnostics").hidden) {
    return;
  }

  healthDetailsBusy = true;

  try {
    const details = await parent.EXTENSIONS.action(moduleID, "health-status");
    const root = document.getElementById("health-results");
    root.replaceChildren();

    for (const row of details.results || []) {
      const card = document.createElement("p");
      card.appendChild(document.createTextNode(row.target.host + ":" + row.target.port + " · "));
      const status = document.createElement("span");
      status.textContent = row.observation.success && row.observation.stage === "complete" ? "Available" : discoveryStages[row.observation.stage] || "Unavailable";
      card.appendChild(status);
      card.appendChild(document.createTextNode(" · " + (row.observation.milliseconds || 0) + " ms · "));
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

document.getElementById("monitor-use-targets").addEventListener("click", () => {
  const targets = discoveryTargets.length ? discoveryTargets : [currentDiscoveryTarget()];

  if (targets.some((target) => !target.host)) {
    result.textContent = "Choose monitoring targets";
    return;
  }

  monitoringTargets = structuredClone(targets);
  showMonitoringTargets();
  setDirty(true);
});

document.getElementById("health-check").addEventListener("click", async () => {
  if (!config || pending || healthBusy) {
    return;
  }

  healthBusy = true;
  document.getElementById("health-check").disabled = true;

  try {
    await parent.EXTENSIONS.call(moduleID, "health-check", {revision: config.revision});
    healthDetailsSignature = "";
    document.getElementById("health-state").textContent = "Waiting to start";
  } catch (error) {
    document.getElementById("health-state").textContent = error.message;
  } finally {
    healthBusy = false;
  }
});

document.getElementById("tab-diagnostics").addEventListener("click", () => renderHealth(latestHealth));
