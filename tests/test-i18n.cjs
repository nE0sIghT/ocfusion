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
modules.push({
  id: "adguard-home",
  name: "AdGuard Home",
  version: "0.4",
  state: "RUNNING",
  ui: "/user/extensions-modules/adguard-home/ui/index.html",
});
const requests = [];

let mailbox = {};

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

function createFixtureServer() {
  const profiles = ["default"];
  let commandReply = null;
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

    if (url.pathname === "/user/extensions-status/adguard-home.json") {
      return send(
        JSON.stringify({
          module: "adguard-home",
          state: "RUNNING",
          updated_at: Date.now(),
          data_updated_at: Date.now(),
          data: { state: "DISABLED", dns_path: "WAN" },
        }),
        "application/json",
      );
    }

    if (url.pathname === "/user/extensions-command.json") {
      return send(
        JSON.stringify({
          state: "READY",
          resource: "ocf_probe_0123456789abcdef",
          instance: "0123456789abcdef0123456789abcdef",
          max_payload_bytes: 65536,
        }),
        "application/json",
      );
    }

    if (url.pathname === "/splash_page.cgi") {
      const parts = [];

      for await (const part of request) {
        parts.push(part);
      }

      const text = Buffer.concat(parts).toString();
      const fields = text.split(/------[^\r\n]+\r\n/);
      const css = fields.find((field) =>
        field.includes('name="splash_page_css"'),
      );
      assert(css, "CSS multipart field missing");
      assert(
        text.indexOf('name="splash_page_id"') <
          text.indexOf('name="splash_page_css"'),
      );

      const start = css.indexOf("\r\n\r\n") + 4;
      const packet = JSON.parse(css.slice(start).split("\r\n")[0]);
      if (packet.protocol === "ocfusion-command-v1") {
        assert.equal(packet.crc32, crc32(packet.payload));
        const q = JSON.parse(packet.payload);
        requests.push(q);
        if (q.action === "module.call" && q.module === "openconnect") {
          assert(
            openconnectManifest.actions.includes(q.operation),
            "Undeclared module action: " + q.operation,
          );
        }
        if (q.action === "module.call" && q.operation === "add-profile") {
          profiles.push(q.config.profile);
        }
        if (q.action === "module.call" && q.operation === "delete-profile") {
          profiles.splice(profiles.indexOf(q.config.profile), 1);
        }

        const data =
          q.module === "adguard-home" && q.operation === "settings"
            ? {
                revision: 0,
                enabled: false,
                upstreams: [],
                rules: [],
                filters: [],
                clients: [],
              }
            : q.module === "adguard-home" && q.operation === "catalog"
              ? [{name: "AdGuard DNS filter", url: "https://adguardteam.github.io/HostlistsRegistry/assets/filter_1.txt", homepage: "https://github.com/AdguardTeam/AdGuardSDNSFilter", description: "Ads and trackers, including Russian-language sites"}]
            : q.module === "adguard-home" && q.operation === "filter-status"
              ? {filters: [], updating: false}
            : q.module === "adguard-home" && q.operation === "statistics"
              ? { num_dns_queries: 3, num_blocked_filtering: 1 }
            : q.operation === "profiles"
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
                : { ok: true };

        mailbox = {
          state: "UP",
          networks: [],
          request_id: q.id,
          request_ok: true,
          request_data: data,
        };

        const payload = JSON.stringify(mailbox);
        commandReply = {
          instance: packet.instance,
          id: packet.id,
          payload,
          crc32: crc32(payload),
        };
        return send("{}", "text/html");
      }

      response.statusCode = 400;
      return send("{}", "application/json");
    }

    if (url.pathname === "/user/extensions-command-reply.json") {
      if (!commandReply) {
        response.statusCode = 404;
      }

      return send(JSON.stringify(commandReply), "application/json");
    }

    if (url.pathname === "/state.js") {
      return send(
        'var ui_lang = window.TEST_LANGUAGE || "EN"; function show_banner() { document.getElementById("TopBanner").innerHTML = \'<div class="banner1"><img src="/images/logo.png"></div>\'; }',
        "text/javascript",
      );
    }

    if (url.pathname === "/user/ocfusion.js") {
      return send(
        'window.OCF_SETTINGS={version:"0.2.9",revision:"0123456789012345678901234567890123456789",language:"RU",model:"TEST-ROUTER"};',
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

      if (relative.startsWith("adguard-home/ui/")) {
        filename = "modules/adguard-home/ui/" + relative.slice("adguard-home/ui/".length);
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
  const resourceURLs = await page
    .locator("body > script[src]")
    .evaluateAll((scripts) =>
      scripts.map((script) => script.getAttribute("src")),
    );

  assert.deepEqual(
    resourceURLs.map((url) => url.split("?")[0]),
    [
      "/user/ocfusion.js",
      "/user/extensions-panel.js",
      "/user/ocfusion-i18n.js",
    ],
  );
  assert(
    resourceURLs.every((url) => /\?_=[0-9]+$/.test(url)),
    "Own scripts can reuse same-version browser cache",
  );
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

  if (language === "EN") {
    await frame.locator("#settings-save:not([disabled])").waitFor();
    await frame.locator("body").evaluate(() => {
      renderConnection({
        state: "WAIT_OTP",
        message: "Enter one-time code",
        networks: [],
        otp: {
          id: "fixture-challenge",
          expires: Math.floor(Date.now() / 1000) + 120,
        },
      });
    });
    await frame.locator("#otp-code").fill("123456");
    await frame.locator("#otp-submit").click();
    await frame.locator("#otp-result").filter({hasText: "Code accepted"}).waitFor();
    assert.equal(await frame.locator("#otp-code").inputValue(), "");
    assert(requests.some((q) =>
      q.operation === "submit-otp" &&
      q.config.data.challenge === "fixture-challenge" &&
      q.config.data.code === "123456",
    ));
  }

  await page.locator('#module-menu a[href="#module-adguard-home"]').click();
  await frame.locator("#fields:not([disabled])").waitFor();
  assert.equal((await frame.locator('button[type="submit"]').textContent()).trim(),
    ru ? "Сохранить настройки" : "Save settings");
  await frame.locator("#tab-lists").click();
  await frame.locator("#catalog input").check();
  await frame.locator("#tab-protection").click();
  await frame.locator("#stats").click();
  await frame.locator("#statistics").filter({hasText: "3"}).waitFor();
  await frame.locator("#enabled").check();
  await frame.locator('button[type="submit"]').click();
  await frame.locator("#result").filter({hasText: ru ? "Настройки сохранены" : "Settings saved"}).waitFor();
  assert(requests.some((q) => q.action === "module.configure" && q.module === "adguard-home"));

  await page.locator('a[data-page="system"]').click();
  assert.equal(await page.locator("#transport-test").count(), 0);
  assert.equal(
    await page.locator("#system-revision").textContent(),
    "0123456789012345678901234567890123456789",
  );

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

async function testHeader(browser) {
  const page = await browser.newPage();
  const script = path.join(root, "platform/header.js");

  await page.setContent("<main>Header not ready</main>");
  await page.addScriptTag({ path: script });
  assert.equal(await page.locator("#asus_extensions_link").count(), 0);

  await page.evaluate(() => {
    const node = document.createElement("div");
    node.id = "status_block";
    node.innerHTML = '<button id="reboot">Reboot</button>';
    document.body.appendChild(node);
  });

  await page.locator("#asus_extensions_link").waitFor();
  assert.equal(await page.locator("#status_block > :first-child").getAttribute("id"), "asus_extensions_link");
  assert.equal(await page.locator("#asus_extensions_link").getAttribute("href"), "/user/extensions.html");
  assert.equal(await page.locator("#asus_extensions_link").evaluate((link) => link.style.marginRight), "16px");

  await page.addScriptTag({ path: script });
  assert.equal(await page.locator("#asus_extensions_link").count(), 1);

  await page.evaluate(() => {
    document.getElementById("status_block").innerHTML = '<button id="reboot">Reboot</button>';
  });

  await page.locator("#asus_extensions_link").waitFor();
  assert.equal(await page.locator("#asus_extensions_link").count(), 1);
  assert.equal(await page.locator("#reboot").count(), 1);
  await page.close();
}

async function main() {
  const server = createFixtureServer();
  let browser;

  try {
    await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));

    const address = "http://127.0.0.1:" + server.address().port;

    browser = await chromium.launch({ headless: true, args: ["--no-sandbox"] });

    await testHeader(browser);

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
