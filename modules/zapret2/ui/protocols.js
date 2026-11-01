// SPDX-License-Identifier: GPL-3.0-or-later
"use strict";

const packetOptionKeys = ["fake_ttl", "fake_repeats", "fake_fooling", "auto_ttl", "auto_ttl_delta", "auto_ttl_min", "auto_ttl_max", "packet_limit"];
const packetOptionDefaults = {fake_ttl: 3, fake_repeats: 3, fake_fooling: "ttl", auto_ttl: false, auto_ttl_delta: -1, auto_ttl_min: 3, auto_ttl_max: 20, packet_limit: 12};
let strategySelections = {};
let strategySplitPosition = 2;

function scopeField(scope, key) {
  return document.getElementById(scope === "tcp" ? key : scope === "udp" ? "quic-" + key : "application-" + scope + "-packet-" + key);
}

function strategyScopes() {
  return ["tcp", "udp", ...applicationKinds.map((kind) => kind.id)];
}

function buildProtocolEditors() {
  const quic = document.getElementById("quic-packet-options");
  for (const key of packetOptionKeys) {
    const original = document.getElementById(key).closest("tr");
    const row = original.cloneNode(true);
    for (const element of row.querySelectorAll("[id]")) {
      element.id = "quic-" + element.id;
    }
    for (const label of row.querySelectorAll("[for]")) {
      label.htmlFor = "quic-" + label.htmlFor;
    }
    quic.appendChild(row);
  }

  for (const kind of applicationKinds) {
    const details = document.querySelector("#application-" + kind.id + "-options details");
    const options = document.createElement("table");
    options.className = "ocf-config-table";
    for (const key of packetOptionKeys) {
      const row = document.getElementById(key).closest("tr").cloneNode(true);
      for (const element of row.querySelectorAll("[id]")) {
        element.id = "application-" + kind.id + "-packet-" + element.id;
      }
      for (const label of row.querySelectorAll("[for]")) {
        label.htmlFor = "application-" + kind.id + "-packet-" + label.htmlFor;
      }
      options.appendChild(row);
    }
    details.appendChild(options);
  }
}

function readPacketOptions(scope) {
  return Object.fromEntries(packetOptionKeys.map((key) => {
    const field = scopeField(scope, key);
    const value = field.type === "checkbox" ? field.checked : field.type === "number" ? Number(field.value) : field.value;
    return [key, value];
  }));
}

function writePacketOptions(scope, values) {
  for (const key of packetOptionKeys) {
    const field = scopeField(scope, key);
    if (field.type === "checkbox") {
      field.checked = values[key];
    } else {
      field.value = values[key];
    }
  }
}

function collectProtocolOptions() {
  const active = new Set(["tcp", "udp", ...readApplications().map((profile) => profile.id)]);
  const baseline = readPacketOptions("tcp");
  return Object.fromEntries([...active].map((scope) => {
    const options = readPacketOptions(scope);
    return [scope, Object.fromEntries(packetOptionKeys.filter((key) => options[key] !== baseline[key]).map((key) => [key, options[key]]))];
  }).filter(([, options]) => Object.keys(options).length));
}

function writeStrategySettings(settings, selections = {}) {
  strategySplitPosition = settings.split_position || 2;
  for (const [key, value] of Object.entries(settings)) {
    const field = document.getElementById(key);
    if (field && ["INPUT", "SELECT"].includes(field.tagName)) {
      if (field.type === "checkbox") {
        field.checked = value;
      } else {
        field.value = Array.isArray(value) ? value.join(", ") : value;
      }
    }
  }
  document.getElementById("split_positions").value = settings.split_positions || String(settings.split_position || 2);
  writeApplications(settings.applications || [], false);
  const fallback = {...packetOptionDefaults, ...Object.fromEntries(packetOptionKeys.filter((key) => settings[key] !== undefined).map((key) => [key, settings[key]]))};
  for (const scope of strategyScopes()) {
    writePacketOptions(scope, {...fallback, ...settings.protocol_options?.[scope]});
  }
  strategySelections = structuredClone(selections);
  document.getElementById("application-preset").value = "";
  document.getElementById("combined-preset").value = "";
  updateStrategyFields();
}

function protocolSnapshot(scope) {
  const snapshot = readPacketOptions(scope);
  if (scope === "tcp") {
    Object.assign(snapshot, {
      tcp_strategy: document.getElementById("tcp_strategy").value,
      tcp_ports: ports("tcp_ports"),
      split_position: strategySplitPosition,
      split_positions: document.getElementById("split_positions").value.trim(),
      sequence_overlap: Number(document.getElementById("sequence_overlap").value),
      fake_sni: document.getElementById("fake_sni").value.trim(),
    });
  } else if (scope === "udp") {
    snapshot.udp_strategy = document.getElementById("udp_strategy").value;
    snapshot.udp_ports = ports("udp_ports");
  } else {
    snapshot.applications = readApplications().filter((profile) => profile.id === scope);
  }
  return snapshot;
}

function canonicalSettings(value) {
  if (Array.isArray(value)) {
    return value.map(canonicalSettings);
  }
  if (value && typeof value === "object") {
    return Object.fromEntries(Object.keys(value).sort().map((key) => [key, canonicalSettings(value[key])]));
  }
  return value;
}

