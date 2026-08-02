# Rotenso RVF — Home Assistant Bridge

Bridge and test tooling for **Rotenso RVF** VRF systems equipped with the
**Modbus Box** (SP-D168, code 810055200026), based on the service manual
`RO_RVF_ALL_BMSMB3_SM_PL_20260520`.

Written in Go. Current stage: **standalone CLI** for bench testing —
scanning the bus, mapping IDU ids to room names, reading every register
the box exposes and exercising writes. The Home Assistant integration
(MQTT discovery + HAOS add-on) builds on top of this core and lands next.

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
| Modbus TCP | `tcp://192.168.1.50:502` | e.g. PUSR **USR-DR164** in *Modbus gateway* mode (not transparent transmission!) |
| Modbus RTU | `rtu:///dev/serial/by-id/usb-FTDI_...` | direct RS-485, 9600 8N1 by default |

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

## Roadmap

- [ ] `bridge` daemon: poll loop → MQTT with Home Assistant discovery
      (climate + diagnostic sensors per IDU, ODU sensors, availability
      from the online bitmap, optimistic state)
- [ ] HAOS add-on packaging (`config.yaml` + Dockerfile, Supervisor MQTT service)
- [ ] Long-run hardware validation of ODU register scaling

## License

MIT
