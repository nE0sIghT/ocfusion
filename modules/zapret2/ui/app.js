// SPDX-License-Identifier: GPL-3.0-or-later
"use strict";

const moduleID = "zapret2";
const fields = document.getElementById("fields");
const result = document.getElementById("result");
let config = null;
let pending = false;

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

async function load() {
  if (pending) {
    return;
  }

  pending = true;
  fields.disabled = true;

  try {
    config = await parent.EXTENSIONS.action(moduleID, "settings");
    document.getElementById("enabled").checked = config.enabled;

    for (const key of ["networks", "domains"]) {
      document.getElementById(key).value = (config[key] || []).join("\n");
    }

    for (const key of ["tcp_ports", "udp_ports"]) {
      document.getElementById(key).value = (config[key] || []).join(", ");
    }

    for (const key of ["tcp_strategy", "udp_strategy", "split_position", "fake_ttl", "fake_repeats"]) {
      document.getElementById(key).value = config[key];
    }

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
      split_position: Number(document.getElementById("split_position").value),
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
  } catch (error) {
    document.getElementById("state").textContent = "Unavailable";
  }
}

document.getElementById("reload").addEventListener("click", load);
load();
updateStatus();
setInterval(updateStatus, 3000);
