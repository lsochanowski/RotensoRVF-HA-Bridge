// Package mqttbridge polls the Rotenso RVF Modbus Box and exposes all
// units to Home Assistant over MQTT discovery: one climate entity plus
// diagnostic sensors per indoor unit, outdoor unit diagnostics and
// bridge-level controls. Commands are applied with read-modify-write
// and answered with an immediate state republish (optimistic UX).
package mqttbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"

	"github.com/lsochanowski/RotensoRVF-HA-Bridge/rvf-habridge/internal/config"
	"github.com/lsochanowski/RotensoRVF-HA-Bridge/rvf-habridge/internal/rvf"
)

type Bridge struct {
	cfg     *config.Config
	client  *rvf.Client
	mq      mqtt.Client
	version string

	mu         sync.Mutex
	lastIDU    map[int]*rvf.IDUStatus
	discovered map[string]bool // discovery already published for key
	lastOnline string
	oduAlive   map[int]bool
}

func New(cfg *config.Config, client *rvf.Client, version string) *Bridge {
	return &Bridge{
		cfg:        cfg,
		client:     client,
		version:    version,
		lastIDU:    map[int]*rvf.IDUStatus{},
		discovered: map[string]bool{},
		oduAlive:   map[int]bool{},
	}
}

func (b *Bridge) logf(format string, args ...any) { log.Printf(format, args...) }

// --- topics ---

func (b *Bridge) t(parts ...string) string {
	return b.cfg.MQTT.TopicPrefix + "/" + strings.Join(parts, "/")
}
func (b *Bridge) topicBridgeStatus() string     { return b.t("bridge", "status") }
func (b *Bridge) topicSystemState() string      { return b.t("bridge", "state") }
func (b *Bridge) topicAllCmd() string           { return b.t("all", "set") }
func (b *Bridge) topicIDUState(id int) string   { return b.t("idu", strconv.Itoa(id), "state") }
func (b *Bridge) topicIDUAvail(id int) string   { return b.t("idu", strconv.Itoa(id), "availability") }
func (b *Bridge) topicODUState(id int) string   { return b.t("odu", strconv.Itoa(id), "state") }
func (b *Bridge) topicODUAvail(id int) string   { return b.t("odu", strconv.Itoa(id), "availability") }
func (b *Bridge) topicIDUCmd(id int, what string) string {
	return b.t("idu", strconv.Itoa(id), "set", what)
}

func (b *Bridge) iduName(id int) string {
	if n := b.cfg.Name(id); n != "" {
		return n
	}
	return fmt.Sprintf("RVF IDU %d", id)
}

// Run connects to MQTT and polls until the context is cancelled.
func (b *Bridge) Run(ctx context.Context) error {
	opts := mqtt.NewClientOptions().
		AddBroker(b.cfg.MQTT.Broker).
		SetClientID(b.cfg.MQTT.ClientID).
		SetUsername(b.cfg.MQTT.Username).
		SetPassword(b.cfg.MQTT.Password).
		SetAutoReconnect(true).
		SetConnectRetry(true).
		SetConnectRetryInterval(5 * time.Second).
		SetWill(b.topicBridgeStatus(), "offline", 1, true).
		SetOnConnectHandler(func(c mqtt.Client) {
			b.logf("mqtt connected to %s", b.cfg.MQTT.Broker)
			c.Publish(b.topicBridgeStatus(), 1, true, "online")
			b.subscribe(c)
		})
	b.mq = mqtt.NewClient(opts)
	if tok := b.mq.Connect(); tok.Wait() && tok.Error() != nil {
		return fmt.Errorf("mqtt connect: %w", tok.Error())
	}
	defer func() {
		b.mq.Publish(b.topicBridgeStatus(), 1, true, "offline").Wait()
		b.mq.Disconnect(500)
	}()

	b.publishBridgeDiscovery()
	if b.cfg.Bridge.ODUEntities {
		for i := 0; i < b.cfg.ODUCount; i++ {
			b.publishODUDiscovery(i)
		}
	}

	ticker := time.NewTicker(b.cfg.Bridge.PollInterval)
	defer ticker.Stop()
	b.poll() // first poll immediately
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			b.poll()
		}
	}
}

