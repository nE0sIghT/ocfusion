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
  }
}

let vpnTargets = [];
let editingRule = "";
let latestRouteStates = [];
let savedRuleRows = [];
let runtimeRuleIDs = {};
let latestModuleState = "DISABLED";

function translated(text) {
  return window.OCF_I18N ? window.OCF_I18N.translate(text, window.OCF_I18N.language) : text;
}

function openRule(id) {
  rememberStrategyScope();
  editingRule = id;
  const rule = draftRules.find((entry) => entry.id === id);
  if (rule && (!rule.action || rule.action === "dpi")) {
    showStrategyScope(id);
  }
  selectTab("management");
  renderRules();
}


function connectionMethod(rule) {
  if (rule.action === "vpn") {
    return vpnTargets.find((target) => target.id === rule.target)?.name || "VPN";
  }
  return rule.action === "normal" ? "Without processing" : "DPI bypass";
}

function renderStrategyScopes() {
  const select = document.getElementById("scan-scope");
  const selected = select.value;
  select.replaceChildren();
  const empty = document.createElement("option");
  empty.value = "";
  empty.textContent = "New rule";
  select.appendChild(empty);
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

function showStrategyScope(id) {
  strategyScope = id;
  const settings = draftRules.find((rule) => rule.id === id)?.settings || draftBaseStrategy;
  const dirty = settingsDirty;
  fillStrategySettings({name: "Custom strategy settings", protocol: "combined", settings});
  document.getElementById("split_positions").value = settings.split_positions || String(settings.split_position || 2);
  document.getElementById("preset-description").hidden = true;
  setDirty(dirty);
  renderStrategyScopes();
}

function initializeRules(saved) {
  strategyScope = "";
  editingRule = "";
  draftBaseStrategy = readStrategyForm();
  draftRules = structuredClone(saved || []);
  runtimeRuleIDs = {};
  if (config.lists?.length || config.domains?.length || config.networks?.length) {
    let id = "main";
    while (draftRules.some((rule) => rule.id === id)) {
      id += "-x";
    }
    draftRules.push({id, name: translated("Main rule"), enabled: true, action: "dpi", lists: [...(config.lists || [])], domains: [...(config.domains || [])], networks: [...(config.networks || [])], settings: structuredClone(draftBaseStrategy)});
    runtimeRuleIDs[id] = "default";
  }
  savedRuleRows = structuredClone(draftRules);

  for (const rule of draftRules) {
    rule.settings = {...structuredClone(draftBaseStrategy), ...rule.settings};
  }

  renderRules();
  renderStrategyScopes();
  refreshVPNTargets();
}

function destinationRulesConfig() {
  rememberStrategyScope();
  const checkedRule = draftRules.find((entry) => entry.id === editingRule);
  if (checkedRule) {
    checkedRule.monitor = monitoringFormConfig();
  }
  if (draftRules.some((rule) => rule.enabled && !(rule.lists?.length || rule.domains?.length || rule.networks?.length))) {
    throw new Error("Choose at least one destination");
  }
  if (draftRules.some((rule) => rule.action === "vpn" && !rule.target)) {
    throw new Error("Select a VPN");
  }
  return draftRules.map((rule) => ({
    ...structuredClone(rule),
    settings: Object.fromEntries(Object.entries(rule.settings || {}).filter(([key, value]) => JSON.stringify(value) !== JSON.stringify(draftBaseStrategy[key]))),
  }));
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
  if (key === "name") {
    input.required = true;
    input.maxLength = 64;
  }
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
  const context = document.getElementById("scan-context");
  context.textContent = rule.name;
  context.hidden = false;
  discoveryTargets = [];
  renderDiscoveryTargets();
  const suggested = moduleCatalog?.lists?.find((service) => rule.lists?.includes(service.id));
  const host = rule.domains?.[0];
  if (host) {
    document.getElementById("scan-host").value = host;
    document.getElementById("scan-target").value = "";
    document.getElementById("scan-protocol").value = "https";
    document.getElementById("scan-port").value = "443";
    document.getElementById("scan-path").value = "/";
    document.getElementById("scan-path-row").hidden = false;
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
  const title = document.createElement("div");
  title.className = "ext-card-heading";
  title.appendChild(heading);
  title.appendChild(ruleButton("Back to rules", () => {
    rememberStrategyScope();
    editingRule = "";
    renderRules();
  }));
  card.appendChild(title);
  addRuleField(card, rule, "name", "Rule name", false);
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
  custom.open = false;
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
    showStrategyScope(rule.action === "dpi" ? rule.id : "");
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
  } else if (rule.action !== "normal") {
    const actions = document.createElement("div");
    actions.className = "ocf-rule-actions";
    actions.appendChild(ruleButton("Find a strategy", () => findForRule(rule)));
    card.appendChild(actions);
    const strategy = document.getElementById("panel-strategies");
    strategy.hidden = false;
    card.appendChild(strategy);
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
  card.appendChild(actions);
  root.appendChild(card);
  card.appendChild(document.getElementById("rule-checks"));
  loadMonitoringForm(rule.monitor);
  renderHealth(latestHealth);
}

function effectiveRuleStatus(rule, dirty = false) {
  if (!document.getElementById("enabled").checked) {
    return "Processing paused";
  }
  if (dirty) {
    return "Unsaved changes";
  }
  if (!rule.enabled) {
    return "Disabled";
  }
  if (!rule.domains?.length && !rule.networks?.length && rule.lists?.some((id) => !currentServiceStatuses.find((entry) => entry.id === id)?.updated)) {
    return "Waiting for service lists";
  }
  const status = latestRouteStates.find((entry) => entry.id === (runtimeRuleIDs[rule.id] || rule.id));
  if (status?.pending && !status.addresses) {
    return status.failed ? "DNS lookup failed" : "Resolving domains";
  }
  if (latestModuleState === "ERROR") {
    return "Could not apply rules";
  }
  if (status) {
    return {VPN: "Through VPN", WAN: "Through WAN", BLOCKED: "Blocked", DPI: "DPI bypass", NORMAL: "Without processing"}[status.state] || "Waiting to apply";
  }
  if (["RUNNING", "EMPTY"].includes(latestModuleState) && rule.action !== "vpn") {
    return rule.action === "normal" ? "Without processing" : "Running";
  }
  return "Waiting to apply";
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
    row.children[1].textContent = rule.name;
    const targets = [...(rule.lists || []).map((id) => moduleCatalog.lists.find((service) => service.id === id)?.name || id), ...(rule.domains || []), ...(rule.networks || [])];
    row.children[2].textContent = targets.slice(0, 2).join(", ") + (targets.length > 2 ? " (+" + (targets.length - 2) + ")" : "") || "No destinations";
    row.children[3].textContent = connectionMethod(rule);
    if (rule.action === "vpn") {
      const fallback = document.createElement("small");
      fallback.textContent = rule.failure === "wan" ? "Fallback: WAN" : "On disconnect: block";
      row.children[3].appendChild(fallback);
    }
    row.children[4].textContent = effectiveRuleStatus(rule, settingsDirty);
    const status = latestRouteStates.find((entry) => entry.id === rule.id);
    if (status?.stale && rule.enabled && !settingsDirty) {
      const cached = document.createElement("small");
      cached.textContent = "Using saved IP addresses";
      row.children[4].appendChild(cached);
    }
  }
  renderRuleDiagnostics();
}

function renderRuleDiagnostics() {
  const root = document.getElementById("diagnostic-rules");
  root.replaceChildren();
  for (const rule of savedRuleRows) {
    const row = document.createElement("div");
    row.className = "ocf-diagnostic-rule";
    const title = document.createElement("strong");
    title.textContent = rule.name;
    const method = document.createElement("span");
    method.textContent = connectionMethod(rule);
    const status = document.createElement("span");
    status.textContent = effectiveRuleStatus(rule);
    row.append(title, method, status);
    root.appendChild(row);
  }
  const problem = document.getElementById("diagnostic-problem");
  problem.hidden = latestModuleState !== "ERROR";
  problem.textContent = "Could not apply rules";
}

function renderRules() {
  document.getElementById("checks-storage").appendChild(document.getElementById("rule-checks"));
  const strategy = document.getElementById("panel-strategies");
  strategy.hidden = true;
  document.getElementById("strategy-storage").appendChild(strategy);
  const root = document.getElementById("destination-rules");
  root.replaceChildren();
  const rule = draftRules.find((entry) => entry.id === editingRule);
  document.getElementById("rules-empty").hidden = draftRules.length > 0 || Boolean(rule);
  document.getElementById("rule-add").hidden = Boolean(rule);
  document.getElementById("unmatched-note").hidden = Boolean(rule);
  if (rule) {
    renderRuleEditor(root, rule);
  } else if (draftRules.length) {
    const table = document.createElement("table");
    table.className = "ocf-rule-table";
    const head = table.createTHead().insertRow();
    for (const label of ["Enabled", "Rule", "Destinations", "Connection", "Status", "Actions"]) {
      const cell = document.createElement("th");
      cell.textContent = label;
      head.appendChild(cell);
    }
    const body = table.createTBody();
    for (const entry of draftRules) {
      const row = body.insertRow();
      row.dataset.ruleRow = entry.id;
      const toggle = document.createElement("input");
      toggle.type = "checkbox";
      toggle.checked = entry.enabled;
      toggle.setAttribute("aria-label", entry.name);
      toggle.addEventListener("change", () => {
        entry.enabled = toggle.checked;
        setDirty(true);
        updateRuleRows();
      });
      row.insertCell().appendChild(toggle);
      for (let index = 0; index < 4; index++) {
        row.insertCell();
      }
      row.insertCell().appendChild(ruleButton("Edit", () => openRule(entry.id)));
      for (const [index, label] of ["Enabled", "Rule", "Destinations", "Connection", "Status", "Actions"].entries()) {
        row.children[index].dataset.label = translated(label);
      }
    }
    root.appendChild(table);
  }
  updateRuleRows();
  document.getElementById("rule-add").disabled = draftRules.length >= 7;
}

document.getElementById("rule-add").addEventListener("click", () => {
  if (draftRules.length >= 7) {
    return;
  }
  rememberStrategyScope();
  const name = window.OCF_I18N ? window.OCF_I18N.translate("New rule", window.OCF_I18N.language) : "New rule";
  const rule = {id: "rule-" + Date.now().toString(36) + Math.random().toString(36).slice(2, 7), name, enabled: true, action: "dpi", domains: [], networks: [], lists: [], settings: structuredClone(draftBaseStrategy)};
  draftRules.push(rule);
  renderStrategyScopes();
  openRule(rule.id);
  setDirty(true);
});
