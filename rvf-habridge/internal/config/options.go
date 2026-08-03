package config

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// AddonOptionsPath is where the HA Supervisor mounts add-on options.
const AddonOptionsPath = "/data/options.json"

// addonOptions mirrors the schema in the add-on's config.yaml.
type addonOptions struct {
	ConnectionURL string `json:"connection_url"`
	UnitID        uint8  `json:"unit_id"`
	Timeout       string `json:"timeout"`
	PollInterval  string `json:"poll_interval"`
	ODUCount      int    `json:"odu_count"`
	ODUEntities   *bool  `json:"odu_entities"`
	IDUs          []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	} `json:"idus"`
}

// RunningAsAddon reports whether the process runs inside a Home
// Assistant add-on (Supervisor mounts the options file).
func RunningAsAddon() bool {
	_, err := os.Stat(AddonOptionsPath)
	return err == nil
}

// LoadAddon builds a Config from the Supervisor-provided options file.
// MQTT credentials are left empty — FillMQTTFromSupervisor provides
// them via the services API.
func LoadAddon(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var o addonOptions
	if err := json.Unmarshal(data, &o); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	cfg := Config{ODUCount: o.ODUCount}
	cfg.Connection.URL = o.ConnectionURL
	cfg.Connection.UnitID = o.UnitID
	cfg.Bridge.ODUEntities = o.ODUEntities == nil || *o.ODUEntities
	if o.Timeout != "" {
		d, err := time.ParseDuration(o.Timeout)
		if err != nil {
			return nil, fmt.Errorf("timeout: %w", err)
		}
		cfg.Connection.Timeout = d
	}
	if o.PollInterval != "" {
		d, err := time.ParseDuration(o.PollInterval)
		if err != nil {
			return nil, fmt.Errorf("poll_interval: %w", err)
		}
		cfg.Bridge.PollInterval = d
	}
	if cfg.ODUCount == 0 {
		cfg.ODUCount = 1
	}
	if len(o.IDUs) > 0 {
		cfg.IDUs = map[int]string{}
		for _, u := range o.IDUs {
			if u.ID < 0 || u.ID > 63 {
				return nil, fmt.Errorf("idus: id %d out of range 0-63", u.ID)
			}
			cfg.IDUs[u.ID] = u.Name
		}
	}
	cfg.applyDefaults()
	return &cfg, nil
}
