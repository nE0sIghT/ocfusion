// SPDX-License-Identifier: GPL-3.0-or-later
"use strict";

let settingsDirty = false;

const tabs = [...document.querySelectorAll('[role="tab"]')];

function selectTab(tab) {
  for (const item of tabs) {
    const active = item === tab;

    item.className = active ? "tabClicked" : "tab";
    item.setAttribute("aria-selected", String(active));
    item.tabIndex = active ? 0 : -1;
    document.getElementById(item.getAttribute("aria-controls")).hidden =
      !active;
  }

  if (tab.id === "tab-networks") {
    loadDNSPolicy();
  }
}

for (const tab of tabs) {
  tab.addEventListener("click", () => {
    selectTab(tab);
  });
  tab.addEventListener("keydown", (event) => {
    const offset =
      event.key === "ArrowRight" ? 1 : event.key === "ArrowLeft" ? -1 : 0;
    if (!offset) {
      return;
    }

    event.preventDefault();
    const next = tabs[(tabs.indexOf(tab) + offset + tabs.length) % tabs.length];

    selectTab(next);
    next.focus();
  });
}

function confirmSettingsDiscard() {
  return !settingsDirty || confirmUI("Discard unsaved changes?");
}

function updateAuthenticationFields() {
  const certificate =
    document.getElementById("vpn-auth").value === "certificate";

  const passwordField = document
    .getElementById("vpn-password")
    .closest(".ext-field");

  // Some certificate-based servers also request a password or OTP password.
  if (certificate) {
    document
      .querySelector("#settings-fields > details .ext-fields")
      .append(passwordField);
  } else {
    document
      .getElementById("vpn-username")
      .closest(".ext-field")
      .after(passwordField);
  }
  for (const id of ["vpn-certificate", "vpn-private-key", "clear-client"]) {
    document.getElementById(id).closest(".ext-field").hidden = !certificate;
  }
}

for (const event of ["input", "change"]) {
  document.addEventListener(event, (event) => {
    if (event.target.form === settingsForm) {
      settingsDirty = true;
    }
  });
}

document
  .getElementById("vpn-auth")
  .addEventListener("change", updateAuthenticationFields);
window.addEventListener("beforeunload", (event) => {
  if (settingsDirty) {
    event.preventDefault();
    event.returnValue = "";
  }
});

document
  .getElementById("diagnostics-copy")
  .addEventListener("click", async () => {
    try {
      await navigator.clipboard.writeText(view.textContent);
    } catch {
      const selection = window.getSelection();
      const range = document.createRange();

      view.closest("details").open = true;
      range.selectNodeContents(view);
      selection.removeAllRanges();
      selection.addRange(range);
    }
  });

async function loadDNSPolicy() {
  if (pending) {
    return;
  }

  busy(true);
  try {
    const status = await request("dns-state");
    renderDNSStatus({ dns_path: status.path, dns_reason: status.reason });
    const policy = status.policy;

    document.getElementById("dns-policy").value = !policy
      ? ""
      : policy.client === moduleID && policy.profile === selectedProfile
        ? "dns-vpn"
        : !policy.client
          ? "dns-wan"
          : "";
  } catch (error) {
    renderDNSStatus({});
  } finally {
    busy(false);
  }
}

function confirmUI(message) {
  const translated = window.OCF_I18N
    ? window.OCF_I18N.translate(message, window.OCF_I18N.language)
    : message;

  return confirm(translated);
}

function setButtonPending(button, label) {
  const original = button.textContent;
  button.textContent = label;
  button.setAttribute("aria-busy", "true");

  return () => {
    button.textContent = original;
    button.removeAttribute("aria-busy");
  };
}

function showSaved(node, message) {
  node.textContent = message;
  setTimeout(() => {
    if (node.textContent === message) {
      node.textContent = "";
    }
  }, 4000);
}
