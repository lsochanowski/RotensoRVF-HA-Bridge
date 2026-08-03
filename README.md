# Rotenso RVF — Home Assistant Bridge

Bridge and test tooling for **Rotenso RVF** VRF systems equipped with the
**Modbus Box** (SP-D168, code 810055200026), based on the service manual
`RO_RVF_ALL_BMSMB3_SM_PL_20260520`.

Written in Go. Ships as a **Home Assistant add-on** (MQTT discovery)
plus a standalone CLI for bench testing: scanning the bus, mapping IDU
ids to room names, reading every register the box exposes and
exercising writes.

## Home Assistant add-on install

This repository is a Home Assistant add-on repository:

1. Settings → Add-ons → Add-on Store → ⋮ → **Repositories** →
   add this repo's GitHub URL.
2. Install **Rotenso RVF Bridge**, set `connection_url` (and room names)
   in the add-on configuration, start it.
3. With the Mosquitto add-on running, MQTT credentials are picked up
   automatically from the Supervisor; climate entities appear via MQTT
   discovery.

See [rvf-habridge/DOCS.md](rvf-habridge/DOCS.md) for options. The Go
module lives in [rvf-habridge/](rvf-habridge/) (the add-on build context);
run the CLI from there during development.

> Rotenso RVF is a rebadged Midea V6/VRF platform; the register map may
> match other brands using the same Modbus gateway. Use at your own risk.

## Features

- **Full register map decoded** — per-IDU monitoring (temperatures T1/T2A/T2B,
  capacity, power demand, actual fan speed, electric heater, water pump,
  EXV opening, error codes E0–EF, unit type), per-IDU control (power, mode,
  fan, setpoint, louvers, all lock bits), ODU diagnostics (pressures,
  discharge temps, compressor speeds, EXV, protection P0–PF / error codes),
  system mode and the online bitmap of all 64 possible IDUs.
- **Correct read-modify-write** on the bit-packed holding registers —
  change just the setpoint without clobbering mode/fan/power.
- **Block reads** — one Modbus transaction covers many units instead of
  one transaction per value.
- **Group (broadcast) control** via registers 43841/43842 (e.g. all off).
- **Simulator** — a fake Modbus Box over TCP, so everything can be
  developed and tested without touching the hardware.

## Install

```
go install ./cmd/rvf-habridge
go install ./cmd/rvf-simulator   # optional, for offline testing
```

or just `go build ./...` and use the binaries from the repo.

## Connection

Two transports are supported (flag `--conn` or `connection.url` in config):

| Transport | URL | Notes |
|---|---|---|
| Modbus TCP | `tcp://192.168.1.50:502` | serial server in *Modbus gateway* mode (MBAP) |
| RTU over TCP | `rtutcp://192.168.1.50:8899` | serial server in *transparent* mode — raw RTU frames over a TCP socket (native transport with CRC16, auto-reconnect and a warm-up request: the USR-DR164 swallows the first frame on a fresh connection) |
| Modbus RTU | `rtu:///dev/serial/by-id/usb-FTDI_...` | direct RS-485, 9600 8N1 by default |

Field-tested with a PUSR USR-DR164 in transparent mode (`rtutcp://`).
On that device the *Protocol Conversion* (Modbus gateway) setting did not
survive a reboot, so transparent + `rtutcp://` is the more reliable path.

The slave address (`--unit`, default 1) is set by the SW1/SW2 rotary
switches on the Modbus Box. Parity is configurable via holding register
42001 (0=none, 2=odd, 3=even).

## Quick start

```bash
cp rvf-habridge.example.yaml rvf-habridge.yaml   # edit connection + names

rvf-habridge scan            # who's on the bus?
rvf-habridge status          # full decoded state of every online IDU
rvf-habridge odu             # outdoor unit diagnostics
```

### Mapping IDU ids to rooms

Run the watcher, then poke each unit with its IR remote / wall controller
(change the setpoint, toggle the fan). The id that reacts is the one
you're standing next to:

```bash
rvf-habridge watch
# 12:03:41 IDU 3: setpoint 23→24°C
```

Put the names in `rvf-habridge.yaml` under `idus:` — every command shows
them from then on.

### Controlling units

