// SPDX-License-Identifier: GPL-3.0-or-later
"use strict";

const OCF_TRANSLATIONS = {
  "Upload diagnostics unavailable. Reload the page.":
    "Диагностика загрузки недоступна. Перезагрузите страницу.",
  "Public status unavailable": "Публичный статус пока недоступен",
  "Upload channel diagnostics": "Диагностика канала загрузки",
  "Test a round trip through the ASUS uploader and RAM replies with 1, 8, 32 and 64 KiB payloads. VPN and captive portal settings are unchanged.":
    "Проверка двунаправленного обмена через загрузчик ASUS и ответы в RAM с данными размером 1, 8, 32 и 64 КиБ. Настройки VPN и captive portal не меняются.",
  "Run diagnostics": "Запустить диагностику",
  "Testing upload round trips...":
    "Проверяем двунаправленный обмен через загрузчик...",
  "Upload receiver unavailable. Reinstall the current distribution and reload the page.":
    "Приёмник загрузки недоступен. Переустановите текущий дистрибутив и перезагрузите страницу.",
  "Copy report": "Копировать отчёт",
  "Diagnostic report": "Отчёт диагностики",
  "Diagnostics complete. Copy the report to share it.":
    "Диагностика завершена. Скопируйте отчёт для передачи.",
  "Report copied.": "Отчёт скопирован.",
  "Report selected. Copy it with Ctrl+C.":
    "Отчёт выделен. Скопируйте его с помощью Ctrl+C.",
  "OCFusion is stopping. Please wait.": "OCFusion останавливается. Подождите.",
  "Checking platform availability...": "Проверяем доступность платформы...",
  "Installation or update in progress. Please wait.":
    "Идёт установка или обновление. Подождите.",
  "OCFusion is stopped. Connect the USB drive to start services.":
    "OCFusion остановлен. Подключите USB-накопитель для запуска служб.",
  "OCFusion is unavailable. Check the USB drive and installation log.":
    "OCFusion недоступен. Проверьте USB-накопитель и журнал установки.",
  "OCFusion services are not ready. Check the platform status.":
    "Службы OCFusion не готовы. Проверьте состояние платформы.",
  "Loading…": "Загрузка…",
  "VPN integration": "Интеграция VPN",
  "ASUSWRT extensions": "Расширения ASUSWRT",
  "Back to ASUS ↗": "Вернуться в ASUS ↗",
  Overview: "Обзор",
  Modules: "Модули",
  System: "Система",
  "Additional router services and their status.":
    "Дополнительные службы роутера и их состояние.",
  Diagnostics: "Диагностика",
  "Open settings →": "Открыть настройки →",
  "Installation and updates": "Установка и обновление",
  Platform: "Платформа",
  Router: "Роутер",
  "Extension version": "Версия расширения",
  Panel: "Панель",
  "ASUS web server": "Штатный веб-сервер ASUS",
  "Keep the USB drive connected for the service to run.":
    "Для работы службы USB-накопитель должен оставаться подключённым.",
  "SSH diagnostics": "Диагностика через SSH",
  Module: "Модуль",
  "Module settings": "Настройки модуля",
  "Panel parameters missing. Reinstall the distribution.":
    "Нет параметров панели. Переустановите дистрибутив.",
  "Configuration field unavailable": "Поле конфигурации недоступно",
  "Request exceeds the ASUS field size": "Запрос превышает размер поля ASUS",
  "Service rejected the request": "Служба отклонила запрос",
  "Service did not acknowledge the request. Check its status through SSH.":
    "Служба не подтвердила запрос. Проверьте её состояние через SSH.",
  "Configuration exceeds 16 KiB": "Конфигурация больше 16 КиБ",
  "Wait for the current action to finish":
    "Дождитесь завершения текущего действия",
  "Uploading configuration: ": "Загрузка конфигурации: ",
  "Unable to read status: ": "Не удалось получить статус: ",
  "Open the panel over HTTPS to upload configuration.":
    "Откройте панель по HTTPS для загрузки конфигурации.",
  About: "О проекте",
  "Report an issue ↗": "Сообщить об ошибке ↗",
  "Releases ↗": "Релизы ↗",
  "Support the project": "Поддержать проект",
  "If you want to support the work or say thanks:":
    "Если хотите поддержать работу или сказать спасибо:",
  ". Check your ASUS session.": ". Проверьте сессию ASUS.",
  "Install packages through SSH. Updating preserves your profile and network bindings.":
    "Пакеты устанавливаются через SSH. Обновление текущего дистрибутива сохраняет профиль и привязки сетей.",
  "Install a module: /jffs/ocfusion-platform/extctl install /tmp/module.tar.gz":
    "Для установки отдельного модуля: /jffs/ocfusion-platform/extctl install /tmp/module.tar.gz",
  ...window.OCF_MODULE_TRANSLATIONS,
};

