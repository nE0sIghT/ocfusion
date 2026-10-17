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
  APPLIED: "Native profile active",
  VERIFY_REQUIRED:
    "Live profile differs from saved settings. Inspect radio state before making further changes.",
  RECOVERY_REQUIRED: "Saving was interrupted. Restore the original settings.",
  UNAVAILABLE: "Service unavailable",
};
const profileNames = {
  EU: "Europe",
  AA: "ASUS AA",
  AU: "Australia",
  JP: "Japan",
  KR: "South Korea",
  XX: "ASUS XX",
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
  const row = document.createElement("p");
  const title = document.createElement("strong");
  title.textContent = label;
  row.append(title, document.createTextNode(": " + value));
  container.appendChild(row);
}

function showPreview() {
  const note = document.getElementById("preview-note");
  const bands = document.getElementById("preview-bands");
  const id = document.getElementById("profile").value;
  const plan = profilePlans[id];
  bands.replaceChildren();

  if (!plan) {
    note.textContent = "No channel reference is available for this profile.";
    return;
  }

  note.textContent =
    "Reference channel plan. Hardware, firmware, channel width and AiMesh can further limit availability. Changing region also changes power limits.";
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
    section.appendChild(heading);
    const step = definition.band === "2.4 GHz" ? 1 : 4;
    const channels = definition.ranges.flatMap(([first, last]) => {
      const values = [];
      for (let channel = first; channel <= last; channel += step) {
        values.push(channel);
      }

      return values;
    });
    previewRow(section, "20 MHz channels", channels.join(", "));
    previewRow(
      section,
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
    previewRow(section, "Channel widths", definition.widths);
    if (definition.band === "5 GHz") {
      previewRow(section, "160 MHz center channels", plan.wide5.join(", "));
    } else if (definition.band === "6 GHz") {
      previewRow(
        section,
        "320 MHz center channels",
        plan.six === 93 ? "31, 63" : "31, 63, 95, 127, 159, 191",
      );
    }

    bands.appendChild(section);
  }
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
  const profiles = settings.profiles || [];
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
    option.appendChild(
      document.createTextNode(
        " (" + profile.country + "/" + profile.country_revision + ")",
      ),
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
      : messages[settings.phase] || "Service unavailable";
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
  const heading = document.createElement("h3");
  heading.textContent = radio.interface + " · " + radio.band;
  article.appendChild(heading);

  const rows = [
    ["Driver country", radio.country],
    ["Current channel and width", radio.current],
    ["Available 20 MHz channels", radio.channels],
    ["Channel center frequencies, MHz", channelFrequencies(radio)],
    ["Base exclusion mask", radio.base_mask],
    ["Additional exclusion mask", radio.user_mask],
  ];

  for (const [label, value] of rows) {
    const paragraph = document.createElement("p");
    const title = document.createElement("strong");
    const text = document.createElement("span");
    title.textContent = label;
    text.textContent = value || "UNAVAILABLE";
    paragraph.append(title, document.createTextNode(": "), text);
    article.appendChild(paragraph);
  }

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
    document.getElementById("state").textContent =
      messages[data?.state] || "Service unavailable";

    const radios = document.getElementById("radios");
    radios.replaceChildren(...(data?.radios || []).map(radioCard));
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
    result.textContent = messages[response.phase] || "Service unavailable";
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

loadSettings();
updateStatus();
setInterval(updateStatus, 5000);
