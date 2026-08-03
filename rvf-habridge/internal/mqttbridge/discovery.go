package mqttbridge

import (
	"encoding/json"
	"fmt"

	"github.com/lsochanowski/RotensoRVF-HA-Bridge/rvf-habridge/internal/rvf"
)

// device is the HA MQTT discovery device block.
type device struct {
	Identifiers  []string `json:"identifiers"`
	Name         string   `json:"name"`
	Manufacturer string   `json:"manufacturer,omitempty"`
	Model        string   `json:"model,omitempty"`
	ViaDevice    string   `json:"via_device,omitempty"`
	SWVersion    string   `json:"sw_version,omitempty"`
}

// availability entry for HA discovery.
type availability struct {
	Topic string `json:"topic"`
}

func (b *Bridge) bridgeDevice() device {
	return device{
		Identifiers:  []string{"rvf_bridge"},
		Name:         "RVF Modbus Bridge",
		Manufacturer: "Rotenso",
		Model:        "Modbus Box SP-D168",
		SWVersion:    b.version,
	}
}

func (b *Bridge) iduDevice(id int, s *rvf.IDUStatus) device {
	name := b.iduName(id)
	model := "RVF indoor unit"
	if s != nil {
		model = fmt.Sprintf("RVF %s %.1f HP", s.UnitType, s.CapacityHP)
	}
	return device{
		Identifiers:  []string{fmt.Sprintf("rvf_idu_%d", id)},
		Name:         name,
		Manufacturer: "Rotenso",
		Model:        model,
		ViaDevice:    "rvf_bridge",
	}
}

func (b *Bridge) oduDevice(id int) device {
	return device{
		Identifiers:  []string{fmt.Sprintf("rvf_odu_%d", id)},
		Name:         fmt.Sprintf("RVF Outdoor Unit %d", id),
		Manufacturer: "Rotenso",
		Model:        "RVF outdoor unit",
		ViaDevice:    "rvf_bridge",
	}
}

// both bridge-online and per-device availability must be "online".
func (b *Bridge) iduAvailability(id int) []availability {
	return []availability{
		{Topic: b.topicBridgeStatus()},
		{Topic: b.topicIDUAvail(id)},
	}
}

