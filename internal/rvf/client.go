package rvf

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/simonvetter/modbus"
)

// maxRegsPerRead keeps block reads within the Modbus limit of 125
// registers per request, aligned to whole IDU input blocks (12*9=108).
const maxRegsPerRead = 108

// ConnConfig describes how to reach the Modbus Box: either directly
// over RS-485 (url "rtu:///dev/tty...") or through an RTU<->TCP gateway
// such as the PUSR USR-DR164 (url "tcp://host:502").
type ConnConfig struct {
	URL     string        // tcp://host:port or rtu:///dev/ttyUSB0
	UnitID  uint8         // Modbus slave address (SW1/SW2 on the box)
	Timeout time.Duration // per-request timeout
	// Serial parameters, used only for rtu:// URLs. The Modbus Box
	// talks 9600 8N1 by default (parity configurable via reg 42001).
	Speed    uint
	Parity   string // "none", "even", "odd"
	StopBits uint
}

// Client wraps a Modbus connection with the RVF register map. All
// methods are safe for concurrent use; requests are serialized because
// the RS-485 side of the gateway is half-duplex.
type Client struct {
	mc *modbus.ModbusClient
	mu sync.Mutex
}

// Dial opens the Modbus connection.
func Dial(cfg ConnConfig) (*Client, error) {
	if cfg.Timeout == 0 {
		cfg.Timeout = 2 * time.Second
	}
	if cfg.Speed == 0 {
		cfg.Speed = 9600
	}
	parity := modbus.PARITY_NONE
	switch cfg.Parity {
	case "", "none":
	case "even":
		parity = modbus.PARITY_EVEN
	case "odd":
		parity = modbus.PARITY_ODD
	default:
		return nil, fmt.Errorf("invalid parity %q", cfg.Parity)
	}
	stopBits := cfg.StopBits
	if stopBits == 0 {
		stopBits = 1
	}
	mc, err := modbus.NewClient(&modbus.ClientConfiguration{
		URL:      cfg.URL,
		Timeout:  cfg.Timeout,
		Speed:    cfg.Speed,
		DataBits: 8,
		Parity:   parity,
		StopBits: stopBits,
	})
	if err != nil {
		return nil, fmt.Errorf("modbus client: %w", err)
	}
	if err := mc.Open(); err != nil {
		return nil, fmt.Errorf("connect %s: %w", cfg.URL, err)
	}
	if cfg.UnitID != 0 {
		if err := mc.SetUnitId(cfg.UnitID); err != nil {
			mc.Close()
			return nil, err
		}
	}
	return &Client{mc: mc}, nil
}

// Close closes the underlying connection.
func (c *Client) Close() error { return c.mc.Close() }

func (c *Client) read(addr uint16, count uint16, regType modbus.RegType) ([]uint16, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.mc.ReadRegisters(addr, count, regType)
}

// readChunked reads a span of registers in <=maxRegsPerRead pieces.
func (c *Client) readChunked(addr uint16, count uint16, regType modbus.RegType) ([]uint16, error) {
	out := make([]uint16, 0, count)
	for count > 0 {
		n := count
		if n > maxRegsPerRead {
			n = maxRegsPerRead
		}
		regs, err := c.read(addr, n, regType)
		if err != nil {
			return nil, fmt.Errorf("read %d regs @ %d: %w", n, addr, err)
		}
		out = append(out, regs...)
		addr += n
		count -= n
	}
	return out, nil
}

// ReadInputs reads raw input registers (function 0x04).
func (c *Client) ReadInputs(addr, count uint16) ([]uint16, error) {
	return c.readChunked(addr, count, modbus.INPUT_REGISTER)
}

// ReadHoldings reads raw holding registers (function 0x03).
func (c *Client) ReadHoldings(addr, count uint16) ([]uint16, error) {
	return c.readChunked(addr, count, modbus.HOLDING_REGISTER)
}

// WriteRegister writes one holding register (function 0x06).
func (c *Client) WriteRegister(addr, value uint16) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.mc.WriteRegister(addr, value)
}

