#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-or-later
export PATH=/usr/bin:/bin:/usr/sbin:/sbin
CTL=/jffs/ocfusion/ocfusion
case "$1" in
    preflight)
        if [ -f /jffs/ocfusion/manifest.json ]; then
            "$EXT_PAYLOAD_PATH/bin/ocfusion-openconnect" verify-upgrade "$EXT_PAYLOAD_PATH" >/dev/null || exit 1
        fi
        printf '%s\n' '{"ok":true}'
        ;;
    install)
        mkdir -p /jffs/ocfusion
        chmod 700 /jffs/ocfusion
        cp "$EXT_PAYLOAD_PATH/bin/ocfusion-openconnect" "$CTL.new" || exit 1
        chmod 700 "$CTL.new"
        mv "$CTL.new" "$CTL" || exit 1
        cp "$EXT_PAYLOAD_PATH/scripts/vpnc-script" /jffs/ocfusion/vpnc-script || exit 1
        chmod 700 /jffs/ocfusion/vpnc-script
        printf '%s\n' '{"ok":true}'
        ;;
    validate)
        exec "$CTL" module-validate
        ;;
    prepare)
        "$CTL" claim >/dev/null || exit 1
        "$CTL" setup "$EXT_PACKAGE_PATH" >/dev/null || exit 1
        printf '%s\n' '{"ok":true}'
        ;;
    run)
        exec "$CTL" supervise
        ;;
    reload)
        exec "$CTL" module-import
        ;;
    settings | update-settings | diagnose | connect | disconnect | reconnect | profiles | add-profile | delete-profile | import-profile)
        exec "$CTL" profile-action "$1"
        ;;
    remove)
        "$CTL" uninstall >/dev/null && printf '%s\n' '{"ok":true}'
        ;;
    *)
        exit 1
        ;;
esac
