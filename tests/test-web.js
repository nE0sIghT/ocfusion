// SPDX-License-Identifier: GPL-3.0-or-later
"use strict";

const assert = require("assert");
const crypto = require("crypto");
const fs = require("fs");
const vm = require("vm");

for (const filename of ["import.html", "panel.js", "i18n.js"]) {
  const shared = fs.readFileSync(__dirname + "/../web/" + filename, "utf8");

  assert(
    !/openconnect|GT-BE98/i.test(shared),
    "Module or router hardcode in shared " + filename,
  );
}

const panelScript = fs.readFileSync(__dirname + "/../web/panel.js", "utf8");
const mailboxKey = "vpn_client4_custom";
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

function createMailbox(acknowledge) {
  let status = { state: "UP", networks: [] };
  const requests = [];
  const chunks = [];

  async function fetch(url, options) {
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

    if (options.method !== "POST") {
      const encoded = Buffer.from(JSON.stringify(status)).toString("base64");

      return {
        ok: true,
        json: async () => ({
          [mailboxKey]: encodeURIComponent("# OCF-STATUS1 " + encoded),
        }),
      };
    }

    const form = JSON.parse(decodeURIComponent(options.body));

    assert.equal(form.rc_service, "");
    assert.equal(form.action_mode, "apply");

    const envelope = form[mailboxKey];

    assert(envelope.length < 4096);
    assert(envelope.startsWith("# EXT-REQUEST1 "));

    const request = JSON.parse(
      Buffer.from(envelope.slice(15), "base64").toString(),
    );

    requests.push(request);

    if (request.action === "upload.chunk") {
      chunks.push(Buffer.from(request.data, "base64"));
    }

    status = {
      state: "UP",
      networks: [],
      request_id: acknowledge ? request.id : "a".repeat(32),
      request_ok: true,
      request_data: { result: "accepted" },
    };

    return { ok: true, text: async () => "" };
  }

  return { fetch, requests, chunks };
}

function createContext(nodes, mailbox) {
  return {
    window: {
      OCF_SETTINGS: { unit: 4, index: 7, key: mailboxKey, version: "0.2.1" },
      addEventListener() {},
    },
    location: { hash: "" },
    document: {
      forms: {},
      querySelector: () => null,
      addEventListener() {},
      getElementById: (id) => nodes[id],
      querySelectorAll: () => [],
    },
    crypto: crypto.webcrypto,
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
    setTimeout(callback) {
      callback();
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

  if (!acknowledge) {
    // A stale UP response must not acknowledge a newly submitted request.
    assert.equal(mailbox.requests.length, 1);
    assert(error && error.message.includes("did not acknowledge"));

    return;
  }

  assert.equal(mailbox.requests[0].action, "upload.begin");
  assert.equal(mailbox.requests.at(-1).action, "upload.finish");
  assert.equal(Buffer.concat(mailbox.chunks).toString(), profile);
  assert.equal(
    mailbox.requests[0].sha256,
    crypto.createHash("sha256").update(profile).digest("hex"),
  );
  assert.equal(error, undefined);
  assert.equal(reply.result, "accepted");

  if (operation) {
    assert.equal(mailbox.requests[0].kind, "module.call");
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

async function main() {
  await testUnavailablePlatform();
  await testProfileUpload(true);
  await testProfileUpload(false);
  await testProfileUpload(true, "update-settings");
  console.log(
    "PASS: >4KiB chunk upload, hash, bounded stock mailbox, nonce acknowledgement; stale UP cannot acknowledge",
  );
}

main().catch((error) => {
  console.error(error);
  process.exit(1);
});
