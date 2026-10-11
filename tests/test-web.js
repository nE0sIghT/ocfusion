// SPDX-License-Identifier: GPL-3.0-or-later
"use strict";

const assert = require("assert");
const crypto = require("crypto");
const fs = require("fs");
const vm = require("vm");

for (const filename of [
  "import.html",
  "panel.js",
  "i18n.js",
  "diagnostics.js",
]) {
  const shared = fs.readFileSync(__dirname + "/../web/" + filename, "utf8");

  assert(
    !/openconnect|GT-BE98/i.test(shared),
    "Module or router hardcode in shared " + filename,
  );
}

// Stock ASUS template markers are interpreted even inside JavaScript strings.
for (const directory of ["web", "modules/openconnect/ui", "modules/vpn/ui"]) {
  for (const filename of fs.readdirSync(__dirname + "/../" + directory)) {
    if (!filename.endsWith(".js")) {
      continue;
    }

    const source = fs.readFileSync(
      __dirname + "/../" + directory + "/" + filename,
      "utf8",
    );

    assert(
      !source.includes("<%"),
      "ASUS template marker in runtime JavaScript: " + filename,
    );
  }
}

const panelScript = fs.readFileSync(__dirname + "/../web/panel.js", "utf8");

const nodeIds = [
  "result",
  "status",
  "import",
  "file",
  "api-error",
  "platform-state",
  "module-list",
  "module-overview",
  "module-menu",
  "state-badge",
  "state-message",
  "connection-state",
  "network-summary",
  "networks",
  "platform-version",
  "system-version",
  "about-version",
  "vpn-settings",
  "settings-fields",
  "settings-result",
  "settings-reload",
  "settings-save",
];

function createNodes() {
  const nodes = {};

  for (const id of nodeIds) {
    nodes[id] = {
      textContent: "",
      disabled: false,
      hidden: false,
      listeners: {},
      replaceChildren() {},
      addEventListener(event, handler) {
        this.listeners[event] = handler;
      },
    };
  }

  return nodes;
}

function crc32(payload) {
  let crc = 0xffffffff;

  for (const byte of Buffer.from(payload)) {
    crc ^= byte;

    for (let bit = 0; bit < 8; bit++) {
      crc = (crc >>> 1) ^ (crc & 1 ? 0xedb88320 : 0);
    }
  }

  return ((crc ^ 0xffffffff) >>> 0).toString(16).padStart(8, "0");
}

function createMailbox(acknowledge) {
  let reply = null;
  const requests = [];
  const instance = "0123456789abcdef0123456789abcdef";

  async function fetch(url, options = {}) {
    if (url.startsWith("/user/extensions-modules.json")) {
      return {
        ok: true,
        json: async () => ({
          state: "READY",
          updated_at: Date.now(),
          modules: [],
        }),
      };
    }

    if (url.startsWith("/user/extensions-upload-probe.json")) {
      return {
        ok: true,
        json: async () => ({
          state: "READY",
          resource: "ocf_probe_0123456789abcdef",
          instance,
        }),
      };
    }

    if (url.startsWith("/user/extensions-command-reply.json")) {
      return { ok: true, text: async () => JSON.stringify(reply) };
    }

    assert.equal(url, "/splash_page.cgi");
    assert.equal(options.method, "POST");
    const packet = JSON.parse(await options.body.get("splash_page_css").text());

    assert.equal(packet.crc32, crc32(packet.payload));
    const request = JSON.parse(packet.payload);

    requests.push(request);
    const id = acknowledge ? request.id : "a".repeat(32);
    const payload = JSON.stringify({
      request_id: id,
      request_ok: true,
      request_data: { result: "accepted" },
    });

    reply = {
      id,
      instance,
      payload,
      crc32: acknowledge === "corrupt" ? "00000000" : crc32(payload),
    };

    return { ok: true, text: async () => "" };
  }

  return { fetch, requests };
}

function createContext(nodes, mailbox) {
  return {
    window: {
      OCF_SETTINGS: { version: "0.3" },
      addEventListener() {},
    },
    location: { hash: "", protocol: "https:" },
    document: {
      forms: {},
      createElement: () => ({}),
      head: { appendChild() {} },
      querySelector: () => null,
      addEventListener() {},
      getElementById: (id) => nodes[id],
      querySelectorAll: () => [],
    },
    crypto: crypto.webcrypto,
    FormData,
    Blob,
    AbortController,
    clearTimeout() {},
    TextEncoder,
    TextDecoder,
    Uint8Array,
    Date,
    JSON,
    Promise,
    Error,
    atob: (value) => Buffer.from(value, "base64").toString("binary"),
    btoa: (value) => Buffer.from(value, "binary").toString("base64"),
    setInterval() {},
    setTimeout(callback, milliseconds) {
      if (milliseconds !== 30000) {
        callback();
      }
    },
    fetch: mailbox.fetch,
  };
}

