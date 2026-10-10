// SPDX-License-Identifier: GPL-3.0-or-later
"use strict";

let pending = false;

const moduleID = "openconnect";
const result = document.getElementById("result");
const view = document.getElementById("status");
const button = document.getElementById("import");
const file = document.getElementById("file");

async function request(operation) {
  const actions = {
    enable: "connect",
    disable: "disconnect",
    status: "diagnose",
  };

  return parent.EXTENSIONS.action(moduleID, actions[operation] || operation);
}

async function uploadConfig(bytes, module, onProgress, kind = "") {
  const config = JSON.parse(new TextDecoder().decode(bytes));

  if (kind === "profile.patch") {
    return parent.EXTENSIONS.call(
      module,
      "update-settings",
      config,
      onProgress,
    );
  }

  return parent.EXTENSIONS.configure(module, config, onProgress);
}

function busy(value) {
  pending = value;
  settingsFields.disabled = value || !settingsLoaded;
  button.disabled = value || !file.files.length;
  document
    .querySelectorAll("[data-action],#settings-save,#settings-reload")
    .forEach((node) => {
      node.disabled = value;
    });
}
