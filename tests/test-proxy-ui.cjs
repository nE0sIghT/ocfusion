// SPDX-License-Identifier: GPL-3.0-or-later
"use strict";

const assert = require("assert");
const fs = require("fs");
const http = require("http");
const path = require("path");
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || "playwright");
const root = path.resolve(__dirname, "..");

async function main() {
  const assets = {
    "/module/index.html": "modules/http-proxy/ui/index.html",
    "/module/app.js": "modules/http-proxy/ui/app.js",
    "/module/translations.js": "modules/http-proxy/ui/translations.js",
    "/user/ocfusion-i18n.js": "web/i18n.js",
    "/user/extensions-panel.css": "web/panel.css",
  };
  const server = http.createServer((request, response) => {
    const url = new URL(request.url, "http://localhost");

    if (url.pathname === "/") {
      const language = url.searchParams.get("language") === "ru" ? "ru" : "en";
      response.end(`<!doctype html><script>
        window.OCF_I18N = {language: "${language}"};
        window.saved = {revision: 0, enabled: false, target: "", bridge: "br0", port: 3128, allowed: [], dns: ["1.1.1.1"]};
        window.changes = [];
        window.proxyState = "DISABLED";
        window.EXTENSIONS = {
          action: async (id, operation) => {
            if (operation === "settings") {
              return {...window.saved};
            }
            return {
              targets: [
                {id: "managed:office", name: "Office", protocol: "openconnect"},
                {id: "native:7", name: "Native VPN", protocol: "WireGuard"}
              ],
              networks: [{bridge: "br0", cidr: "192.0.2.0/24"}]
            };
          },
          configure: async (id, settings) => {
            window.changes.push(settings);
            await new Promise((resolve) => setTimeout(resolve, 200));
            window.saved = settings;
          },
          status: async () => ({data: {state: window.proxyState, address: "192.0.2.1", port: 3128}})
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
      assert.equal(await frame.locator("#target option").count(), 3);
      assert.equal(await frame.locator("#port").inputValue(), "3128");
      assert.match(await frame.locator("h1").innerText(), language === "ru" ? /HTTP-прокси/ : /HTTP proxy/);
      const style = await frame.locator("#target").evaluate((node) => getComputedStyle(node).backgroundColor);
      assert.notEqual(style, "rgb(255, 255, 255)");
      await frame.locator("#target").selectOption("native:7");
      await frame.locator("#enabled").check();
      await frame.locator("#allowed").fill("192.0.2.20\n192.0.2.128/25");
      await frame.locator("#save").click();
      assert.equal(await frame.locator("#save").isDisabled(), true);
      await frame.locator("#fields:not([disabled])").waitFor();
      const changes = await page.evaluate(() => window.changes);
      assert.equal(changes.length, 1);
      assert.equal(changes[0].revision, 1);
      assert.equal(changes[0].target, "native:7");
      assert.deepEqual(changes[0].allowed, ["192.0.2.20", "192.0.2.128/25"]);
      await page.evaluate(() => {
        window.proxyState = "WAITING_VPN";
      });
      await frame.locator("#state").filter({hasText: language === "ru" ? "Ожидание VPN" : "Waiting for VPN"}).waitFor();
      assert.deepEqual(errors, []);
      await page.close();
    }

    console.log("PASS: proxy iframe, native/managed VPN choice, EN/RU and revisioned settings");
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
