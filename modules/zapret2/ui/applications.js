// SPDX-License-Identifier: GPL-3.0-or-later
"use strict";

let applicationKinds = [];

const applicationMethods = {
  split: "Split packets",
  disorder: "Reordered segments",
  fake: "Fake packets",
  "fake-split": "Fake and split",
  "fake-disorder": "Fake and reorder",
  fragment: "IP fragmentation",
  "fake-fragment": "Fake packets and fragmentation",
  length: "UDP padding",
};

function applicationInput(kind, key) {
  return document.getElementById("application-" + kind.id + "-" + key);
}

function addApplicationField(parentNode, kind, key, label, type, minimum, maximum) {
  const row = document.createElement("label");
  row.className = "ext-field";
  const title = document.createElement("span");
  title.textContent = label;
  row.appendChild(title);
  const input = document.createElement("input");
  input.id = "application-" + kind.id + "-" + key;
  input.type = type;

  if (type === "number") {
    input.min = String(minimum);
    input.max = String(maximum);
  } else {
    input.maxLength = 256;
  }

  row.appendChild(input);
  parentNode.appendChild(row);
}

function buildApplicationEditors(kinds) {
  applicationKinds = kinds;
  const root = document.getElementById("application-profiles");

  for (const kind of kinds) {
    const card = document.createElement("div");
    card.className = "ocf-application-profile";
    const label = document.createElement("label");
    const enabled = document.createElement("input");
    enabled.type = "checkbox";
    enabled.id = "application-" + kind.id + "-enabled";
    label.appendChild(enabled);
    const name = document.createElement("span");
    name.textContent = kind.name;
    label.appendChild(name);
    card.appendChild(label);
    const options = document.createElement("div");
    options.id = "application-" + kind.id + "-options";
    const method = document.createElement("select");
    method.id = "application-" + kind.id + "-method";
    method.setAttribute("aria-label", kind.name);

    for (const value of kind.methods) {
      const option = document.createElement("option");
      option.value = value;
      option.textContent = applicationMethods[value];
      method.appendChild(option);
    }

    options.appendChild(method);
    const details = document.createElement("details");
    const summary = document.createElement("summary");
    summary.textContent = "Profile settings";
    details.appendChild(summary);
    const fields = document.createElement("div");
    fields.className = "ext-fields";
    addApplicationField(fields, kind, "ports", "Ports or ranges; * for all ports", "text");
    addApplicationField(fields, kind, "packets", "Initial outgoing data packets", "number", 1, 16);
    addApplicationField(fields, kind, "position", "Split byte position", "number", 1, 1500);
    addApplicationField(fields, kind, "repeats", "Fake packet repeats", "number", 1, 20);
    addApplicationField(fields, kind, "fragment", "IP fragment offset", "number", 8, 1400);
    addApplicationField(fields, kind, "padding", "UDP padding bytes", "number", 1, 256);
    details.appendChild(fields);
    options.appendChild(details);
    const note = document.createElement("p");
    note.id = "application-" + kind.id + "-padding-note";
    note.textContent = "Padding changes UDP payload length. Some protocols reject padded packets.";
    options.appendChild(note);
    card.appendChild(options);
    root.appendChild(card);
    applicationInput(kind, "fragment").step = "8";
    enabled.addEventListener("change", updateApplicationSummary);
    method.addEventListener("change", updateApplicationSummary);
  }
}

function writeApplications(profiles, merge) {
  const next = new Map();

  if (merge) {
    for (const profile of readApplications()) {
      next.set(profile.id, profile);
    }
  }

  for (const profile of profiles || []) {
    next.set(profile.id, profile);
  }

  for (const kind of applicationKinds) {
    const profile = next.get(kind.id);
    applicationInput(kind, "enabled").checked = Boolean(profile);

    for (const key of ["method", "ports", "packets", "position", "repeats", "fragment", "padding"]) {
      applicationInput(kind, key).value = profile?.[key] ?? kind.defaults[key];
    }
  }

  updateApplicationSummary();
}

function readApplications() {
  const profiles = [];

  for (const kind of applicationKinds) {
    if (!applicationInput(kind, "enabled").checked) {
      continue;
    }

    const profile = {id: kind.id, method: applicationInput(kind, "method").value};
    profile.ports = applicationInput(kind, "ports").value.trim();

    if (!/^(\*|[0-9]+(?:\s*-\s*[0-9]+)?(?:\s*,\s*[0-9]+(?:\s*-\s*[0-9]+)?)*)$/.test(profile.ports)) {
      throw Error("Invalid application ports");
    }

    for (const key of ["packets", "position", "repeats", "fragment", "padding"]) {
      profile[key] = Number(applicationInput(kind, key).value);
    }

    profiles.push(profile);
  }

  return profiles;
}

function updateApplicationSummary() {
  const summary = document.getElementById("summary-applications");
  summary.replaceChildren();
  let count = 0;

  for (const kind of applicationKinds) {
    const enabled = applicationInput(kind, "enabled").checked;
    const method = applicationInput(kind, "method").value;
    document.getElementById("application-" + kind.id + "-options").hidden = !enabled;
    const visible = {
      position: ["split", "disorder", "fake-split", "fake-disorder"].includes(method),
      repeats: method.includes("fake"),
      fragment: method.includes("fragment"),
      padding: method === "length",
    };

    for (const [key, show] of Object.entries(visible)) {
      applicationInput(kind, key).closest("label").hidden = !show;
    }

    document.getElementById("application-" + kind.id + "-padding-note").hidden = method !== "length";

    if (enabled) {
      const row = document.createElement("p");
      const name = document.createElement("span");
      name.textContent = kind.name;
      const selection = currentStrategySelection(kind.id);
      const label = document.createElement("span");
      label.textContent = selection?.name || applicationMethods[method];
      const status = document.createElement("span");
      status.textContent = selection?.checked ? "Tested by finder" : "Not tested";
      row.append(name, document.createTextNode(": "), label, document.createTextNode(" · "), status);
      summary.appendChild(row);
      count++;
    }
  }

  if (count === 0) {
    summary.textContent = "Off";
  }

  const activeRule = draftRules.find((rule) => rule.id === strategyScope);
  const manualIPs = Boolean(activeRule?.networks?.length);
  const serviceIPs = currentServiceStatuses.some((status) => activeRule?.lists?.includes(status.id) && status.networks > 0);
  document.getElementById("application-ip-notice").hidden = count === 0 || manualIPs || serviceIPs;
}