// Cosmetic identity only; the platform transport provides request integrity.
function protocolSignature(scope) {
  const bytes = new TextEncoder().encode(JSON.stringify(canonicalSettings(protocolSnapshot(scope))));
  let signature = 14695981039346656037n;
  for (const byte of bytes) {
    signature = BigInt.asUintN(64, (signature ^ BigInt(byte)) * 1099511628211n);
  }
  return signature.toString(16).padStart(16, "0");
}

function currentStrategySelection(scope) {
  const selection = strategySelections[scope];
  try {
    if (selection && selection.signature === protocolSignature(scope)) {
      return selection;
    }
  } catch (error) {
    return null;
  }
  return null;
}

function scopePresetSelect(scope) {
  return document.getElementById(scope === "tcp" ? "preset" : "quic-preset");
}

function refreshProtocolSelections() {
  for (const scope of ["tcp", "udp"]) {
    const select = scopePresetSelect(scope);
    for (const option of select.querySelectorAll("[data-finder-result]")) {
      option.remove();
    }
    const disabled = document.getElementById(scope + "_strategy").value === "off";
    const selection = currentStrategySelection(scope);
    select.value = disabled ? "off" : "";
    if (!disabled && selection) {
      let option = [...select.options].find((entry) => entry.value === selection.id);
      if (!option) {
        option = document.createElement("option");
        option.value = "found:" + scope;
        option.dataset.finderResult = "true";
        select.appendChild(option);
      }
      option.textContent = selection.name;
      select.value = option.value;
    }
    const summary = document.getElementById("summary-" + scope);
    summary.replaceChildren();
    if (!disabled && !selection) {
      const method = document.createElement("span");
      method.textContent = document.getElementById(scope + "_strategy").selectedOptions[0].dataset.label;
      summary.append(method, document.createTextNode(" · "));
    }
    const status = document.createElement("span");
    status.textContent = disabled ? "Off" : selection?.checked ? "Tested by finder" : "Not tested";
    summary.appendChild(status);
  }
}

function populateProtocolPresets() {
  const selectors = {tcp: "preset", udp: "quic-preset", application: "application-preset", combined: "combined-preset"};
  for (const preset of catalog) {
    const select = document.getElementById(selectors[preset.protocol]);
    if (!select) {
      continue;
    }
    const option = document.createElement("option");
    option.value = preset.id;
    option.textContent = preset.name;
    select.appendChild(option);
  }
}

function applyProtocolPreset(preset, checked = false) {
  const settings = preset.settings;
  let scopes;
  if (preset.protocol === "tcp" || preset.protocol === "udp") {
    scopes = [preset.protocol];
  } else {
    scopes = (settings.applications || []).map((profile) => profile.id);
    if (preset.protocol === "combined") {
      scopes.unshift("tcp", "udp");
    }
  }
  for (const scope of scopes) {
    const options = {...readPacketOptions(scope), ...Object.fromEntries(packetOptionKeys.filter((key) => settings[key] !== undefined).map((key) => [key, settings[key]]))};
    writePacketOptions(scope, options);
  }
  if (scopes.includes("tcp")) {
    if (settings.split_position !== undefined) {
      strategySplitPosition = settings.split_position;
    }
    for (const key of ["tcp_strategy", "split_positions", "sequence_overlap", "fake_sni"]) {
      if (settings[key] !== undefined) {
        document.getElementById(key).value = settings[key];
      }
    }
  }
  if (scopes.includes("udp") && settings.udp_strategy !== undefined) {
    document.getElementById("udp_strategy").value = settings.udp_strategy;
  }
  if (settings.applications) {
    const incoming = new Set(settings.applications.map((profile) => profile.id));
    writeApplications([...readApplications().filter((profile) => !incoming.has(profile.id)), ...settings.applications], false);
  }
  for (const scope of scopes) {
    strategySelections[scope] = {id: preset.id || "found", name: preset.name, checked, signature: protocolSignature(scope)};
  }
  updateStrategyFields();
}

for (const [scope, id] of [["tcp", "preset"], ["udp", "quic-preset"], ["applications", "application-preset"], ["combined", "combined-preset"]]) {
  document.getElementById(id).addEventListener("change", (event) => {
    if (event.target.value === "") {
      document.getElementById(scope === "tcp" ? "custom-strategy" : scope === "udp" ? "quic-advanced" : "application-settings").open = true;
      return;
    }
    if (event.target.value === "off") {
      if (scope === "tcp" || scope === "udp") {
        document.getElementById(scope + "_strategy").value = "off";
        delete strategySelections[scope];
      } else if (scope === "applications") {
        writeApplications([], false);
        for (const kind of applicationKinds) {
          delete strategySelections[kind.id];
        }
      }
      setDirty(true);
      updateStrategyFields();
      return;
    }
    selectPreset(event.target.value);
    if (scope === "applications" || scope === "combined") {
      event.target.value = "";
    }
  });
}

function updateProtocolOptionVisibility() {
  for (const scope of strategyScopes().filter((value) => value !== "tcp")) {
    const kind = applicationKinds.find((entry) => entry.id === scope);
    const fake = scope === "udp" ? document.getElementById("udp_strategy").value !== "off" : applicationInput(kind, "enabled").checked && applicationInput(kind, "method").value.includes("fake");
    const automatic = scopeField(scope, "auto_ttl").checked;
    for (const key of packetOptionKeys) {
      const row = scopeField(scope, key).closest("tr");
      row.hidden = scope !== "udp" && key === "fake_repeats" ? true : key === "packet_limit" ? false : !fake || key === "fake_fooling" && (scope === "udp" || kind.transport !== "tcp") || key.startsWith("auto_ttl_") && !automatic;
    }
  }
}
