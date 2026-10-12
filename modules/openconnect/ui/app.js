// SPDX-License-Identifier: GPL-3.0-or-later
"use strict";

let pending = false;

const moduleID = "openconnect";
let selectedProfile = "default";
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

  return parent.EXTENSIONS.call(moduleID, actions[operation] || operation, {
    profile: selectedProfile,
  });
}

async function uploadConfig(bytes, module, onProgress, kind = "") {
  const config = JSON.parse(new TextDecoder().decode(bytes));

  if (kind === "profile.patch") {
    return parent.EXTENSIONS.call(
      module,
      "update-settings",
      { profile: selectedProfile, data: config },
      onProgress,
    );
  }

  return parent.EXTENSIONS.call(
    module,
    "import-profile",
    { profile: selectedProfile, data: config },
    onProgress,
  );
}

function busy(value) {
  pending = value;
  settingsFields.disabled = value || !settingsLoaded;
  button.disabled = value || !file.files.length;
  document
    .querySelectorAll(
      "[data-action],#settings-save,#settings-reload,#profile-select,#profile-name,#profile-add,#profile-delete,#profile-new,#profile-cancel,#otp-submit,#otp-cancel,#dns-vpn,#dns-wan",
    )
    .forEach((node) => {
      node.disabled =
        value ||
        (node.id === "profile-delete" && selectedProfile === "default");
    });
}

function updateProfileOptions(profiles) {
  const ids = Object.keys(profiles).sort();
  const selector = document.getElementById("profile-select");
  const current = Array.from(selector.options, (option) => option.value);

  if (JSON.stringify(current) !== JSON.stringify(ids)) {
    selector.replaceChildren();

    for (const id of ids) {
      const option = document.createElement("option");

      option.value = id;
      option.textContent = id === "default" ? "Default profile" : id;
      selector.appendChild(option);
    }
  }

  selector.value = selectedProfile;
  document.getElementById("profile-delete").disabled =
    selectedProfile === "default";
}

async function loadProfiles() {
  for (let attempt = 0; attempt < 6; attempt++) {
    const snapshot = await parent.EXTENSIONS.status(moduleID);
    const profiles = snapshot.data && snapshot.data.profiles;

    if (profiles && profiles[selectedProfile]) {
      updateProfileOptions(profiles);

      return;
    }

    await new Promise((resolve) => setTimeout(resolve, 1000));
  }

  throw Error("Public status unavailable");
}

document
  .getElementById("profile-select")
  .addEventListener("change", async (event) => {
    selectedProfile = event.target.value;
    document.getElementById("profile-delete").disabled =
      selectedProfile === "default";
    settingsLoaded = false;
    await loadSettings(true);
  });

document.getElementById("profile-new").addEventListener("click", () => {
  document.getElementById("profile-create").hidden = false;
  document.getElementById("profile-name").focus();
});

document.getElementById("profile-cancel").addEventListener("click", () => {
  document.getElementById("profile-create").hidden = true;
});

document.getElementById("profile-add").addEventListener("click", async () => {
  const id = document.getElementById("profile-name").value.trim();
  if (!/^[a-z][a-z0-9-]{0,31}$/.test(id)) {
    result.textContent = "Use a lowercase profile ID, up to 32 characters.";
    return;
  }

  busy(true);

  try {
    await parent.EXTENSIONS.call(moduleID, "add-profile", { profile: id });
    selectedProfile = id;
    document.getElementById("profile-create").hidden = true;
    await loadProfiles();
    settingsLoaded = false;
  } catch (error) {
    result.textContent = error.message;
  } finally {
    busy(false);
  }

  await loadSettings(true);
});

document
  .getElementById("profile-delete")
  .addEventListener("click", async () => {
    busy(true);
    try {
      await parent.EXTENSIONS.call(moduleID, "delete-profile", {
        profile: selectedProfile,
      });
      selectedProfile = "default";
      await loadProfiles();
      settingsLoaded = false;
    } catch (error) {
      result.textContent = error.message;
    } finally {
      busy(false);
    }
    await loadSettings(true);
  });
