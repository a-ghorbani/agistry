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
| `agistry-adb-provider.sh` | Android devices reachable via `adb devices -l` on this host |

## Nothing runs unless you configure it

Most hosts have no devices attached and should run no provider at all, so the default
is **none**. Providers are named in `~/.config/agistry/client.env`:

```sh
AGISTRY_PROVIDERS="adb"          # space/comma separated; unset or empty = none run
AGISTRY_PROVIDER_INTERVAL=300    # re-announce cadence, seconds
AGISTRY_ADB_MAX_HOLD=21600       # 6h cap on a single hold of a phone
```

The launcher is idempotent — a pidfile guard means one daemon per provider per host,
however many times it is called:

```bash
agistry-providers.sh list      # what exists, and what this host enables
agistry-providers.sh start     # start the enabled ones (no-op if unset)
agistry-providers.sh status
agistry-providers.sh stop
```

Two ways to get them running, both opt-in:

- **User service (preferred on a host that permanently owns devices).** Copy
  `agistry-providers.service` to `~/.config/systemd/user/` and
  `systemctl --user enable --now agistry-providers`. Starts at boot, restarts on
  failure, does not depend on anyone opening a Claude session.
- **Session autostart.** Set `AGISTRY_PROVIDERS_AUTOSTART=1` as well, and the
  SessionStart hook calls the launcher. Both gates must be set, so a host that has
  not asked for providers never spawns one.

You can also run a single provider directly, without the launcher:

```bash
./agistry-adb-provider.sh            # one pass
./agistry-adb-provider.sh --loop 300 # keep refreshing
```

Devices are keyed by adb serial (`adb:R5CT21…`) because that is what the tooling already
uses; the friendly model name rides along as metadata. Phones default to a 6h max hold
(`AGISTRY_ADB_MAX_HOLD`) since a full e2e suite can run for hours — the cap is a backstop
against an agent that is alive but has wandered off, not a normal expiry.

Copy this shape for anything else that is exclusive and host-attached: GPUs, serial
rigs, licence dongles. Things with nobody to announce them — a staging URL, an API quota
— belong in the server's `REGISTRY_RESOURCES_FILE` seed instead.