func (b *Bridge) subscribe(c mqtt.Client) {
	c.Subscribe(b.t("idu", "+", "set", "+"), 1, b.handleIDUCommand)
	c.Subscribe(b.topicAllCmd(), 1, b.handleAllCommand)
}

// --- polling ---

func (b *Bridge) poll() {
	online, ids, err := b.client.OnlineIDUs()
	if err != nil {
		b.logf("poll: %v", err)
		return
	}
	if fmt.Sprint(ids) != b.lastOnline {
		b.logf("poll: %d IDU(s) online: %v", len(ids), ids)
		b.lastOnline = fmt.Sprint(ids)
	}
	mode, _, err := b.client.SystemMode()
	if err != nil {
		b.logf("poll system mode: %v", err)
		return
	}
	sys, _ := json.Marshal(map[string]any{
		"system_mode":  mode,
		"online_count": len(ids),
	})
	b.mq.Publish(b.topicSystemState(), 1, true, sys)

	statuses, err := b.client.ReadIDUs(ids)
	if err != nil {
		b.logf("poll IDUs: %v", err)
		return
	}
	for id, s := range statuses {
		s.Name = b.cfg.Name(id)
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	for id := 0; id < rvf.MaxIDUs; id++ {
		key := fmt.Sprintf("idu%d", id)
		s, ok := statuses[id]
		if !ok {
			// Publish availability offline for units that were seen before.
			if b.discovered[key] && !online[id] {
				b.mq.Publish(b.topicIDUAvail(id), 1, true, "offline")
			}
			continue
		}
		if !b.discovered[key] {
			b.publishIDUDiscovery(id, s)
			b.discovered[key] = true
		}
		b.mq.Publish(b.topicIDUAvail(id), 1, true, "online")
		b.publishIDUState(id, s)
		b.lastIDU[id] = s
	}

	if b.cfg.Bridge.ODUEntities {
		for i := 0; i < b.cfg.ODUCount; i++ {
			b.pollODU(i)
		}
	}
}

func (b *Bridge) pollODU(id int) {
	s, err := b.client.ReadODU(id)
	if err != nil {
		b.logf("poll ODU %d: %v", id, err)
		return
	}
	// An all-zero block means the box has no communication with this
	// outdoor unit — mark the device unavailable instead of showing
	// zeros as live values.
	alive := s.TypeWord != 0 || s.AmbientTemp != 0 || s.HighPressure != 0
	if alive != b.oduAlive[id] {
		b.logf("ODU %d availability: %v", id, alive)
		b.oduAlive[id] = alive
	}
	if !alive {
		b.mq.Publish(b.topicODUAvail(id), 1, true, "offline")
		return
	}
	b.mq.Publish(b.topicODUAvail(id), 1, true, "online")
	payload, _ := json.Marshal(s)
	b.mq.Publish(b.topicODUState(id), 1, true, payload)
}

func (b *Bridge) publishIDUState(id int, s *rvf.IDUStatus) {
	payload, err := json.Marshal(s)
	if err != nil {
		b.logf("marshal IDU %d: %v", id, err)
		return
	}
	b.mq.Publish(b.topicIDUState(id), 1, true, payload)
}

// --- commands ---

// handleIDUCommand processes rvf/idu/<id>/set/<what> messages.
func (b *Bridge) handleIDUCommand(_ mqtt.Client, msg mqtt.Message) {
	parts := strings.Split(msg.Topic(), "/")
	if len(parts) < 4 {
		return
	}
	id, err := strconv.Atoi(parts[len(parts)-3])
	if err != nil || id < 0 || id >= rvf.MaxIDUs {
		return
	}
	what := parts[len(parts)-1]
	payload := strings.TrimSpace(string(msg.Payload()))
	b.logf("command IDU %d: %s = %q", id, what, payload)

	req, err := b.buildRequest(id, what, payload)
	if err != nil {
		b.logf("command IDU %d rejected: %v", id, err)
		return
	}
	if err := b.client.SetIDU(id, req); err != nil {
		b.logf("command IDU %d write: %v", id, err)
		return
	}
	// The box reflects holding writes with a delay, so an immediate
	// read-back returns the pre-write state. Publish an optimistic
	// state right away for snappy UI, then verify with a real read.
	// The box needs a few seconds to apply a write to its table.
	b.publishOptimistic(id, req)
	time.AfterFunc(5*time.Second, func() { b.refreshIDU(id) })
}

// publishOptimistic merges the accepted request into the cached state
// and publishes it, so HA reflects the change instantly.
func (b *Bridge) publishOptimistic(id int, req rvf.SetRequest) {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.lastIDU[id]
	if s == nil {
		return
	}
	c := *s
	req.ApplyOptimistic(&c)
	b.lastIDU[id] = &c
	b.publishIDUState(id, &c)
}

// refreshIDU re-reads one unit and publishes the authoritative state.
func (b *Bridge) refreshIDU(id int) {
	statuses, err := b.client.ReadIDUs([]int{id})
	if err != nil {
		b.logf("refresh IDU %d: %v", id, err)
		return
	}
	if s, ok := statuses[id]; ok {
		s.Name = b.cfg.Name(id)
		b.mu.Lock()
		b.lastIDU[id] = s
		b.mu.Unlock()
		b.publishIDUState(id, s)
	}
}

// buildRequest translates an HA command into a SetRequest, filling in
// defaults (mode/fan/temp) required by the box when powering on a unit
// whose control word has empty fields.
func (b *Bridge) buildRequest(id int, what, payload string) (rvf.SetRequest, error) {
	var req rvf.SetRequest
	on := true

	b.mu.Lock()
	last := b.lastIDU[id]
	b.mu.Unlock()

	switch what {
	case "mode":
		if payload == "off" {
			off := false
			req.Power = &off
			return req, nil
		}
		m, err := rvf.ParseMode(payload)
		if err != nil {
			return req, err
		}
		req.Power = &on
		req.Mode = &m
		// Powering on requires valid fan and temp in the packed word.
		// Note: units reject auto fan in fan_only mode (they clear the
		// field and the fan never spins), so default to medium there.
		if last != nil {
			if last.FanSetRaw == 0 || (m == rvf.ModeFan && last.FanSetRaw == uint16(rvf.FanAuto)) {
				f := rvf.FanAuto
				if m == rvf.ModeFan {
					f = rvf.FanMed
				}
				req.Fan = &f
			}
			if last.Setpoint < 16 || last.Setpoint > 32 {
				t := 22
				req.Temp = &t
			}
		}
	case "temp":
		v, err := strconv.ParseFloat(payload, 64)
		if err != nil {
			return req, fmt.Errorf("bad temperature %q", payload)
		}
		t := int(v + 0.5)
		req.Temp = &t
	case "fan":
		f, err := rvf.ParseFan(payload)
		if err != nil {
			return req, err
		}
		req.Fan = &f
	case "swing":
		var v uint16
		if payload == "on" {
			v = 6
		}
		req.VSwing = &v
	default:
		return req, fmt.Errorf("unknown command %q", what)
	}
	return req, nil
}

// handleAllCommand processes the group (broadcast) topic.
func (b *Bridge) handleAllCommand(_ mqtt.Client, msg mqtt.Message) {
	payload := strings.TrimSpace(string(msg.Payload()))
	if payload != "off" {
		b.logf("all: unsupported payload %q (only \"off\")", payload)
		return
	}
	off := false
	if err := b.client.SetGroup(rvf.SetRequest{Power: &off}); err != nil {
		b.logf("all off: %v", err)
		return
	}
	b.logf("all off: group command sent")
	b.poll()
}
