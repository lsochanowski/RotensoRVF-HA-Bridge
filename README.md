# Rotenso RVF — Home Assistant Bridge

Home Assistant integration for **Rotenso RVF / miniRVF** VRF air
conditioning systems equipped with the **Modbus Box** (SP-D168, code
810055200026). Written in Go, no Python dependencies, talks Modbus RTU
to the box and MQTT (with discovery) to Home Assistant.

> Rotenso RVF is built on the Midea V4/V6 VRF platform — the register
> map may match other brands shipping the same Modbus gateway. Use at
> your own risk; this is not an official Rotenso product.

## What it does

- **`climate` entity per indoor unit** — on/off, cool/heat/dry/fan_only/auto,
  target temperature (16–32 °C), fan speed, vertical swing, hvac action,
  current room temperature (return air sensor).
- **Diagnostic sensors per indoor unit** — return / evaporator-inlet /
  evaporator-mid temperatures, EXV opening, power demand, actual fan
  speed, error codes E0–EF decoded to human-readable text, binary
  sensors for electric heater, water pump and problem state.
- **Outdoor unit diagnostics** (when the Modbus Box communicates with
  the ODU) — ambient temperature, high pressure, discharge temperatures,
  compressor speeds, fan, EXV openings, protection/error codes P0–PF/E0–EF.
- **Bridge device** — system mode, online unit count, an **All units
  off** button (group broadcast register).
- Availability tracking: units that drop off the bus go *unavailable*
  in HA (per-unit online bitmap + bridge LWT).
- Correct **read-modify-write** on the bit-packed control registers —
  changing the setpoint never clobbers mode/fan/power. Optimistic state
  publish for a snappy UI, verified re-read a few seconds later.
- Standalone **CLI** (`scan`, `status`, `watch`, `set`, `all`, `raw`,
  `odu`) and a **Modbus Box simulator** for development without hardware.

## Tested hardware

Field-tested end to end (August 2026) on:

| Element | Model | Notes |
|---|---|---|
| Outdoor unit | Rotenso miniRVF **RVF-224V4OMI3** (V4, R11) | see [ODU status caveat](#odu-status-caveat) |
| Indoor units | Enos **RVF-56V5IWM**, 2× Enos **RVF-36V5IWM** (wall), Tenji CS **RVF-56V4ICS** (cassette), **RVF-90V4IFC** (floor-ceiling) | full control + telemetry |
| Modbus gateway | **Modbus Box SP-D168** (810055200026) | slave address via SW1/SW2 — **SW1=0 disables the box** |
| Serial server | PUSR **USR-DR164** (WiFi, fw V1.0.15) | transparent mode, `rtutcp://` |
| Host | Home Assistant OS / any box running the binary | |

### Hardware quirks discovered along the way

- **USR-DR164**: the *Modbus gateway (Protocol Conversion)* setting does
  **not survive a reboot** on fw V1.0.15 — run it in transparent mode and
  use this bridge's native `rtutcp://` transport instead. The device also
  swallows the first frame on every fresh TCP connection (the transport
  sends a warm-up request to absorb that).
- **Only one Modbus client at a time**: in transparent mode the serial
  server relays every TCP client onto the same half-duplex RS-485 bus —
  run the bridge *or* the CLI, never both.
- **The Modbus Box applies writes with a delay** (~3–5 s): a read-back
  right after a write returns the old values.
- **Units reject `auto` fan in fan_only mode** (the field is silently
  cleared and the fan never starts) — the bridge defaults to medium there.
- When powering a unit on, mode + temperature + fan speed must all be
  valid in one write (per the service manual) — the bridge fills in
  defaults automatically.

## Home Assistant add-on install

This repository is a Home Assistant add-on repository:

1. Settings → Add-ons → Add-on Store → ⋮ → **Repositories** → add
   `https://github.com/lsochanowski/RotensoRVF-HA-Bridge`.
2. Install **Rotenso RVF Bridge**, set `connection_url` and room names
   in the configuration tab, start it.
3. With the Mosquitto add-on running, MQTT credentials are picked up
   automatically from the Supervisor. Entities appear via MQTT discovery.

Full option reference: [rvf-habridge/DOCS.md](rvf-habridge/DOCS.md).

## Standalone usage (CLI)

The Go module lives in [rvf-habridge/](rvf-habridge/):

```bash
cd rvf-habridge
make build

bin/rvf-habridge scan   --conn rtutcp://192.168.1.50:8899   # who's on the bus?
bin/rvf-habridge status                                     # full decoded state
bin/rvf-habridge watch                                      # live diff — map ids to rooms
bin/rvf-habridge set --idu 3 --power on --mode cool --temp 24 --fan auto
bin/rvf-habridge all --power off                            # broadcast
bin/rvf-habridge raw read-input --addr 2572 --count 4       # online bitmap
bin/rvf-habridge bridge --broker tcp://127.0.0.1:1883       # MQTT bridge
```

Connection transports:

| Transport | URL | When |
|---|---|---|
| RTU over TCP | `rtutcp://host:8899` | serial server in transparent mode (recommended for USR-DR164) |
| Modbus TCP | `tcp://host:502` | serial server in Modbus gateway (MBAP) mode |
| Modbus RTU | `rtu:///dev/serial/by-id/...` | direct RS-485 converter, 9600 8N1 |

Config file (`rvf-habridge.yaml`, see the example): connection, IDU
id → room-name map, MQTT broker.

### Mapping unit ids to rooms

```bash
bin/rvf-habridge watch
# poke a unit with its IR remote; the id that reacts is that room:
# 12:03:41 IDU 3: setpoint 23→24°C
```

### Simulator (no hardware needed)

```bash
bin/rvf-simulator --listen tcp://127.0.0.1:5502 --idus 5 &
bin/rvf-habridge scan --conn tcp://127.0.0.1:5502
```

## Register map

Implemented 1:1 from the service manual (see [docs/](docs/)):

| Registers (protocol addr) | Function | Content |
|---|---|---|
| `idu*9 + 0..8` | 0x04 | IDU monitoring: T1/T2A/T2B temps, capacity, power demand, fan/heater/pump/EXV word, error code, model word |
| `idu*2 + 0..1` | 0x03/06/10 | IDU control: power/mode/fan/setpoint word, locks/louver word |
| `2000` | 0x03/06 | RS-485 parity selection |
| `2560` | 0x04 | system mode (off/cooling/heating/offline) |
| `2572..2575` | 0x04 | online bitmap, IDU 0–63 |
| `odu*49 + 2048..2088` | 0x04 | ODU: protection/error, high pressure, ambient & discharge temps, compressor speeds, EXV, type, capacity |
| `3840..3841` | 0x03/06/10 | group (broadcast) control of all IDUs |

Constraints honoured: max 10 registers per 0x10 write, mode+temp+fan
required together at power-on, ~1 write/s pacing.

### ODU status caveat

On the tested miniRVF V4 the box relays all indoor-unit data but reports
no outdoor-unit status block (registers all zero, box LED blinking 2×/8 s
= "cannot find outdoor unit"). Indoor control and telemetry are fully
functional; ODU sensors stay *unavailable* in HA until that is resolved.
If your ODU answers, the entities light up automatically.

## Documentation

Vendor documentation used to build this (in [docs/](docs/)): Modbus Box
BMS service manual (register map, PL), miniRVF V4 control-system and
installation manuals (EN), commissioning manual (PL) and unit datasheets.

## License

MIT
