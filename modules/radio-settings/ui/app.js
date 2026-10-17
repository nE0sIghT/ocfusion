// SPDX-License-Identifier: GPL-3.0-or-later
"use strict";

const moduleID = "radio-settings";
const fields = document.getElementById("fields");
const result = document.getElementById("result");
const messages = {
  STOCK: "Original settings",
  APPLYING: "Saving settings",
  RESTORING: "Restoring settings",
  REBOOT_REQUIRED: "Reboot required",
  APPLIED: "Saved region is active",
  VERIFY_REQUIRED:
    "Live profile differs from saved settings. Inspect radio state before making further changes.",
  RECOVERY_REQUIRED: "Saving was interrupted. Restore the original settings.",
  UNAVAILABLE: "Service unavailable",
};
const profileNames = {
  EU: "Europe (E0/101)",
  AA: "United States (US/102)",
  AU: "Australia (AU/100)",
  JP: "Japan (JP/101)",
  KR: "South Korea (KR/100)",
  XX: "United States (US/104)",
};
let settings = null;
let pending = false;
let currentRadios = null;

// Reference channel plans from CLM BE98_20240925 / v17.6. Apply no restrictions
// from this preview; the running firmware and driver own channel selection.
const profilePlans = {
  EU: {
    two: 13,
    five: [
      [36, 64],
      [100, 140],
    ],
    six: 93,
    wide5: [50, 114],
  },
  AA: {
    two: 11,
    five: [
      [36, 64],
      [100, 144],
      [149, 165],
    ],
    six: 93,
    wide5: [50, 114],
  },
  AU: {
    two: 11,
    five: [
      [36, 64],
      [100, 116],
      [132, 140],
      [149, 165],
    ],
    six: 93,
    wide5: [50],
  },
  JP: {
    two: 13,
    five: [
      [36, 64],
      [100, 144],
    ],
    six: 93,
    wide5: [50, 114],
  },
  KR: {
    two: 13,
    five: [
      [36, 64],
      [100, 144],
      [149, 165],
    ],
    six: 229,
    wide5: [50, 114],
  },
  XX: {
    two: 11,
    five: [
      [36, 64],
      [100, 144],
      [149, 165],
    ],
    six: 93,
    wide5: [50, 114],
  },
};

function previewRow(container, label, value) {
  const title = document.createElement("dt");
  const text = document.createElement("dd");
  title.textContent = label;
  text.textContent = value;
  container.append(title, text);
}

function compareProfiles(left, right) {
  const language = window.OCF_I18N?.language || "en";
  const label = (id) => {
    const name = profileNames[id] || id;
    return window.OCF_I18N?.translate(name, language) || name;
  };

  return label(left).localeCompare(label(right), language, { numeric: true });
}

function channelRanges(ranges) {
  return ranges
    .map(([first, last]) =>
      first === last ? String(first) : first + "-" + last,
    )
    .join(", ");
}

function showComparison() {
  const body = document.getElementById("region-comparison");
  const selected = document.getElementById("profile").value;
  const profiles = settings?.profiles || [];
  const rows = Object.entries(profilePlans)
    .sort(([left], [right]) => compareProfiles(left, right))
    .map(([id, plan]) => {
      const profile = profiles.find((entry) => entry.id === id);
      const row = document.createElement("tr");
      row.dataset.selected = String(id === selected);
      row.dataset.profile = id;
      for (const value of [
        profileNames[id],
        "1-" + plan.two,
        channelRanges(plan.five),
        "1-" + plan.six,
        profile?.available ? "Supported" : "Unavailable",
      ]) {
        const cell = document.createElement(row.children.length ? "td" : "th");
        if (!row.children.length) {
          cell.scope = "row";
        }
        cell.textContent = value;
        row.appendChild(cell);
      }

      return row;
    });
  body.replaceChildren(...rows);
}

function showSelection() {
  const status = document.getElementById("selection-status");
  const selected = document.getElementById("profile").value;
  status.textContent =
    settings && selected !== settings.mode ? "Changes not saved" : "";
}