async function testProfileUpload(acknowledge, operation = "") {
  const nodes = createNodes();
  const profile = JSON.stringify({
    schema: 1,
    server: "https://vpn.example.org/",
    password: "x".repeat(6000),
    enabled: true,
  });

  nodes.file.files = [{ size: profile.length, text: async () => profile }];

  const mailbox = createMailbox(acknowledge);
  const context = createContext(nodes, mailbox);

  vm.runInNewContext(panelScript, context);

  await vm.runInNewContext("loadModules()", context);

  let error;
  let reply;

  try {
    reply = operation
      ? await context.window.EXTENSIONS.call(
          "example",
          operation,
          JSON.parse(profile),
        )
      : await context.window.EXTENSIONS.configure(
          "openconnect",
          JSON.parse(profile),
        );
  } catch (caught) {
    error = caught;
  }

  if (!acknowledge || acknowledge === "corrupt") {
    // A stale UP response must not acknowledge a newly submitted request.
    assert.equal(mailbox.requests.length, 1);
    assert(error && error.message.includes("did not acknowledge"));

    return;
  }

  assert.equal(mailbox.requests.length, 1);
  assert.equal(
    mailbox.requests[0].action,
    operation ? "module.call" : "module.configure",
  );
  assert.equal(JSON.stringify(mailbox.requests[0].config), profile);
  assert.equal(error, undefined);
  assert.equal(reply.result, "accepted");

  if (operation) {
    assert.equal(mailbox.requests[0].operation, operation);
    assert.equal(mailbox.requests[0].module, "example");
  }
}

async function testUnavailablePlatform() {
  for (const state of ["STOPPED", "INSTALLING", "READY"]) {
    const nodes = createNodes();
    const mailbox = createMailbox(true);
    const originalFetch = mailbox.fetch;

    mailbox.fetch = async (url, options) => {
      if (url.startsWith("/user/extensions-modules.json")) {
        return {
          ok: true,
          json: async () => ({
            state,
            updated_at: Date.now() - 60000,
            modules: [],
          }),
        };
      }

      return originalFetch(url, options);
    };

    const context = createContext(nodes, mailbox);

    vm.runInNewContext(panelScript, context);
    await vm.runInNewContext("loadModules()", context);
    assert.equal(nodes["platform-state"].hidden, false);
    await assert.rejects(
      context.window.EXTENSIONS.action("example", "diagnose"),
      /not ready/,
    );
    assert.equal(mailbox.requests.length, 0);
  }
}

async function testPublicStatusReads() {
  const nodes = createNodes();
  const mailbox = createMailbox(true);
  const originalFetch = mailbox.fetch;
  let timestamp = Date.now();
  let body = { profiles: { default: { state: "UP" } } };

  mailbox.fetch = async (url, options) => {
    if (url.startsWith("/user/extensions-status/example.json")) {
      return {
        ok: true,
        text: async () =>
          JSON.stringify({
            module: "example",
            state: "RUNNING",
            updated_at: timestamp,
            data_updated_at: timestamp,
            data: body,
          }),
      };
    }

    return originalFetch(url, options);
  };

  const context = createContext(nodes, mailbox);

  vm.runInNewContext(panelScript, context);
  await vm.runInNewContext("loadModules()", context);
  const status = await context.window.EXTENSIONS.status("example");

  assert.equal(status.data.profiles.default.state, "UP");
  assert.equal(mailbox.requests.length, 0);

  timestamp -= 60000;
  await assert.rejects(
    context.window.EXTENSIONS.status("example"),
    /unavailable/,
  );
  await assert.rejects(
    context.window.EXTENSIONS.status("..\/example"),
    /Invalid/,
  );

  timestamp = Date.now();
  body = null;
  assert.equal((await context.window.EXTENSIONS.status("example")).data, null);
}

async function testHTTPWithoutWebCrypto() {
  const nodes = createNodes();
  const mailbox = createMailbox(true);
  const context = createContext(nodes, mailbox);

  context.location.protocol = "http:";
  context.crypto = {
    getRandomValues: (bytes) => crypto.webcrypto.getRandomValues(bytes),
  };
  vm.runInNewContext(panelScript, context);
  await vm.runInNewContext("loadModules()", context);

  assert.equal(vm.runInNewContext('crc32("123456789")', context), "cbf43926");
  assert.equal(vm.runInNewContext('crc32("Привет")', context), "a8eab199");
  const result = await context.window.EXTENSIONS.call("example", "settings", {
    label: "Привет",
  });

  assert.equal(result.result, "accepted");
  await assert.rejects(
    context.window.EXTENSIONS.configure("example", {
      secret: "x".repeat(2000),
    }),
    /HTTPS/,
  );
  assert.equal(mailbox.requests.length, 1);
}

async function main() {
  await testHTTPWithoutWebCrypto();
  await testPublicStatusReads();
  await testUnavailablePlatform();
  await testProfileUpload(true);
  await testProfileUpload(false);
  await testProfileUpload("corrupt");
  await testProfileUpload(true, "update-settings");
  console.log(
    "PASS: direct >4KiB RAM upload, CRC32, nonce acknowledgement and readiness; stale replies cannot acknowledge",
  );
}

main().catch((error) => {
  console.error(error);
  process.exit(1);
});
