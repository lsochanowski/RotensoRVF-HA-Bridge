package config

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
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
		log.Printf("mqtt: broker configured explicitly (%s), skipping supervisor discovery", c.MQTT.Broker)
		return nil
	}
	token := os.Getenv("SUPERVISOR_TOKEN")
	if token == "" {
		token = os.Getenv("HASSIO_TOKEN") // legacy name
	}
	if token == "" {
		log.Printf("mqtt: no SUPERVISOR_TOKEN/HASSIO_TOKEN in environment — not running as add-on?")
		return nil
	}
	log.Printf("mqtt: querying supervisor services API for broker credentials")

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
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("supervisor services/mqtt: HTTP %d: %s (is the Mosquitto add-on installed and started?)",
			resp.StatusCode, string(body))
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
	log.Printf("mqtt: supervisor provided broker %s (user %q)", c.MQTT.Broker, c.MQTT.Username)
	return nil
}
