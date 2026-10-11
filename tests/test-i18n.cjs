// SPDX-License-Identifier: GPL-3.0-or-later
"use strict";

const { chromium } = require(process.env.PLAYWRIGHT_MODULE || "playwright");
const assert = require("assert");
const fs = require("fs");
const http = require("http");
const path = require("path");
const root = path.resolve(__dirname, "..");
const openconnectManifest = JSON.parse(
  fs.readFileSync(
    path.join(root, "modules/openconnect/packaging/module.json.in"),
    "utf8",
  ),
);
const modules = [
  {
    id: "openconnect",
    name: "OpenConnect VPN",
    version: "0.2.9",
    state: "RUNNING",
    ui: "/user/extensions-modules/openconnect/ui/index.html",
  },
  {
    id: "heartbeat",
    name: "Heartbeat example",
    version: "1.0.0",
    state: "STOPPED",
    ui: "/user/extensions-modules/heartbeat/ui/index.html",
  },
];
const requests = [];

let mailbox = {};

function statusWire() {
  return (
    "# OCF-STATUS1 " + Buffer.from(JSON.stringify(mailbox)).toString("base64")
  );
}

function createFixtureServer() {
  let uploadOperation = "";
  let uploadBytes = [];
  const profiles = ["default"];
  return http.createServer(async (request, response) => {
    const url = new URL(request.url, "http://localhost");
    const send = (body, type) => {
      response.setHeader("Content-Type", type);
      response.end(body);
    };

    if (url.pathname === "/user/extensions-modules.json") {
      return send(
        JSON.stringify({ modules, state: "READY", updated_at: Date.now() }),
        "application/json",
      );
    }

    if (url.pathname === "/user/extensions-status/openconnect.json") {
      const statuses = Object.fromEntries(
        profiles.map((profile) => [
          profile,
          {
            profile,
            state: "UP",
            fusion_name: "OCFusion (managed)",
            message: "IPv4 full tunnel; Wi-Fi/VLAN bindings active",
            networks: [
              { bridge: "br55", cidr: "192.168.52.0/24", sdn_idx: "4" },
            ],
          },
        ]),
      );

      return send(
        JSON.stringify({
          module: "openconnect",
          state: "RUNNING",
          updated_at: Date.now(),
          data_updated_at: Date.now(),
          data: { profiles: statuses },
        }),
        "application/json",
      );
    }

    if (url.pathname === "/user/ocfusion-probe.cgi") {
      return send("#!/bin/sh\nprintf 'probe'", "text/plain");
    }
    if (url.pathname === "/user/ocfusion-probe.asp") {
      return send(
        JSON.stringify({ ocfusion_template: "EN" }),
        "application/json",
      );
    }

    if (
      url.pathname === "/appGet.cgi" &&
      url.searchParams.get("hook") === "nvram_get(preferred_lang)"
    ) {
      return send(JSON.stringify({ preferred_lang: "EN" }), "application/json");
    }

    if (url.pathname === "/appGet.cgi") {
      return send(
        JSON.stringify({
          vpn_client4_custom: encodeURIComponent(statusWire()),
        }),
        "application/json",
      );
    }

    if (url.pathname === "/applyapp.cgi") {
      let body = "";

      for await (const part of request) {
        body += part;
      }

      const payload = JSON.parse(decodeURIComponent(body));
      const q = JSON.parse(
        Buffer.from(payload.vpn_client4_custom.slice(15), "base64").toString(),
      );

      requests.push(q);
      if (q.action === "module.call" && q.module === "openconnect") {
        assert(
          openconnectManifest.actions.includes(q.operation),
          "Undeclared module action: " + q.operation,
        );
      }
      if (q.action === "upload.begin") {
        uploadOperation = q.operation;
        uploadBytes = [];
      }
      if (q.action === "upload.chunk") {
        uploadBytes.push(Buffer.from(q.data, "base64"));
      }
      if (q.action === "upload.finish") {
        q.operation = uploadOperation;
        const payload = JSON.parse(Buffer.concat(uploadBytes).toString());
        if (q.operation === "add-profile") {
          profiles.push(payload.profile);
        }
        if (q.operation === "delete-profile") {
          profiles.splice(profiles.indexOf(payload.profile), 1);
        }
      }

      if (q.action === "module.call" && q.operation === "add-profile") {
        profiles.push(q.config.profile);
      }
      if (q.action === "module.call" && q.operation === "delete-profile") {
        profiles.splice(profiles.indexOf(q.config.profile), 1);
      }

      const data =
        q.operation === "profiles"
          ? profiles
          : q.operation === "settings"
            ? {
                revision: "revision1",
                server: "https://vpn.example/",
                enabled: true,
                has_certificate: true,
                has_private_key: true,
              }
            : q.operation === "diagnose"
              ? {
                  state: "UP",
                  message: "IPv4 full tunnel; Wi-Fi/VLAN bindings active",
                  networks: [
                    { bridge: "br55", cidr: "192.168.52.0/24", sdn_idx: "4" },
                  ],
                }
              : q.action === "platform.ping"
                ? { pong: true, response_storage: "RAM" }
                : { ok: true };

      mailbox = {
        state: "UP",
        networks: [],
        request_id: q.id,
        request_ok: true,
        request_data: data,
      };

      return send("{}", "application/json");
    }

    if (url.pathname === "/state.js") {
      return send(
        'var ui_lang = window.TEST_LANGUAGE || "EN"; function show_banner() { document.getElementById("TopBanner").innerHTML = \'<div class="banner1"><img src="/images/logo.png"></div>\'; }',
        "text/javascript",
      );
    }

    if (url.pathname === "/user/ocfusion.js") {
      return send(
        'window.OCF_SETTINGS={version:"0.2.9",key:"vpn_client4_custom",language:"RU",model:"TEST-ROUTER"};',
        "text/javascript",
      );
    }

    const files = {
      "/user/extensions.html": "web/import.html",
      "/user/extensions-panel.js": "web/panel.js",
      "/user/extensions-diagnostics.js": "web/diagnostics.js",
      "/user/ocfusion-i18n.js": "web/i18n.js",
      "/user/extensions-panel.css": "web/panel.css",
    };
    let filename = files[url.pathname];
    const prefix = "/user/extensions-modules/";

    if (url.pathname.startsWith(prefix)) {
      const relative = url.pathname.slice(prefix.length);

      if (relative.startsWith("openconnect/ui/")) {
        filename =
          "modules/openconnect/ui/" + relative.slice("openconnect/ui/".length);
      }

      if (relative.startsWith("heartbeat/ui/")) {
        filename =
          "docs/examples/heartbeat/ui/" +
          relative.slice("heartbeat/ui/".length);
      }
    }

    if (filename) {
      const type = filename.endsWith("js")
        ? "text/javascript"
        : filename.endsWith("css")
          ? "text/css"
          : "text/html";

      return send(fs.readFileSync(path.join(root, filename)), type);
    }

    if (url.pathname.endsWith(".js")) {
      return send("", "text/javascript");
    }

    if (url.pathname.endsWith(".css")) {
      return send("", "text/css");
    }

    response.statusCode = 404;
    response.end();
  });
}

