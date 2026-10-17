// SPDX-License-Identifier: GPL-3.0-or-later
"use strict";

const assert = require("assert");
const fs = require("fs");
const http = require("http");
const path = require("path");
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || "playwright");
const root = path.resolve(__dirname, "..");

async function testPage(browser, address, language) {
  const page = await browser.newPage();
  const errors = [];
  page.on("pageerror", (error) => errors.push(error.message));
  await page.goto(address + "/?language=" + language);

  const frame = page.frameLocator("iframe");
  const apply = frame.locator("#apply");
  await apply.waitFor();
  await page.waitForFunction(() => window.calls.includes("settings"));
  await frame.locator("#fields:not([disabled])").waitFor();
  assert.equal(await apply.isEnabled(), false);
  assert.equal(await frame.locator("#profile").inputValue(), "stock");
  assert.deepEqual(
    await frame
      .locator("#profile option")
      .evaluateAll((options) => options.map((option) => option.value)),
    language === "RU" ? ["stock", "AU", "AA", "XX", "KR"] : ["stock", "AU", "KR", "AA", "XX"],
  );
  assert.match(
    await frame.locator('#profile option[value="AA"]').innerText(),
    language === "RU" ? /^США \(US\/102\)$/ : /^United States \(US\/102\)$/,
  );
  const region = frame.locator("#profile");
  assert.deepEqual(
    await region.evaluate((node) => ({
      background: getComputedStyle(node).backgroundColor,
      color: getComputedStyle(node).color,
      scheme: getComputedStyle(node).colorScheme,
    })),
    {
      background: "rgb(16, 16, 16)",
      color: "rgb(238, 238, 238)",
      scheme: "dark",
    },
  );
  await region.focus();
  assert.equal(
    await region.evaluate((node) => getComputedStyle(node).outlineColor),
    "rgb(239, 146, 47)",
  );

  assert.equal(await frame.locator("#restore").isDisabled(), true);
  assert.equal(await frame.locator("#radios article").count(), 3);
  assert.match(await frame.locator("#radios").innerText(), /100 104 108 140/);
  assert.match(
    await frame.locator("h1").innerText(),
    language === "RU" ? /Настройки радиомодулей/ : /Wi-Fi radio settings/,
  );
  assert.equal(
    await frame.locator('#profile option[value="XX"]').isDisabled(),
    true,
  );
  assert.equal(await frame.locator("#preview-bands").isVisible(), false);
  assert.equal(await frame.locator("#preview-note").isVisible(), false);
  assert.match(
    await frame.locator("#radios").textContent(),
    /5500, 5520, 5540, 5700/,
  );
  if (language === "RU") {
    await frame
      .locator('#profile option[value="AU"]')
      .filter({ hasText: "Австралия" })
      .waitFor({ state: "attached" });
  }
  assert.equal(await frame.locator("#radios details[open]").count(), 0);
  assert.equal(await frame.locator("#preview-bands .radio-band").count(), 0);
  assert.equal(
    await frame.locator("#radios").getByText("23 (0x17)").isVisible(),
    false,
  );
  await frame.locator("#radios summary").first().click();
  assert.equal(
    await frame.locator("#radios").getByText("23 (0x17)").isVisible(),
    true,
  );
  assert.deepEqual(await page.evaluate(() => window.changes), []);
  assert.equal(await frame.locator("#reboot-notice").isVisible(), false);

  await frame.locator("#profile").selectOption("KR");
  assert.match(
    await frame.locator("#selection-status").innerText(),
    language === "RU" ? /не сохранены/ : /not saved/,
  );
  await frame.locator("#profile-preview summary").click();
  assert.equal(await frame.locator("#region-comparison tr").count(), 6);
  assert.deepEqual(
    await frame
      .locator("#region-comparison tr")
      .evaluateAll((rows) => rows.map((row) => row.dataset.profile)),
    language === "RU"
      ? ["AU", "EU", "AA", "XX", "KR", "JP"]
      : ["AU", "EU", "JP", "KR", "AA", "XX"],
  );
  assert.match(
    await frame.locator('tr[data-profile="KR"]').innerText(),
    /1-229/,
  );
  assert.equal(
    await frame.locator('tr[data-profile="KR"]').getAttribute("data-selected"),
    "true",
  );
  assert.match(
    await frame.locator('tr[data-profile="XX"]').innerText(),
    language === "RU" ? /Недоступен/ : /Unavailable/,
  );
  assert.match(
    await frame.locator("#profile-preview").textContent(),
    /GT-BE98/,
  );
  assert.match(await frame.locator("#preview-bands").innerText(), /5955-7095/);
  assert.match(
    await frame.locator("#preview-bands").innerText(),
    /31, 63, 95, 127, 159, 191/,
  );
  assert.deepEqual(await page.evaluate(() => window.changes), []);

  await frame.locator("#profile").selectOption("AU");
  assert.match(await frame.locator("#preview-bands").innerText(), /2412-2462/);
  assert.match(await frame.locator("#preview-bands").innerText(), /5955-6415/);
  await apply.click();
  await frame.locator("#restore:not([disabled])").waitFor();
  assert.equal(await apply.isDisabled(), true);
  assert.equal(await frame.locator("#selection-status").innerText(), "");
  assert.equal(await frame.locator("#reboot-notice").isVisible(), true);
  const noticeBox = await frame.locator("#reboot-notice").boundingBox();
  const fieldsBox = await frame.locator("#fields").boundingBox();
  assert(noticeBox.y < fieldsBox.y, "Reboot notice must precede the controls");
  assert.equal(await frame.locator("#result").innerText(), "");
  assert.deepEqual(await page.evaluate(() => window.changes), [
    { operation: "apply", data: { revision: 0, profile: "AU" } },
  ]);

  await frame.locator("#restore").click();
  await frame.locator("#restore[disabled]").waitFor();
  assert.equal(await frame.locator("#profile").inputValue(), "stock");
  assert.equal(await apply.isDisabled(), true);
  assert.equal(await frame.locator("#selection-status").innerText(), "");
  const changes = await page.evaluate(() => window.changes);
  assert.equal(changes[1].operation, "restore");
  assert.equal(changes[1].data.revision, 1);

  await page.evaluate(() => {
    window.saved = {
      ...window.saved,
      mode: "AA",
      phase: "APPLIED",
      can_apply: true,
    };
  });
  await frame.locator("#reload").click();
  await frame.locator("#fields:not([disabled])").waitFor();
  await frame.locator("#profile").selectOption("AU");
  await page.waitForTimeout(5200);
  assert.match(await frame.locator("#state").innerText(), /US\/102/);
  assert.doesNotMatch(await frame.locator("#state").innerText(), /AU\/100/);
  assert.match(
    await frame.locator("#selection-status").innerText(),
    language === "RU" ? /не сохранены/ : /not saved/,
  );
  assert.equal(await frame.locator("#result").innerText(), "");

  await page.evaluate(() => {
    window.available = false;
  });
  await page.waitForTimeout(5200);
  assert.equal(await frame.locator("#radios article").count(), 0);
  assert.match(
    await frame.locator("#state").innerText(),
    language === "RU" ? /недоступна/ : /unavailable/,
  );
  assert.deepEqual(errors, []);
  await page.close();
}

