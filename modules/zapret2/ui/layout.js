// SPDX-License-Identifier: GPL-3.0-or-later
"use strict";

const tabs = [...document.querySelectorAll('[role="tab"]')];
let settingsDirty = false;

for (const option of document.querySelectorAll("#tcp_strategy option, #udp_strategy option")) {
  option.dataset.label = option.textContent;
}

function selectTab(id) {
  if (id === "diagnostics") {
    returnChecksToDiagnostics();
  }
  for (const tab of tabs) {
    const active = tab.id === "tab-" + id;
    tab.setAttribute("aria-selected", String(active));
    tab.tabIndex = active ? 0 : -1;
    document.getElementById(tab.getAttribute("aria-controls")).hidden = !active;
  }

  document.getElementById("settings").hidden = id !== "management";
}

function setDirty(dirty) {
  settingsDirty = dirty;
  document.getElementById("unsaved").hidden = !dirty;
  document.getElementById("save-bar").hidden = !dirty;
}

for (const tab of tabs) {
  tab.addEventListener("click", () => selectTab(tab.id.slice(4)));
  tab.addEventListener("keydown", (event) => {
    let index = tabs.indexOf(tab);

    if (event.key === "ArrowRight") {
      index = (index + 1) % tabs.length;
    } else if (event.key === "ArrowLeft") {
      index = (index + tabs.length - 1) % tabs.length;
    } else if (event.key === "Home") {
      index = 0;
    } else if (event.key === "End") {
      index = tabs.length - 1;
    } else {
      return;
    }

    event.preventDefault();
    selectTab(tabs[index].id.slice(4));
    tabs[index].focus();
  });
}

document.getElementById("settings").addEventListener("input", () => setDirty(true));
window.addEventListener("beforeunload", (event) => {
  if (settingsDirty) {
    event.preventDefault();
    event.returnValue = "";
  }
});

document.getElementById("copy-report").addEventListener("click", async () => {
  const button = document.getElementById("copy-report");
  const status = document.getElementById("report-result");
  const report = document.getElementById("diagnostic-report");
  button.disabled = true;
  status.textContent = "";
  try {
    const snapshot = await parent.EXTENSIONS.status(moduleID);
    report.value = JSON.stringify(snapshot.data || {}, null, 2);
    try {
      await navigator.clipboard.writeText(report.value);
      report.hidden = true;
      button.textContent = "Copied";
      setTimeout(() => {
        button.textContent = "Copy report";
      }, 2000);
    } catch (error) {
      report.hidden = false;
      report.focus();
      report.select();
      status.textContent = "Select and copy the report.";
    }
  } catch (error) {
    status.textContent = "Report unavailable";
  } finally {
    button.disabled = false;
  }
});
