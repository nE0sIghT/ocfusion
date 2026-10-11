// SPDX-License-Identifier: GPL-3.0-or-later
"use strict";

const cfg = window.OCF_SETTINGS;

let pending = false;
let platformReady = false;

const platformMessages = {
  READY: "",
  INSTALLING: "Installation or update in progress. Please wait.",
  STOPPING: "OCFusion is stopping. Please wait.",
  STOPPED: "OCFusion is stopped. Connect the USB drive to start services.",
  UNAVAILABLE:
    "OCFusion is unavailable. Check the USB drive and installation log.",
};

function setPlatformState(state) {
  platformReady = state === "READY";

  const banner = document.getElementById("platform-state");

  banner.hidden = platformReady;
  banner.textContent = platformMessages[state] || platformMessages.UNAVAILABLE;

  const frame = document.getElementById("module-frame");

  if (frame && !platformReady) {
    frame.hidden = true;
    frame.removeAttribute("src");
  }

  document.querySelectorAll("[data-module-state]").forEach((node) => {
    if (!platformReady) {
      node.textContent = state || "UNAVAILABLE";
    }
  });

  navigate();
}

const decode = (s) =>
  JSON.parse(
    new TextDecoder().decode(Uint8Array.from(atob(s), (c) => c.charCodeAt(0))),
  );

const bytes64 = (b) =>
  btoa(Array.from(b, (n) => String.fromCharCode(n)).join(""));

const encode = (s) => bytes64(new TextEncoder().encode(s));

const nonce = () =>
  Array.from(crypto.getRandomValues(new Uint8Array(16)), (b) =>
    b.toString(16).padStart(2, "0"),
  ).join("");

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

async function read() {
  if (!cfg) {
    throw Error("Panel parameters missing. Reinstall the distribution.");
  }

  const response = await fetch(
    "/appGet.cgi?hook=nvram_char_to_ascii(" +
      cfg.key +
      "," +
      cfg.key +
      ")&_=" +
      Date.now(),
    { credentials: "same-origin", cache: "no-store" },
  );

  if (!response.ok) {
    throw Error("ASUS API: " + response.status);
  }

  const obj = await response.json();

  if (typeof obj[cfg.key] !== "string") {
    throw Error("Configuration field unavailable");
  }

  return decodeURIComponent(obj[cfg.key]);
}

async function write(value) {
  if (new TextEncoder().encode(value).length > 4096) {
    throw Error("Request exceeds the ASUS field size");
  }

  const payload = { action_mode: "apply", rc_service: "" };

  payload[cfg.key] = value;

  const response = await fetch("/applyapp.cgi", {
    method: "POST",
    credentials: "same-origin",
    headers: {
      "Content-Type": "application/x-www-form-urlencoded; charset=UTF-8",
    },
    body: encodeURIComponent(JSON.stringify(payload)),
  });

  if (!response.ok) {
    throw Error("ASUS API: " + response.status);
  }

  await response.text();
}

function render() {
  document.getElementById("api-error").hidden = true;
}

async function request(action, data = {}) {
  if (!platformReady) {
    throw Error("OCFusion services are not ready. Check the platform status.");
  }

  const id = nonce();
  const wire =
    "# EXT-REQUEST1 " + encode(JSON.stringify({ id, action, ...data }));

  await write(wire);

  for (let i = 0; i < 20; i++) {
    await sleep(1000);

    if (!platformReady) {
      throw Error(
        "OCFusion services are not ready. Check the platform status.",
      );
    }

    const value = await read();

    if (!value.startsWith("# OCF-STATUS1 ")) {
      continue;
    }

    const st = decode(value.slice(14));

    if (st.request_id !== id) {
      continue;
    }

    render(st);

    if (!st.request_ok) {
      throw Error(st.request_message || "Service rejected the request");
    }

    return st;
  }

  throw Error(
    "Service did not acknowledge the request. Check its status through SSH.",
  );
}

function busy(value) {
  pending = value;
}

let installedModules = [];

