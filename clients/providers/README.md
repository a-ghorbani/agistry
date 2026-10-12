# agistry providers

A **provider** announces the devices attached to its host (Android phones, iPhones)
so agents can [lease](../../README.md#resources--leases) them. The server can't see a
USB device on another machine, so a script on that machine posts what it finds to
`/resources/register` on a loop. A device that stops being announced goes `gone`
after the server's `REGISTRY_RESOURCE_TTL_SECONDS` (default 900); plugging it back in
revives it.

Providers belong to the **host**, not to an agent or a Claude profile, so each host
has one copy in `~/.config/agistry/providers/`, and at most one running daemon per
provider.

## Enable on a host with devices

1. **Install.** The Claude Code and Codex installers already did this. On its own:
   ```bash
   clients/providers/install.sh
   ```
2. **Choose providers** in `~/.config/agistry/client.env`. Nothing runs until you do:
   ```bash
   AGISTRY_PROVIDERS="adb ios"      # any of: adb, ios
   ```
3. **Start them**, one of:
   - **Session autostart** (macOS, or any host): add `AGISTRY_PROVIDERS_AUTOSTART=1`.
     The first Claude Code or Codex session to start on the host launches them;
     later sessions find them running.
   - **systemd user unit** (Linux host that always has the devices; starts at boot
     and restarts on failure):
     ```bash
     cp ~/.config/agistry/providers/agistry-provider@.service ~/.config/systemd/user/
     systemctl --user daemon-reload
     systemctl --user enable --now agistry-provider@adb   # one per provider
     ```

   Not both: systemd doesn't know about the autostarted copy, so each device would be
   announced twice. That does no harm, but it's wasted work.

Check with `~/.config/agistry/providers/agistry-providers.sh status` (also `list`,
`start`, `stop`), or run one pass by hand: `agistry-adb-provider.sh` (add `--loop`
to keep going).

**Uninstall:** `clients/providers/install.sh --uninstall` stops and removes them.
Uninstalling a Claude profile or the Codex client leaves them alone, since other
agents on the host may use them.

## Settings

All optional, in `~/.config/agistry/client.env`:

| Variable | Default | |
| --- | --- | --- |
| `AGISTRY_PROVIDER_INTERVAL` | `300` | Seconds between announcements. Keep it well under the server's resource TTL. |
| `AGISTRY_ADB_MAX_HOLD`, `AGISTRY_IOS_MAX_HOLD` | `21600` (6h) | Longest single hold on a phone: long enough for a full e2e suite, short enough to catch an agent that wandered off. |
| `AGISTRY_ADB` | found | Path to `adb`, if it isn't in `ANDROID_HOME`, the default SDK, or `PATH`. |
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
a serial rig, a licence dongle. Name it `agistry-<name>-provider.sh` and the launcher
and systemd unit pick it up as `<name>`. Things nothing on a host can discover, like a
staging URL or an API quota, go in the server's `REGISTRY_RESOURCES_FILE` instead.
