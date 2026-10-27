// SPDX-License-Identifier: GPL-3.0-or-later
"use strict";

const assert = require("assert");
const fs = require("fs");
const http = require("http");
const path = require("path");
const {chromium} = require(process.env.PLAYWRIGHT_MODULE || "playwright");
const root = path.resolve(__dirname, "..");

async function main() {
  const assets = {
    "/module/index.html": "modules/zapret2/ui/index.html",
    "/module/applications.js": "modules/zapret2/ui/applications.js",
    "/module/lists.js": "modules/zapret2/ui/lists.js",
    "/module/app.js": "modules/zapret2/ui/app.js",
    "/module/targets.js": "modules/zapret2/ui/targets.js",
    "/module/layout.js": "modules/zapret2/ui/layout.js",
    "/module/layout.css": "modules/zapret2/ui/layout.css",
    "/module/translations.js": "modules/zapret2/ui/translations.js",
    "/user/ocfusion-i18n.js": "web/i18n.js",
    "/user/extensions-panel.css": "web/panel.css",
  };
  const server = http.createServer((request, response) => {
    const url = new URL(request.url, "http://localhost");

    if (url.pathname === "/") {
      const language = url.searchParams.get("language") === "ru" ? "ru" : "en";
      response.end(`<!doctype html><script>
        window.OCF_I18N = {language: "${language}"};
        window.saved = {revision: 0, enabled: false, lists: [], networks: [], domains: [], tcp_ports: [80,443], udp_ports: [443], tcp_strategy: "split", udp_strategy: "off", split_position: 2, fake_ttl: 3, fake_repeats: 3};
        window.changes = [];
        window.moduleState = "DISABLED";
        window.listStatuses = [];
        window.EXTENSIONS = {
          action: async (id, operation) => {
            if (operation === "list-catalog") {
              return [{id: "telegram", name: "Telegram", paths: ["Services/telegram.lst", "Subnets/IPv4/telegram.lst"]}, {id: "youtube", name: "YouTube", paths: ["Services/youtube.lst"]}];
            }

            if (operation === "catalog") {
              return {presets: await window.EXTENSIONS.action(id, "presets"), lists: await window.EXTENSIONS.action(id, "list-catalog"), applications: [{id: "mtproto", name: "Telegram MTProto", transport: "tcp", methods: ["split", "fake"], defaults: {method:"split", ports:"*", packets:3, position:2, repeats:3, fragment:8, padding:2}}]};
            }

            if (operation === "presets") {
              return [{id: "hostname", name: "Hostname split", description: "Split within the server name.", protocol: "tcp", settings: {tcp_strategy: "split", split_positions: "1,midsld", sequence_overlap: 0, auto_ttl: false}}];
            }

            return {...window.saved};
          },
          call: async (id, operation, request) => {
            window.lastCall = {operation, request};

            if (operation === "refresh-lists") {
              window.listStatuses = [{id: "telegram", state: "ERROR", updated: 1793000000, domains: 3, networks: 2}];
              return {ok: true};
            }

            if (operation === "scan-start") {
              window.scan = {id: "test", state: "RUNNING", revision: window.saved.revision, results: []};
            } else {
              window.scan.state = "CANCELLED";
            }

            return window.scan;
          },
          configure: async (id, settings) => {
            window.changes.push(settings);
            await new Promise((resolve) => setTimeout(resolve, 200));
            window.saved = settings;
            window.listStatuses = settings.lists.map((id) => ({id, state: "READY", updated: 1793000000, domains: 3, networks: 2}));
          },
          status: async () => ({data: {state: window.moduleState, lists: window.listStatuses, scan: window.scan || {state: "IDLE"}}})
        };
      </script><iframe src="/module/index.html"></iframe>`);
      return;
    }

    const asset = assets[url.pathname];
    response.setHeader("Content-Type", url.pathname.endsWith(".js") ? "text/javascript" : url.pathname.endsWith(".css") ? "text/css" : "text/html");
    response.end(asset ? fs.readFileSync(path.join(root, asset)) : "");
  });
  let browser;

  try {
    await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
    browser = await chromium.launch({headless: true, args: ["--no-sandbox"]});

    for (const language of ["en", "ru"]) {
      const page = await browser.newPage();
      const errors = [];
      page.on("pageerror", (error) => errors.push(error.message));
      await page.goto("http://127.0.0.1:" + server.address().port + "/?language=" + language);
      const frame = page.frameLocator("iframe");
      await frame.locator("#fields:not([disabled])").waitFor();
      assert.equal(await frame.locator("#enabled").isChecked(), false);
      assert.equal(await frame.locator("#udp_strategy").inputValue(), "off");
      assert.equal(await frame.locator("[role=tabpanel]:visible").count(), 1);
      assert.equal(await frame.locator("#custom-strategy").getAttribute("open"), null);
      await frame.locator("#tab-management").focus();
      await frame.locator("#tab-management").press("End");
      assert.equal(await frame.locator("#panel-diagnostics").isVisible(), true);
      assert.equal(await frame.locator("#settings").isVisible(), false);
      await frame.locator("#tab-diagnostics").press("Home");
      await frame.locator("#service-picker").selectOption("telegram");
      await frame.locator("#service-add").click();
      assert.equal(await frame.locator("#service-lists > div").count(), 1);
      assert.equal((await page.evaluate(() => window.changes)).length, 0);
      await frame.locator("#manual-destinations summary").click();
      await frame.locator("#networks").fill("203.0.113.10\n2001:db8::/32");
      await frame.locator("#domains").fill("example.com");
      await frame.locator("#enabled").check();
      await frame.locator("#tab-strategies").click();
      await frame.locator("#custom-strategy > summary").click();
      assert.equal(await frame.locator("#fake_ttl").evaluate((element) => element.closest("tr").hidden), true);
      await frame.locator("#tcp_strategy").selectOption("fake-split");
      assert.equal(await frame.locator("#fake_ttl").evaluate((element) => element.closest("tr").hidden), false);
      await frame.locator("#split_positions").fill("host, midsld+1");

      await frame.locator("#application-mtproto-enabled").check();
      await frame.locator("#application-mtproto-ports").evaluate((element) => { element.value = "80,443,5222,50000-65535"; });
      await frame.locator("#auto_ttl").check();
      await frame.locator("#fake_sni").fill("example.net");
      await frame.locator("#fake_fooling").selectOption("badseq");
      await frame.locator("#packet_limit").fill("24");
      await frame.locator("#settings button[type=submit]").click();
      assert.equal(await frame.locator("#settings button[type=submit]").isDisabled(), true);
      await frame.locator("#fields:not([disabled])").waitFor();
      const saved = await page.evaluate(() => window.saved);
      assert.deepEqual(saved.networks, ["203.0.113.10", "2001:db8::/32"]);
      assert.deepEqual(saved.domains, ["example.com"]);
      assert.deepEqual(saved.tcp_ports, [80, 443]);
      assert.deepEqual(saved.lists, ["telegram"]);
      assert.equal(saved.applications[0].id, "mtproto");
      assert.equal(saved.applications[0].ports, "80,443,5222,50000-65535");
      assert.equal(saved.revision, 1);
      assert.equal(saved.enabled, true);
      assert.equal(saved.tcp_strategy, "fake-split");
      assert.equal(saved.split_positions, "host, midsld+1");
      assert.equal(saved.fake_fooling, "badseq");
      assert.equal(saved.fake_sni, "example.net");
      assert.equal(saved.auto_ttl, true);
      assert.equal(saved.auto_ttl_delta, -1);
      assert.equal(saved.packet_limit, 24);
      assert.equal(await frame.locator("#unsaved").isVisible(), false);
      await frame.locator("#preset").selectOption("hostname");
      assert.equal(await frame.locator("#unsaved").isVisible(), true);
      assert.equal(await frame.locator("#split_positions").inputValue(), "1,midsld");
      assert.equal(await frame.locator("#application-mtproto-enabled").isChecked(), true);
      assert.equal((await page.evaluate(() => window.changes)).length, 1);
      await frame.locator("#tab-finder").click();
      await frame.locator("#scan-target").selectOption("youtube.com");
      assert.equal(await frame.locator("#scan-host").inputValue(), "youtube.com");
      assert.equal((await page.evaluate(() => window.changes)).length, 1);
      assert.equal(await frame.locator("#settings").isVisible(), false);
      await frame.locator("#scan-host").fill("example.com");
      await frame.locator("#scan-start").click();
      await frame.locator("#scan-cancel:not([disabled])").waitFor();
      assert.deepEqual(await page.evaluate(() => window.lastCall), {operation: "scan-start", request: {revision: 1, host: "example.com", port: 443}});
      await page.evaluate(() => {
        window.scan = {id: "test", state: "COMPLETE", revision: 1, results: [{preset: "baseline", success: 0, attempts: 2}, {preset: "hostname", success: 2, attempts: 2, milliseconds: 125}]};
      });
      await frame.locator("#scan-results button").waitFor();
      assert((await frame.locator("#scan-results").innerText()).includes("2/2"));
      assert((await frame.locator("#scan-results").innerText()).includes("125 ms"));
      await frame.locator("#scan-results button").click();
      assert.equal(await frame.locator("#tcp_strategy").inputValue(), "split");
      assert.equal((await page.evaluate(() => window.changes)).length, 1);
      assert.equal(await frame.locator("#tab-strategies").getAttribute("aria-selected"), "true");
      await frame.locator("#tab-management").click();
      const body = await frame.locator("body").innerText();
      assert(body.includes(language === "ru" ? "IP-адреса и подсети" : "IP addresses and subnets"));
      assert(body.includes(language === "ru" ? "объединяются" : "are combined"));
      await frame.locator("#service-lists summary").click();
      await frame.locator("#service-refresh").click();
      await frame.locator("#service-refresh:not([disabled])").waitFor();
      await page.waitForTimeout(3200);
      assert.equal(await frame.locator("#service-lists details").getAttribute("open"), "");
      const serviceText = await frame.locator("#service-lists").innerText();
      assert(serviceText.includes(language === "ru" ? "Используется сохранённая копия" : "Using the saved copy"));
      await frame.locator("#service-lists button").click();
      assert.equal(await frame.locator("#service-lists > div").count(), 0);
      assert.deepEqual(await page.evaluate(() => window.saved.lists), ["telegram"]);
      assert.equal(await frame.locator("#domains").inputValue(), "example.com");
      assert.deepEqual(errors, []);
      await page.close();
    }
  } finally {
    if (browser) {
      await browser.close();
    }

    await new Promise((resolve) => server.close(resolve));
  }

  console.log("PASS: zapret2 EN/RU tabs, keyboard navigation, unsaved settings, target suggestions and scanner results");
}

main().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
