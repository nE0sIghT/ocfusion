// SPDX-License-Identifier: GPL-3.0-or-later
"use strict";

const { chromium } = require(process.env.PLAYWRIGHT_MODULE || "playwright");
const assert = require("assert");
const fs = require("fs");
const http = require("http");
const path = require("path");
const root = path.resolve(__dirname, "..");
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
  return http.createServer(async (request, response) => {
    const url = new URL(request.url, "http://localhost");
    const send = (body, type) => {
      response.setHeader("Content-Type", type);
      response.end(body);
    };

    if (url.pathname === "/user/extensions-modules.json") {
      return send(JSON.stringify({ modules }), "application/json");
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

      const data =
        q.operation === "settings"
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
          q.action === "upload.begin" &&
          q.kind === "module.call" &&
          q.module === "openconnect" &&
          q.operation === "update-settings",
      ),
    );
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

    for (const language of ["EN", "RU"]) {
      await testFusion(browser, address, language);
    }

    assert(
      requests.some(
        (q) => q.module === "openconnect" && q.operation === "settings",
      ),
    );
    assert(
      requests.some(
        (q) => q.module === "openconnect" && q.operation === "diagnose",
      ),
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