(() => {
  const cfg = window.OCF_SETTINGS || {};
  const parentLanguage =
    window.parent !== window && window.parent.OCF_I18N
      ? window.parent.OCF_I18N.language
      : null;
  // state.js is rendered by ASUS on each request; the published config may predate a language change.
  const asusLanguage =
    parentLanguage ||
    (typeof ui_lang !== "undefined" ? ui_lang : cfg.language || "EN");
  const lang = String(asusLanguage).toUpperCase() === "RU" ? "ru" : "en";
  const texts = new WeakMap();
  const attrs = new WeakMap();
  const pairs = Object.entries(OCF_TRANSLATIONS).sort(
    (a, b) => b[0].length - a[0].length,
  );

  function translate(text, to) {
    let trimmed = text.trim();

    for (const [en, ru] of pairs) {
      const from = to === "en" ? ru : en;
      const dest = to === "en" ? en : ru;

      if (trimmed.replace(/\s+/g, " ") === from.trim().replace(/\s+/g, " ")) {
        return text.replace(trimmed, dest.trim());
      }
    }

    for (const [en, ru] of pairs) {
      const from = to === "en" ? ru : en;
      const dest = to === "en" ? en : ru;

      if (from.length > 10 && text.includes(from)) {
        text = text.split(from).join(dest);
      }
    }

    return text;
  }

  function apply() {
    document.documentElement.lang = lang;
    document.title =
      lang === "ru"
        ? "OCFusion - расширения ASUSWRT"
        : "OCFusion - ASUSWRT extensions";

    const walker = document.createTreeWalker(
      document.body,
      NodeFilter.SHOW_TEXT,
    );
    let node;

    while ((node = walker.nextNode())) {
      if (node.parentElement.closest("script,style,#TopBanner")) {
        continue;
      }

      if (
        node.parentElement.closest("pre") &&
        node.nodeValue.trim() !== "Loading…" &&
        node.nodeValue.trim() !== "Загрузка…"
      ) {
        continue;
      }

      const old = texts.get(node);
      const base =
        old && node.nodeValue === old.rendered
          ? old.base
          : translate(node.nodeValue, "en");
      const rendered = translate(base, lang);

      texts.set(node, { base, rendered });

      if (node.nodeValue !== rendered) {
        node.nodeValue = rendered;
      }
    }

    document.querySelectorAll("[placeholder],[title]").forEach((n) => {
      if (n.closest("#TopBanner")) {
        return;
      }

      const old = attrs.get(n) || {};

      for (const key of ["placeholder", "title"]) {
        if (!n.hasAttribute(key)) {
          continue;
        }

        const value = n.getAttribute(key);
        const previous = old[key];
        const base =
          previous && value === previous.rendered
            ? previous.base
            : translate(value, "en");
        const rendered = translate(base, lang);

        old[key] = { base, rendered };

        if (value !== rendered) {
          n.setAttribute(key, rendered);
        }
      }

      attrs.set(n, old);
    });
  }

  let queued = false;
  const observer = new MutationObserver(() => {
    if (queued) {
      return;
    }

    queued = true;
    Promise.resolve().then(() => {
      queued = false;
      apply();
    });
  });

  apply();
  observer.observe(document.body, {
    subtree: true,
    childList: true,
    characterData: true,
    attributes: true,
    attributeFilter: ["placeholder", "title"],
  });
  window.OCF_I18N = {
    get language() {
      return lang;
    },
    translate,
  };
})();
