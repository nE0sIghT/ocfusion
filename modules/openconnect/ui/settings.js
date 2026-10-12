// SPDX-License-Identifier: GPL-3.0-or-later
let settingsSnapshot = null;
let settingsLoading = false;
let settingsLoaded = false;

const settingsForm = document.getElementById("vpn-settings");
const settingsFields = document.getElementById("settings-fields");
const settingsResult = document.getElementById("settings-result");

function fillSettings(s) {
  settingsSnapshot = s;
  document.getElementById("fusion-profile-name").textContent =
    s.fusion_name || "OCFusion (managed)";

  const values = {
    "vpn-server": s.server,
    "vpn-protocol": s.protocol || "anyconnect",
    "vpn-username": s.username,
    "vpn-authgroup": s.authgroup,
    "vpn-servercert": s.servercert,
    "vpn-dns": (s.dns || []).join(", "),
    "vpn-mtu": s.mtu || "",
  };

  for (const [id, value] of Object.entries(values)) {
    document.getElementById(id).value = value || "";
  }

  document.getElementById("vpn-auth").value =
    s.has_certificate && s.has_private_key ? "certificate" : "password";
  document.getElementById("vpn-native-dns").checked = s.native_dns;
  document.getElementById("vpn-dtls").checked = !s.no_dtls;
  document.getElementById("vpn-enabled").checked = s.enabled;
  document.getElementById("vpn-password").value = "";
  document.getElementById("vpn-password").placeholder = s.has_password
    ? "Leave blank to keep the saved password"
    : "Enter password";

  for (const id of ["clear-password", "clear-ca", "clear-client"]) {
    document.getElementById(id).checked = false;
  }

  for (const id of ["vpn-ca", "vpn-certificate", "vpn-private-key"]) {
    document.getElementById(id).value = "";
  }

  for (const [id, exists] of [
    ["password-state", s.has_password],
    ["ca-state", s.has_ca],
    ["certificate-state", s.has_certificate],
    ["private-key-state", s.has_private_key],
  ]) {
    document.getElementById(id).textContent = exists
      ? "Saved on the router."
      : "Not configured.";
  }

  settingsLoaded = true;
  settingsFields.disabled = false;
  settingsResult.textContent = s.revision
    ? "Current settings loaded."
    : "Create a connection profile.";
}

async function loadSettings(force = false) {
  if (settingsLoading || pending || (settingsLoaded && !force)) {
    return;
  }

  settingsLoading = true;
  busy(true);
  settingsResult.textContent = "Loading settings…";

  try {
    const st = await request("settings");

    if (!st) {
      throw Error("Service did not return settings");
    }

    fillSettings(st);
  } catch (e) {
    settingsResult.textContent = "Unable to load settings: " + e.message;
  } finally {
    settingsLoading = false;
    busy(false);
  }
}

document
  .getElementById("settings-reload")
  .addEventListener("click", () => loadSettings(true));