async function testModuleUI(browser, address, language) {
  const page = await browser.newPage();
  const errors = [];

  page.on("pageerror", (error) => errors.push(error.message));
  await page.addInitScript((language) => {
    window.TEST_LANGUAGE = language;
  }, language);
  await page.addInitScript(() => {
    Object.defineProperty(window.crypto, "subtle", { value: undefined });
  });
  await page.goto(address + "/user/extensions.html#openconnect");

  const frame = page.frameLocator("#module-frame");

  await frame.locator("#vpn-server").waitFor();
  await page
    .waitForFunction(
      () => {
        const frame = document.getElementById("module-frame");
        const node =
          frame.contentDocument &&
          frame.contentDocument.getElementById("vpn-server");

        return node && node.value;
      },
      null,
      { timeout: 10000 },
    )
    .catch(async (error) => {
      console.error(
        "UI errors:",
        errors,
        "requests:",
        requests,
        "frame:",
        await frame.locator("body").textContent(),
      );

      throw error;
    });

  const ru = language === "RU";

  assert.equal(
    await page.locator("html").getAttribute("lang"),
    ru ? "ru" : "en",
  );
  assert.equal(
    (await frame.locator("#settings-save").textContent()).trim(),
    ru ? "Сохранить настройки" : "Save settings",
  );
  assert.equal(
    await frame.locator("#vpn-server").inputValue(),
    "https://vpn.example/",
  );
  await frame.locator("#connection-state").filter({ hasText: "UP" }).waitFor();
  assert.equal(
    await page.locator("dd[data-router-model]").textContent(),
    "TEST-ROUTER",
  );
  assert.equal(
    await page.locator("#TopBanner .banner1 a").getAttribute("href"),
    "https://github.com/nE0sIghT/ocfusion",
  );

  if (language === "EN") {
    await frame.locator("#vpn-mtu").fill("1400");
    await frame.locator("#settings-save").click();
    await frame
      .locator("#settings-result")
      .filter({ hasText: "Settings saved" })
      .waitFor();
    assert(
      requests.some(
        (q) =>
          q.action === "module.call" &&
          q.module === "openconnect" &&
          q.operation === "update-settings",
      ),
    );
  }

  if (language === "EN") {
    await frame.locator("#profile-new").click();
    await frame.locator("#profile-name").fill("office");
    await frame.locator("#profile-add").click();
    await frame
      .locator('#profile-select option[value="office"]')
      .waitFor({ state: "attached" });
    await frame.locator("#profile-select").selectOption("default");
    await frame.locator("#settings-save").waitFor({ state: "visible" });
  }

  assert(
    !requests.some(
      (request) =>
        request.operation === "diagnose" || request.operation === "profiles",
    ),
    "Status polling used the command mailbox",
  );

  await page.locator('a[data-page="system"]').click();
  assert.equal(
    await page.locator("#transport-module-json").getAttribute("href"),
    "/user/extensions-status/openconnect.json",
  );
  if (language === "RU") {
    assert.equal(
      (await page.locator("#transport-test").textContent()).trim(),
      "Запустить диагностику",
    );
  }

  if (language === "EN") {
    await page.locator("#transport-test").click();
    await page
      .locator("#transport-result")
      .filter({ hasText: "Diagnostics complete" })
      .waitFor();

    const report = JSON.parse(
      await page.locator("#transport-report").inputValue(),
    );

    assert.equal(
      report.tests.find((test) => test.channel === "ram_json").result,
      "PASS",
    );
    assert.equal(
      report.tests.find((test) => test.channel === "custom_cgi").result,
      "STATIC_FILE_ONLY",
    );
    assert.equal(
      report.tests.find((test) => test.channel === "stock_template").result,
      "PASS",
    );
    assert.equal(
      report.tests.find((test) => test.channel === "nvram_mailbox").result,
      "PASS",
    );
    assert.equal(report.authentication.result, "NOT_CHECKED");
  }

  // External and shipped modules use the same generated menu and iframe route.
  await page.locator('#module-menu a[href="#module-heartbeat"]').click();
  await frame.locator("#title").waitFor();
  assert.equal(
    await frame.locator("#title").textContent(),
    ru ? "Пример модуля" : "Module example",
  );
  assert.equal(
    await page.locator("#module-frame").getAttribute("src"),
    modules[1].ui,
  );
  assert.deepEqual(errors, []);

  for (const state of ["INSTALLING", "STOPPED"]) {
    await page.route("**/user/extensions-modules.json?*", (route) =>
      route.fulfill({
        contentType: "application/json",
        body: JSON.stringify({ modules, state, updated_at: Date.now() }),
      }),
    );
    await page.evaluate(() => loadModules());
    await page.locator("#platform-state").waitFor({ state: "visible" });
    await page.locator("#module-frame").waitFor({ state: "hidden" });
    assert.equal(await page.locator("#module-frame").getAttribute("src"), null);
    await page.unroute("**/user/extensions-modules.json?*");
  }

  await page.evaluate(() => loadModules());
  await frame.locator("#title").waitFor();
  await page.close();
}