function showPreview() {
  showComparison();
  showSelection();
  const note = document.getElementById("preview-note");
  const bands = document.getElementById("preview-bands");
  const id = document.getElementById("profile").value;
  const plan = profilePlans[id];
  bands.replaceChildren();

  if (!plan) {
    note.textContent = "No channel reference is available for this profile.";
    return;
  }

  note.textContent = "Reference channels for the selected region.";
  const definitions = [
    {
      band: "2.4 GHz",
      ranges: [[1, plan.two]],
      offset: 2407,
      widths: "20 / 40 MHz",
    },
    {
      band: "5 GHz",
      ranges: plan.five,
      offset: 5000,
      widths: "20 / 40 / 80 / 160 MHz",
    },
    {
      band: "6 GHz",
      ranges: [[1, plan.six]],
      offset: 5950,
      widths: "20 / 40 / 80 / 160 / 320 MHz",
    },
  ];

  for (const definition of definitions) {
    if (
      currentRadios?.length &&
      !currentRadios.some((radio) => radio.band === definition.band)
    ) {
      continue;
    }

    const section = document.createElement("section");
    const heading = document.createElement("h4");
    heading.textContent = definition.band;
    section.className = "radio-band";
    section.appendChild(heading);
    const rows = document.createElement("dl");
    section.appendChild(rows);
    const channels = definition.ranges
      .map(([first, last]) =>
        first === last ? String(first) : first + "-" + last,
      )
      .join(", ");
    previewRow(rows, "20 MHz channels", channels);
    previewRow(
      rows,
      "Channel center frequencies, MHz",
      definition.ranges
        .map(
          ([first, last]) =>
            definition.offset +
            first * 5 +
            "-" +
            (definition.offset + last * 5),
        )
        .join(", "),
    );
    previewRow(rows, "Channel widths", definition.widths);
    if (definition.band === "5 GHz") {
      previewRow(rows, "160 MHz center channels", plan.wide5.join(", "));
    } else if (definition.band === "6 GHz") {
      previewRow(
        rows,
        "320 MHz center channels",
        plan.six === 93 ? "31, 63" : "31, 63, 95, 127, 159, 191",
      );
    }

    bands.appendChild(section);
  }
}

function showLiveState(data) {
  const state = document.getElementById("state");
  if (data?.state === "APPLIED" && profileNames[data.mode]) {
    const label = document.createElement("span");
    const name = document.createElement("span");
    label.textContent = "Active region";
    name.textContent = profileNames[data.mode];
    state.replaceChildren(label, document.createTextNode(": "), name);
    return;
  }

  state.textContent = messages[data?.state] || "Service unavailable";
}

function settingsMessage(phase) {
  if (["APPLIED", "STOCK", "REBOOT_REQUIRED"].includes(phase)) {
    return "";
  }

  return messages[phase] || "Service unavailable";
}

function showReboot(phase) {
  document.getElementById("reboot-notice").hidden = phase !== "REBOOT_REQUIRED";
}

function controls() {
  fields.disabled = pending || settings === null;
  const selected = document.getElementById("profile").selectedOptions[0];
  document.getElementById("apply").disabled =
    !settings || !settings.can_apply || !selected || selected.disabled;
  document.getElementById("restore").disabled =
    !settings ||
    (settings.mode === "stock" &&
      ["STOCK", "REBOOT_REQUIRED"].includes(settings.phase));
}

function showProfiles() {
  const select = document.getElementById("profile");
  const profiles = [...(settings.profiles || [])].sort((left, right) =>
    compareProfiles(left.id, right.id),
  );
  select.replaceChildren();
  const original = document.createElement("option");
  original.value = "stock";
  original.textContent = "Original settings";
  original.disabled = true;
  select.appendChild(original);

  for (const profile of profiles) {
    const option = document.createElement("option");
    option.value = profile.id;
    option.appendChild(
      document.createTextNode(profileNames[profile.id] || profile.id),
    );
    option.disabled = !profile.available;
    select.appendChild(option);
  }

  const chosen = profiles.find(
    (profile) => profile.id === settings.mode && profile.available,
  );
  select.value = chosen ? chosen.id : "stock";

  showPreview();
}

async function loadSettings() {
  if (pending) {
    return;
  }

  pending = true;
  controls();

  try {
    settings = await parent.EXTENSIONS.action(moduleID, "settings");
    showReboot(settings.phase);
    showProfiles();
    result.textContent = !settings.profiles?.some(
      (profile) => profile.available,
    )
      ? "No supported regional profiles are available."
      : settingsMessage(settings.phase);
  } catch (error) {
    settings = null;
    result.textContent = error.message;
  } finally {
    pending = false;
    controls();
  }
}

