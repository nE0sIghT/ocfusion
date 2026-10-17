#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-or-later
set -eu
export PATH=/usr/bin:/bin:/usr/sbin:/sbin
HERE=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)

fail() {
    echo "ocfusion install: $*" >&2
    exit 1
}

# Restore platform and module state as one transaction.
restore_previous_installation() {
    if [ -f "$BACKUP/state.json" ]; then
        "$HERE/bin/extd" restore-state "$BACKUP" || return 1
    fi

    if [ -d "$APP_ROOT/ocfusion.previous" ]; then
        rm -rf "$APP_ROOT/ocfusion"
        mv "$APP_ROOT/ocfusion.previous" "$APP_ROOT/ocfusion"
    fi

    for f in S95ocfusion ocfusion.control; do
        if [ -f "$BACKUP/$f" ]; then
            case "$f" in
                S95ocfusion)
                    cp "$BACKUP/$f" "$APP_ROOT/etc/init.d/$f"
                    ;;
                *)
                    cp "$BACKUP/$f" "$APP_ROOT/lib/ipkg/info/$f"
                    ;;
            esac
        fi
    done

    if [ -f "$APP_ROOT/etc/init.d/S95ocfusion" ]; then
        /bin/sh "$APP_ROOT/etc/init.d/S95ocfusion" start || true
    fi
}

remove_incomplete_installation() {
    if [ -f "$BACKUP/state.json" ]; then
        "$HERE/bin/extd" restore-state "$BACKUP" || true
    fi

    rm -f "$APP_ROOT/etc/init.d/S95ocfusion" "$APP_ROOT/lib/ipkg/info/ocfusion.control"
    rm -rf "$APP_ROOT/ocfusion"
    [ "$CREATED_MARKER" = 0 ] || rm -f "$APP_ROOT/.asusrouter"
    [ "$CREATED_OPT" = 0 ] || rm -f /tmp/opt
}

cleanup() {
    if [ "$SUCCESS" != 1 ]; then
        echo 'INSTALL FAILED: restoring previous owned files where possible.' >&2
        cru d ocfusion >/dev/null 2>&1 || true
        if [ -x /jffs/ocfusion-platform/extctl ]; then
            /jffs/ocfusion-platform/extctl stop >/dev/null 2>&1 || true
        fi
        "$HERE/bin/extd" web-reset >/dev/null 2>&1 || true
        if [ "$MODE" = update ]; then
            restore_previous_installation
        else
            remove_incomplete_installation
        fi
    fi
    rm -f /tmp/ocfusion-installing

    if [ "$SUCCESS" != 1 ]; then
        printf '{"state":"UNAVAILABLE","modules":[]}\n' > /tmp/var/wwwext/extensions-modules.json.new
        mv /tmp/var/wwwext/extensions-modules.json.new /tmp/var/wwwext/extensions-modules.json
    fi

    rm -rf "$APP_ROOT/ocfusion.next" "$LOCK"
}

check_asuswrt() {
    [ -f /rom/.asusrouter ] && [ -f /www/VPN/vpnc.html ] &&
        [ -x /usr/sbin/app_get_field.sh ] ||
        fail 'ASUSWRT required: stock web and Apps components not found. No changes made.'

    ASUS_NVRAM=
    for dir in /usr/sbin /usr/bin /sbin /bin; do
        if [ -x "$dir/nvram" ] && [ ! -d "$dir/nvram" ]; then
            ASUS_NVRAM=$dir/nvram
            break
        fi
    done
    [ -n "$ASUS_NVRAM" ] || fail 'ASUSWRT required: nvram utility not found. No changes made.'

    [ -n "$("$ASUS_NVRAM" get productid)" ] &&
        [ -n "$("$ASUS_NVRAM" get firmver)" ] &&
        [ -n "$("$ASUS_NVRAM" get buildno)" ] ||
        fail 'ASUSWRT required: firmware identity missing. No changes made.'
}

check_router_identity() {
    [ "$(awk '$1 == "Uid:" {print $3}' /proc/$$/status)" = 0 ] || fail 'SSH administrator needs UID 0. Username may be admin; root login is not required.'
    [ "$(uname -m)" = aarch64 ] || fail "This distribution requires AArch64 (detected: $(uname -m))."
    check_asuswrt

    MODE=install
    if [ -f /jffs/ocfusion-platform/transport.json ]; then
        MODE=update
    fi
    [ -d /jffs ] && [ -w /jffs ] || fail 'JFFS is not writable.'
}

