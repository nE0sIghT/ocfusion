#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-or-later
set -eu
HERE=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)

fail() {
    echo "ocfusion prepare-usb: $*" >&2
    exit 1
}

check_usb() {
    [ "$#" = 1 ] || fail 'Usage: install.sh --prepare-usb DIRECTORY'
    [ -d "$1" ] && [ -w "$1" ] || fail 'Use a writable, mounted ext4 USB directory.'
    USB=$(CDPATH= cd -- "$1" && pwd)
    [ "$USB" != / ] || fail 'Refusing the filesystem root.'
    [ "$(stat -f -c %T "$USB")" = ext2/ext3 ] || fail 'Use an ext4 USB partition on Linux; preparation does not format it.'
    [ -f "$HERE/SHA256SUMS" ] || fail 'Use the complete runtime distribution.'
    (cd "$HERE" && sha256sum -c SHA256SUMS >/dev/null) || fail 'Distribution checksum mismatch.'
}

check_apps_owner() {
    APP=$USB/asusware.arm
    if [ -e "$APP" ]; then
        [ ! -L "$APP" ] || fail 'Apps root must not be a symlink.'
        if [ -f "$APP/.asusrouter" ] && [ "$(cat "$APP/.asusrouter")" = 'OCFusion prepared USB marker v1' ]; then
            :
        elif [ -f "$APP/lib/ipkg/info/ocfusion.control" ] &&
            grep -qx 'Package: ocfusion' "$APP/lib/ipkg/info/ocfusion.control" &&
            [ -f "$APP/etc/init.d/S95ocfusion" ] &&
            grep -q '^# ASUS app entrypoint.' "$APP/etc/init.d/S95ocfusion" &&
            [ -f "$APP/ocfusion/bin/extd" ] &&
            [ -f "$APP/.asusrouter" ]; then
            # Existing OCFusion installation, including SSH installation.
            # Preserve its ASUS marker and all other package records.
            :
        else
            fail 'Existing ASUS Apps root has no recognized OCFusion installation; use SSH installation instead.'
        fi
    fi
}

check_destinations() {
    case "$HERE/" in
        "$APP/"*)
            fail 'Extract the distribution outside the Apps directory.'
            ;;
    esac
    for p in \
        "$APP/.asusrouter" \
        "$APP/etc/init.d/S94ocfusionprep" \
        "$APP/lib/ipkg/info/ocfusionprep.control" \
        "$APP/etc/init.d" \
        "$APP/lib/ipkg" \
        "$APP/lib/ipkg/info" \
        "$APP/ocfusion-installer" \
        "$APP/ocfusion-installer.next" \
        "$APP/etc" \
        "$APP/lib"; do
        [ ! -L "$p" ] || fail 'Refusing symlink destinations.'
    done
}

copy_installer() {
    mkdir -p "$APP"
    # Copy before publishing the entrypoint; a partial copy never runs on the router.
    [ ! -e "$APP/ocfusion-installer.next" ] || fail 'Incomplete preparation exists; inspect ocfusion-installer.next first.'
    mkdir "$APP/ocfusion-installer.next"
    cp -R "$HERE/." "$APP/ocfusion-installer.next/"
    rm -rf "$APP/ocfusion-installer"
    mv "$APP/ocfusion-installer.next" "$APP/ocfusion-installer"
}

publish_startup_entry() {
    mkdir -p "$APP/etc/init.d" "$APP/lib/ipkg/info"
    if [ ! -e "$APP/.asusrouter" ]; then
        printf '%s\n' 'OCFusion prepared USB marker v1' >"$APP/.asusrouter"
    fi
    cat >"$APP/lib/ipkg/info/ocfusionprep.control" <<'CONTROL'
Package: ocfusionprep
Version: 1
Enabled: yes
CONTROL
    cp "$HERE/init/S94ocfusionprep" "$APP/etc/init.d/S94ocfusionprep"
    sync
    echo 'PREPARE OK: safely eject the ext4 USB drive, connect it to the router and wait for ASUS Apps startup.'
    echo 'Open https://<router>/user/extensions.html. Router installation log: /tmp/ocfusion-install.log.'
}

main() {
    check_usb "$@"
    check_apps_owner
    check_destinations
    copy_installer
    publish_startup_entry
}

main "$@"
