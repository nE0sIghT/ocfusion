// SPDX-License-Identifier: GPL-3.0-or-later
"use strict";

const assert = require("assert");
const fs = require("fs");
const http = require("http");
const path = require("path");
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || "playwright");
const contribution = fs.readFileSync(
  path.join(__dirname, "../modules/radio-settings/ui/stock-channels.js"),
  "utf8",
);

async function checkPage(browser, address, mesh, initial = "0") {
  const page = await browser.newPage();
  const errors = [];
  page.on("pageerror", (error) => errors.push(error.message));
  await page.goto(
    address +
      "/Advanced_Wireless_Content.asp?mesh=" +
      mesh +
      "&initial=" +
      initial,
  );
  const select = page.locator('[id="6g1_control_channel"]');
  await select.waitFor();
  await page.waitForFunction(() => window.renderCount > 0);
  await page.waitForTimeout(100);

  const expected = mesh
    ? ["0", "5", "21", "37", "53", "69", "85"]
    : [
        "0",
        "5",
        "21",
        "37",
        "53",
        "69",
        "85",
        "101",
        "117",
        "133",
        "149",
        "165",
        "181",
        "197",
        "213",
      ];
  assert.deepEqual(
    await select
      .locator("option")
      .evaluateAll((nodes) => nodes.map((node) => node.value)),
    expected,
  );
  assert.equal(await select.inputValue(), initial);
  assert.equal(
    await page.evaluate(() => systemManipulable.wlBandSeq["6g1"].channelValue),
    initial,
  );

  const renders = await page.evaluate(() => window.renderCount);
  await page.addScriptTag({ content: contribution });
  await page.waitForTimeout(100);
  assert.equal(await page.evaluate(() => window.renderCount), renders);

  if (!mesh) {
    await select.selectOption("117");
    await page.evaluate(() => {
      systemManipulable.wlBandSeq["6g1"].channelValue = "6g117/80";
      systemManipulable.wlBandSeq["6g1"].pscChannel = [
        "5",
        "21",
        "37",
        "53",
        "69",
        "85",
      ];
      document.body.appendChild(document.createElement("div"));
    });
    await page.waitForTimeout(100);
    assert.equal(await select.inputValue(), "117");
    assert.equal(
      await page.evaluate(
        () => systemManipulable.wlBandSeq["6g1"].channelValue,
      ),
      "6g117/80",
    );

    // A deliberate Auto selection survives a later stock list refresh.
    await select.selectOption("0");
    await page.evaluate(() => {
      systemManipulable.wlBandSeq["6g1"].pscChannel = [];
      document.body.appendChild(document.createElement("div"));
    });
    await page.waitForTimeout(100);
    assert.equal(await select.inputValue(), "0");
    assert.equal(await page.evaluate(() => window.handlerCount), 1);

    // Native width filtering stays authoritative when rebuilding the selector.
    await page.evaluate(() => {
      systemManipulable.wlBandSeq["6g1"].widthChannels = ["5", "101"];
      controlChannelHandler("6g1");
    });
    assert.deepEqual(
      await select
        .locator("option")
        .evaluateAll((nodes) => nodes.map((node) => node.value)),
      ["0", "5", "101"],
    );

    // With PSC disabled, the stock full-channel list is left intact.
    await page.evaluate(() => {
      systemManipulable.psc6g = "0";
      systemManipulable.wlBandSeq["6g1"].widthChannels = null;
      controlChannelHandler("6g1");
    });
    assert.equal(await select.locator('option[value="1"]').count(), 1);
  }

  assert.equal(await page.evaluate(() => window.writes), 0);
  assert.deepEqual(errors, []);
  await page.close();
}

async function main() {
  const server = http.createServer((request, response) => {
    const url = new URL(request.url, "http://localhost");
    if (url.pathname === "/stock-channels.js") {
      response.setHeader("Content-Type", "text/javascript");
      response.end(contribution);
      return;
    }

    response.setHeader("Content-Type", "text/html");
    const mesh = url.searchParams.get("mesh") === "true";
    const initial = url.searchParams.get("initial") === "165" ? "165" : "0";
    response.end(`<!doctype html><html><head><script src="/stock-channels.js"></script></head><body>
      <select id="6g1_control_channel"></select>
      <script>
        window.writes = 0;
        window.renderCount = 0;
        window.handlerCount = 0;
        window.httpApi = {nvramSet() { window.writes++; throw new Error("unexpected write"); }};
        let systemManipulable = {psc6g: "1", wlBandSeq: {}};
        function generateControlChannel(prefix) {
          const radio = systemManipulable.wlBandSeq[prefix];
          const select = document.getElementById(prefix + "_control_channel");
          let channels = systemManipulable.psc6g === "1" ? radio.pscChannel : radio.channel;
          if (radio.widthChannels) {
            channels = channels.filter((channel) => radio.widthChannels.includes(channel));
          }
          window.renderCount++;
          return '<select id="' + prefix + '_control_channel">' +
            ["0", ...channels].map((value) => '<option value="' + value + '"' +
              (value === radio.channelValue ? ' selected' : '') + '>' + value + '</option>').join('') + '</select>';
        }
        function controlChannelHandler(prefix) {
          window.handlerCount++;
          const template = document.createElement("template");
          template.innerHTML = generateControlChannel(prefix);
          const rendered = template.content.querySelector("select");
          const value = rendered.value;
          const select = document.getElementById(prefix + "_control_channel");
          select.replaceChildren(...rendered.options);
          select.value = value;
        }
        setTimeout(() => {
          const channel = [];
          for (let value = 1; value <= ${mesh ? 93 : 221}; value += 4) {
            channel.push(String(value));
          }
          systemManipulable.wlBandSeq["6g1"] = {
            channel, pscChannel: ["5", "21", "37", "53", "69", "85"], channelValue: "${initial}"
          };
          controlChannelHandler("6g1");
        }, 20);
      </script></body></html>`);
  });
  let browser;
  try {
    await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
    browser = await chromium.launch({ headless: true, args: ["--no-sandbox"] });
    const address = "http://127.0.0.1:" + server.address().port;
    await checkPage(browser, address, false);
    await checkPage(browser, address, true);
    await checkPage(browser, address, false, "165");
    const page = await browser.newPage();
    await page.goto(address + "/unrelated");
    await page.waitForTimeout(100);
    assert.equal(
      await page.evaluate(() => window.OCF_RADIO_CHANNELS_LOADED),
      undefined,
    );
    await page.close();
    console.log(
      "PASS: stock PSC list, Auto/manual preservation, native width/Mesh limits, delayed initialization and idempotence",
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
