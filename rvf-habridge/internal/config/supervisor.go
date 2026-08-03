package config

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"
)

// FillMQTTFromSupervisor queries the Home Assistant Supervisor for the
// broker credentials provided by the Mosquitto add-on (services API).
// It is a no-op unless running inside an add-on (SUPERVISOR_TOKEN set)
// and no broker is configured explicitly. This is what lets the add-on
// run with zero MQTT configuration.
func (c *Config) FillMQTTFromSupervisor() error {
	if c.MQTT.Broker != "" {
		return nil
	}
	token := os.Getenv("SUPERVISOR_TOKEN")
	if token == "" {
		return nil
	}

	req, err := http.NewRequest("GET", "http://supervisor/services/mqtt", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("supervisor services/mqtt: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("supervisor services/mqtt: HTTP %d (is the Mosquitto add-on installed?)", resp.StatusCode)
	}

	var out struct {
		Data struct {
			Host     string `json:"host"`
			Port     int    `json:"port"`
			Username string `json:"username"`
			Password string `json:"password"`
			SSL      bool   `json:"ssl"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return fmt.Errorf("supervisor services/mqtt: %w", err)
	}
	scheme := "tcp"
	if out.Data.SSL {
		scheme = "ssl"
	}
	c.MQTT.Broker = fmt.Sprintf("%s://%s:%d", scheme, out.Data.Host, out.Data.Port)
	c.MQTT.Username = out.Data.Username
	c.MQTT.Password = out.Data.Password
	return nil
}
