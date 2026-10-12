# agistry providers

A **provider** announces the devices attached to its host (Android phones, iPhones)
so agents can [lease](../../README.md#resources--leases) them. The server can't see a
USB device on another machine, so a script on that machine posts what it finds to
`/resources/register` on a loop. A device that stops being announced goes `gone`
after the server's `REGISTRY_RESOURCE_TTL_SECONDS` (default 900); plugging it back in
revives it.

Providers belong to the **host**, not to an agent session, so they run as user
services: a launchd LaunchAgent on macOS, a systemd user unit on Linux. They start
when enabled, at every login or boot, and restart if they die. Each host has one copy
of the scripts in `~/.config/agistry/providers/`.

## Enable on a host with devices

1. **Choose providers** in `~/.config/agistry/client.env`:
   ```bash
   AGISTRY_PROVIDERS="adb ios"      # any of: adb, ios
   ```
2. **Apply** — this installs the scripts and starts a service per provider listed,
   stopping any that are no longer listed:
   ```bash
   clients/providers/install.sh
   ```
   Every agent client's installer runs it too, so if `AGISTRY_PROVIDERS` was set
   before you installed a client, the providers are already running. After changing
   the list, run it again.

Check them with `~/.config/agistry/providers/agistry-providers.sh status` (or `list`).
Logs: `~/Library/Logs/agistry/provider-<name>.log` on macOS,
`journalctl --user -u agistry-provider@<name>` on Linux. On a Linux server you don't
stay logged in to, run `loginctl enable-linger $USER` once so the services keep running
after you log out; the installer reminds you.

**Uninstall:** `clients/providers/install.sh --uninstall` stops the services and
removes the scripts. Uninstalling an agent client (or one Claude profile) leaves them
alone, since other agents on the host may use them.

## Settings

All optional, in `~/.config/agistry/client.env`. Re-run `install.sh` after changing
them, which restarts the services:

| Variable | Default | |
| --- | --- | --- |
| `AGISTRY_PROVIDER_INTERVAL` | `300` | Seconds between announcements. Keep it well under the server's resource TTL. |
| `AGISTRY_ADB_MAX_HOLD`, `AGISTRY_IOS_MAX_HOLD` | `21600` (6h) | Longest single hold on a phone: long enough for a full e2e suite, short enough to catch an agent that wandered off. |
| `AGISTRY_ADB` | found | Path to `adb`, if it isn't in `ANDROID_HOME` or the default SDK. Services don't get your shell's `PATH`, so set this or `ANDROID_HOME` here. |
| `AGISTRY_IOS_TRANSPORTS` | `wired` | Add `localNetwork` to also announce iPhones connected over Wi-Fi (see below). |
| `AGISTRY_IOS_TOOL` | found | Force `devicectl` or `libimobiledevice`. |

## Devices

| Provider | Id | Found with |
| --- | --- | --- |
| `adb` | `adb:<serial>` | `adb devices -l`; devices that are offline or unauthorized are skipped |
| `ios` | `ios:<UDID>` | macOS: `xcrun devicectl list devices` (Xcode 15+). Linux: libimobiledevice, USB only |

The id is what the device tooling already takes: `adb -s <serial>`,
`xcodebuild -destination id=<UDID>`, `devicectl --device`, `idevice* -u`. The model
(and for iOS the device name and OS version) is included as metadata.

**iPhones over Wi-Fi.** A phone paired to several Macs is visible to all of them over
the network, and two hosts must not announce the same device. So by default only
cabled phones are announced. Set `AGISTRY_IOS_TRANSPORTS="wired localNetwork"` on the
one Mac that should own the network-attached ones. Simulators are never announced:
you can run as many as you like, so there is nothing to lease.

## Adding a provider

Copy `agistry-adb-provider.sh` for anything exclusive and attached to a host: a GPU,
a serial rig, a licence dongle. Name it `agistry-<name>-provider.sh` with a `--loop`
mode, and `AGISTRY_PROVIDERS` can list it as `<name>`. Things nothing on a host can
discover, like a staging URL or an API quota, go in the server's
`REGISTRY_RESOURCES_FILE` instead.
