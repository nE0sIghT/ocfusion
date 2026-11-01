// SPDX-License-Identifier: GPL-3.0-or-later
"use strict";

const moduleID = "zapret2";
const fields = document.getElementById("fields");
const result = document.getElementById("result");
let config = null;
let pending = false;
let catalog = [];
let moduleCatalog = null;
let scan = null;
let scanBusy = false;
let renderedScan = "";

function ports(id) {
  const value = document.getElementById(id).value.trim();

  if (!value) {
    return [];
  }

  const items = value.split(",").map((item) => item.trim());

  if (items.some((item) => !/^[0-9]+$/.test(item))) {
    throw Error("Invalid port list");
  }

  return items.map(Number);
}

function updateStrategyFields() {
  const tcp = document.getElementById("tcp_strategy").value;
  const udp = document.getElementById("udp_strategy").value;
  const tcpFakes = ["fake", "fake-split", "fake-disorder", "fakedsplit", "fakeddisorder", "hostfakesplit"].includes(tcp);
  const standaloneFakes = ["fake", "fake-split", "fake-disorder"].includes(tcp);
  const split = !["off", "fake", "hostfakesplit"].includes(tcp);
  const applicationFakes = applicationKinds.filter((kind) => applicationInput(kind, "enabled").checked && applicationInput(kind, "method").value.includes("fake"));
  const fakes = tcpFakes || udp !== "off" || applicationFakes.length > 0;
  const automatic = document.getElementById("auto_ttl").checked;

  for (const id of ["split_positions", "sequence_overlap"]) {
    document.getElementById(id).closest("tr").hidden = !split;
  }

  for (const id of ["fake_ttl", "auto_ttl"]) {
    document.getElementById(id).closest("tr").hidden = !fakes;
  }

  for (const id of ["auto_ttl_delta", "auto_ttl_min", "auto_ttl_max"]) {
    document.getElementById(id).closest("tr").hidden = !fakes || !automatic;
  }

  document.getElementById("fake_fooling").closest("tr").hidden = !tcpFakes && !applicationFakes.some((kind) => kind.transport === "tcp");
  document.getElementById("fake_sni").closest("tr").hidden = !standaloneFakes && tcp !== "hostfakesplit";
  document.getElementById("fake_repeats").closest("tr").hidden = !standaloneFakes && udp === "off";

  for (const protocol of ["tcp", "udp"]) {
    const select = document.getElementById(protocol + "_strategy");
    document.getElementById("summary-" + protocol).textContent = select.selectedOptions[0].dataset.label;
  }
}

async function load() {
  if (pending) {
    return;
  }

  pending = true;
  fields.disabled = true;
  updateProcessingControl();

  try {
    config = await parent.EXTENSIONS.action(moduleID, "settings");

    if (catalog.length === 0) {
      moduleCatalog = await parent.EXTENSIONS.action(moduleID, "catalog");
      catalog = moduleCatalog.presets;
      buildApplicationEditors(moduleCatalog.applications);
      const select = document.getElementById("preset");

      const groups = {};

      for (const preset of catalog) {
        if (!groups[preset.protocol]) {
          const group = document.createElement("optgroup");
          group.label = {tcp: "HTTP / TLS", udp: "QUIC", application: "Applications", combined: "Web and applications"}[preset.protocol];
          groups[preset.protocol] = group;
          select.appendChild(group);
        }

        const option = document.createElement("option");
        option.value = preset.id;
        option.textContent = preset.name;
        groups[preset.protocol].appendChild(option);
      }
    }

    document.getElementById("preset").value = "";
    document.getElementById("preset-description").textContent = "Choose a preset or adjust the settings.";
    document.getElementById("enabled").checked = config.enabled;
    loadServiceLists(moduleCatalog.lists);
    for (const key of ["tcp_ports", "udp_ports"]) {
      document.getElementById(key).value = (config[key] || []).join(", ");
    }

    const defaults = {
      fake_fooling: "ttl",
      fake_sni: "",
      sequence_overlap: 0,
      auto_ttl_delta: -1,
      auto_ttl_min: 3,
      auto_ttl_max: 20,
      packet_limit: 12,
    };

    for (const key of ["tcp_strategy", "udp_strategy", "fake_ttl", "fake_repeats", ...Object.keys(defaults)]) {
      document.getElementById(key).value = config[key] ?? defaults[key];
    }

    document.getElementById("split_positions").value = config.split_positions || String(config.split_position || 2);
    document.getElementById("auto_ttl").checked = config.auto_ttl || false;
    writeApplications(config.applications || [], false);
    updateStrategyFields();
    initializeRules(config.rules);

    renderedScan = "";
    renderScan(scan);
    setDirty(false);
    document.getElementById("diagnostic-networks").textContent = String(new Set(savedRuleRows.flatMap((rule) => rule.networks || [])).size);
    document.getElementById("diagnostic-domains").textContent = String(new Set(savedRuleRows.flatMap((rule) => rule.domains || [])).size);
    result.textContent = "";
    document.getElementById("processing-result").textContent = "";
  } catch (error) {
    result.textContent = error.message;
    document.getElementById("monitor-result").textContent = error.message;
  } finally {
    pending = false;
    fields.disabled = !config;
    document.getElementById("toggle-processing").disabled = !config;
    updateProcessingControl();
    renderServiceLists();
  }
}

