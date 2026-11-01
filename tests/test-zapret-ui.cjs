// SPDX-License-Identifier: GPL-3.0-or-later
"use strict";

const assert = require("assert");
const fs = require("fs");
const http = require("http");
const path = require("path");
const {chromium} = require(process.env.PLAYWRIGHT_MODULE || "playwright");
const root = path.resolve(__dirname, "..");

function normalizeWithBackend(settings) {
  if (!process.env.OCF_ZAPRET_VALIDATOR) {
    return settings;
  }
  const directory = fs.mkdtempSync(path.join(require("os").tmpdir(), "ocf-zapret-ui-"));
  try {
    const filename = path.join(directory, "config.json");
    fs.writeFileSync(filename, JSON.stringify(settings));
    const output = require("child_process").execFileSync(process.env.OCF_ZAPRET_VALIDATOR, ["settings"], {input: JSON.stringify({config_path: filename}), encoding: "utf8"});
    return JSON.parse(output);
  } finally {
    fs.rmSync(directory, {recursive: true, force: true});
  }
}

async function main() {
  const assets = {
    "/module/index.html": "modules/zapret2/ui/index.html",
    "/module/applications.js": "modules/zapret2/ui/applications.js",
    "/module/lists.js": "modules/zapret2/ui/lists.js",
    "/module/discovery.js": "modules/zapret2/ui/discovery.js",
    "/module/rules.js": "modules/zapret2/ui/rules.js",
    "/module/monitoring.js": "modules/zapret2/ui/monitoring.js",
    "/module/app.js": "modules/zapret2/ui/app.js",
    "/module/protocols.js": "modules/zapret2/ui/protocols.js",
    "/module/targets.js": "modules/zapret2/ui/targets.js",
    "/module/layout.js": "modules/zapret2/ui/layout.js",
    "/module/layout.css": "modules/zapret2/ui/layout.css",
    "/module/translations.js": "modules/zapret2/ui/translations.js",
    "/user/ocfusion-i18n.js": "web/i18n.js",
    "/user/extensions-panel.css": "web/panel.css",
  };
  const server = http.createServer((request, response) => {
    const url = new URL(request.url, "http://localhost");

    if (process.env.OCF_STOCK_STYLE_DIR && ["/form_style.css", "/index_style.css"].includes(url.pathname)) {
      response.setHeader("Content-Type", "text/css");
      response.end(fs.readFileSync(path.join(process.env.OCF_STOCK_STYLE_DIR, path.basename(url.pathname))));
      return;
    }

    if (url.pathname === "/form_style.css") {
      const theme = new URL(request.headers.referer || "http://localhost/").searchParams.get("theme");
      const accent = theme === "standard" ? "#246b8e" : "#9a1735";
      const panel = theme === "standard" ? "#253b46" : "#26121a";
      response.setHeader("Content-Type", "text/css");
      // Synthetic stock-style fixtures reproduce the conflicting form header
      // and distinct theme palettes without redistributing ASUS assets.
      response.end(`.FormTable thead th {color:#000}
        .FormTable_table th {color:#000;background:${panel}}
        .button_gen {background:${accent};color:#fff;border:0;padding:7px 12px;min-width:100px}
        .input_option,.input_32_table {background:${panel};color:#f0f0f0;border:1px solid ${accent};padding:6px;height:25px}
        button:hover {color:red;font:italic 20px serif}
        .tab,.tabClicked {color:#fff;background:${panel};padding:8px}
        .tabClicked {background:${accent}}`);
      return;
    }

    if (url.pathname === "/") {
      const theme = url.searchParams.get("theme") === "standard" ? "standard" : "rog";
      const language = url.searchParams.get("language") === "ru" ? "ru" : "en";
      response.end(`<!doctype html><script>
        window.OCF_I18N = {language: "${language}"};
        window.saved = {revision: 0, enabled: false, lists: [], networks: [], domains: [], tcp_ports: [80,443], udp_ports: [443], tcp_strategy: "split", udp_strategy: "off", split_position: 2, fake_ttl: 3, fake_repeats: 3};
        window.changes = [];
        window.moduleState = "DISABLED";
        window.listStatuses = [];
        window.EXTENSIONS = {
          action: async (id, operation) => {
            if (operation === "vpn-targets") {
              return [{id:"native:2:012345abcdef",name:"Test VPN",protocol:"WireGuard",ready:true}];
            }

            if (operation === "history") {
              return window.historyEntries === undefined ? [] : window.historyEntries;
            }

            if (operation === "list-catalog") {
              return [{id: "telegram", name: "Telegram", paths: ["Services/telegram.lst", "Subnets/IPv4/telegram.lst"]}, {id: "youtube", name: "YouTube", paths: ["Services/youtube.lst"]}];
            }

            if (operation === "catalog") {
              return {presets: await window.EXTENSIONS.action(id, "presets"), lists: await window.EXTENSIONS.action(id, "list-catalog"), applications: [{id: "mtproto", name: "Telegram MTProto", transport: "tcp", methods: ["split", "fake"], defaults: {method:"split", ports:"*", packets:3, position:2, repeats:3, fragment:8, padding:2}}]};
            }

            if (operation === "presets") {
              return [{id: "hostname", name: "Hostname split", description: "Split within the server name.", protocol: "tcp", settings: {tcp_strategy: "split", split_positions: "1,midsld", sequence_overlap: 0, auto_ttl: false}}, {id:"disorder", name:"Reordered segments", protocol:"tcp", settings:{tcp_strategy:"disorder",split_positions:"1,midsld"}}, {id:"mtproto-split",name:"Telegram MTProto - split",protocol:"application",settings:{applications:[{id:"mtproto",method:"split",ports:"*",packets:3,position:2,repeats:3,fragment:8,padding:2}]}}];
            }

            return {...window.saved};
          },
          call: async (id, operation, request) => {
            window.lastCall = {operation, request};

            if (operation === "refresh-lists") {
              window.listStatuses = [{id: "telegram", state: "ERROR", updated: 1793000000, domains: 3, networks: 2}];
              return {ok: true};
            }

            if (operation === "health-check") {
              return {ok:true};
            }

            if (operation === "scan-settings") {
              if (request.id === "quic") {
                return {id:"quic",name:"QUIC fake packets",protocol:"udp",settings:{udp_strategy:"fake-quic",fake_ttl:6,fake_repeats:11,auto_ttl:false,auto_ttl_delta:-1,auto_ttl_min:3,auto_ttl_max:20}};
              }
              return {id:"disorder",name:"Reordered segments",protocol:"tcp",settings:{tcp_strategy:"disorder",split_position:2,split_positions:"1,midsld",sequence_overlap:0,fake_ttl:9,fake_repeats:7,auto_ttl:true,auto_ttl_delta:-1,auto_ttl_min:3,auto_ttl_max:20,packet_limit:12,fake_fooling:"md5",fake_sni:""}};
            }

            if (operation === "history-settings") {
              return {name:"Hostname split", protocol:"tcp", settings:{tcp_strategy:"split",split_positions:"1,midsld"}};
            }

            if (operation === "scan-start") {
              window.scan = {id: "test", state: "RUNNING", revision: window.saved.revision, targets:request.targets, results: []};
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
      </script><iframe style="width:1100px;height:1400px" src="/module/index.html?theme=${theme}"></iframe>`);
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

    for (const [language, theme] of (process.env.OCF_STOCK_STYLE_DIR ? [["en", "rog"], ["ru", "rog"]] : [["en", "rog"], ["ru", "rog"], ["en", "standard"], ["ru", "standard"]])) {
      const page = await browser.newPage();
      const errors = [];
      page.on("pageerror", (error) => errors.push(error.message));
      await page.goto("http://127.0.0.1:" + server.address().port + "/?language=" + language + "&theme=" + theme);
      await page.evaluate(() => {
        window.saved = {...window.saved, enabled:true, lists:["youtube"], domains:["legacy.example.com"], networks:["203.0.113.0/24"], rules:[{id:"telegram",name:"Telegram VPN",enabled:true,action:"vpn",target:"native:2:012345abcdef",failure:"wan",lists:["telegram"],domains:[],networks:[],settings:{}}]};
        window.moduleState = "RUNNING";
      });
      const frame = page.frameLocator("iframe");
      await frame.locator("#fields:not([disabled])").waitFor();
      await frame.locator("body").evaluate(async () => {
        await load();
        await updateStatus();
      });
      assert.equal(await frame.locator('[role="tab"]').count(), 3);
      assert.equal(await frame.locator("#tab-strategies").count(), 0);
      assert.equal(await frame.locator("#default-policy").count(), 0);
      assert.equal(await frame.locator("#enabled").isVisible(), false);
      assert.equal(await frame.locator("#destination-rules tbody tr").count(), 2);
      assert.equal(await frame.locator("#save-bar").isVisible(), false);
      assert.equal(await frame.locator("[data-rule-row=telegram]").innerText().then((text) => text.includes("Test VPN")), true);
      assert.equal(await frame.locator("[data-rule-row=main] td").nth(2).innerText().then((text) => text.includes("YouTube")), true);
      const editBox = await frame.locator("[data-rule-row=telegram] button").boundingBox();
      assert(editBox.width >= 100 && editBox.height < 70, JSON.stringify(editBox));
      assert.equal(await frame.locator(".ocf-rule-table").evaluate((element) => element.classList.contains("FormTable_table")), false);
      assert.equal(await frame.locator(".ocf-rule-table th").first().evaluate((element) => getComputedStyle(element).color), "rgb(221, 221, 221)");
      assert.equal(await frame.locator("#toggle-processing").evaluate((element) => getComputedStyle(element).backgroundColor), "rgba(0, 0, 0, 0)");
      const buttonStyle = await frame.locator("#toggle-processing").evaluate((element) => {
        const style = getComputedStyle(element);
        return {font: style.font, color: style.color, background: style.backgroundImage};
      });
      assert(buttonStyle.background.includes("gradient"));
      await frame.locator("#toggle-processing").hover();
      assert.deepEqual(await frame.locator("#toggle-processing").evaluate((element) => {
        const style = getComputedStyle(element);
        return {font: style.font, color: style.color, background: style.backgroundImage};
      }), buttonStyle);
      assert.equal(await frame.locator("#toggle-processing").evaluate((element) => getComputedStyle(element).filter), "brightness(1.18)");
      await frame.locator("#tab-management").hover();
      assert.equal(await frame.locator("#tab-management").evaluate((element) => getComputedStyle(element).filter), "brightness(1.18)");
      await frame.locator("#tab-management").focus();
      await frame.locator("#tab-management").press("End");
      assert.equal(await frame.locator("#panel-diagnostics").isVisible(), true);
      assert.equal(await frame.locator("#settings").isVisible(), false);
      await frame.locator("#tab-diagnostics").press("Home");

      await frame.locator("[data-rule-row=telegram] button").click();
      assert.equal(await frame.locator(".ocf-rule-table").count(), 0);
      assert.equal(await frame.locator("#panel-strategies").isVisible(), false);
      assert.equal(await frame.locator("#rule-vpn-target").inputValue(), "native:2:012345abcdef");
      assert.equal(await frame.locator('[data-rule-field="failure"]').inputValue(), "wan");
      assert.equal(await frame.locator("#rule-vpn-target").evaluate((element) => getComputedStyle(element).backgroundColor), "rgb(16, 16, 16)");
      assert.equal(await frame.locator('.ocf-rule-editor summary').filter({hasText: language === "ru" ? "О маршрутизации" : "About VPN routing"}).count(), 0);
      assert(await frame.locator('[data-rule-field="name"]').evaluate((element) => {
        const field = element.closest(".ext-field").getBoundingClientRect();
        const control = element.getBoundingClientRect();
        return control.left >= field.left && control.right <= field.right + 0.5;
      }), "Rule name must fit inside its field including stock margins");
      assert(await frame.locator("#rule-vpn-target").evaluate((element) => {
        const style = getComputedStyle(element);
        return element.clientHeight - parseFloat(style.paddingTop) - parseFloat(style.paddingBottom) >= parseFloat(style.fontSize);
      }));
      assert.equal(await frame.locator('[data-rule-action="checks"]').count(), 0);
      assert.equal(await frame.locator(".ocf-rule-editor #rule-checks").isVisible(), true);
      await frame.locator("#monitor-targets-input").fill("https://telegram.org/");
      await frame.locator("#monitor-enabled").check();

      await frame.locator('[data-rule-field="name"]').fill("Draft VPN name");
      await page.waitForTimeout(3200);
      assert.equal(await frame.locator('[data-rule-field="name"]').inputValue(), "Draft VPN name");
      page.on("dialog", (dialog) => dialog.accept());
      await frame.locator("#reload").click();
      await frame.locator("[data-rule-row=telegram] button").waitFor();
      assert.equal(await frame.locator("[data-rule-row=telegram] td").nth(1).innerText(), "Telegram VPN");

      await frame.locator("[data-rule-row=main] button").click();
      assert.equal(await frame.locator("#panel-strategies").isVisible(), true);
      assert.equal(await frame.locator("#custom-strategy").getAttribute("open"), null);
      if (process.env.OCF_UI_SCREENSHOT_DIR) {
        await frame.locator("body").screenshot({path:path.join(process.env.OCF_UI_SCREENSHOT_DIR, "zapret-editor-" + theme + "-" + language + ".png")});
      }
      await frame.locator("#preset").selectOption("hostname");
      await frame.locator("#application-preset").selectOption("mtproto-split");
      await frame.locator("#custom-strategy > summary").click();
      await frame.locator("#quic-advanced > summary").click();
      await frame.locator("#udp_strategy").selectOption("fake-quic");
      await frame.locator("#settings button[type=submit]").click();
      await frame.locator("#fields:not([disabled])").waitFor();
      let saved = await page.evaluate(() => window.saved);
      assert.deepEqual(saved.lists, []);
      assert.deepEqual(saved.domains, []);
      assert.deepEqual(saved.networks, []);
      assert.equal(saved.rules.length, 2);
      let mainRule = saved.rules.find((entry) => entry.id === "main");
      assert.deepEqual(mainRule.lists, ["youtube"]);
      assert.deepEqual(mainRule.domains, ["legacy.example.com"]);
      assert.deepEqual(mainRule.networks, ["203.0.113.0/24"]);
      assert.equal(mainRule.settings.split_positions, "1,midsld");
      assert.equal(mainRule.settings.udp_strategy, "fake-quic");
      assert.equal(saved.rules[0].failure, "wan");
      assert.equal(saved.rules[0].target, "native:2:012345abcdef");
      assert.equal(await frame.locator("#save-bar").isVisible(), false);
      const unsavedText = language === "ru" ? "Есть несохранённые изменения" : "Unsaved changes";
      assert(!(await frame.locator("#destination-rules").innerText()).includes(unsavedText));
      await frame.locator("body").evaluate(() => setDirty(true));
      assert((await frame.locator("#destination-rules").innerText()).includes(unsavedText));
      await frame.locator("body").evaluate(() => setDirty(false));
      assert(!(await frame.locator("#destination-rules").innerText()).includes(unsavedText));

      await frame.locator('[data-rule-row="telegram"] input[type=checkbox]').uncheck();
      await frame.locator("#settings button[type=submit]").click();
      await frame.locator("#fields:not([disabled])").waitFor();
      saved = await page.evaluate(() => window.saved);
      assert.equal(saved.enabled, true);
      assert.equal(saved.rules[0].enabled, false);
      assert.deepEqual(saved.rules[0].lists, ["telegram"]);
      const beforePause = structuredClone(saved.rules);
      await frame.locator("[data-rule-row=main] button").click();
      await frame.locator('[data-rule-field="name"]').fill("Unsaved main name");
      await frame.locator("#toggle-processing").click();
      await frame.locator("#toggle-processing:not([disabled])").waitFor();
      assert.equal(await page.evaluate(() => window.saved.enabled), false);
      assert.deepEqual(await page.evaluate(() => window.saved.rules), beforePause);
      await frame.locator("#toggle-processing").click();
      await frame.locator("#toggle-processing:not([disabled])").waitFor();
      assert.equal(await page.evaluate(() => window.saved.enabled), true);
      assert.deepEqual(await page.evaluate(() => window.saved.rules), beforePause);
      assert.equal(await frame.locator('[data-rule-field="name"]').inputValue(), "Unsaved main name");
      await frame.locator("#reload").click();
      await frame.locator("[data-rule-row=main] button").waitFor();

      await frame.locator("[data-rule-row=main] button").click();
      await frame.locator(".ocf-rule-editor button").filter({hasText: language === "ru" ? "Подобрать стратегию" : "Find a strategy"}).click();
      assert.equal(await frame.locator("#panel-finder").isVisible(), true);
      assert.equal(await frame.locator("#scan-scope").inputValue(), "main");
      assert.equal(await frame.locator("#scan-selected-actions").isVisible(), false);
      assert.equal(await frame.locator("#scan-host").inputValue(), "legacy.example.com");
      await frame.locator("#scan-host").fill("changed.example.com");
      await frame.locator("#scan-start").click();
      await frame.locator("#scan-cancel:not([disabled])").waitFor();
      assert.equal((await page.evaluate(() => window.lastCall)).operation, "scan-start");
      assert.equal((await page.evaluate(() => window.lastCall)).request.targets[0].host, "changed.example.com");
      const showResult = async (id) => {
        await page.evaluate((scanID) => {
          window.scan = {id:scanID,state:"COMPLETE",revision:window.saved.revision,targets:[{host:"example.com",port:443,protocol:"https"}],results:[{preset:"baseline",success:0,attempts:2,protocol:"tcp"},{preset:"disorder",success:2,attempts:2,milliseconds:125,protocol:"tcp"}]};
        }, id);
        await frame.locator("body").evaluate(() => updateStatus());
        await frame.locator('#scan-results input[type="radio"]').check();
      };
      await showResult("first");
      assert.equal(await frame.locator("#scan-results button").count(), 0);
      assert.equal(await frame.locator("#scan-selected-actions").isVisible(), true);
      assert.equal(await frame.locator("#scan-improve").getAttribute("open"), null);
      if (process.env.OCF_UI_SCREENSHOT_DIR) {
        await frame.locator("body").screenshot({path:path.join(process.env.OCF_UI_SCREENSHOT_DIR, "zapret-finder-" + theme + "-" + language + ".png")});
      }
      await frame.locator("#scan-confirm").click();
      assert.equal((await page.evaluate(() => window.lastCall)).request.mode, "confirm");
      await showResult("confirmed");
      await frame.locator("#scan-improve > summary").click();
      await frame.locator("#scan-refine").click();
      assert.equal((await page.evaluate(() => window.lastCall)).request.mode, "refine");
      await showResult("refined");
      await frame.locator("#scan-simplify").click();
      assert.equal((await page.evaluate(() => window.lastCall)).request.mode, "lighten");
      await showResult("final");
      const changesBeforeUse = await page.evaluate(() => window.changes.length);
      assert.equal(await frame.locator("#scan-use").innerText(), language === "ru" ? "Использовать для TCP" : "Use for TCP");
      const quicBefore = await frame.locator("body").evaluate(() => readPacketOptions("udp"));
      const appsBefore = await frame.locator("body").evaluate(() => readApplications());
      assert.equal(appsBefore.length, 1);
      await frame.locator("#scan-use").click();
      assert.equal(await frame.locator("#panel-management").isVisible(), true);
      assert.equal(await frame.locator('.ocf-rule-editor').getAttribute("data-rule-id"), "main");
      assert.equal(await page.evaluate(() => window.changes.length), changesBeforeUse);
      assert.equal(await frame.locator("#preset").inputValue(), "disorder");
      assert.equal(await frame.locator("#preset option:checked").innerText(), language === "ru" ? "Перестановка сегментов" : "Reordered segments");
      assert((await frame.locator("#summary-tcp").innerText()).includes(language === "ru" ? "Проверено подбором" : "Tested by finder"));
      assert((await frame.locator("#summary-udp").innerText()).includes(language === "ru" ? "Не проверено" : "Not tested"));
      assert.deepEqual(await frame.locator("body").evaluate(() => readPacketOptions("udp")), quicBefore);
      assert.deepEqual(await frame.locator("body").evaluate(() => readApplications()), appsBefore);
      if (await frame.locator("#custom-strategy").getAttribute("open") === null) {
        await frame.locator("#custom-strategy > summary").click();
      }
      await frame.locator("#split_positions").fill("2,midsld");
      assert.equal(await frame.locator("#preset").inputValue(), "");
      assert(!(await frame.locator("#summary-tcp").innerText()).includes(language === "ru" ? "Проверено подбором" : "Tested by finder"));
      await frame.locator("#split_positions").fill("1,midsld");
      assert.equal(await frame.locator("#preset").inputValue(), "disorder");
      await frame.locator("#settings button[type=submit]").click();
      await frame.locator("#fields:not([disabled])").waitFor();
      saved = await page.evaluate(() => window.saved);
      assert.equal(saved.rules.find((entry) => entry.id === "main").settings.udp_strategy, "fake-quic");
      const normalized = normalizeWithBackend(saved);
      const selectedTCP = normalized.rules.find((entry) => entry.id === "main").strategy_selections.tcp;
      assert.equal(selectedTCP.name, "Reordered segments");
      assert.equal(selectedTCP.checked, true);
      await page.evaluate((settings) => { window.saved = settings; }, normalized);
      await frame.locator("body").evaluate(() => load());
      await frame.locator("#fields:not([disabled])").waitFor();
      await frame.locator("[data-rule-row=main] button").click();
      assert.equal(await frame.locator("#preset").inputValue(), "disorder");
      assert((await frame.locator("#summary-tcp").innerText()).includes(language === "ru" ? "Проверено подбором" : "Tested by finder"));
      assert.deepEqual(await frame.locator("body").evaluate(() => readPacketOptions("udp")), quicBefore);
      const tcpBeforeQUIC = await frame.locator("body").evaluate(() => readPacketOptions("tcp"));
      await frame.locator(".ocf-rule-editor button").filter({hasText: language === "ru" ? "Подобрать стратегию" : "Find a strategy"}).click();
      await page.evaluate(() => {
        window.scan = {id:"quic-check",state:"COMPLETE",revision:window.saved.revision,targets:[{host:"example.com",port:443,protocol:"http3"}],results:[{preset:"baseline",success:0,attempts:2,protocol:"udp"},{preset:"quic",success:2,attempts:2,milliseconds:100,protocol:"udp"}]};
      });
      await frame.locator("body").evaluate(() => updateStatus());
      await frame.locator('#scan-results input[type="radio"]').check();
      assert.equal(await frame.locator("#scan-use").innerText(), language === "ru" ? "Использовать для QUIC" : "Use for QUIC");
      await frame.locator("#scan-use").click();
      assert.equal(await frame.locator("#preset").inputValue(), "disorder");
      assert.equal(await frame.locator("#quic-preset option:checked").innerText(), language === "ru" ? "Подставные пакеты QUIC" : "QUIC fake packets");
      assert.deepEqual(await frame.locator("body").evaluate(() => readPacketOptions("tcp")), tcpBeforeQUIC);
      assert((await frame.locator("#summary-tcp").innerText()).includes(language === "ru" ? "Проверено подбором" : "Tested by finder"));
      await frame.locator("#settings button[type=submit]").click();
      await frame.locator("#fields:not([disabled])").waitFor();
      const both = normalizeWithBackend(await page.evaluate(() => window.saved));
      assert.equal(both.rules.find((entry) => entry.id === "main").strategy_selections.tcp.checked, true);
      assert.equal(both.rules.find((entry) => entry.id === "main").strategy_selections.udp.checked, true);
      await frame.locator("[data-rule-row=main] button").click();
      await frame.locator(".ocf-rule-editor button").filter({hasText: language === "ru" ? "К списку правил" : "Back to rules"}).click();

      await frame.locator("#tab-diagnostics").click();
      assert.equal(await frame.locator("#technical-details").getAttribute("open"), null);
      assert.equal(await frame.locator("#diagnostic-rules > div").count(), 2);
      if (process.env.OCF_UI_SCREENSHOT_DIR) {
        await frame.locator("body").screenshot({path:path.join(process.env.OCF_UI_SCREENSHOT_DIR, "zapret-diagnostics-" + theme + "-" + language + ".png")});
      }
      assert.equal(await frame.locator("#panel-diagnostics #rule-checks").count(), 0);
      await frame.locator("#tab-management").click();
      await frame.locator("[data-rule-row=main] button").click();
      await frame.locator("#monitor-targets-input").fill("https://example.com/\nstun://stun.example.net:19302");
      await frame.locator("#monitor-enabled").check();
      await frame.locator("#settings button[type=submit]").click();
      await frame.locator("#fields:not([disabled])").waitFor();
      assert.equal((await page.evaluate(() => window.saved)).rules.find((rule) => rule.id === "main").monitor.targets.length, 2);
      assert.equal((await page.evaluate(() => window.saved)).rules.find((rule) => rule.id === "telegram").monitor?.targets?.length || 0, 0);
      await frame.locator("[data-rule-row=main] button").click();
      await frame.locator("#health-check:not([disabled])").click();
      assert.equal((await page.evaluate(() => window.lastCall)).request.rule_id, "main");
      await frame.locator("#tab-diagnostics").click();
      await frame.locator("#copy-report").click();
      await frame.locator("#copy-report:not([disabled])").waitFor();
      assert.equal(JSON.parse(await frame.locator("#diagnostic-report").inputValue()).state, "RUNNING");
      await frame.locator("#tab-management").click();
      await page.evaluate(() => { window.historyEntries = null; });
      await frame.locator("#confirmed-history > summary").click();
      await frame.locator("#history-refresh").click();
      await frame.locator("#history-results").filter({hasText: language === "ru" ? "Нет сохранённых" : "No saved"}).waitFor();
      await frame.locator("#confirmed-history > summary").click();
      await page.evaluate(() => {
        window.historyEntries = [{id:"remembered",name:"Hostname split",protocol:"tcp",updated:1793000000,success:3,attempts:3,targets:[{host:"example.com",port:443,protocol:"https"}]}];
      });
      await frame.locator("#confirmed-history > summary").click();
      await frame.locator("#history-results button").nth(1).waitFor();
      await frame.locator("#history-results button").nth(0).click();
      assert.equal(await frame.locator("#panel-management").isVisible(), true);
      await frame.locator("#history-results button").nth(1).click();
      assert.equal((await page.evaluate(() => window.lastCall)).request.mode, "history");
      await frame.locator("#tab-management").click();
      await frame.locator(".ocf-rule-editor button").filter({hasText: language === "ru" ? "К списку правил" : "Back to rules"}).click();
      if (process.env.OCF_UI_SCREENSHOT_DIR) {
        await frame.locator("body").screenshot({path:path.join(process.env.OCF_UI_SCREENSHOT_DIR, "zapret-wide-" + theme + "-" + language + ".png")});
      }
      await frame.locator("#rule-add").click();
      await frame.locator('[data-rule-field="name"]').fill("New VPN rule");
      await frame.locator("#settings button[type=submit]").click();
      assert.equal(await page.evaluate(() => window.saved.rules.length), 2);
      await frame.locator('.ocf-rule-editor [data-service="telegram"]').check();
      await frame.locator('[data-rule-field="action"]').selectOption("vpn");
      await frame.locator("#rule-vpn-target").selectOption("native:2:012345abcdef");
      await frame.locator('[data-rule-field="failure"]').selectOption("wan");
      await frame.locator("#settings button[type=submit]").click();
      await frame.locator("#fields:not([disabled])").waitFor();
      saved = await page.evaluate(() => window.saved);
      assert.equal(saved.rules.length, 3);
      const created = saved.rules[2];
      assert.equal(created.failure, "wan");
      assert.deepEqual(created.lists, ["telegram"]);
      await frame.locator('[data-rule-row="' + created.id + '"] button').click();
      await frame.locator(".ocf-rule-editor button").filter({hasText: language === "ru" ? "Убрать" : "Remove"}).click();
      assert.equal(await page.evaluate(() => window.saved.rules.length), 3);
      await frame.locator("#settings button[type=submit]").click();
      await frame.locator("#fields:not([disabled])").waitFor();
      assert.equal(await page.evaluate(() => window.saved.rules.length), 2);
      await page.locator("iframe").evaluate((element) => { element.style.width = "460px"; });
      assert.equal(await frame.locator(".ocf-rule-table thead").isVisible(), false);
      const narrowEdit = await frame.locator("[data-rule-row=main] button").boundingBox();
      assert(narrowEdit.width >= 100 && narrowEdit.height < 70);
      assert.equal(await frame.locator("body").evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth + 2), true);
      const text = await frame.locator("body").innerText();
      assert(text.includes(language === "ru" ? "Приостановить обработку" : "Pause processing"));
      assert(!text.includes(language === "ru" ? "Остальные назначения" : "Other destinations"));
      if (process.env.OCF_UI_SCREENSHOT_DIR) {
        await frame.locator("body").screenshot({path:path.join(process.env.OCF_UI_SCREENSHOT_DIR, "zapret-rules-" + theme + "-" + language + ".png")});
      }
      await page.locator("iframe").evaluate((element) => { element.style.width = "320px"; });
      assert.equal(await frame.locator("body").evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth + 2), true);
      assert.deepEqual(errors, []);
      await page.close();
    }
  } finally {
    if (browser) {
      await browser.close();
    }

    await new Promise((resolve) => server.close(resolve));
  }

  console.log("PASS: zapret2 EN/RU rule workflow, legacy selection preservation, contextual strategies, finder actions, diagnostics and responsive layout");
}

main().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