```bash
rvf-habridge set --idu 3 --power on --mode cool --fan auto --temp 24
rvf-habridge set --idu 3 --temp 22                 # RMW: only the setpoint changes
rvf-habridge set --idu 3 --vswing 6 --hswing on    # louvers
rvf-habridge set --idu 3 --controller-lock on      # block the wall/IR controller
rvf-habridge all --power off                       # broadcast: everything off
```

Setpoint range is 16–32 °C (the unit itself may limit to 17–30 °C —
`status` shows the `extended_temp_range` capability bit). Per the manual,
when turning a unit on, mode + fan + temperature must all be valid; the
CLI enforces this.

### Raw register access

```bash
rvf-habridge raw read-input   --addr 0    --count 9    # IDU 0 block
rvf-habridge raw read-holding --addr 0    --count 2
rvf-habridge raw read-input   --addr 2572 --count 4    # online bitmap
rvf-habridge raw write        --addr 2000 --value 0    # parity: none
```

Addresses are raw protocol addresses (the manual's "Adres odczytu"
column), not 3xxxx/4xxxx notation.

Every command accepts `--json` for machine-readable output.

## Offline testing

```bash
rvf-simulator --listen tcp://127.0.0.1:5502 --idus 5 &
rvf-habridge scan --conn tcp://127.0.0.1:5502
```

The simulator models 5 IDUs + 1 ODU with drifting temperatures, honours
writes (including group control fan-out) and periodically raises a fake
E4 error on the last unit.

## Register map summary

| Registers | Function | Content |
|---|---|---|
| `idu*9 + 0..8` | 0x04 | IDU monitoring (temps, capacity, fan/heater/pump/EXV word, error, model word) |
| `idu*2 + 0..1` | 0x03/06/10 | IDU control (power/mode/fan/setpoint word, locks/louver word) |
| `2000` | 0x03/06 | parity selection |
| `2560` | 0x04 | system mode (off/cooling/heating/offline) |
| `2572..2575` | 0x04 | online bitmap, IDU 0–63 |
| `odu*49 + 2048..2088` | 0x04 | ODU diagnostics (protection/error, pressure, temps, compressors, EXV, type, capacity) |
| `3840..3841` | 0x03/06/10 | group (broadcast) control of all IDUs |

Constraints from the manual honoured by the client: max 10 registers per
0x10 write, ~1 request/s recommended pacing for writes, mode+temp+fan
required at power-on.

## MQTT bridge (Home Assistant)

```bash
rvf-habridge bridge                # broker from config or Supervisor
rvf-habridge bridge --broker tcp://192.168.1.10:1883
```

Publishes Home Assistant MQTT discovery for:

- **climate** entity per indoor unit (modes off/cool/heat/dry/fan_only/auto,
  fan low/medium/high/auto, vertical swing on/off, target + current
  temperature, hvac action),
- diagnostic **sensors** per IDU: return/evaporator temperatures, EXV
  opening, power demand, actual fan speed, error code (+ binary sensors:
  electric heater, water pump, problem),
- **ODU sensors** (ambient, high pressure, discharge temps, compressor
  speeds, EXV, protection/error) — marked unavailable while the box has
  no ODU communication,
- bridge device: system mode, online IDU count, **All units off** button
  (group register 3840).

Availability chains the bridge LWT with the per-unit online bitmap, so
units vanish from HA when they drop off the bus. Commands are applied
via read-modify-write; the bridge publishes an optimistic state
immediately and verifies with a real read after ~5 s (the box applies
writes to its table with a delay). Powering on fills in required
defaults (fan/temp) per the manual; `fan_only` defaults to medium fan
because units reject auto fan in that mode.

Inside a Home Assistant add-on, MQTT credentials are pulled from the
Supervisor (`http://supervisor/services/mqtt`) automatically — no
manual broker config needed.

**Single client rule:** in transparent mode the serial server relays
frames from every TCP client onto the same half-duplex RS-485 bus. Run
either the bridge *or* ad-hoc CLI commands — not both at once — or
responses will collide.

## Roadmap

- [x] `bridge` daemon: poll loop → MQTT with Home Assistant discovery
- [ ] HAOS add-on packaging (`config.yaml` + Dockerfile, Supervisor MQTT service)
- [ ] Long-run hardware validation of ODU register scaling

## License

MIT