async function testDiagnosticScriptFailure(browser, address) {
  const page = await browser.newPage();

  await page.addInitScript(() => {
    window.TEST_LANGUAGE = "EN";
  });
  await page.route("**/user/extensions-diagnostics.js?*", (route) =>
    route.fulfill({
      contentType: "text/javascript",
      body: "(() => {",
    }),
  );

  const failure = page.waitForEvent("pageerror");

  await page.goto(address + "/user/extensions.html#system");
  await failure;
  assert.equal(await page.locator("#transport-test").isDisabled(), true);
  assert(
    (await page.locator("#transport-result").textContent()).includes(
      "Communication diagnostics unavailable",
    ),
  );
  await page.close();
}

async function testFusion(browser, address, language) {
  const page = await browser.newPage();

  await page.goto(address + "/VPN/vpnc.html");
  await page.setContent(
    '<div class="svr_item_container"><div class="svr_item_text_container"><div class="main_info_text">OCFusion (managed)</div></div><div class="switch"></div></div>',
  );
  await page.evaluate((language) => {
    window.ui_lang = language;
  }, language);
  await page.addScriptTag({
    path: path.join(root, "modules/vpn/ui/fusion.js"),
  });

  const notice = page.locator(".ocfusion-managed-note");

  assert(
    (await notice.textContent()).includes(
      language === "RU" ? "Оставьте выключенным" : "Keep OFF",
    ),
  );
  assert.equal(
    await notice.locator("a").getAttribute("href"),
    "/user/extensions.html#modules",
  );
  assert.equal(await notice.locator("a").getAttribute("target"), "_top");
  await page.close();
}

async function main() {
  const server = createFixtureServer();
  let browser;

  try {
    await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));

    const address = "http://127.0.0.1:" + server.address().port;

    browser = await chromium.launch({ headless: true, args: ["--no-sandbox"] });

    for (const language of ["EN", "RU", "DE"]) {
      await testModuleUI(browser, address, language);
    }

    await testDiagnosticScriptFailure(browser, address);

    for (const language of ["EN", "RU"]) {
      await testFusion(browser, address, language);
    }

    assert(
      requests.some(
        (q) => q.module === "openconnect" && q.operation === "settings",
      ),
    );
    assert(
      !requests.some(
        (q) => q.module === "openconnect" && q.operation === "diagnose",
      ),
      "Routine status polling wrote to the mailbox",
    );
    console.log(
      "PASS: one iframe contract for builtin/external modules, settings/status API, EN/RU/fallback, model and Fusion links",
    );
  } finally {
    if (browser) {
      await browser.close();
    }

    await new Promise((resolve) => server.close(resolve));
  }
}

main().catch((error) => {
  console.error(error);
  process.exit(1);
});