async function main() {
  const files = {
    "/module/index.html": "modules/radio-settings/ui/index.html",
    "/module/app.js": "modules/radio-settings/ui/app.js",
    "/module/translations.js": "modules/radio-settings/ui/translations.js",
    "/user/ocfusion-i18n.js": "web/i18n.js",
    "/user/extensions-panel.css": "web/panel.css",
  };
  const server = http.createServer((request, response) => {
    const url = new URL(request.url, "http://localhost");
    response.setHeader(
      "Content-Type",
      url.pathname.endsWith(".js")
        ? "text/javascript"
        : url.pathname.endsWith(".css")
          ? "text/css"
          : "text/html",
    );

    if (url.pathname === "/") {
      const language = url.searchParams.get("language") === "RU" ? "ru" : "en";
      response.end(`<!doctype html><script>
        window.OCF_I18N = {language: "${language}"};
        window.calls = [];
        window.changes = [];
        window.available = true;
        window.saved = {revision: 0, mode: "stock", phase: "STOCK", can_apply: true, profiles: [
          {id: "AA", country: "US", country_revision: "102", available: true},
          {id: "AU", country: "AU", country_revision: "100", available: true},
          {id: "KR", country: "KR", country_revision: "100", available: true},
          {id: "XX", country: "US", country_revision: "104", available: false}
        ]};
        window.EXTENSIONS = {
          action: async (id, operation) => {
            window.calls.push(operation);
            return {...window.saved};
          },
          call: async (id, operation, data) => {
            window.changes.push({operation, data});
            window.saved = {...window.saved, revision: data.revision + 1, mode: operation === "apply" ? data.profile : "stock", phase: "REBOOT_REQUIRED", can_apply: false};
            return {...window.saved};
          },
          status: async () => ({data: window.available ? {
            state: window.saved.phase,
            mode: window.saved.mode,
            revision: window.saved.revision,
            radios: [
              {interface: "wl1", band: "5 GHz", country: "DE (E0/101)", current: "100/160", channels: "100 104 108 140", base_mask: "23 (0x17)", user_mask: "0"},
              {interface: "wl2", band: "6 GHz", country: "DE (E0/101)", current: "6g5/320-1", channels: "1 5 9 13", base_mask: "0", user_mask: "0"},
              {interface: "wl3", band: "2.4 GHz", country: "DE (E0/101)", current: "10", channels: "1 2 3 4 5 6 7 8 9 10 11 12 13", base_mask: "0", user_mask: "0"}
            ]
          } : null})
        };
      </script><iframe src="/module/index.html"></iframe>`);
      return;
    }

    const filename = files[url.pathname];
    response.end(filename ? fs.readFileSync(path.join(root, filename)) : "");
  });
  let browser;

  try {
    await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
    browser = await chromium.launch({ headless: true, args: ["--no-sandbox"] });

    for (const language of ["EN", "RU"]) {
      await testPage(
        browser,
        "http://127.0.0.1:" + server.address().port,
        language,
      );
    }

    console.log(
      "PASS: radio module iframe, EN/RU, passive startup, revisioned actions and unavailable status",
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