settingsForm.addEventListener("submit", async (event) => {
  event.preventDefault();

  if (pending || !settingsSnapshot) {
    return;
  }

  busy(true);
  settingsResult.textContent = "Checking settings…";

  try {
    const server = document.getElementById("vpn-server").value.trim();
    const url = new URL(server);

    if (url.protocol !== "https:" || url.username || url.password || url.hash) {
      throw Error(
        "Enter an HTTPS URL without embedded credentials or a fragment.",
      );
    }

    const mtuText = document.getElementById("vpn-mtu").value.trim();
    const mtu = mtuText ? Number(mtuText) : 0;

    if (mtu !== 0 && (!Number.isInteger(mtu) || mtu < 576 || mtu > 1500)) {
      throw Error("MTU: 576 to 1500, or leave blank.");
    }

    const dns = document
      .getElementById("vpn-dns")
      .value.trim()
      .split(/[\s,;]+/)
      .filter(Boolean);

    if (
      dns.some(
        (ip) =>
          !/^\d{1,3}(\.\d{1,3}){3}$/.test(ip) ||
          ip.split(".").some((n) => Number(n) > 255),
      )
    ) {
      throw Error("DNS: enter IPv4 addresses.");
    }

    const fields = {
      native_dns: document.getElementById("vpn-native-dns").checked,
      server,
      protocol: document.getElementById("vpn-protocol").value,
      username: document.getElementById("vpn-username").value,
      authgroup: document.getElementById("vpn-authgroup").value,
      servercert: document.getElementById("vpn-servercert").value.trim(),
      dns,
      mtu,
      no_dtls: !document.getElementById("vpn-dtls").checked,
      enabled: document.getElementById("vpn-enabled").checked,
    };
    const password = document.getElementById("vpn-password").value;
    const auth = document.getElementById("vpn-auth").value;
    const previousAuth =
      settingsSnapshot.has_certificate && settingsSnapshot.has_private_key
        ? "certificate"
        : "password";

    if (document.getElementById("clear-password").checked && password) {
      throw Error("Choose either password replacement or removal.");
    }

    if (password) {
      fields.password = password;
    }

    if (document.getElementById("clear-password").checked) {
      fields.password = null;
    }

    if (auth !== previousAuth) {
      if (auth === "password") {
        fields.certificate_pem = null;
        fields.private_key_pem = null;
      } else {
        fields.password = password || null;
      }
    }

    const files = [
      ["vpn-ca", "ca_pem", "clear-ca"],
      ["vpn-certificate", "certificate_pem", "clear-client"],
      ["vpn-private-key", "private_key_pem", "clear-client"],
    ];

    for (const [id, key, clear] of files) {
      const f = document.getElementById(id).files[0];

      if (f && document.getElementById(clear).checked) {
        throw Error("Choose either PEM replacement or removal.");
      }

      if (f) {
        if (f.size > 16384) {
          throw Error("PEM file exceeds 16 KiB");
        }

        fields[key] = await f.text();
      }

      if (document.getElementById(clear).checked) {
        fields[key] = null;
      }
    }

    const hasPassword =
      fields.password === undefined
        ? settingsSnapshot.has_password
        : !!fields.password;
    const hasCert =
      fields.certificate_pem === undefined
        ? settingsSnapshot.has_certificate
        : !!fields.certificate_pem;
    const hasKey =
      fields.private_key_pem === undefined
        ? settingsSnapshot.has_private_key
        : !!fields.private_key_pem;

    if (auth === "password" && !hasPassword) {
      throw Error("Enter a password.");
    }

    if (auth === "certificate" && (!hasCert || !hasKey)) {
      throw Error("Upload the client certificate and private key.");
    }

    if (hasCert !== hasKey) {
      throw Error("Provide both client certificate and private key.");
    }

    const bytes = new TextEncoder().encode(
      JSON.stringify({ revision: settingsSnapshot.revision, fields }),
    );

    await uploadConfig(
      bytes,
      "openconnect",
      (text) => {
        settingsResult.textContent = text;
      },
      "profile.patch",
    );
    settingsResult.textContent =
      "Settings saved. Wi-Fi/VLAN bindings preserved.";
    settingsLoaded = false;
  } catch (e) {
    settingsResult.textContent = e.message;
  } finally {
    busy(false);
  }

  if (!settingsLoaded) {
    await loadSettings(true);

    if (settingsLoaded) {
      settingsResult.textContent =
        "Settings saved. Wi-Fi/VLAN bindings preserved.";
    }
  }
});

for (const action of ["dns-vpn", "dns-wan"]) {
  document.getElementById(action).addEventListener("click", async () => {
    if (pending) {
      return;
    }

    busy(true);
    const output = document.getElementById("dns-status");

    try {
      const status = await request(action);
      output.textContent = status.path === "VPN" ? "DNS uses VPN." : "DNS uses WAN.";
    } catch (error) {
      output.textContent = error.message;
    } finally {
      busy(false);
    }
  });
}
