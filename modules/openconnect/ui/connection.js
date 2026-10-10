// SPDX-License-Identifier: GPL-3.0-or-later
function renderConnection(st) {
  const { request_data, ...diagnostics } = st;

  view.textContent = JSON.stringify(diagnostics, null, 2);

  const badge = document.getElementById("state-badge");

  badge.textContent = st.state;
  badge.className =
    "ext-badge" +
    (st.state === "UP"
      ? " up"
      : /ERROR|AUTH|CONFLICT|REJECTED/.test(st.state)
        ? " error"
        : "");
  document.getElementById("state-message").textContent = st.message || "";
  document.getElementById("connection-state").textContent =
    st.state + (st.message ? " - " + st.message : "");

  const lines = (st.networks || []).map(
    (n) => n.bridge + " · " + n.cidr + " · SDN " + n.sdn_idx,
  );

  for (const id of ["network-summary", "networks"]) {
    document.getElementById(id).textContent =
      lines.join("\n") || "Assign OCFusion (managed) to your network in ASUS.";
  }

  document.getElementById("api-error").hidden = true;
}

file.addEventListener("change", () => {
  button.disabled = !file.files.length || pending;
});

button.addEventListener("click", async () => {
  busy(true);
  result.textContent = "Checking file…";

  try {
    const f = file.files[0];

    if (!f) {
      throw Error("Select a file");
    }

    if (f.size > 16384) {
      throw Error("Profile exceeds 16 KiB");
    }

    const text = await f.text();
    const profile = JSON.parse(text);

    if (profile.schema !== 1 || typeof profile.server !== "string") {
      throw Error("An ocfusion schema=1 profile is required, not .ovpn");
    }

    const bytes = new TextEncoder().encode(text);

    if (bytes.length > 16384) {
      throw Error("Profile exceeds 16 KiB");
    }

    // A chunk transaction fits stock NVRAM even with embedded certificates.
    if (!crypto.subtle) {
      throw Error("Open the panel over HTTPS to import, or use SSH import.");
    }

    await uploadConfig(bytes, "openconnect", (text) => {
      result.textContent = text;
    });
    settingsLoaded = false;
    result.textContent = "Profile saved. Wi-Fi/VLAN bindings preserved.";
  } catch (e) {
    result.textContent = e.message;
  } finally {
    busy(false);
  }
});

document.querySelectorAll("[data-action]").forEach((b) =>
  b.addEventListener("click", async () => {
    busy(true);

    const node = document.getElementById("action-result");

    node.textContent = "Waiting for service…";

    try {
      await request(b.dataset.action);
      node.textContent = "Command accepted. Status will update automatically.";
    } catch (e) {
      node.textContent = e.message;
    } finally {
      busy(false);
    }
  }),
);

async function refreshConnection() {
  if (pending || parent.EXTENSIONS.busy) {
    return;
  }

  try {
    renderConnection(await request("diagnose"));
  } catch (error) {
    const node = document.getElementById("api-error");

    node.hidden = false;
    node.textContent = error.message;
    document.getElementById("state-badge").textContent = "UNKNOWN";
    document.getElementById("connection-state").textContent = error.message;
  }
}

loadSettings();

refreshConnection();

setInterval(refreshConnection, 5000);
