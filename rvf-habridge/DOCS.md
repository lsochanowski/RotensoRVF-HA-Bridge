# Rotenso RVF Bridge

Exposes a Rotenso RVF VRF system (via the Modbus Box SP-D168) to Home
Assistant: a `climate` entity per indoor unit plus diagnostic sensors,
using MQTT discovery. Requires a running MQTT broker (the official
Mosquitto add-on is picked up automatically).

## Connection

Set `connection_url` to how the Modbus Box is reachable:

| Setup | Example |
|---|---|
| Serial server in transparent mode (PUSR USR-DR164 and similar) | `rtutcp://192.168.1.50:8899` |
| Serial server in Modbus TCP gateway mode | `tcp://192.168.1.50:502` |
| RS-485 USB converter plugged into the HA host | `rtu:///dev/serial/by-id/usb-FTDI_...-port0` |

`unit_id` is the Modbus slave address set with the SW1/SW2 switches on
the Modbus Box. **SW1=0 means the box is disabled and will not respond**
— set it to 1.

Only one Modbus client may talk to the box at a time. Stop the add-on
before using other tools on the same serial server.

## Options

```yaml
connection_url: "rtutcp://192.168.1.50:8899"
unit_id: 1
timeout: "3s"          # per-request timeout
poll_interval: "10s"   # state refresh period
odu_count: 1           # outdoor units (1-4)
odu_entities: true     # create outdoor unit sensors
idus:                  # room names; ids are 0-63
  - id: 0
    name: Living room
  - id: 1
    name: Bedroom
```

Unnamed units appear as "RVF IDU n". To find out which id is which
room, change something on one unit with its remote and watch which
entity reacts.

## Entities

- `climate.<room>` — power/mode/setpoint/fan/vertical swing
- per-unit diagnostic sensors: return & evaporator temperatures, EXV
  opening, power demand, actual fan speed, error code; binary sensors
  for electric heater, water pump and problem state
- outdoor unit: ambient, high pressure, discharge temperatures,
  compressor speeds, fan, EXV, error/protection codes (unavailable when
  the Modbus Box reports no communication with the outdoor unit)
- bridge: system mode, online unit count, an **All units off** button

Units that drop off the bus are marked unavailable automatically.
