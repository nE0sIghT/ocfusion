<p align="center">
  <a href="https://github.com/nE0sIghT/ocfusion"><img src="web/logo.png" alt="OCFusion" width="360"></a>
</p>

# OCFusion

OCFusion is a platform for extending stock ASUSWRT functionality without flashing replacement firmware. It provides an extension panel and module lifecycle management.

## Available modules

- **OpenConnect** - VPN connectivity for selected ASUS Wi-Fi/VLAN networks.

It lets you route a selected ASUS Wi-Fi/VLAN network through an OpenConnect tunnel while keeping network configuration in the ASUS interface. The OCFusion panel provides connection settings, certificate authentication, DTLS, status and automatic reconnection. English and Russian follow the router's native language setting.

The extension-module API is experimental.

## Compatibility

Tested router: **ASUS GT-BE98**, running stock ASUSWRT **3.0.0.6 build 102 / extension 39393**.

Other routers have not been tested.

## Quick start

The simplest installation uses a USB drive prepared on a Linux computer. Use an ext4 partition with at least 128 MiB free space and leave it connected during operation. Preparation does not format the drive. Stock ASUS Apps autorun must be enabled and another USB drive must not already own the Apps root.

1. Download the runtime archive from [Releases](https://github.com/nE0sIghT/ocfusion/releases).
2. Extract the archive, then prepare the mounted USB partition (replace `/media/user/USB`):

   ```sh
   tar -xzf ocfusion-0.3-aarch64.tar.gz
   ./ocfusion/install.sh --prepare-usb /media/user/USB
   ```

3. Safely eject the drive, plug it into the supported router and wait for ASUS Apps startup. Installation runs automatically; SSH is not required for this path. The new preparation path still needs its first physical-device validation.
4. Sign in to your router's ASUS web interface and click the OCFusion icon <img src="web/icon.png" alt="OCFusion" width="24" height="24"> in the top-right header. Configure OpenConnect in the OCFusion panel. In the ASUS settings for your SDN Wi-Fi/VLAN network, select the **OCFusion (managed)** VPN Fusion profile as its VPN connection. Leave this profile's switch **OFF** in VPN Fusion; OCFusion controls the tunnel.

See [installation, updates and troubleshooting](INSTALLATION.md). Reinstalling preserves the profile, Fusion index and network bindings. Updating briefly interrupts the VPN connection.

## Features

- OpenConnect with full URL/query support, password or client-certificate authentication, additional CA, server pin, DTLS, MTU and DNS settings.
- IPv4 full tunnel for assigned SDN networks, with firewall/routing reconciliation and IPv6 blocking for those networks.
- Native ASUS login, header and language-change flow; a separate OCFusion panel with English/Russian UI.
- USB-backed lifecycle, supervision and an experimental manager for separate executable modules.
- Static ARM64 executables built from pinned sources. No Entware installation is required.

Server-advertised split exclusions are intentionally ignored for the managed full tunnel.

## Development

Only source files and source assets are tracked. Meson/Ninja builds the runtime and source archives through the `bundle` target; no firmware image or prebuilt executable is needed.

- [Build and test](docs/building.md)
- [Module format and development](docs/modules.md)
- [Architecture](docs/architecture.md)
- [Third-party inputs and licenses](third_party/README.md)

Documentation and development use English. Product UI supports English and Russian equally. Use ASCII `-` instead of typographic dashes in documentation and UI text.

## Project and support

[GitHub](https://github.com/nE0sIghT/ocfusion) | [Issues](https://github.com/nE0sIghT/ocfusion/issues) | [Releases](https://github.com/nE0sIghT/ocfusion/releases)

If you want to support the work or say thanks:

- BTC: `bc1q3qmyy5wx8antx2e44lrr4fv9h3z8hs4x7jlnat`
- EVM: `0x6b513f6853003726502ec258351fcf6b82336d49`
- [Boosty](https://boosty.to/ne0sight/donate)

## License

OCFusion is free software licensed under the **GNU General Public License, version 3 or later**. See [LICENSE](LICENSE) for the full license text.

Third-party components retain their own licenses. The runtime distribution includes their required license notices.

Patches to third-party projects may be distributed under the original project's license. The OpenConnect patch is licensed under the **GNU Lesser General Public License, version 2.1**.
