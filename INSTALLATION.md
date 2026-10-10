# Installation and operation

## Requirements

Use an AArch64 router with stock ASUSWRT. See [tested compatibility](../README.md#compatibility); other routers have not been tested.

Use an ext4 USB partition with at least 128 MiB free space and 16 MiB free JFFS space on the router. Leave the drive connected during operation. Stock ASUS Apps autorun must be enabled, and another USB drive must not already own the Apps root. Preparation does not format or partition the drive.

## Installation

Both methods install OCFusion on the USB drive. Choose whether to prepare it on your computer or run the installer through router SSH.

### Prepare on a Linux computer

1. Download and extract the runtime archive from [Releases](https://github.com/nE0sIghT/ocfusion/releases) on a Linux computer.
2. Prepare the mounted USB partition, replacing `/media/user/USB` with its path:

   ```sh
   ./ocfusion/install.sh --prepare-usb /media/user/USB
   ```

3. Safely eject the drive, connect it to the router and wait for installation to finish. If installation does not start, reboot with the drive connected.
4. Sign in to the ASUS web interface and click the OCFusion icon <img src="web/icon.png" alt="OCFusion" width="24" height="24"> in the top-right header.

If installation fails, read `/tmp/ocfusion-install.log` through SSH. The prepared-USB installation path still needs its first physical-device validation.

### Run through router SSH

Download the runtime archive from [Releases](https://github.com/nE0sIghT/ocfusion/releases) and enable SSH in ASUS administration. Transfer the runtime archive to the USB drive. If SCP is unavailable, use SSH streaming from your computer:

```sh
ssh admin@192.168.50.1 'cat > /tmp/mnt/sda/ocfusion-0.2.9-aarch64.tar.gz' < ocfusion-0.2.9-aarch64.tar.gz
```

Replace the login, router address and USB mount path. On the router:

```sh
cd /tmp/mnt/sda
tar -xzf ocfusion-0.2.9-aarch64.tar.gz
cd ocfusion
/bin/sh install.sh
```

Use the explicit `/bin/sh` on the router because the inspected firmware has another executable named `sh`. A successful installation prints `INSTALL OK`. `CONNECTING` immediately after installation is normal. If installation fails, retain the complete output for troubleshooting.

## Update

Updates preserve VPN settings, credentials and Wi-Fi/VLAN assignments. Using the same drive also preserves additional modules. Updating briefly interrupts VPN traffic. Never disconnect storage during installation.

### Prepare on a Linux computer

1. Safely eject the USB drive through ASUS. The VPN stops while the drive is disconnected.
2. Connect the same drive to Linux and extract the new runtime archive outside its `asusware.arm` directory.
3. Run `./ocfusion/install.sh --prepare-usb /media/user/USB` with the actual mount path.
4. Safely eject it and reconnect it to the router. Reboot with the drive connected if installation does not start.
5. Check the new version and connection status in the OCFusion panel.

### Run through router SSH

Leave the USB drive connected to the router. Follow the [SSH installation steps](#run-through-router-ssh) with the new runtime archive: transfer it, extract it and run `/bin/sh install.sh` again. Check `INSTALL OK`, then open the panel and confirm the new version and connection status.

## Configure OpenConnect

Open the panel using the OCFusion icon <img src="web/icon.png" alt="OCFusion" width="24" height="24"> in the ASUS header. If the icon is unavailable, open `/user/extensions.html` at the router's HTTPS address and sign in.

Enter the complete VPN HTTPS URL, including any query parameters, and select the protocol and authentication method. For certificate authentication, upload the client certificate and private key. Add a CA certificate if needed for server verification. DTLS is enabled by default.

Empty replacement fields retain saved passwords and certificates; use the removal controls to delete them. Full JSON import replaces the profile. The runtime archive includes `modules/openconnect/examples/profile.occonf` as a configuration example. To import through SSH:

```sh
/jffs/ocfusion/ocfusion import /tmp/profile.occonf
rm -f /tmp/profile.occonf
```

In the ASUS settings for the desired SDN Wi-Fi/VLAN network, select the **OCFusion (managed)** VPN Fusion profile as its VPN connection. Leave this profile's switch **OFF** in VPN Fusion; OCFusion controls the tunnel.

The panel follows the ASUS language setting and supports English and Russian. Other languages use English. Save form edits before switching languages because the page reloads.

## Status and troubleshooting

Check connection status in the panel or through SSH:

```sh
/jffs/ocfusion/ocfusion status
/jffs/ocfusion/ocfusion doctor
```

- `UP`: the VPN connection and Wi-Fi/VLAN assignments are active.
- `CONNECTING`: connection is in progress.
- `AUTH_REQUIRED`: check server trust, client credentials and router time.
- `WAIT_NETWORK`: check the VPN assignment in ASUS network settings.
- `STOPPED`: the service has stopped.

To disable or enable the tunnel:

```sh
/jffs/ocfusion/ocfusion disable
/jffs/ocfusion/ocfusion enable
```

Assigned networks lose VPN access when the tunnel is disabled or USB is absent. Reconnect the drive to restore operation. After an update, refresh the browser cache if the panel still shows old assets.

## Disconnecting USB

Safely eject the drive through ASUS before unplugging it. Services stop and assigned networks lose VPN access. Removing the drive and rebooting disables startup but retains settings; it does not uninstall OCFusion.

## Uninstall

Keep USB connected and extract the runtime archive on it. First change every Wi-Fi/VLAN network assigned to **OCFusion (managed)** to another VPN or to no VPN in ASUS.

From the extracted `ocfusion` directory on the router, run:

```sh
/bin/sh install.sh --uninstall
```

This removes OCFusion services, installed files and VPN integration while retaining saved settings and credentials. Remove additional modules with `extctl remove MODULE` first; platform removal refuses to proceed while they remain installed.

To also delete all retained OCFusion settings, credentials and module data:

```sh
/bin/sh install.sh --uninstall --prune
```

After removal, safely eject USB and reboot. Other ASUS Apps files are left in place. Removing USB and rebooting without uninstalling only disables OCFusion and retains its settings.

## Reports

Report problems through [GitHub Issues](https://github.com/nE0sIghT/ocfusion/issues). Include the router model, firmware, OCFusion version, installation output and sanitized status/doctor output. Do not include private keys, passwords, profile files, cookies or secret query parameters.

See [architecture](architecture.md) for implementation details and [module development](modules.md) for the module interfaces.