// WriteRegisters writes multiple holding registers (function 0x10).
// The Modbus Box accepts at most 10 registers per write.
func (c *Client) WriteRegisters(addr uint16, values []uint16) error {
	if len(values) > 10 {
		return fmt.Errorf("modbus box accepts max 10 registers per write, got %d", len(values))
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.mc.WriteRegisters(addr, values)
}

// OnlineIDUs reads the system online bitmap and returns the flags plus
// the sorted list of online IDU ids.
func (c *Client) OnlineIDUs() ([MaxIDUs]bool, []int, error) {
	regs, err := c.ReadInputs(AddrOnlineBitmap, 4)
	if err != nil {
		return [MaxIDUs]bool{}, nil, fmt.Errorf("online bitmap: %w", err)
	}
	online := DecodeOnlineBitmap(regs)
	var ids []int
	for id, on := range online {
		if on {
			ids = append(ids, id)
		}
	}
	return online, ids, nil
}

// SystemMode reads input register 2560 and returns the decoded mode.
func (c *Client) SystemMode() (string, uint16, error) {
	regs, err := c.ReadInputs(AddrSystemMode, 1)
	if err != nil {
		return "", 0, fmt.Errorf("system mode: %w", err)
	}
	return SystemModeName(regs[0]), regs[0], nil
}

// ReadIDUs reads and decodes input + holding registers for the given
// IDU ids using block reads over the covering span.
func (c *Client) ReadIDUs(ids []int) (map[int]*IDUStatus, error) {
	if len(ids) == 0 {
		return map[int]*IDUStatus{}, nil
	}
	sorted := append([]int(nil), ids...)
	sort.Ints(sorted)
	lo, hi := sorted[0], sorted[len(sorted)-1]
	if lo < 0 || hi >= MaxIDUs {
		return nil, fmt.Errorf("IDU id out of range 0-%d", MaxIDUs-1)
	}

	inputs, err := c.ReadInputs(uint16(lo*IDUInputStride),
		uint16((hi-lo+1)*IDUInputStride))
	if err != nil {
		return nil, fmt.Errorf("IDU inputs: %w", err)
	}
	holdings, err := c.ReadHoldings(uint16(lo*IDUHoldingStride),
		uint16((hi-lo+1)*IDUHoldingStride))
	if err != nil {
		return nil, fmt.Errorf("IDU holdings: %w", err)
	}

	out := make(map[int]*IDUStatus, len(sorted))
	for _, id := range sorted {
		s := &IDUStatus{ID: id, Online: true}
		off := (id - lo) * IDUInputStride
		DecodeIDUInputs(s, inputs[off:off+IDUInputStride])
		hoff := (id - lo) * IDUHoldingStride
		DecodeIDUHoldings(s, holdings[hoff:hoff+IDUHoldingStride])
		out[id] = s
	}
	return out, nil
}

// ReadODU reads and decodes the register block of one outdoor unit.
func (c *Client) ReadODU(id int) (ODUStatus, error) {
	if id < 0 || id >= MaxODUs {
		return ODUStatus{}, fmt.Errorf("ODU id out of range 0-%d", MaxODUs-1)
	}
	regs, err := c.ReadInputs(uint16(ODUInputBase+id*ODUInputStride), ODUReadCount)
	if err != nil {
		return ODUStatus{}, fmt.Errorf("ODU %d: %w", id, err)
	}
	return DecodeODU(id, regs), nil
}

// SetIDU applies a partial change to one IDU via read-modify-write of
// its two packed holding registers. Only touched words are written.
func (c *Client) SetIDU(id int, req SetRequest) error {
	if id < 0 || id >= MaxIDUs {
		return fmt.Errorf("IDU id out of range 0-%d", MaxIDUs-1)
	}
	return c.rmw(uint16(id*IDUHoldingStride), req)
}

// SetGroup applies a change to all IDUs at once via the group control
// registers (43841/43842). Note: group writes have no meaningful
// read-back, so the current group words are read and merged the same
// way as per-IDU words.
func (c *Client) SetGroup(req SetRequest) error {
	return c.rmw(AddrGroupControl, req)
}

func (c *Client) rmw(base uint16, req SetRequest) error {
	cur, err := c.ReadHoldings(base, 2)
	if err != nil {
		return fmt.Errorf("read-modify-write read @ %d: %w", base, err)
	}
	newControl, newLocks := cur[0], cur[1]
	if req.touchesControl() {
		if newControl, err = req.applyControl(cur[0]); err != nil {
			return err
		}
	}
	if req.touchesLocks() {
		if newLocks, err = req.applyLocks(cur[1]); err != nil {
			return err
		}
	}
	switch {
	case newControl != cur[0] && newLocks != cur[1]:
		return c.WriteRegisters(base, []uint16{newControl, newLocks})
	case newControl != cur[0]:
		return c.WriteRegister(base, newControl)
	case newLocks != cur[1]:
		return c.WriteRegister(base+1, newLocks)
	}
	return nil // no-op: requested state already active
}