function channelFrequencies(radio) {
  const frequencies = [];
  for (const value of (radio.channels || "").trim().split(/\s+/)) {
    if (!/^\d+$/.test(value)) {
      continue;
    }

    const channel = Number(value);
    if (radio.band === "2.4 GHz" && channel >= 1 && channel <= 14) {
      frequencies.push(channel === 14 ? 2484 : 2407 + channel * 5);
    } else if (radio.band === "5 GHz" && channel >= 1 && channel <= 200) {
      frequencies.push(5000 + channel * 5);
    } else if (radio.band === "6 GHz" && channel >= 1 && channel <= 233) {
      frequencies.push(channel === 2 ? 5935 : 5950 + channel * 5);
    }
  }

  return frequencies.join(", ") || "UNAVAILABLE";
}

function radioCard(radio) {
  const article = document.createElement("article");
  article.className = "radio-band";
  const heading = document.createElement("h3");
  heading.textContent = radio.band + " · " + radio.interface;
  article.appendChild(heading);

  const rows = document.createElement("dl");
  previewRow(rows, "Current channel and width", radio.current || "UNAVAILABLE");
  previewRow(
    rows,
    "Available 20 MHz channels",
    radio.channels || "UNAVAILABLE",
  );
  article.appendChild(rows);

  const details = document.createElement("details");
  details.className = "radio-details";
  const summary = document.createElement("summary");
  summary.textContent = "Technical details";
  details.appendChild(summary);
  const technical = document.createElement("dl");
  for (const [label, value] of [
    ["Driver country", radio.country],
    ["Channel center frequencies, MHz", channelFrequencies(radio)],
    ["Base exclusion mask", radio.base_mask],
    ["Additional exclusion mask", radio.user_mask],
  ]) {
    previewRow(technical, label, value || "UNAVAILABLE");
  }

  details.appendChild(technical);
  article.appendChild(details);
  return article;
}

async function updateStatus() {
  try {
    const snapshot = await parent.EXTENSIONS.status(moduleID);
    const data = snapshot.data;
    currentRadios = data?.radios || [];
    showPreview();
    if (data && (!settings || data.revision >= settings.revision)) {
      showReboot(data.state);
    }
    showLiveState(data);

    const radios = document.getElementById("radios");
    const expanded = new Set(
      [...radios.children]
        .filter((card) => card.querySelector("details")?.open)
        .map((card) => card.dataset.interface),
    );
    const cards = (data?.radios || []).map((radio) => {
      const card = radioCard(radio);
      card.dataset.interface = radio.interface;
      card.querySelector("details").open = expanded.has(radio.interface);
      return card;
    });
    radios.replaceChildren(...cards);
  } catch (error) {
    currentRadios = [];
    showPreview();
    document.getElementById("state").textContent = "Service unavailable";
    document.getElementById("radios").replaceChildren();
  }
}

async function changeProfile(operation) {
  if (pending || settings === null) {
    return;
  }

  pending = true;
  controls();

  try {
    const response = await parent.EXTENSIONS.call(moduleID, operation, {
      revision: settings.revision,
      profile: document.getElementById("profile").value,
    });
    settings = { ...settings, ...response };
    showReboot(response.phase);
    if (operation === "restore") {
      showProfiles();
    }
    if (response.phase === "REBOOT_REQUIRED") {
      document
        .getElementById("reboot-notice")
        .scrollIntoView({ block: "start" });
    }
    result.textContent = settingsMessage(response.phase);
  } catch (error) {
    settings = null;
    result.textContent = error.message;
  } finally {
    pending = false;
    controls();
    await updateStatus();
  }
}

document.getElementById("profile").addEventListener("change", () => {
  controls();
  showPreview();
});
document
  .getElementById("apply")
  .addEventListener("click", () => changeProfile("apply"));
document
  .getElementById("restore")
  .addEventListener("click", () => changeProfile("restore"));
document.getElementById("reload").addEventListener("click", loadSettings);

window.addEventListener("DOMContentLoaded", () => {
  loadSettings();
  updateStatus();
  setInterval(updateStatus, 5000);
});
