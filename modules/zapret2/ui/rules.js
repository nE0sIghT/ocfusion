// SPDX-License-Identifier: GPL-3.0-or-later
"use strict";

let draftRules = [];
let draftBaseStrategy = {};
let strategyScope = "";

function readStrategyForm() {
  return {
    applications: readApplications(),
    tcp_ports: ports("tcp_ports"),
    udp_ports: ports("udp_ports"),
    tcp_strategy: document.getElementById("tcp_strategy").value,
    udp_strategy: document.getElementById("udp_strategy").value,
    split_position: config?.split_position || 2,
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
}

function rememberStrategyScope() {
  const settings = readStrategyForm();
  const rule = draftRules.find((entry) => entry.id === strategyScope);

  if (rule) {
    rule.settings = settings;
  } else {
    draftBaseStrategy = settings;
  }
}

let vpnTargets = [];
let editingRule = "";
let latestRouteStates = [];

function connectionMethod(rule) {
  if (rule.action === "vpn") {
    return vpnTargets.find((target) => target.id === rule.target)?.name || "VPN";
  }
  return rule.action === "normal" ? "Without processing" : "DPI bypass";
}

function renderStrategyScopes() {
  for (const id of ["strategy-scope", "scan-scope"]) {
    const select = document.getElementById(id);
    const selected = id === "strategy-scope" ? strategyScope : select.value;
    select.replaceChildren();
    const base = document.createElement("option");
    base.value = "";
    base.textContent = "Default strategy";
    select.appendChild(base);

    for (const rule of draftRules) {
      if (rule.action && rule.action !== "dpi") {
        continue;
      }
      const option = document.createElement("option");
      option.value = rule.id;
      option.textContent = rule.name;
      select.appendChild(option);
    }

    select.value = [...select.options].some((option) => option.value === selected) ? selected : "";
  }
}

function showStrategyScope(id) {
  strategyScope = id;
  const settings = draftRules.find((rule) => rule.id === id)?.settings || draftBaseStrategy;
  const dirty = settingsDirty;
  fillStrategySettings({name: "Custom strategy settings", protocol: "combined", settings});
  document.getElementById("split_positions").value = settings.split_positions || String(settings.split_position || 2);
  setDirty(dirty);
  renderStrategyScopes();
}

function initializeRules(saved) {
  strategyScope = "";
  editingRule = "";
  draftBaseStrategy = readStrategyForm();
  draftRules = structuredClone(saved || []);

  for (const rule of draftRules) {
    rule.settings = {...structuredClone(draftBaseStrategy), ...rule.settings};
  }

  renderRules();
  renderStrategyScopes();
  refreshVPNTargets();
}

function destinationRulesConfig() {
  rememberStrategyScope();
  if (draftRules.some((rule) => rule.action === "vpn" && !rule.target)) {
    throw new Error("Select a VPN");
  }
  return structuredClone(draftRules);
}

async function refreshVPNTargets() {
  try {
    const targets = await parent.EXTENSIONS.action(moduleID, "vpn-targets");
    vpnTargets = Array.isArray(targets) ? targets : [];
  } catch (error) {
    vpnTargets = [];
  }

  updateRuleRows();
  const select = document.getElementById("rule-vpn-target");
  if (select) {
    fillVPNChoices(select, draftRules.find((rule) => rule.id === editingRule));
  }
}

function fillVPNChoices(select, rule) {
  if (!rule) {
    return;
  }
  select.replaceChildren();
  const empty = document.createElement("option");
  empty.value = "";
  empty.textContent = "Select a VPN";
  select.appendChild(empty);

  for (const target of vpnTargets) {
    const option = document.createElement("option");
    option.value = target.id;
    option.textContent = target.name;
    select.appendChild(option);
  }

  if (rule.target && !vpnTargets.some((target) => target.id === rule.target)) {
    const unavailable = document.createElement("option");
    unavailable.value = rule.target;
    unavailable.textContent = "Saved VPN unavailable";
    select.appendChild(unavailable);
  }
  select.value = rule.target || "";
}

function fieldLabel(parent, title, input) {
  const label = document.createElement("label");
  label.className = "ext-field";
  const text = document.createElement("span");
  text.textContent = title;
  label.append(text, input);
  parent.appendChild(label);
  return input;
}

function addRuleField(card, rule, key, title, multiline) {
  const input = document.createElement(multiline ? "textarea" : "input");
  input.value = multiline ? (rule[key] || []).join("\n") : rule[key];
  input.dataset.ruleField = key;
  if (multiline) {
    input.rows = 3;
  }
  input.addEventListener("input", () => {
    rule[key] = multiline ? input.value.split(/\r?\n/).map((line) => line.trim()).filter(Boolean) : input.value;
    setDirty(true);
    updateRuleRows();
    if (key === "name") {
      renderStrategyScopes();
    }
  });
  fieldLabel(card, title, input);
}

function ruleButton(label, action, disabled = false) {
  const button = document.createElement("button");
  button.type = "button";
  button.className = "ext-secondary";
  button.textContent = label;
  button.disabled = disabled;
  button.addEventListener("click", action);
  return button;
}

function findForRule(rule) {
  rememberStrategyScope();
  document.getElementById("scan-scope").value = rule.id;
  const suggested = moduleCatalog?.lists?.find((service) => rule.lists?.includes(service.id));
  const host = rule.domains?.[0];
  if (host) {
    discoveryTargets = [{host, port: 443, protocol: "https", path: "/"}];
    renderDiscoveryTargets();
  } else if (suggested) {
    const group = [...document.getElementById("scan-target").querySelectorAll("optgroup")].find((entry) => entry.label === suggested.name);
    const option = group?.querySelector("option");
    if (option) {
      document.getElementById("scan-target").value = option.value;
      document.getElementById("scan-target").dispatchEvent(new Event("change"));
    }
  }
  selectTab("finder");
}

function renderRuleEditor(root, rule) {
  const card = document.createElement("section");
  card.className = "ocf-rule-editor";
  card.dataset.ruleId = rule.id;
  const heading = document.createElement("h2");
  heading.textContent = "Edit rule";
  card.appendChild(heading);
  addRuleField(card, rule, "name", "Rule name", false);
  const enabled = document.createElement("input");
  enabled.type = "checkbox";
  enabled.checked = rule.enabled;
  enabled.addEventListener("change", () => {
    rule.enabled = enabled.checked;
    updateRuleRows();
    setDirty(true);
  });
  fieldLabel(card, "Enable rule", enabled);

  const services = document.createElement("div");
  services.className = "ocf-service-choices";
  const legend = document.createElement("h3");
  legend.textContent = "Services";
  services.appendChild(legend);
  for (const service of moduleCatalog.lists) {
    const input = document.createElement("input");
    input.type = "checkbox";
    input.checked = rule.lists?.includes(service.id) || false;
    input.dataset.service = service.id;
    input.addEventListener("change", () => {
      rule.lists = [...services.querySelectorAll("input:checked")].map((entry) => entry.dataset.service);
      updateRuleRows();
      setDirty(true);
    });
    fieldLabel(services, service.name, input);
  }
  card.appendChild(services);
  const custom = document.createElement("details");
  const summary = document.createElement("summary");
  summary.textContent = "Custom addresses";
  custom.appendChild(summary);
  custom.open = Boolean(rule.domains?.length || rule.networks?.length);
  addRuleField(custom, rule, "domains", "Domains", true);
  addRuleField(custom, rule, "networks", "IP addresses and subnets", true);
  card.appendChild(custom);

  const method = document.createElement("select");
  method.dataset.ruleField = "action";
  for (const [value, title] of [["dpi", "DPI bypass"], ["vpn", "Through VPN"], ["normal", "Without processing"]]) {
    const option = document.createElement("option");
    option.value = value;
    option.textContent = title;
    method.appendChild(option);
  }
  method.value = rule.action || "dpi";
  method.addEventListener("change", () => {
    rememberStrategyScope();
    rule.action = method.value;
    if (rule.action !== "vpn") {
      delete rule.target;
      delete rule.failure;
    } else {
      rule.failure = "block";
    }
    if (strategyScope === rule.id && rule.action !== "dpi") {
      showStrategyScope("");
    }
    renderRules();
    renderStrategyScopes();
    setDirty(true);
  });
  fieldLabel(card, "Connection method", method);

  if (rule.action === "vpn") {
    const target = document.createElement("select");
    target.id = "rule-vpn-target";
    target.dataset.ruleField = "target";
    fillVPNChoices(target, rule);
    target.addEventListener("change", () => {
      rule.target = target.value;
      updateRuleRows();
      setDirty(true);
    });
    fieldLabel(card, "VPN profile", target);
    const failure = document.createElement("select");
    failure.dataset.ruleField = "failure";
    for (const [value, label] of [["block", "Block connections"], ["wan", "Use WAN"]]) {
      const option = document.createElement("option");
      option.value = value;
      option.textContent = label;
      failure.appendChild(option);
    }
    failure.value = rule.failure || "block";
    failure.addEventListener("change", () => {
      rule.failure = failure.value;
      setDirty(true);
    });
    fieldLabel(card, "If VPN is unavailable", failure);
    const note = document.createElement("p");
    note.className = "ext-muted";
    note.textContent = "Applies to WAN traffic. Domains use periodically refreshed IP addresses; shared IPs may include other sites.";
    card.appendChild(note);
    const ipv6 = document.createElement("p");
    ipv6.className = "ext-muted";
    ipv6.textContent = "IPv6 to selected addresses is blocked while using an IPv4 VPN.";
    card.appendChild(ipv6);
  } else if (rule.action !== "normal") {
    const actions = document.createElement("div");
    actions.className = "ocf-rule-actions";
    actions.appendChild(ruleButton("Choose strategy", () => {
      rememberStrategyScope();
      showStrategyScope(rule.id);
      selectTab("strategies");
    }));
    actions.appendChild(ruleButton("Find a strategy", () => findForRule(rule)));
    card.appendChild(actions);
  }

  const actions = document.createElement("div");
  actions.className = "ocf-rule-actions";
  const index = draftRules.indexOf(rule);
  for (const [label, offset] of [["Move up", -1], ["Move down", 1]]) {
    actions.appendChild(ruleButton(label, () => {
      [draftRules[index], draftRules[index + offset]] = [draftRules[index + offset], rule];
      renderRules();
      setDirty(true);
    }, index + offset < 0 || index + offset >= draftRules.length));
  }
  const checks = ruleButton("Set up checks", () => setupRuleChecks(rule));
  checks.dataset.ruleAction = "checks";
  actions.appendChild(checks);
  actions.appendChild(ruleButton("Remove", () => {
    draftRules.splice(index, 1);
    if (strategyScope === rule.id) {
      showStrategyScope("");
    }
    editingRule = "";
    renderRules();
    renderStrategyScopes();
    setDirty(true);
  }));
  actions.appendChild(ruleButton("Done", () => {
    editingRule = "";
    renderRules();
  }));
  card.appendChild(actions);
  root.appendChild(card);
}

function updateRuleRows(states) {
  if (states) {
    latestRouteStates = states;
  }
  for (const rule of draftRules) {
    const row = document.querySelector('[data-rule-row="' + rule.id + '"]');
    if (!row) {
      continue;
    }
    row.children[0].textContent = rule.name;
    const targets = [...(rule.lists || []).map((id) => moduleCatalog.lists.find((service) => service.id === id)?.name || id), ...(rule.domains || []), ...(rule.networks || [])];
    row.children[1].textContent = targets.slice(0, 2).join(", ") + (targets.length > 2 ? " (+" + (targets.length - 2) + ")" : "") || "No destinations";
    row.children[2].textContent = connectionMethod(rule);
    const status = latestRouteStates.find((entry) => entry.id === rule.id);
    const labels = {VPN: "VPN", WAN: "WAN", BLOCKED: "Blocked", DPI: "DPI bypass", NORMAL: "Without processing"};
    row.children[3].textContent = !rule.enabled || !document.getElementById("enabled").checked ? "Disabled" : settingsDirty ? "Unsaved changes" : status?.pending && !status.addresses ? (status.failed ? "DNS lookup failed" : "Resolving domains") : labels[status?.state] || "Not checked";
    if (status?.pending && status.addresses && rule.enabled && !settingsDirty) {
      const pending = document.createElement("small");
      pending.textContent = status.failed ? "DNS lookup failed" : "Resolving domains";
      row.children[3].appendChild(pending);
    }
    if (status?.stale && rule.enabled && !settingsDirty) {
      const cached = document.createElement("small");
      cached.textContent = "Using saved IP addresses";
      row.children[3].appendChild(cached);
    }
  }
}

function renderRules() {
  const root = document.getElementById("destination-rules");
  root.replaceChildren();
  document.getElementById("rules-empty").hidden = draftRules.length > 0;
  const table = document.createElement("table");
  table.className = "FormTable ocf-rule-table";
  const head = table.createTHead().insertRow();
  for (const label of ["Rule", "Destinations", "Connection", "Status", ""]) {
    const cell = document.createElement("th");
    cell.textContent = label;
    head.appendChild(cell);
  }
  const body = table.createTBody();
  for (const rule of draftRules) {
    const row = body.insertRow();
    row.dataset.ruleRow = rule.id;
    for (let index = 0; index < 4; index++) {
      row.insertCell();
    }
    row.insertCell().appendChild(ruleButton("Edit", () => {
      editingRule = rule.id;
      renderRules();
    }));
  }
  table.hidden = draftRules.length === 0;
  const scroll = document.createElement("div");
  scroll.className = "ext-table-scroll";
  scroll.appendChild(table);
  root.appendChild(scroll);
  const rule = draftRules.find((entry) => entry.id === editingRule);
  if (rule) {
    renderRuleEditor(root, rule);
  }
  updateRuleRows();
  document.getElementById("rule-add").disabled = draftRules.length >= 6;
}

document.getElementById("rule-add").addEventListener("click", () => {
  if (draftRules.length >= 6) {
    return;
  }
  rememberStrategyScope();
  const name = window.OCF_I18N ? window.OCF_I18N.translate("New rule", window.OCF_I18N.language) : "New rule";
  const rule = {id: "rule-" + Date.now().toString(36) + Math.random().toString(36).slice(2, 7), name, enabled: true, action: "dpi", domains: [], networks: [], lists: [], settings: structuredClone(draftBaseStrategy)};
  draftRules.push(rule);
  editingRule = rule.id;
  renderRules();
  renderStrategyScopes();
  setDirty(true);
});

document.getElementById("strategy-scope").addEventListener("change", (event) => {
  rememberStrategyScope();
  showStrategyScope(event.target.value);
});
