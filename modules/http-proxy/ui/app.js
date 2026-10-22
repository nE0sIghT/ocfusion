// SPDX-License-Identifier: GPL-3.0-or-later
"use strict";

const moduleID = "http-proxy";
const fields = document.getElementById("fields");
const result = document.getElementById("result");
let config = null;
let pending = false;

function options(id, items, selected) {
  const select = document.getElementById(id);
  select.replaceChildren();

  for (const item of items) {
    const option = document.createElement("option");
    option.value = item.value;
    option.textContent = item.label;
    select.appendChild(option);
  }

  select.value = selected;
}

async function load() {
  if (pending) {
    return;
  }

  pending = true;
  fields.disabled = true;

  try {
    config = await parent.EXTENSIONS.action(moduleID, "settings");
    const catalog = await parent.EXTENSIONS.action(moduleID, "targets");
    options(
      "target",
      [
        { value: "", label: "Select a VPN connection" },
        ...(catalog.targets || []).map((item) => ({
          value: item.id,
          label: item.name + " (" + item.protocol + ")",
        })),
      ],
      config.target,
    );
    options(
      "bridge",
      (catalog.networks || []).map((item) => ({
        value: item.bridge,
        label: item.bridge + " - " + item.cidr,
      })),
      config.bridge,
    );
    document.getElementById("enabled").checked = config.enabled;
    document.getElementById("port").value = config.port;
    document.getElementById("allowed").value = (config.allowed || []).join("\n");
    document.getElementById("dns").value = (config.dns || []).join("\n");
    result.textContent = "";
  } catch (error) {
    result.textContent = error.message;
  } finally {
    pending = false;
    fields.disabled = !config;
  }
}

function lines(id) {
  return document
    .getElementById(id)
    .value.split(/\r?\n/)
    .map((line) => line.trim())
    .filter(Boolean);
}

document.getElementById("settings").addEventListener("submit", async (event) => {
  event.preventDefault();

  if (pending || !config) {
    return;
  }

  pending = true;
  fields.disabled = true;
  result.textContent = "";

  try {
    await parent.EXTENSIONS.configure(moduleID, {
      revision: config.revision + 1,
      enabled: document.getElementById("enabled").checked,
      target: document.getElementById("target").value,
      bridge: document.getElementById("bridge").value,
      port: Number(document.getElementById("port").value),
      allowed: lines("allowed"),
      dns: lines("dns"),
    });
    pending = false;
    await load();
  } catch (error) {
    result.textContent = error.message;
  } finally {
    pending = false;
    fields.disabled = false;
  }
});

document.getElementById("reload").addEventListener("click", load);
load();

async function updateStatus() {
  try {
    const snapshot = await parent.EXTENSIONS.status(moduleID);
    const data = snapshot.data;
    const labels = {
      DISABLED: "Disabled",
      WAITING_VPN: "Waiting for VPN",
      RUNNING: "Running",
      UNAVAILABLE: "Unavailable",
      CONFIG_ERROR: "DNS servers must be external to the router.",
    };
    document.getElementById("state").textContent = data
      ? labels[data.state] || "Unavailable"
      : "Unavailable";
    document.getElementById("endpoint").textContent = data?.address
      ? "http://" + data.address + ":" + data.port
      : "";
  } catch (error) {
    document.getElementById("state").textContent = "Unavailable";
  }
}

updateStatus();
setInterval(updateStatus, 3000);
