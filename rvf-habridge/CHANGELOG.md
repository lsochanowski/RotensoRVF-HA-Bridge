# Changelog

## 0.2.2

- Verbose startup logging with timestamps: add-on mode detection,
  parsed options, Modbus connection, Supervisor MQTT discovery steps
  (token presence, API response) and online-unit changes.
- Fall back to the legacy `HASSIO_TOKEN` env var when
  `SUPERVISOR_TOKEN` is absent.


## 0.2.1

- Fix MQTT auto-discovery from the Supervisor: enable `hassio_api` so
  `SUPERVISOR_TOKEN` is injected into the container. Without it the
  add-on exited with "no MQTT broker".

## 0.2.0

- First add-on release: MQTT bridge with Home Assistant discovery
  (climate + diagnostic sensors per indoor unit, outdoor unit sensors,
  availability tracking, group all-off button).
