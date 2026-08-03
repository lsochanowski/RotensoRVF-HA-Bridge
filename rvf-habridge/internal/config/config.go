// Package config loads the bridge configuration file (YAML).
package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// DefaultPath is looked up when --config is not given.
const DefaultPath = "rvf-habridge.yaml"

type Config struct {
	Connection Connection     `yaml:"connection"`
	IDUs       map[int]string `yaml:"idus"`      // id -> human name
	ODUCount   int            `yaml:"odu_count"` // default 1
	MQTT       MQTT           `yaml:"mqtt"`
	Bridge     Bridge         `yaml:"bridge"`
}

type MQTT struct {
	Broker          string `yaml:"broker"` // tcp://host:1883
	Username        string `yaml:"username"`
	Password        string `yaml:"password"`
	ClientID        string `yaml:"client_id"`        // default rvf-habridge
	TopicPrefix     string `yaml:"topic_prefix"`     // default rvf
	DiscoveryPrefix string `yaml:"discovery_prefix"` // default homeassistant
}

type Bridge struct {
	PollInterval time.Duration `yaml:"poll_interval"` // default 10s
	ODUEntities  bool          `yaml:"odu_entities"`  // default true
}

type Connection struct {
	URL      string        `yaml:"url"` // tcp://host:502 or rtu:///dev/ttyUSB0
	UnitID   uint8         `yaml:"unit_id"`
	Timeout  time.Duration `yaml:"timeout"`
	Speed    uint          `yaml:"speed"`     // rtu only, default 9600
	Parity   string        `yaml:"parity"`    // rtu only: none/even/odd
	StopBits uint          `yaml:"stop_bits"` // rtu only, default 1
}

// Load reads the config file at path. When path is empty, DefaultPath
// is tried; a missing default file yields an empty config, while an
// explicitly given path must exist.
func Load(path string) (*Config, error) {
	explicit := path != ""
	if !explicit {
		path = DefaultPath
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) && !explicit {
			cfg := Config{ODUCount: 1}
			cfg.Bridge.ODUEntities = true
			cfg.applyDefaults()
			return &cfg, nil
		}
		return nil, err
	}
	var cfg Config
	cfg.Bridge.ODUEntities = true
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if cfg.ODUCount == 0 {
		cfg.ODUCount = 1
	}
	cfg.applyDefaults()
	for id := range cfg.IDUs {
		if id < 0 || id > 63 {
			return nil, fmt.Errorf("%s: IDU id %d out of range 0-63", path, id)
		}
	}
	return &cfg, nil
}

func (c *Config) applyDefaults() {
	if c.MQTT.ClientID == "" {
		c.MQTT.ClientID = "rvf-habridge"
	}
	if c.MQTT.TopicPrefix == "" {
		c.MQTT.TopicPrefix = "rvf"
	}
	if c.MQTT.DiscoveryPrefix == "" {
		c.MQTT.DiscoveryPrefix = "homeassistant"
	}
	if c.Bridge.PollInterval == 0 {
		c.Bridge.PollInterval = 10 * time.Second
	}
}

// Name returns the configured name for an IDU id, or "" if unnamed.
func (c *Config) Name(id int) string {
	if c == nil || c.IDUs == nil {
		return ""
	}
	return c.IDUs[id]
}
