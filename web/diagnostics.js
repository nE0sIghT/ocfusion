// SPDX-License-Identifier: GPL-3.0-or-later
"use strict";

(() => {
  const run = document.getElementById("transport-test");
  const report = document.getElementById("transport-report");
  const auth = document.getElementById("transport-auth");
  const result = document.getElementById("transport-result");
  let latest = { tests: [] };

  function renderReport() {
    latest.authentication = {
      method: "manual_private_window",
      result: auth.value,
    };
    report.value = JSON.stringify(latest, null, 2);
  }

  async function probe(
    path,
    validate,
    classify = () => "UNSUPPORTED_OR_BLOCKED",
  ) {
    const started = performance.now();
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), 5000);

    try {
      const response = await fetch(path, {
        credentials: "same-origin",
        cache: "no-store",
        signal: controller.signal,
      });
      const text = await response.text();
      const accepted = response.ok && text.length < 32768 && validate(text);

      return {
        result: accepted
          ? "PASS"
          : response.ok
            ? classify(text)
            : "UNSUPPORTED_OR_BLOCKED",
        http_status: response.status,
        redirected: response.redirected,
        content_type: response.headers.get("Content-Type"),
        server_date: response.headers.get("Date"),
        elapsed_ms: Math.round(performance.now() - started),
      };
    } catch (error) {
      return { result: "UNAVAILABLE_OR_TIMEOUT" };
    } finally {
      clearTimeout(timer);
    }
  }

  function jsonMatches(text, check) {
    try {
      return check(JSON.parse(text));
    } catch (error) {
      return false;
    }
  }

  run.addEventListener("click", async () => {
    run.disabled = true;
    result.textContent = "Running communication diagnostics...";
    latest = {
      version: cfg ? cfg.version : "",
      scheme: location.protocol,
      router: cfg ? cfg.model : "",
      browser_capabilities: {
        secure_context: window.isSecureContext,
        sha256: Boolean(crypto.subtle),
        websocket: typeof WebSocket === "function",
        event_source: typeof EventSource === "function",
      },
      tests: [],
    };

    const tests = [
      [
        "ram_json",
        "/user/extensions-modules.json",
        (text) =>
          jsonMatches(
            text,
            (data) =>
              Array.isArray(data.modules) && typeof data.state === "string",
          ),
      ],
      [
        "stock_read_api",
        "/appGet.cgi?hook=nvram_get(preferred_lang)",
        (text) =>
          jsonMatches(text, (data) => typeof data.preferred_lang === "string"),
      ],
      [
        "custom_cgi",
        "/user/ocfusion-probe.cgi",
        (text) =>
          jsonMatches(text, (data) => data.ocfusion_cgi_executed === true),
        (text) =>
          text.startsWith("#!/bin/sh")
            ? "STATIC_FILE_ONLY"
            : "UNSUPPORTED_OR_BLOCKED",
      ],
      [
        "stock_template",
        "/user/ocfusion-probe.asp",
        (text) =>
          jsonMatches(text, (data) =>
            /^[A-Za-z_-]{1,16}$/.test(data.ocfusion_template),
          ),
        (text) =>
          text.includes("<" + "%")
            ? "STATIC_TEMPLATE_ONLY"
            : "UNSUPPORTED_OR_BLOCKED",
      ],
    ];

    for (const [channel, path, validate, classify] of tests) {
      latest.tests.push({
        channel,
        ...(await probe(path, validate, classify)),
      });
      renderReport();
    }

    for (let attempt = 0; pending && attempt < 40; attempt++) {
      await sleep(250);
    }

    const started = performance.now();

    if (pending || !platformReady) {
      latest.tests.push({
        channel: "nvram_mailbox",
        result: "BUSY_OR_UNAVAILABLE",
      });
    } else {
      busy(true);

      try {
        const response = await request("platform.ping");

        latest.tests.push({
          channel: "nvram_mailbox",
          result:
            response.request_data?.pong === true ? "PASS" : "INVALID_REPLY",
          elapsed_ms: Math.round(performance.now() - started),
          response_storage: response.request_data?.response_storage,
        });
      } catch (error) {
        latest.tests.push({ channel: "nvram_mailbox", result: "FAILED" });
      } finally {
        busy(false);
      }
    }

    latest.tests.push({
      channel: "reverse_proxy",
      result: "NOT_CONFIGURED_NOT_TESTED",
    });
    renderReport();
    result.textContent = "Diagnostics complete. Copy the report to share it.";
    run.disabled = false;
  });

  run.disabled = false;
  result.textContent = "";

  auth.addEventListener("change", renderReport);
  document
    .getElementById("transport-copy")
    .addEventListener("click", async () => {
      renderReport();

      try {
        if (!navigator.clipboard) {
          throw Error("Clipboard unavailable");
        }

        await navigator.clipboard.writeText(report.value);
        result.textContent = "Report copied.";
      } catch (error) {
        report.focus();
        report.select();
        result.textContent = "Report selected. Copy it with Ctrl+C.";
      }
    });
})();