check_required_tools() {
    # ASUS shell may omit the POSIX command builtin. Inspect our fixed PATH directly.
    for tool in nvram ip iptables ip6tables cru get_apps_name service sha256sum; do
        FOUND=0
        for dir in /usr/sbin /usr/bin /sbin /bin; do
            if [ -x "$dir/$tool" ] && [ ! -d "$dir/$tool" ]; then
                FOUND=1
                break
            fi
        done
        [ "$FOUND" = 1 ] || fail "Missing $tool"
    done
}

check_distribution() {
    if [ -f "$HERE/SHA256SUMS" ]; then
        (cd "$HERE" && sha256sum -c SHA256SUMS >/tmp/ocfusion-checksums.log 2>&1) || fail 'Distribution checksum mismatch.'
    fi
    "$HERE/bin/extd" version || fail 'Compiled AArch64 platform cannot execute.'
    iptables -S FORWARD >/dev/null || fail 'No firewall privileges in this administrator session.'
    ip6tables -S FORWARD >/dev/null || fail 'IPv6 firewall unavailable.'
}

locate_apps_root() {
    if [ "$(nvram get stop_runapp)" = 1 ]; then
        echo 'CHECK boot_autostart=WARN; disabled in ASUS Apps settings.'
    fi
    APP_FOLDER=$(nvram get apps_install_folder)
    case "$APP_FOLDER" in
        "" | . | .. | *[!a-zA-Z0-9._-]*)
            fail 'ASUS Apps folder is missing or not a safe directory name.'
            ;;
    esac
    APP_MOUNT=$(nvram get apps_mounted_path)
    if [ -z "$APP_MOUNT" ]; then
        # Bundle must already reside on the intended USB partition. No format/mount guessing.
        APP_MOUNT=$(df -P "$HERE" | awk 'NR==2 {print $6}')
        case "$APP_MOUNT" in
            /tmp/mnt/*)
                :
                ;;
            *)
                fail 'Extract the distribution on ext4 USB under /tmp/mnt/<partition>/ocfusion-dist.'
                ;;
        esac
    fi
    case "$APP_MOUNT" in
        *' '* | *'>'* | *'<'*)
            fail 'Mountpoint containing spaces/delimiters is not supported.'
            ;;
    esac
    mount | awk -v p="$APP_MOUNT" '$3==p && $5=="ext4" {found=1} END{exit !found}' || fail 'Chosen USB mount must be ext4.'
    APP_ROOT=$APP_MOUNT/$APP_FOLDER
}

check_destination() {
    [ ! -f "$APP_ROOT/.asusrouter.disabled" ] || fail 'ASUS Apps marker disabled.'
    if [ "$MODE" = install ]; then
        [ ! -e "$APP_ROOT/ocfusion" ] || fail 'Unowned destination bundle exists.'
        [ ! -e "$APP_ROOT/lib/ipkg/info/ocfusion.control" ] || fail 'Unowned package metadata exists.'
        [ ! -e "$APP_ROOT/etc/init.d/S95ocfusion" ] || fail 'Unowned boot entry exists.'
    fi
    if [ -e /tmp/opt ]; then
        [ "$(readlink -f /tmp/opt)" = "$(readlink -f "$APP_ROOT")" ] || fail '/tmp/opt belongs to another ASUS Apps root; no takeover performed.'
    fi
    SPACE=$(df -Pk /jffs | awk 'NR==2 {print $4}')
    [ "$SPACE" -gt 16384 ] || fail 'At least 16 MiB free JFFS required.'
}

acquire_install_lock() {
    LOCK=/tmp/ocfusion-install.lock
    if [ -d "$LOCK" ]; then
        OLD_PID=$(cat "$LOCK/pid" 2>/dev/null || true)
        case "$OLD_PID" in
            "" | *[!0-9]*)
                fail 'Invalid installation lock.'
                ;;
        esac
        [ ! -d "/proc/$OLD_PID" ] || fail 'Another installation is running.'
        rm -rf "$LOCK"
    fi
    mkdir "$LOCK" || fail 'Cannot lock installation.'
    echo $$ >"$LOCK/pid"
}

initialize_transaction() {
    CREATED_MARKER=0
    CREATED_OPT=0
    SUCCESS=0
    CORE_PREVIOUS=0
    [ ! -f /jffs/ocfusion-platform/extd ] || CORE_PREVIOUS=1
    BACKUP=/jffs/ocfusion-update-backup
}

link_apps_root() {
    if [ ! -e /tmp/opt ]; then
        ln -s "$APP_ROOT" /tmp/opt
        CREATED_OPT=1
    fi
    [ "$(readlink -f /tmp/opt)" = "$(readlink -f "$APP_ROOT")" ] || fail '/tmp/opt belongs to another ASUS Apps root; no takeover performed.'
}

backup_existing_installation() {
    if [ -d "$BACKUP" ]; then
        if [ -x /jffs/ocfusion-platform/extctl ]; then
            /jffs/ocfusion-platform/extctl stop || fail 'Cannot stop interrupted installation.'
        fi

        restore_previous_installation || fail 'Cannot restore interrupted installation.'
        rm -rf "$BACKUP"
    fi

    "$HERE/bin/extd" backup-state "$HERE/modules" "$BACKUP" || fail 'Cannot verify or back up module state.'
    if [ -x /jffs/ocfusion-platform/extctl ]; then
        /jffs/ocfusion-platform/extctl stop || fail 'Cannot stop extension manager.'
    fi

    [ ! -f "$APP_ROOT/etc/init.d/S95ocfusion" ] || cp "$APP_ROOT/etc/init.d/S95ocfusion" "$BACKUP/"
    [ ! -f "$APP_ROOT/lib/ipkg/info/ocfusion.control" ] || cp "$APP_ROOT/lib/ipkg/info/ocfusion.control" "$BACKUP/"
}

install_bundle() {
    mkdir -p "$APP_ROOT/etc/init.d" "$APP_ROOT/lib/ipkg/info"
    if [ ! -e "$APP_ROOT/.asusrouter" ]; then
        cp /rom/.asusrouter "$APP_ROOT/.asusrouter"
        CREATED_MARKER=1
    fi
    rm -rf "$APP_ROOT/ocfusion.next"
    mkdir "$APP_ROOT/ocfusion.next"
    cp -R "$HERE/bin" "$HERE/web" "$HERE/modules" "$APP_ROOT/ocfusion.next/"
    if [ "$MODE" = update ] && [ -d "$APP_ROOT/ocfusion" ]; then
        mv "$APP_ROOT/ocfusion" "$APP_ROOT/ocfusion.previous"
    fi
    mv "$APP_ROOT/ocfusion.next" "$APP_ROOT/ocfusion"

}

install_apps_entry() {
    cp "$HERE/init/S95ocfusion" "$APP_ROOT/etc/init.d/S95ocfusion"
    chmod 755 "$APP_ROOT/etc/init.d/S95ocfusion"
    cat >"$APP_ROOT/lib/ipkg/info/ocfusion.control" <<'CONTROL'
Package: ocfusion
Version: @VERSION@
Architecture: aarch64
Enabled: yes
Description: OCFusion OpenConnect supervisor
CONTROL
    [ "$(get_apps_name /opt/etc/init.d/S95ocfusion)" = ocfusion ] || fail 'Unexpected ASUS app-name parser.'
    [ "$(/usr/sbin/app_get_field.sh ocfusion Enabled 1)" = yes ] || fail 'ASUS Apps did not accept metadata.'
}



install_extension_manager() {
    mkdir -p /jffs/ocfusion-platform
    chmod 700 /jffs/ocfusion-platform
    cp "$HERE/bin/extd" /jffs/ocfusion-platform/extd.new
    chmod 700 /jffs/ocfusion-platform/extd.new
    mv /jffs/ocfusion-platform/extd.new /jffs/ocfusion-platform/extd
    ln -sf extd /jffs/ocfusion-platform/extctl
    /jffs/ocfusion-platform/extctl prepare-platform /tmp/opt/ocfusion || fail 'Cannot prepare platform web transport.'
    /jffs/ocfusion-platform/extctl prepare-bundled /tmp/opt/ocfusion/modules || fail 'Cannot install bundled modules.'
    /jffs/ocfusion-platform/extctl start || fail 'Cannot start extension manager.'
    /jffs/ocfusion-platform/extctl register-set /tmp/opt/ocfusion/modules >/dev/null || fail 'Cannot register bundled modules.'

}

start_and_check_services() {
    /bin/sh /opt/etc/init.d/S95ocfusion start || fail 'Service startup failed.'
    /jffs/ocfusion-platform/extctl web || echo 'CHECK web_overlay=WARN; direct panel remains available.'
    /jffs/ocfusion-platform/extctl check-bundled /tmp/opt/ocfusion/modules || fail 'Module startup checks failed.'

}

finish_installation() {
    SUCCESS=1
    rm -rf "$BACKUP" "$APP_ROOT/ocfusion.previous"
    echo 'CHECK extension_manager=OK panel=/user/extensions.html'
    echo "INSTALL OK mode=$MODE; existing profile, Fusion index and SDN bindings preserved on update."
    echo 'Enabled modules start automatically. Configure modules in the OCFusion panel.'
}

parse_uninstall_options() {
    PRUNE=0

    case "$#:${1:-}" in
        0:)
            :
            ;;
        1:--prune)
            PRUNE=1
            ;;
        *)
            fail 'Usage: install.sh --uninstall [--prune]'
            ;;
    esac
}

check_uninstall_destination() {
    [ -f /jffs/ocfusion-platform/transport.json ] ||
        [ -f /jffs/ocfusion-platform/transport.removed.json ] ||
        fail 'No owned OCFusion installation found.'

    if [ -e /tmp/opt ]; then
        [ "$(readlink -f /tmp/opt)" = "$(readlink -f "$APP_ROOT")" ] ||
            fail 'ASUS Apps root belongs to another drive.'
    fi

    "$HERE/bin/extd" check-removal ||
        fail 'Remove additional modules before uninstalling the platform.'

}

stop_installed_services() {
    if [ -x /jffs/ocfusion-platform/extctl ] && [ -f /jffs/ocfusion-platform/transport.json ]; then
        /jffs/ocfusion-platform/extctl start || fail 'Cannot start module manager for removal.'
        /jffs/ocfusion-platform/extctl remove-bundled /tmp/opt/ocfusion/modules ||
            fail 'Module cleanup failed. Resolve dependencies or restore its settings, then retry. Saved data retained.'
        /jffs/ocfusion-platform/extctl web-reset || fail 'Cannot remove owned web overlays.'
        /jffs/ocfusion-platform/extctl remove-transport || fail 'Cannot remove platform transport.'

    fi

    for manager in /jffs/ocfusion-platform/extctl; do
        if [ -x "$manager" ]; then
            "$manager" stop || fail 'Cannot stop extension manager.'
        fi
    done

    cru d ocfusion
}

remove_installed_programs() {
    rm -f "$APP_ROOT/etc/init.d/S95ocfusion" "$APP_ROOT/etc/init.d/S94ocfusionprep"
    rm -f "$APP_ROOT/lib/ipkg/info/ocfusion.control" "$APP_ROOT/lib/ipkg/info/ocfusionprep.control"
    rm -rf "$APP_ROOT/ocfusion" "$APP_ROOT/ocfusion-installer" "$APP_ROOT/ocfusion-modules"


    for directory in /jffs/ocfusion-platform; do
        rm -f "$directory/extd" "$directory/extctl" "$directory/registry.json"
    done
}

finish_uninstall() {
    REBOOT_REQUIRED=0
    if [ -f /tmp/ocfusion-platform/removal-reboot-required ]; then
        REBOOT_REQUIRED=1
    fi

    if [ "$PRUNE" = 1 ]; then
        "$HERE/bin/extd" prune-state "$HERE/modules" || fail 'Cannot prune module data.'
        echo 'UNINSTALL OK; saved OCFusion data deleted.'
    else
        echo 'UNINSTALL OK; saved OCFusion data retained. Use --uninstall --prune to delete it.'
    fi

    if [ "$REBOOT_REQUIRED" = 1 ]; then
        echo 'REBOOT REQUIRED: restart the router through ASUS to finish restoring module settings.'
    fi
}

uninstall_platform() {
    parse_uninstall_options "$@"

    check_router_identity
    locate_apps_root
    check_uninstall_destination

    acquire_install_lock
    trap 'rm -rf "$LOCK"' EXIT

    stop_installed_services
    remove_installed_programs
    finish_uninstall
}

usage() {
    cat <<'USAGE'
Usage:
  ./install.sh                         Install or update on the router via SSH
  ./install.sh --prepare-usb DIRECTORY Prepare a mounted ext4 USB drive on Linux
  ./install.sh --uninstall             Remove OCFusion, keeping saved data
  ./install.sh --uninstall --prune     Remove OCFusion and its saved data
  ./install.sh --help                  Show this help
USAGE
}

main() {
    if [ "${1:-}" = --help ] || [ "${1:-}" = -h ]; then
        if [ "$#" != 1 ]; then
            usage >&2
            exit 1
        fi

        usage
        return
    fi

    if [ "${1:-}" = --prepare-usb ]; then
        shift
        exec /bin/sh "$HERE/scripts/prepare-usb.sh" "$@"
    fi
    if [ "${1:-}" = --uninstall ]; then
        shift
        uninstall_platform "$@"
        return
    fi
    [ "$#" = 0 ] || {
        usage >&2
        exit 1
    }

    check_router_identity
    check_required_tools
    check_distribution
    locate_apps_root
    check_destination
    acquire_install_lock
    initialize_transaction
    trap cleanup EXIT
    : > /tmp/ocfusion-installing
    mkdir -p /tmp/var/wwwext
    printf '{"state":"INSTALLING","updated_at":%s000,"modules":[]}\n' "$(date +%s)" > /tmp/var/wwwext/extensions-modules.json.new
    mv /tmp/var/wwwext/extensions-modules.json.new /tmp/var/wwwext/extensions-modules.json
    link_apps_root
    backup_existing_installation
    install_bundle
    install_apps_entry
    install_extension_manager
    start_and_check_services
    finish_installation
}

main "$@"