// publishDiscovery pushes retained discovery configs for one IDU.
func (b *Bridge) publishIDUDiscovery(id int, s *rvf.IDUStatus) {
	dev := b.iduDevice(id, s)
	avail := b.iduAvailability(id)
	state := b.topicIDUState(id)
	uid := func(kind string) string { return fmt.Sprintf("rvf_idu_%d_%s", id, kind) }

	// Climate entity.
	climate := map[string]any{
		"unique_id":           uid("climate"),
		"name":                nil, // use device name
		"device":              dev,
		"availability":        avail,
		"availability_mode":   "all",
		"modes":               []string{"off", "cool", "heat", "dry", "fan_only", "auto"},
		"mode_state_topic":    state,
		"mode_state_template": `{% if not value_json.power %}off{% elif value_json.mode in ['cool','heat','dry','fan_only','auto'] %}{{ value_json.mode }}{% else %}auto{% endif %}`,
		"mode_command_topic":  b.topicIDUCmd(id, "mode"),

		"current_temperature_topic":    state,
		"current_temperature_template": "{{ value_json.return_air_temp }}",

		"temperature_state_topic":    state,
		"temperature_state_template": "{{ value_json.setpoint }}",
		"temperature_command_topic":  b.topicIDUCmd(id, "temp"),
		"min_temp":                   16,
		"max_temp":                   32,
		"temp_step":                  1,
		"precision":                  1.0,

		"fan_modes":              []string{"auto", "low", "medium", "high"},
		"fan_mode_state_topic":   state,
		"fan_mode_state_template": `{% if value_json.fan_set in ['low','medium','high'] %}{{ value_json.fan_set }}{% else %}auto{% endif %}`,
		"fan_mode_command_topic": b.topicIDUCmd(id, "fan"),

		"swing_modes":              []string{"on", "off"},
		"swing_mode_state_topic":   state,
		"swing_mode_state_template": `{% if value_json.v_swing == 6 %}on{% else %}off{% endif %}`,
		"swing_mode_command_topic": b.topicIDUCmd(id, "swing"),

		"action_topic":    state,
		"action_template": `{% if not value_json.power %}off{% elif value_json.fan_actual == 'off' %}idle{% elif value_json.mode == 'cool' %}cooling{% elif value_json.mode == 'heat' %}heating{% elif value_json.mode == 'dry' %}drying{% else %}fan{% endif %}`,
	}
	b.publishConfig("climate", uid("climate"), climate)

	// Diagnostic sensors sharing the state topic.
	type sensorDef struct {
		kind     string
		name     string
		tpl      string
		devClass string
		unit     string
		icon     string
	}
	sensors := []sensorDef{
		{"return_temp", "Return air temperature", "{{ value_json.return_air_temp }}", "temperature", "°C", ""},
		{"evap_inlet_temp", "Evaporator inlet temperature", "{{ value_json.evap_inlet_temp }}", "temperature", "°C", ""},
		{"evap_mid_temp", "Evaporator mid temperature", "{{ value_json.evap_mid_temp }}", "temperature", "°C", ""},
		{"exv", "EXV opening", "{{ value_json.exv_opening }}", "", "", "mdi:valve"},
		{"power_demand", "Power demand", "{{ value_json.power_demand }}", "", "", "mdi:lightning-bolt"},
		{"fan_actual", "Fan speed actual", "{{ value_json.fan_actual }}", "", "", "mdi:fan"},
		{"error", "Error", `{% if value_json.error %}{{ value_json.error }}{% else %}OK{% endif %}`, "", "", "mdi:alert-circle-outline"},
	}
	for _, sd := range sensors {
		cfg := map[string]any{
			"unique_id":         uid(sd.kind),
			"name":              sd.name,
			"device":            dev,
			"availability":      avail,
			"availability_mode": "all",
			"state_topic":       state,
			"value_template":    sd.tpl,
			"entity_category":   "diagnostic",
		}
		if sd.devClass != "" {
			cfg["device_class"] = sd.devClass
			cfg["state_class"] = "measurement"
		}
		if sd.unit != "" {
			cfg["unit_of_measurement"] = sd.unit
		}
		if sd.icon != "" {
			cfg["icon"] = sd.icon
		}
		b.publishConfig("sensor", uid(sd.kind), cfg)
	}

	binaries := []struct {
		kind, name, tpl, devClass string
	}{
		{"heater", "Electric heater", `{{ 'ON' if value_json.heater_on else 'OFF' }}`, "running"},
		{"pump", "Water pump", `{{ 'ON' if value_json.water_pump_on else 'OFF' }}`, "running"},
		{"problem", "Problem", `{{ 'ON' if value_json.error else 'OFF' }}`, "problem"},
	}
	for _, bd := range binaries {
		b.publishConfig("binary_sensor", uid(bd.kind), map[string]any{
			"unique_id":         uid(bd.kind),
			"name":              bd.name,
			"device":            dev,
			"availability":      avail,
			"availability_mode": "all",
			"state_topic":       state,
			"value_template":    bd.tpl,
			"device_class":      bd.devClass,
			"entity_category":   "diagnostic",
		})
	}
}

