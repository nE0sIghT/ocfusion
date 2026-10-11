// SPDX-License-Identifier: GPL-3.0-or-later
"use strict";

(() => {
  const run = document.getElementById("transport-test");
  const report = document.getElementById("transport-report");
  const result = document.getElementById("transport-result");
  const sizes = [1024, 8192, 32768, 65536];
  const pattern = "OCF0123456789abcdef";
  let latest = { tests: [] };

  function renderReport() {
    report.value = JSON.stringify(latest, null, 2);
  }

  async function fetchWithTimeout(path, options = {}, timeout = 5000) {
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), timeout);

    try {
      const response = await fetch(path, {
        credentials: "same-origin",
        cache: "no-store",
        ...options,
        signal: controller.signal,
      });
      const text = await response.text();

      if (text.length > 70000) {
        throw Error("Response exceeds diagnostic limit");
      }

      return { response, text };
    } finally {
      clearTimeout(timer);
    }
  }

  async function roundTrip(config, size) {
    const started = performance.now();
    const bytes = new Uint8Array(16);
    crypto.getRandomValues(bytes);

    const id = Array.from(bytes, (byte) =>
      byte.toString(16).padStart(2, "0"),
    ).join("");
    const payload = pattern
      .repeat(Math.ceil(size / pattern.length))
      .slice(0, size);
    const envelope = JSON.stringify({
      protocol: "ocfusion-upload-probe-v1",
      instance: config.instance,
      id,
      size,
      payload,
    });
    const form = new FormData();
    form.append("splash_page_id", config.resource);
    form.append(
      "splash_page_css",
      new Blob([envelope], { type: "text/plain" }),
      "probe.css",
    );

    const row = {
      payload_bytes: size,
      request_json_bytes: new TextEncoder().encode(envelope).length,
      result: "FAILED",
    };

    try {
      const { response } = await fetchWithTimeout(
        "/splash_page.cgi",
        { method: "POST", body: form },
        20000,
      );
      row.http_status = response.status;
      row.redirected = response.redirected;
      row.upload_ms = Math.round(performance.now() - started);

      if (!response.ok) {
        row.result = "UPLOAD_REJECTED";

        return row;
      }

      const deadline = performance.now() + 10000;

      while (performance.now() < deadline) {
        const reply = await fetchWithTimeout(
          "/user/extensions-upload-reply.json?_=" + Date.now(),
        );
        let data = null;

        if (reply.response.ok) {
          try {
            data = JSON.parse(reply.text);
          } catch (error) {
            // A login page or stock HTML callback is not a protocol reply.
          }
        }

        if (data?.id === id && data.instance === config.instance) {
          row.echo_matches = data.payload === payload;
          row.received_bytes = data.received_bytes;
          row.response_json_bytes = new TextEncoder().encode(reply.text).length;
          row.response_storage = data.response_storage;
          row.server_sha256 = data.sha256;
          row.result =
            data.result === "PASS" &&
            row.echo_matches &&
            data.received_bytes === size &&
            data.response_storage === "RAM"
              ? "PASS"
              : "INTEGRITY_FAILED";

          return row;
        }

        await new Promise((resolve) => setTimeout(resolve, 100));
      }

      row.result = "NO_MATCHING_RAM_REPLY";
    } catch (error) {
      row.result = "UNAVAILABLE_OR_TIMEOUT";
    } finally {
      row.round_trip_ms = Math.round(performance.now() - started);
    }

    return row;
  }

  run.addEventListener("click", async () => {
    run.disabled = true;
    result.textContent = "Testing upload round trips...";
    latest = {
      version: cfg ? cfg.version : "",
      scheme: location.protocol,
      router: cfg ? cfg.model : "",
      channel: "stock_multipart_to_ram",
      tests: [],
    };
    renderReport();

    try {
      const bootstrap = await fetchWithTimeout(
        "/user/extensions-upload-probe.json?_=" + Date.now(),
      );
      const config = JSON.parse(bootstrap.text);

      if (
        !bootstrap.response.ok ||
        config.state !== "READY" ||
        !/^ocf_probe_[a-f0-9]{16}$/.test(config.resource) ||
        !/^[a-f0-9]{32}$/.test(config.instance) ||
        config.max_payload_bytes !== 65536
      ) {
        throw Error("Upload receiver unavailable");
      }

      latest.receiver = config.state;
      latest.test_limit_bytes = config.max_payload_bytes;

      for (const size of sizes) {
        latest.tests.push(await roundTrip(config, size));
        renderReport();
      }

      latest.largest_passed_payload_bytes = Math.max(
        0,
        ...latest.tests
          .filter((test) => test.result === "PASS")
          .map((test) => test.payload_bytes),
      );
      latest.absolute_maximum_tested = false;
      result.textContent = "Diagnostics complete. Copy the report to share it.";
    } catch (error) {
      latest.receiver = "UNAVAILABLE";
      result.textContent =
        "Upload receiver unavailable. Reinstall the current distribution and reload the page.";
    } finally {
      renderReport();
      run.disabled = false;
    }
  });

  run.disabled = false;
  result.textContent = "";

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
