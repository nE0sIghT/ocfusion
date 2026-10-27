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
    "/module/app.js": "modules/zapret2/ui/app.js",
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
        window.saved = {revision: 0, enabled: false, networks: [], domains: [], tcp_ports: [80,443], udp_ports: [443], tcp_strategy: "split", udp_strategy: "off", split_position: 2, fake_ttl: 3, fake_repeats: 3};
        window.changes = [];
        window.moduleState = "DISABLED";
        window.EXTENSIONS = {
          action: async () => ({...window.saved}),
          configure: async (id, settings) => {
            window.changes.push(settings);
            await new Promise((resolve) => setTimeout(resolve, 200));
            window.saved = settings;
          },
          status: async () => ({data: {state: window.moduleState}})
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
      await frame.locator("#networks").fill("203.0.113.10\n2001:db8::/32");
      await frame.locator("#domains").fill("example.com");
      await frame.locator("#enabled").check();
      await frame.locator("#settings button[type=submit]").click();
      assert.equal(await frame.locator("#settings button[type=submit]").isDisabled(), true);
      await frame.locator("#fields:not([disabled])").waitFor();
      const saved = await page.evaluate(() => window.saved);
      assert.deepEqual(saved.networks, ["203.0.113.10", "2001:db8::/32"]);
      assert.deepEqual(saved.domains, ["example.com"]);
      assert.deepEqual(saved.tcp_ports, [80, 443]);
      assert.equal(saved.revision, 1);
      assert.equal(saved.enabled, true);
      const body = await frame.locator("body").innerText();
      assert(body.includes(language === "ru" ? "IP-адреса и подсети" : "IP addresses and subnets"));
      assert(body.includes(language === "ru" ? "соответствовать обоим" : "match both"));
      assert.deepEqual(errors, []);
      await page.close();
    }
  } finally {
    if (browser) {
      await browser.close();
    }

    await new Promise((resolve) => server.close(resolve));
  }

  console.log("PASS: zapret2 EN/RU, selective targets, settings revision and pending controls");
}

main().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
