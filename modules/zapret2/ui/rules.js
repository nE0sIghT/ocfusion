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

function renderStrategyScopes() {
  const select = document.getElementById("strategy-scope");
  select.replaceChildren();
  const base = document.createElement("option");
  base.value = "";
  base.textContent = "Default strategy";
  select.appendChild(base);

  for (const rule of draftRules) {
    const option = document.createElement("option");
    option.value = rule.id;
    option.textContent = rule.name;
    select.appendChild(option);
  }

  select.value = strategyScope;
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
  draftBaseStrategy = readStrategyForm();
  draftRules = structuredClone(saved || []);

  for (const rule of draftRules) {
    rule.settings = {...structuredClone(draftBaseStrategy), ...rule.settings};
  }

  renderRules();
  renderStrategyScopes();
}

function destinationRulesConfig() {
  rememberStrategyScope();
  return structuredClone(draftRules);
}

function addRuleField(card, rule, key, title, multiline) {
  const label = document.createElement("label");
  label.className = "ext-field";
  const text = document.createElement("span");
  text.textContent = title;
  label.appendChild(text);
  const input = document.createElement(multiline ? "textarea" : "input");
  input.value = multiline ? (rule[key] || []).join("\n") : rule[key];
  input.maxLength = multiline ? 2048 : 64;
  input.dataset.ruleField = key;

  if (multiline) {
    input.rows = 3;
  }

  input.addEventListener("input", () => {
    rule[key] = multiline ? input.value.split(/\r?\n/).map((line) => line.trim()).filter(Boolean) : input.value;
    setDirty(true);

    if (key === "name") {
      card.querySelector("summary").textContent = input.value;
      renderStrategyScopes();
    }
  });
  label.appendChild(input);
  card.appendChild(label);
}

function renderRules() {
  const root = document.getElementById("destination-rules");
  root.replaceChildren();

  for (const [index, rule] of draftRules.entries()) {
    const card = document.createElement("details");
    card.dataset.ruleId = rule.id;
    const summary = document.createElement("summary");
    summary.textContent = rule.name;
    card.appendChild(summary);
    const enableLabel = document.createElement("label");
    const enabled = document.createElement("input");
    enabled.type = "checkbox";
    enabled.checked = rule.enabled;
    enabled.addEventListener("change", () => {
      rule.enabled = enabled.checked;
      setDirty(true);
    });
    enableLabel.appendChild(enabled);
    const enableText = document.createElement("span");
    enableText.textContent = "Enable rule";
    enableLabel.appendChild(enableText);
    card.appendChild(enableLabel);
    addRuleField(card, rule, "name", "Rule name", false);
    addRuleField(card, rule, "domains", "Domains", true);
    addRuleField(card, rule, "networks", "IP addresses and subnets", true);
    const serviceLabel = document.createElement("label");
    serviceLabel.className = "ext-field";
    const serviceTitle = document.createElement("span");
    serviceTitle.textContent = "Service lists";
    serviceLabel.appendChild(serviceTitle);
    const services = document.createElement("select");
    services.multiple = true;
    services.size = 4;
    services.dataset.ruleField = "lists";

    for (const service of moduleCatalog.lists) {
      const option = document.createElement("option");
      option.value = service.id;
      option.textContent = service.name;
      option.selected = rule.lists?.includes(service.id) || false;
      services.appendChild(option);
    }

    services.addEventListener("change", () => {
      rule.lists = [...services.selectedOptions].map((option) => option.value);
      setDirty(true);
    });
    serviceLabel.appendChild(services);
    card.appendChild(serviceLabel);
    const actions = document.createElement("div");
    actions.className = "ocf-rule-actions";
    const definitions = [
      {label: "Edit strategy", action: () => {
        rememberStrategyScope();
        showStrategyScope(rule.id);
        selectTab("strategies");
      }},
      {label: "Move up", disabled: index === 0, action: () => {
        [draftRules[index - 1], draftRules[index]] = [draftRules[index], draftRules[index - 1]];
        renderRules();
        setDirty(true);
      }},
      {label: "Move down", disabled: index === draftRules.length - 1, action: () => {
        [draftRules[index + 1], draftRules[index]] = [draftRules[index], draftRules[index + 1]];
        renderRules();
        setDirty(true);
      }},
      {label: "Remove", action: () => {
        draftRules.splice(index, 1);

        if (strategyScope === rule.id) {
          showStrategyScope("");
        }

        renderRules();
        renderStrategyScopes();
        setDirty(true);
      }},
    ];

    for (const definition of definitions) {
      const button = document.createElement("button");
      button.type = "button";
      button.textContent = definition.label;
      button.className = "ext-secondary";
      button.disabled = definition.disabled || false;
      button.addEventListener("click", definition.action);
      actions.appendChild(button);
    }

    card.appendChild(actions);
    root.appendChild(card);
  }

  document.getElementById("rule-add").disabled = draftRules.length >= 6;
}

document.getElementById("rule-add").addEventListener("click", () => {
  if (draftRules.length >= 6) {
    return;
  }

  rememberStrategyScope();
  const name = window.OCF_I18N ? window.OCF_I18N.translate("New rule", window.OCF_I18N.language) : "New rule";
  const rule = {id: "rule-" + Date.now().toString(36) + Math.random().toString(36).slice(2, 7), name, enabled: true, domains: [], networks: [], lists: [], settings: structuredClone(readStrategyForm())};
  draftRules.push(rule);
  renderRules();
  renderStrategyScopes();
  document.querySelector('[data-rule-id="' + rule.id + '"]').open = true;
  setDirty(true);
});

document.getElementById("strategy-scope").addEventListener("change", (event) => {
  rememberStrategyScope();
  showStrategyScope(event.target.value);
});
