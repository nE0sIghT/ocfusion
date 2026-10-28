// SPDX-License-Identifier: GPL-3.0-or-later
"use strict";

let serviceCatalogEntries = [];
let selectedServiceLists = [];
let currentServiceStatuses = [];
let serviceRefreshBusy = false;

async function loadServiceLists(saved, catalogEntries) {
  if (serviceCatalogEntries.length === 0) {
    serviceCatalogEntries = catalogEntries;
    const select = document.getElementById("service-picker");

    for (const service of serviceCatalogEntries) {
      const option = document.createElement("option");
      option.value = service.id;
      option.textContent = service.name;
      select.appendChild(option);
    }
  }

  selectedServiceLists = [...(saved || [])];
  renderServiceLists();
}

function serviceListsConfig() {
  return [...selectedServiceLists];
}

function appendServiceText(parentNode, text) {
  const span = document.createElement("span");
  span.textContent = text;
  parentNode.appendChild(span);
}

function renderServiceLists(statuses) {
  if (statuses) {
    currentServiceStatuses = statuses;
  }

  if (applicationKinds.length > 0) {
    updateApplicationSummary();
  }

  const root = document.getElementById("service-lists");
  const signature = JSON.stringify([selectedServiceLists, currentServiceStatuses, config?.lists, parent.OCF_I18N?.language]);

  for (const option of document.getElementById("service-picker").options) {
    option.disabled = selectedServiceLists.includes(option.value);
  }

  document.getElementById("service-refresh").disabled = serviceRefreshBusy || !savedServiceIDs().length || pending;

  if (root.dataset.rendered === signature) {
    return;
  }

  const expanded = new Set([...root.querySelectorAll("details[open]")].map((item) => item.closest("[data-service]").dataset.service));
  root.dataset.rendered = signature;
  root.replaceChildren();

  for (const id of selectedServiceLists) {
    const service = serviceCatalogEntries.find((entry) => entry.id === id);

    if (!service) {
      continue;
    }

    const status = currentServiceStatuses.find((entry) => entry.id === id);
    const saved = config?.lists?.includes(id);
    const card = document.createElement("div");
    card.className = "ocf-service-list";
    card.dataset.service = id;
    const heading = document.createElement("div");
    heading.className = "ext-card-heading";
    const name = document.createElement("strong");
    name.textContent = service.name;
    heading.appendChild(name);
    const remove = document.createElement("button");
    remove.type = "button";
    remove.className = "ext-secondary";
    remove.textContent = "Remove";
    remove.addEventListener("click", () => {
      selectedServiceLists = selectedServiceLists.filter((entry) => entry !== id);
      setDirty(true);
      renderServiceLists();
    });
    heading.appendChild(remove);
    card.appendChild(heading);
    const state = document.createElement("p");
    const labels = {READY: "Up to date", UPDATING: "Updating lists", NOT_LOADED: "Waiting for download", ERROR: "List update failed"};
    const message = !saved ? "Apply to use this list" : labels[status?.state] || "Waiting for download";
    appendServiceText(state, message);

    if (saved && status?.state === "ERROR" && status.updated) {
      state.appendChild(document.createTextNode(". "));
      appendServiceText(state, "Using the saved copy");
    }

    card.appendChild(state);

    if (service.notice) {
      const notice = document.createElement("p");
      notice.textContent = service.notice;
      card.appendChild(notice);
    }

    const details = document.createElement("details");
    details.open = expanded.has(id);
    const summary = document.createElement("summary");
    summary.textContent = "List contents and sources";
    details.appendChild(summary);
    const counts = document.createElement("p");
    appendServiceText(counts, "Domains");
    counts.appendChild(document.createTextNode(": " + (status?.domains || 0) + " · "));
    appendServiceText(counts, "Subnets");
    counts.appendChild(document.createTextNode(": " + (status?.networks || 0)));
    details.appendChild(counts);

    if (status?.updated) {
      const updated = document.createElement("p");
      appendServiceText(updated, "Last updated");
      const language = parent.OCF_I18N?.language === "ru" ? "ru-RU" : "en-US";
      updated.appendChild(document.createTextNode(": " + new Date(status.updated * 1000).toLocaleString(language)));
      details.appendChild(updated);
    }

    const sources = document.createElement("ul");

    for (const path of service.paths) {
      const item = document.createElement("li");
      const link = document.createElement("a");
      link.href = "https://github.com/itdoginfo/allow-domains/blob/main/" + path;
      link.target = "_blank";
      link.rel = "noopener noreferrer";
      link.textContent = path;
      item.appendChild(link);
      sources.appendChild(item);
    }

    details.appendChild(sources);
    card.appendChild(details);
    root.appendChild(card);
  }
}

document.getElementById("service-add").addEventListener("click", () => {
  const select = document.getElementById("service-picker");

  if (select.value && !selectedServiceLists.includes(select.value)) {
    selectedServiceLists.push(select.value);
    select.value = "";
    setDirty(true);
    renderServiceLists();
  }
});

document.getElementById("service-refresh").addEventListener("click", async () => {
  if (serviceRefreshBusy || pending || !savedServiceIDs().length) {
    return;
  }

  serviceRefreshBusy = true;
  renderServiceLists();
  document.getElementById("service-result").textContent = "";

  try {
    await parent.EXTENSIONS.call("zapret2", "refresh-lists", {revision: config.revision});
    currentServiceStatuses = currentServiceStatuses.map((entry) => savedServiceIDs().includes(entry.id) ? {...entry, state: "UPDATING"} : entry);
  } catch (error) {
    document.getElementById("service-result").textContent = error.message;
  } finally {
    serviceRefreshBusy = false;
    renderServiceLists();
  }
});

function savedServiceIDs() {
  return [...new Set([...(config?.lists || []), ...(config?.rules || []).filter((rule) => rule.enabled).flatMap((rule) => rule.lists || [])])];
}