async function uploadConfig(
  bytes,
  module,
  onProgress = () => {},
  kind = "",
  operation = "",
) {
  if (bytes.length > 16384) {
    throw Error("Configuration exceeds 16 KiB");
  }

  if (!crypto.subtle) {
    throw Error("Open the panel over HTTPS to upload configuration.");
  }

  const hash = Array.from(
    new Uint8Array(await crypto.subtle.digest("SHA-256", bytes)),
    (b) => b.toString(16).padStart(2, "0"),
  ).join("");
  const upload = nonce();

  await request("upload.begin", {
    upload,
    module,
    size: bytes.length,
    sha256: hash,
    kind,
    operation,
  });

  for (let offset = 0; offset < bytes.length; offset += 1024) {
    onProgress(
      "Uploading configuration: " +
        Math.round((offset / bytes.length) * 100) +
        "%",
    );
    await request("upload.chunk", {
      upload,
      offset,
      data: bytes64(bytes.slice(offset, offset + 1024)),
    });
  }

  return (await request("upload.finish", { upload })).request_data;
}

async function publicStatus(module) {
  if (!platformReady) {
    throw Error("OCFusion services are not ready. Check the platform status.");
  }

  if (!/^[a-z][a-z0-9-]{0,31}$/.test(module)) {
    throw Error("Invalid module ID");
  }

  const response = await fetch(
    "/user/extensions-status/" + module + ".json?_=" + Date.now(),
    {
      credentials: "same-origin",
      cache: "no-store",
    },
  );

  if (!response.ok) {
    throw Error("Public status unavailable");
  }

  const text = await response.text();

  if (text.length > 32768) {
    throw Error("Public status unavailable");
  }

  const snapshot = JSON.parse(text);
  const serverDate = response.headers?.get("Date");
  const now = serverDate ? Date.parse(serverDate) : Date.now();

  if (
    snapshot.module !== module ||
    !Number.isFinite(snapshot.updated_at) ||
    now - snapshot.updated_at >= 15000
  ) {
    throw Error("Public status unavailable");
  }

  if (
    !Number.isFinite(snapshot.data_updated_at) ||
    now - snapshot.data_updated_at >= 15000
  ) {
    snapshot.data = null;
  }

  return snapshot;
}

window.EXTENSIONS = {
  status: publicStatus,
  get busy() {
    return pending;
  },
  action: async (module, operation) => {
    if (pending) {
      throw Error("Wait for the current action to finish");
    }

    busy(true);

    try {
      const status = await request("module.action", { module, operation });

      return status.request_data;
    } finally {
      busy(false);
    }
  },
  configure: async (module, config, onProgress) => {
    if (pending) {
      throw Error("Wait for the current action to finish");
    }

    busy(true);

    try {
      return await uploadConfig(
        new TextEncoder().encode(JSON.stringify(config)),
        module,
        onProgress,
      );
    } finally {
      busy(false);
    }
  },
  call: async (module, operation, data = {}, onProgress) => {
    if (pending) {
      throw Error("Wait for the current action to finish");
    }

    busy(true);

    try {
      if (new TextEncoder().encode(JSON.stringify(data)).length <= 1024) {
        const status = await request("module.call", {
          module,
          operation,
          config: data,
        });

        return status.request_data;
      }

      return await uploadConfig(
        new TextEncoder().encode(JSON.stringify(data)),
        module,
        onProgress,
        "module.call",
        operation,
      );
    } finally {
      busy(false);
    }
  },
};

