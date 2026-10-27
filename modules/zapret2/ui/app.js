// SPDX-License-Identifier: GPL-3.0-or-later
"use strict";

const moduleID = "zapret2";
const fields = document.getElementById("fields");
const result = document.getElementById("result");
let config = null;
let pending = false;
let catalog = [];
let scan = null;
let scanBusy = false;
let renderedScan = "";

function lines(id) {
  return document.getElementById(id).value.split(/\r?\n/).map((value) => value.trim()).filter(Boolean);
}

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
  const fakes = tcpFakes || udp !== "off";
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

  document.getElementById("fake_fooling").closest("tr").hidden = !tcpFakes;
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

  try {
    config = await parent.EXTENSIONS.action(moduleID, "settings");

    if (catalog.length === 0) {
      catalog = await parent.EXTENSIONS.action(moduleID, "presets");
      const select = document.getElementById("preset");

      const groups = {};

      for (const preset of catalog) {
        if (!groups[preset.protocol]) {
          const group = document.createElement("optgroup");
          group.label = preset.protocol === "tcp" ? "TCP" : "QUIC";
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
    document.getElementById("preset-description").textContent = "Choose a starting point, then apply or adjust its settings.";
    document.getElementById("enabled").checked = config.enabled;

    for (const key of ["networks", "domains"]) {
      document.getElementById(key).value = (config[key] || []).join("\n");
    }

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
    updateStrategyFields();

    renderedScan = "";
    renderScan(scan);
    setDirty(false);
    document.getElementById("diagnostic-networks").textContent = String((config.networks || []).length);
    document.getElementById("diagnostic-domains").textContent = String((config.domains || []).length);
    result.textContent = "";
  } catch (error) {
    result.textContent = error.message;
  } finally {
    pending = false;
    fields.disabled = !config;
  }
}

document.getElementById("settings").addEventListener("submit", async (event) => {
  event.preventDefault();

  if (!config || pending) {
    return;
  }

  pending = true;
  fields.disabled = true;
  result.textContent = "";

  try {
    const next = {
      revision: config.revision + 1,
      enabled: document.getElementById("enabled").checked,
      networks: lines("networks"),
      domains: lines("domains"),
      tcp_ports: ports("tcp_ports"),
      udp_ports: ports("udp_ports"),
      tcp_strategy: document.getElementById("tcp_strategy").value,
      udp_strategy: document.getElementById("udp_strategy").value,
      split_position: config.split_position || 2,
      split_positions: document.getElementById("split_positions").value.trim(),
      sequence_overlap: Number(document.getElementById("sequence_overlap").value),
      fake_fooling: document.getElementById("fake_fooling").value,
      fake_sni: document.getElementById("fake_sni").value.trim(),
      auto_ttl: document.getElementById("auto_ttl").checked,
      auto_ttl_delta: Number(document.getElementById("auto_ttl_delta").value),
      auto_ttl_min: Number(document.getElementById("auto_ttl_min").value),
      auto_ttl_max: Number(document.getElementById("auto_ttl_max").value),
      packet_limit: Number(document.getElementById("packet_limit").value),
      fake_ttl: Number(document.getElementById("fake_ttl").value),
      fake_repeats: Number(document.getElementById("fake_repeats").value),
    };
    await parent.EXTENSIONS.configure(moduleID, next);
    pending = false;
    await load();
  } catch (error) {
    result.textContent = error.message;
  } finally {
    pending = false;
    fields.disabled = false;
  }
});

async function updateStatus() {
  try {
    const snapshot = await parent.EXTENSIONS.status(moduleID);
    const labels = {
      DISABLED: "Disabled",
      EMPTY: "Add destinations to start processing",
      RUNNING: "Running",
      ERROR: "Packet processing unavailable",
    };
    document.getElementById("state").textContent = labels[snapshot.data?.state] || "Unavailable";
    renderScan(snapshot.data?.scan);
    document.getElementById("diagnostic-version").textContent = snapshot.data?.version || "-";
    document.getElementById("diagnostic-engine").textContent = snapshot.data?.upstream || "-";
  } catch (error) {
    document.getElementById("state").textContent = "Unavailable";
  }
}

for (const id of ["tcp_strategy", "udp_strategy", "auto_ttl"]) {
  document.getElementById(id).addEventListener("change", updateStrategyFields);
}

document.getElementById("reload").addEventListener("click", () => {
  if (confirmDiscard()) {
    load();
  }
});
load().then(updateStatus);
setInterval(updateStatus, 3000);

function selectPreset(id) {
  const preset = catalog.find((item) => item.id === id);

  if (!preset || pending || !config) {
    return;
  }

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

  setDirty(true);
  document.getElementById("preset").value = id;
  document.getElementById("preset-description").textContent = preset.description;
  updateStrategyFields();
}

function presetName(id) {
  if (id === "baseline") {
    return "Without processing";
  }

  return catalog.find((item) => item.id === id)?.name || id;
}

function renderScan(state) {
  if (!state) {
    return;
  }

  scan = state;
  const running = ["QUEUED", "RUNNING"].includes(state.state);
  document.getElementById("scan-start").disabled = running || scanBusy || !config;
  document.getElementById("scan-cancel").disabled = !running || scanBusy;
  document.getElementById("scan-host").disabled = running || scanBusy;
  document.getElementById("scan-target").disabled = running || scanBusy;
  document.getElementById("scan-port").disabled = running || scanBusy;
  const signature = JSON.stringify([state, config?.revision, pending, catalog.length]);

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
    text.appendChild(document.createTextNode(" - "));
    const detail = document.createElement("span");
    detail.textContent = state.error || presetName(state.current);
    text.appendChild(detail);
  }

  const table = document.getElementById("scan-results");
  table.replaceChildren();

  for (const entry of state.results || []) {
    const row = document.createElement("tr");

    for (const value of [presetName(entry.preset), entry.success + "/" + entry.attempts, entry.success ? entry.milliseconds + " ms" : "-", entry.http_status || "-"]) {
      const cell = document.createElement("td");
      cell.textContent = value;
      row.appendChild(cell);
    }

    const action = document.createElement("td");

    if (entry.preset !== "baseline" && entry.success > 0) {
      const button = document.createElement("button");
      button.type = "button";
      button.className = "ext-secondary";
      button.textContent = "Use settings";
      button.disabled = pending || state.revision !== config?.revision;
      button.addEventListener("click", () => {
        selectTab("strategies");
        selectPreset(entry.preset);
        document.getElementById("preset").scrollIntoView({block: "center", behavior: "smooth"});
      });
      action.appendChild(button);
    }

    row.appendChild(action);
    table.appendChild(row);
  }
}

document.getElementById("preset").addEventListener("change", (event) => selectPreset(event.target.value));

for (const id of ["tcp_strategy", "udp_strategy", "split_positions", "sequence_overlap", "fake_fooling", "fake_sni", "fake_ttl", "fake_repeats", "auto_ttl", "auto_ttl_delta", "auto_ttl_min", "auto_ttl_max", "packet_limit"]) {
  document.getElementById(id).addEventListener("input", () => {
    document.getElementById("preset").value = "";
    document.getElementById("preset-description").textContent = "Custom strategy settings";
  });
}


document.getElementById("scan-start").addEventListener("click", async () => {
  if (!config || scanBusy || pending) {
    return;
  }

  scanBusy = true;
  renderScan(scan || {state: "IDLE"});

  try {
    const state = await parent.EXTENSIONS.call(moduleID, "scan-start", {
      revision: config.revision,
      host: document.getElementById("scan-host").value.trim(),
      port: Number(document.getElementById("scan-port").value),
    });
    renderScan(state);
  } catch (error) {
    document.getElementById("scan-state").textContent = error.message;
  } finally {
    scanBusy = false;
    renderScan(scan || {state: "IDLE"});
  }
});

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