document.getElementById("settings").addEventListener("submit", async (event) => {
  event.preventDefault();

  if (!config || pending) {
    return;
  }

  pending = true;
  fields.disabled = true;
  updateProcessingControl();
  document.getElementById("monitor-result").textContent = "";
  result.textContent = "";

  try {
    const rules = destinationRulesConfig();
    const next = {
      ...draftBaseStrategy,
      revision: config.revision + 1,
      enabled: document.getElementById("enabled").checked,
      lists: [],
      networks: [],
      domains: [],
      rules,
      monitor: {enabled: false, interval_minutes: 15, targets: []},
    };
    await parent.EXTENSIONS.configure(moduleID, next);
    pending = false;
    await load();
  } catch (error) {
    result.textContent = error.message;
    document.getElementById("monitor-result").textContent = error.message;
  } finally {
    pending = false;
    fields.disabled = false;
    updateProcessingControl();
    renderServiceLists();
  }
});

async function updateStatus() {
  try {
    const snapshot = await parent.EXTENSIONS.status(moduleID);
    const labels = {
      DISABLED: "Disabled",
      EMPTY: "No active rules",
      WAIT_LISTS: "Waiting for service lists",
      RUNNING: "Running",
      ERROR: "Processing error",
    };
    latestModuleState = snapshot.data?.state || "UNAVAILABLE";
    document.getElementById("state").textContent = config && !config.enabled ? "Paused" : labels[latestModuleState] || "Unavailable";
    updateProcessingControl();
    const queue = snapshot.data?.queue;
    document.getElementById("queue-details").textContent = queue ? JSON.stringify(queue, null, 2) : "";
    renderScan(snapshot.data?.scan);
    renderServiceLists(snapshot.data?.lists);
    renderHealth(snapshot.data?.health);
    updateRuleRows(snapshot.data?.routing || []);
    document.getElementById("diagnostic-version").textContent = snapshot.data?.version || "-";
    document.getElementById("diagnostic-engine").textContent = snapshot.data?.upstream || "-";
  } catch (error) {
    document.getElementById("state").textContent = "Unavailable";
  }
}

for (const id of ["tcp_strategy", "udp_strategy", "auto_ttl"]) {
  document.getElementById(id).addEventListener("change", updateStrategyFields);
}

document.getElementById("reload").addEventListener("click", () => load());
load().then(updateStatus);
setInterval(updateStatus, 3000);

function selectPreset(id) {
  const preset = catalog.find((item) => item.id === id);

  if (!preset || pending || !config) {
    return;
  }

  fillStrategySettings(preset);
  document.getElementById("preset").value = id;
}

function fillStrategySettings(preset) {
  for (const [key, value] of Object.entries(preset.settings)) {
    const element = document.getElementById(key);

    if (element) {
      if (element.type === "checkbox") {
        element.checked = value;
      } else {
        element.value = value;
      }
    }
  }

  if (preset.settings.applications) {
    writeApplications(preset.settings.applications, ["application", "stun"].includes(preset.protocol));
  }

  setDirty(true);
  document.getElementById("preset").value = "";
  document.getElementById("preset-description").textContent = preset.description || "";
  document.getElementById("preset-description").hidden = !preset.description;
  updateStrategyFields();
}

function presetName(id) {
  if (id === "baseline") {
    return "Without processing";
  }

  return catalog.find((item) => item.id === id)?.name || id;
}

document.getElementById("preset").addEventListener("change", (event) => {
  try {
    selectPreset(event.target.value);
  } catch (error) {
    document.getElementById("preset-description").textContent = error.message;
  }
});

document.getElementById("application-profiles").addEventListener("input", () => {
  document.getElementById("preset").value = "";
  document.getElementById("preset-description").textContent = "Custom strategy settings";
  updateApplicationSummary();
  updateStrategyFields();
});

for (const id of ["tcp_strategy", "udp_strategy", "split_positions", "sequence_overlap", "fake_fooling", "fake_sni", "fake_ttl", "fake_repeats", "auto_ttl", "auto_ttl_delta", "auto_ttl_min", "auto_ttl_max", "packet_limit"]) {
  document.getElementById(id).addEventListener("input", () => {
    document.getElementById("preset").value = "";
    document.getElementById("preset-description").textContent = "Custom strategy settings";
  });
}

function updateProcessingControl() {
  const button = document.getElementById("toggle-processing");
  button.disabled = pending || !config;
  button.textContent = config?.enabled ? "Pause processing" : "Resume processing";
  document.getElementById("processing-state").textContent = config?.enabled ? "Processing enabled" : "Processing paused";
}

document.getElementById("toggle-processing").addEventListener("click", async () => {
  if (!config || pending) {
    return;
  }
  pending = true;
  fields.disabled = true;
  updateProcessingControl();
  try {
    const next = {...config, revision: config.revision + 1, enabled: !config.enabled};
    await parent.EXTENSIONS.configure(moduleID, next);
    config = next;
    document.getElementById("enabled").checked = config.enabled;
    document.getElementById("processing-result").textContent = "";
    await updateStatus();
  } catch (error) {
    document.getElementById("processing-result").textContent = error.message;
  } finally {
    pending = false;
    fields.disabled = !config;
    updateProcessingControl();
  }
});
