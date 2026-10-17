// SPDX-License-Identifier: GPL-3.0-or-later
let otpChallengeID = "";

function renderDNSStatus(st) {
  const status = document.getElementById("dns-status");
  status.textContent =
    st.dns_path === "VPN"
      ? "Currently: VPN"
      : st.dns_path === "WAN"
        ? "Currently: WAN"
        : "Check DNS routing diagnostics";
  if (st.dns_reason) {
    const detail = document.createElement("span");
    detail.textContent = st.dns_reason;
    status.append(document.createTextNode(" "), detail);
  }
}

function renderConnection(st) {
  renderDNSStatus(st);

  const challenge = st.otp;
  const activeID = challenge ? challenge.id : "";

  if (activeID !== otpChallengeID) {
    document.getElementById("otp-code").value = "";
    document.getElementById("otp-result").textContent = "";
  }

  otpChallengeID = activeID;
  document.getElementById("otp-challenge").hidden = !activeID;

  const { request_data, ...diagnostics } = st;

  view.textContent = JSON.stringify(diagnostics, null, 2);

  const badge = document.getElementById("state-badge");

  const labels = {
    UP: "Connected",
    DISABLED: "Disconnected",
    CONNECTING: "Connecting…",
    STARTING: "Starting…",
    ERROR: "Connection error",
    NO_PROFILE: "Not configured",
    WAIT_NETWORK: "Waiting for network",
    AUTH_REQUIRED: "Authentication required",
    WAIT_OTP: "One-time code required",
    WAIT_TIME: "Waiting for clock synchronization",
    DOWN: "Disconnected",
    CONFIG_CONFLICT: "Configuration conflict",
    NETWORK_CHANGED: "Updating network bindings",
    WAIT_STORAGE: "USB unavailable",
    RECONNECTING: "Reconnecting…",
    STOPPING: "Disconnecting…",
    STOPPED: "Disconnected",
  };

  badge.textContent = labels[st.state] || st.state;

  const active = [
    "UP",
    "CONNECTING",
    "RECONNECTING",
    "STARTING",
    "WAIT_OTP",
    "WAIT_TIME",
    "WAIT_NETWORK",
  ].includes(st.state);
  document.querySelector('[data-action="enable"]').hidden = active;
  document.querySelector('[data-action="disable"]').hidden = !active;
  badge.className =
    "ext-badge" +
    (st.state === "UP"
      ? " up"
      : /ERROR|AUTH|CONFLICT|REJECTED/.test(st.state)
        ? " error"
        : "");
  document.getElementById("state-message").textContent = st.message || "";

  const networks = document.getElementById("networks");
  networks.replaceChildren();

  if (st.networks?.length) {
    const table = document.createElement("table");
    table.className = "ocf-network-table";
    const heading = table.createTHead().insertRow();

    for (const label of ["Network", "Interface", "SDN"]) {
      const cell = document.createElement("th");
      cell.scope = "col";
      cell.textContent = label;
      heading.append(cell);
    }

    const body = table.createTBody();
    for (const network of st.networks) {
      const row = body.insertRow();
      for (const value of [network.cidr, network.bridge, network.sdn_idx]) {
        row.insertCell().textContent = value;
      }
    }

    networks.append(table);
  } else {
    networks.textContent =
      "No networks assigned. Choose this VPN Fusion profile in ASUS to route a network through this VPN.";
  }

  document.getElementById("api-error").hidden = true;
}

file.addEventListener("change", () => {
  button.disabled = !file.files.length || pending;
});

button.addEventListener("click", async () => {
  if (!confirmSettingsDiscard()) {
    return;
  }

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

    const label =
      b.dataset.action === "enable" ? "Connecting…" : "Disconnecting…";
    const restoreButton = setButtonPending(b, label);
    node.textContent = "";

    try {
      await request(b.dataset.action);
      node.textContent = "";
    } catch (e) {
      node.textContent = e.message;
    } finally {
      restoreButton();
      busy(false);
    }

    await refreshConnection();
  }),
);

async function refreshConnection() {
  if (pending || parent.EXTENSIONS.busy) {
    return;
  }

  try {
    const snapshot = await parent.EXTENSIONS.status(moduleID);
    const profiles = snapshot.data && snapshot.data.profiles;

    if (profiles) {
      updateProfileOptions(profiles);
    }

    const status = profiles && profiles[selectedProfile];

    renderConnection(
      status || {
        state: snapshot.state === "RUNNING" ? "STARTING" : snapshot.state,
        message: "Public status unavailable",
        networks: [],
      },
    );
  } catch (error) {
    renderDNSStatus({});
    const node = document.getElementById("api-error");

    node.hidden = false;
    node.textContent = error.message;
    document.getElementById("state-badge").textContent = "UNKNOWN";
    document.getElementById("state-message").textContent = error.message;
  }
}

loadProfiles()
  .catch((error) => {
    result.textContent = error.message;
  })
  .then(async () => {
    await loadSettings();
    await refreshConnection();
  });

setInterval(refreshConnection, 5000);

for (const operation of ["submit-otp", "cancel-otp"]) {
  const buttonID = operation === "submit-otp" ? "otp-submit" : "otp-cancel";

  document.getElementById(buttonID).addEventListener("click", async () => {
    if (pending || !otpChallengeID) {
      return;
    }

    const output = document.getElementById("otp-result");
    const code = document.getElementById("otp-code").value;
    document.getElementById("otp-code").value = "";
    busy(true);

    try {
      await parent.EXTENSIONS.call(moduleID, operation, {
        profile: selectedProfile,
        data: { challenge: otpChallengeID, code },
      });
      output.textContent =
        operation === "cancel-otp"
          ? "OTP challenge cancelled."
          : "Code accepted by the worker. Waiting for server authentication.";
    } catch (error) {
      output.textContent = error.message;
    } finally {
      busy(false);
    }
  });
}
