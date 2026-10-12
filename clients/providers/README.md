# Providers

agistry never discovers resources itself. A phone is attached to a **host**, not to the
registry, so only something running on that host can see it — and a central server has
no way to enumerate USB devices on a machine across the room. Discovery is therefore the
provider's job: whatever can see a resource announces it, on a loop, so it appears and
ages out with reality.

A provider is just a script that POSTs `/resources/register` periodically. If it stops,
its resources go `gone` after `REGISTRY_RESOURCE_TTL_SECONDS` and nobody can claim them.
Re-announcing revives them, so unplugging and replugging a device heals on its own.

| Script | Announces |
| --- | --- |
| `agistry-providers.sh` | Launcher — starts only the providers this host is configured for |
| `agistry-provider@.service` | systemd user unit template — one supervised provider per instance |
| `agistry-adb-provider.sh` | Android devices reachable via `adb devices -l` on this host |
| `agistry-ios-provider.sh` | iPhones/iPads attached to this host (Xcode's `devicectl` on macOS, libimobiledevice on Linux) |
| `install.sh` | Installs the above once per host into `~/.config/agistry/providers/` |

## Install (once per host)

Providers belong to the **host**, not to an agent or a Claude profile: the phones stay
plugged in whichever session is open. So there is one copy per host, in
`~/.config/agistry/providers/` next to the shared `client.env`. The Claude Code and
Codex installers both put it there, or run it on its own:

```bash
clients/providers/install.sh               # copy into ~/.config/agistry/providers
clients/providers/install.sh --uninstall   # stop running providers and remove them
```

Uninstalling a Claude profile or the Codex client leaves the providers alone, since
other profiles or agents on the host may still rely on them.

## Nothing runs unless you configure it

Most hosts have no devices attached and should run no provider at all, so the default
is **none**. Providers are named in `~/.config/agistry/client.env`:

```sh
AGISTRY_PROVIDERS="adb ios"      # space/comma separated; unset or empty = none run
AGISTRY_PROVIDER_INTERVAL=300    # re-announce cadence, seconds
AGISTRY_ADB_MAX_HOLD=21600       # 6h cap on a single hold of a phone
AGISTRY_ADB=/path/to/adb         # optional; else ANDROID_HOME, the SDK, then PATH
AGISTRY_IOS_MAX_HOLD=21600       # 6h cap on a single hold of an iPhone
AGISTRY_IOS_TRANSPORTS=wired     # devicectl: which connections count; add localNetwork to opt in
AGISTRY_IOS_TOOL=devicectl       # optional; force devicectl or libimobiledevice
```

The launcher is idempotent — a pidfile guard means one daemon per provider per host,
however many times it is called:

```bash
P=~/.config/agistry/providers
$P/agistry-providers.sh list      # what exists, and what this host enables
$P/agistry-providers.sh start     # start the enabled ones (no-op if unset)
$P/agistry-providers.sh status
$P/agistry-providers.sh stop
```

Two ways to get them running, both opt-in:

- **User service (preferred on a Linux host that permanently owns devices).**
  ```bash
  cp ~/.config/agistry/providers/agistry-provider@.service ~/.config/systemd/user/
  systemctl --user daemon-reload
  systemctl --user enable --now agistry-provider@adb    # the instance name is the provider
  ```
  It runs the provider in the foreground so **systemd** is what notices it died and
  restarts it; it starts at boot and does not depend on anyone opening a session.
- **Session autostart** (the option on macOS). Set `AGISTRY_PROVIDERS_AUTOSTART=1` as
  well, and the SessionStart hook of either client — Claude Code or Codex — calls the
  launcher. Both gates must be set, so a host that has not asked for providers never
  spawns one.

Use **one or the other**. systemd supervises its copy directly and knows nothing about
the launcher's pidfile, so enabling both announces the same devices twice — harmless,
since registration is an idempotent upsert, but pointless.

You can also run a single provider directly, without the launcher:

```bash
./agistry-adb-provider.sh            # one pass
./agistry-adb-provider.sh --loop 300 # keep refreshing
```

Devices are keyed by adb serial (`adb:R5CT21…`) because that is what the tooling already
uses; the friendly model name rides along as metadata. Phones default to a 6h max hold
(`AGISTRY_ADB_MAX_HOLD`) since a full e2e suite can run for hours — the cap is a backstop
against an agent that is alive but has wandered off, not a normal expiry.

iPhones and iPads are keyed by UDID (`ios:00008110-…`), the id `xcodebuild -destination
id=<UDID>`, `devicectl --device` and `idevice* -u` all take; model, device name and iOS
version ride along as metadata. On macOS the provider asks `xcrun devicectl list
devices` (Xcode 15+) and announces **paired, physical devices on a cable** only: a phone
paired to several Macs is visible to all of them over Wi-Fi, and two hosts must not
announce the same device. Set `AGISTRY_IOS_TRANSPORTS="wired localNetwork"` on the one
host that should own network-attached phones. On Linux it uses libimobiledevice
(`idevice_id -l` + `ideviceinfo`), which sees USB devices only. Simulators are not
announced — any number can run, so there is nothing to contend for.

Copy this shape for anything else that is exclusive and host-attached: GPUs, serial
rigs, licence dongles. Things with nobody to announce them — a staging URL, an API quota
— belong in the server's `REGISTRY_RESOURCES_FILE` seed instead.
