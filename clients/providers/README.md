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
| `agistry-adb-provider.sh` | Android devices reachable via `adb devices -l` on this host |

```bash
# one pass
./agistry-adb-provider.sh

# keep refreshing (systemd timer, cron, or a plain loop)
./agistry-adb-provider.sh --loop 300
```

Devices are keyed by adb serial (`adb:R5CT21…`) because that is what the tooling already
uses; the friendly model name rides along as metadata. Phones default to a 6h max hold
(`AGISTRY_ADB_MAX_HOLD`) since a full e2e suite can run for hours — the cap is a backstop
against an agent that is alive but has wandered off, not a normal expiry.

Copy this shape for anything else that is exclusive and host-attached: GPUs, serial
rigs, licence dongles. Things with nobody to announce them — a staging URL, an API quota
— belong in the server's `REGISTRY_RESOURCES_FILE` seed instead.