async function loadModules() {
  try {
    const response = await fetch(
      "/user/extensions-modules.json?_=" + Date.now(),
      { credentials: "same-origin", cache: "no-store" },
    );

    if (!response.ok) {
      throw Error("Platform status unavailable");
    }

    const data = await response.json();
    const serverDate = response.headers?.get("Date");
    const now = serverDate ? Date.parse(serverDate) : Date.now();
    const fresh =
      Number.isFinite(data.updated_at) &&
      now - data.updated_at < (data.state === "INSTALLING" ? 300000 : 15000);
    const state =
      data.state === "STOPPED" ? "STOPPED" : fresh ? data.state : "UNAVAILABLE";

    setPlatformState(state);

    if (!Array.isArray(data.modules)) {
      return;
    }

    installedModules = data.modules;

    const statusLink = document.getElementById("transport-module-json");
    const statusModule = installedModules.find((module) => module.ui);

    if (statusLink) {
      statusLink.hidden = !statusModule;

      if (statusModule && /^[a-z][a-z0-9-]{0,31}$/.test(statusModule.id)) {
        statusLink.href =
          "/user/extensions-status/" + statusModule.id + ".json";
      }
    }

    for (const id of ["module-list", "module-overview", "module-menu"]) {
      const container = document.getElementById(id);

      container.replaceChildren();

      for (const module of installedModules) {
        if (id === "module-overview" && !module.ui) {
          continue;
        }

        if (id === "module-menu") {
          if (!module.ui) {
            continue;
          }

          const link = document.createElement("a");

          link.href = "#module-" + module.id;
          link.dataset.page = "module-" + module.id;
          link.textContent = module.name;
          container.appendChild(link);

          continue;
        }

        const card = document.createElement("article");

        card.className = "ext-card";

        const title = document.createElement("h2");

        title.textContent = module.name;
        card.appendChild(title);

        const info = document.createElement("p");

        info.dataset.moduleState = module.id;

        info.textContent =
          module.version +
          " · " +
          (platformReady ? module.state : state || "UNAVAILABLE");
        card.appendChild(info);

        if (module.ui) {
          const link = document.createElement("a");

          link.textContent = "Open settings →";
          link.href = "#module-" + module.id;
          card.appendChild(link);
        }

        container.appendChild(card);
      }
    }

    navigate();
  } catch (e) {
    setPlatformState("UNAVAILABLE");
  }
}

function navigate() {
  const page = location.hash.slice(1) || "overview";
  const known = ["overview", "modules", "system"];
  const module = installedModules.find(
    (m) => "module-" + m.id === page || m.id === page,
  );
  const selected = module ? "module" : known.includes(page) ? page : "overview";

  document
    .querySelectorAll("[data-section]")
    .forEach((n) => (n.hidden = n.dataset.section !== selected));
  document
    .querySelectorAll("[data-page]")
    .forEach((n) =>
      n.classList.toggle(
        "active",
        n.dataset.page === (module ? "module-" + module.id : selected),
      ),
    );

  if (module && module.ui.startsWith("/user/extensions-modules/")) {
    document.getElementById("module-title").textContent = module.name;

    const frame = document.getElementById("module-frame");

    frame.hidden = !platformReady;

    if (!platformReady) {
      frame.removeAttribute("src");
    } else if (frame.getAttribute("src") !== module.ui) {
      frame.setAttribute("src", module.ui);
    }
  }
}

document.addEventListener("click", (event) => {
  const link = event.target.closest && event.target.closest('a[href^="#"]');

  if (link) {
    event.preventDefault();
    location.hash = link.getAttribute("href");
  }
});

window.addEventListener("hashchange", navigate);

navigate();

for (const id of ["platform-version", "system-version", "about-version"]) {
  document.getElementById(id).textContent = cfg ? cfg.version : "";
}

document.querySelectorAll("[data-router-model]").forEach((node) => {
  node.textContent = cfg && cfg.model ? cfg.model : "ASUSWRT";
});

try {
  if (typeof show_banner === "function") {
    show_banner();
  }

  if (typeof show_selected_language === "function") {
    show_selected_language();
  }
} catch (e) {
  /* Preserve the native header if rendered; fallback is initially visible. */
}

loadModules();

setInterval(() => {
  loadModules();
}, 5000);

// Change the brand only on our page; the shared ASUS header keeps its controls.
const nativeBrand = document.querySelector("#TopBanner .banner1 > img");

if (nativeBrand) {
  nativeBrand.src = "/user/ocfusion-logo.png";
  nativeBrand.alt = "OCFusion";
  nativeBrand.classList.add("ocfusion-header-logo");

  const link = document.createElement("a");

  link.href = "https://github.com/nE0sIghT/ocfusion";
  link.target = "_blank";
  link.rel = "noopener noreferrer";
  link.setAttribute("aria-label", "OCFusion on GitHub");
  nativeBrand.replaceWith(link);
  link.appendChild(nativeBrand);
}

if (document.forms.form && typeof ui_lang !== "undefined") {
  document.forms.form.elements.preferred_lang.value = ui_lang;
}