// publishODUDiscovery pushes retained discovery configs for one ODU.
func (b *Bridge) publishODUDiscovery(id int) {
	dev := b.oduDevice(id)
	state := b.topicODUState(id)
	avail := []availability{
		{Topic: b.topicBridgeStatus()},
		{Topic: b.topicODUAvail(id)},
	}
	uid := func(kind string) string { return fmt.Sprintf("rvf_odu_%d_%s", id, kind) }

	sensors := []struct {
		kind, name, tpl, devClass, unit, icon string
	}{
		{"ambient", "Ambient temperature", "{{ value_json.ambient_temp }}", "temperature", "°C", ""},
		{"high_pressure", "High pressure", "{{ value_json.high_pressure_mpa }}", "pressure", "MPa", ""},
		{"discharge_a1", "Discharge temperature A1", "{{ value_json.discharge_temp_a1 }}", "temperature", "°C", ""},
		{"discharge_a2", "Discharge temperature A2", "{{ value_json.discharge_temp_a2 }}", "temperature", "°C", ""},
		{"comp1_speed", "Compressor 1 speed", "{{ value_json.comp1_speed_rps }}", "", "rps", "mdi:engine"},
		{"comp2_speed", "Compressor 2 speed", "{{ value_json.comp2_speed_rps }}", "", "rps", "mdi:engine"},
		{"fan", "Fan speed", "{{ value_json.fan_speed }}", "", "", "mdi:fan"},
		{"exv1", "EXV1 opening", "{{ value_json.exv1_opening }}", "", "", "mdi:valve"},
		{"exv2", "EXV2 opening", "{{ value_json.exv2_opening }}", "", "", "mdi:valve"},
		{"error", "Error", `{% if value_json.error %}{{ value_json.error }}{% else %}OK{% endif %}`, "", "", "mdi:alert-circle-outline"},
		{"protection", "Protection", `{% if value_json.protection %}{{ value_json.protection }}{% else %}OK{% endif %}`, "", "", "mdi:shield-alert-outline"},
	}
	for _, sd := range sensors {
		cfg := map[string]any{
			"unique_id":         uid(sd.kind),
			"name":              sd.name,
			"device":            dev,
			"availability":      avail,
			"availability_mode": "all",
			"state_topic":       state,
			"value_template":    sd.tpl,
			"entity_category":   "diagnostic",
		}
		if sd.devClass != "" {
			cfg["device_class"] = sd.devClass
			cfg["state_class"] = "measurement"
		}
		if sd.unit != "" {
			cfg["unit_of_measurement"] = sd.unit
		}
		if sd.icon != "" {
			cfg["icon"] = sd.icon
		}
		b.publishConfig("sensor", uid(sd.kind), cfg)
	}
}

// publishBridgeDiscovery pushes bridge-level entities (all-off button,
// system mode sensor).
func (b *Bridge) publishBridgeDiscovery() {
	dev := b.bridgeDevice()
	avail := []availability{{Topic: b.topicBridgeStatus()}}

	b.publishConfig("button", "rvf_bridge_all_off", map[string]any{
		"unique_id":     "rvf_bridge_all_off",
		"name":          "All units off",
		"device":        dev,
		"availability":  avail,
		"command_topic": b.topicAllCmd(),
		"payload_press": "off",
		"icon":          "mdi:power-plug-off",
	})
	b.publishConfig("sensor", "rvf_bridge_system_mode", map[string]any{
		"unique_id":       "rvf_bridge_system_mode",
		"name":            "System mode",
		"device":          dev,
		"availability":    avail,
		"state_topic":     b.topicSystemState(),
		"value_template":  "{{ value_json.system_mode }}",
		"entity_category": "diagnostic",
		"icon":            "mdi:hvac",
	})
	b.publishConfig("sensor", "rvf_bridge_online_idus", map[string]any{
		"unique_id":       "rvf_bridge_online_idus",
		"name":            "Online indoor units",
		"device":          dev,
		"availability":    avail,
		"state_topic":     b.topicSystemState(),
		"value_template":  "{{ value_json.online_count }}",
		"state_class":     "measurement",
		"entity_category": "diagnostic",
		"icon":            "mdi:counter",
	})
}

func (b *Bridge) publishConfig(component, uniqueID string, cfg map[string]any) {
	payload, err := json.Marshal(cfg)
	if err != nil {
		b.logf("discovery marshal %s: %v", uniqueID, err)
		return
	}
	topic := fmt.Sprintf("%s/%s/%s/config", b.cfg.MQTT.DiscoveryPrefix, component, uniqueID)
	b.mq.Publish(topic, 1, true, payload)
}
