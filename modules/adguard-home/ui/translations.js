// SPDX-License-Identifier: GPL-3.0-or-later
window.OCF_MODULE_TRANSLATIONS = {
  "Filter router DNS independently of VPN. ASUS DNS settings are inherited by default.":
    "Фильтрация DNS роутера независимо от VPN. По умолчанию используются настройки DNS ASUS.",
  Connection: "Подключение",
  "Loading…": "Загрузка…",
  "Refresh statistics": "Обновить статистику",
  Settings: "Настройки",
  "Enable DNS filtering": "Включить фильтрацию DNS",
  "Custom upstreams (optional)": "Дополнительные DNS-серверы (необязательно)",
  "One IP, tls://host or https://host/dns-query per line. Add a connection IP after a space to preserve TLS hostname without bootstrap lookup. Empty inherits ASUS.":
    "Один IP, tls://host или https://host/dns-query в строке. IP подключения после пробела сохраняет имя TLS без предварительного DNS-запроса. Пустое поле использует настройки ASUS.",
  "Filter lists": "Списки фильтрации",
  "One HTTPS URL per line.": "Один HTTPS URL в строке.",
  "Custom filter rules and exceptions": "Правила фильтрации и исключения",
  "One AdGuard rule per line. Use @@ for exceptions.":
    "Одно правило AdGuard в строке. Для исключений используйте @@.",
  "Client filtering overrides": "Настройки фильтрации клиентов",
  "One IP or CIDR followed by on or off per line.":
    "В каждой строке IP или CIDR, затем on или off.",
  "Keep a bounded query log in RAM":
    "Хранить ограниченный журнал запросов в RAM",
  "Save settings": "Сохранить настройки",
  Reload: "Перечитать",
  "Current settings loaded.": "Текущие настройки загружены.",
  "Settings saved.": "Настройки сохранены.",
  "Service unavailable": "Служба недоступна",
  "Invalid upstream entry": "Неверная запись DNS-сервера",
  "Client override requires an IP or CIDR and on or off":
    "Укажите IP или CIDR и on или off для настройки клиента",
};

Object.assign(window.OCF_MODULE_TRANSLATIONS, {
  "List download failed": "Ошибка загрузки списка",
  "Loading filter lists…": "Загрузка списков фильтрации…",
  "Save changes before updating lists.":
    "Сохраните изменения перед обновлением списков.",
  Recommended: "Рекомендуется",
  "Not selected": "Не выбран",
  "Block advertising and tracking domains for devices using router DNS.":
    "Блокировка рекламных и отслеживающих доменов для устройств, использующих DNS роутера.",
  Protection: "Защита",
  Advanced: "Дополнительно",
  "Advanced settings": "Дополнительные настройки",
  Statistics: "Статистика",
  "DNS queries": "DNS-запросы",
  Blocked: "Заблокировано",
  "Average response": "Среднее время ответа",
  "ASUS DNS settings are used unless you configure custom DNS servers.":
    "Используются настройки DNS ASUS, если не указаны собственные DNS-серверы.",
  "DNS filtering blocks domains; it cannot remove individual page elements.":
    "DNS-фильтрация блокирует домены, но не удаляет отдельные элементы страниц.",
  "Update now": "Обновить сейчас",
  "Selected lists download automatically and update every 24 hours while protection is enabled.":
    "Выбранные списки загружаются автоматически и обновляются каждые 24 часа, пока защита включена.",
  "One general-purpose list is usually enough. Additional lists may overlap.":
    "Обычно достаточно одного универсального списка. Дополнительные списки могут дублировать правила.",
  "Custom filter lists": "Собственные списки фильтрации",
  "DNS servers and diagnostics": "DNS-серверы и диагностика",
  "Protection enabled": "Защита включена",
  "Protection disabled": "Защита выключена",
  "Waiting for DNS service": "Ожидание службы DNS",
  "Protection unavailable": "Защита недоступна",
  "Check settings": "Проверьте настройки",
  "External DNS queries: through VPN": "Внешние DNS-запросы: через VPN",
  "External DNS queries: through your ISP":
    "Внешние DNS-запросы: через провайдера",
  "External DNS route unavailable": "Путь внешних DNS-запросов неизвестен",
  "Not downloaded": "Не загружен",
  Rules: "Правил",
  "Update failed. Previous copy retained.":
    "Ошибка обновления. Сохранена предыдущая копия.",
  "Download failed. No list loaded.": "Ошибка загрузки. Список не загружен.",
  "Updating lists…": "Обновление списков…",
  "Enable protection before updating lists.":
    "Включите защиту перед обновлением списков.",
  "About this list ↗": "О списке ↗",
  "Discard unsaved changes?": "Отменить несохранённые изменения?",
  "Ads and trackers, including Russian-language sites":
    "Реклама и отслеживание, включая русскоязычные сайты",
  "Ads, trackers and malicious domains":
    "Реклама, отслеживание и вредоносные домены",
  "Ads and trackers with a focus on compatibility":
    "Реклама и отслеживание с акцентом на совместимость",
});
