package rvf

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"time"
)

// rtuTCP is a minimal Modbus RTU-over-TCP transport for serial servers
// running in transparent mode (e.g. PUSR USR-DR164 with ModBUS
// Enabled=OFF): raw RTU frames with CRC16 are exchanged over a plain
// TCP socket. It reconnects automatically on I/O errors.
type rtuTCP struct {
	addr    string
	unitID  uint8
	timeout time.Duration
	conn    net.Conn
}

func dialRTUTCP(addr string, unitID uint8, timeout time.Duration) (*rtuTCP, error) {
	t := &rtuTCP{addr: addr, unitID: unitID, timeout: timeout}
	if err := t.connect(); err != nil {
		return nil, err
	}
	return t, nil
}

func (t *rtuTCP) connect() error {
	conn, err := net.DialTimeout("tcp", t.addr, t.timeout)
	if err != nil {
		return fmt.Errorf("connect %s: %w", t.addr, err)
	}
	if tc, ok := conn.(*net.TCPConn); ok {
		tc.SetKeepAlive(true)
		tc.SetKeepAlivePeriod(30 * time.Second)
		tc.SetNoDelay(true)
	}
	t.conn = conn
	t.warmup()
	return nil
}

// warmup sends a throwaway read right after connecting: serial servers
// like the USR-DR164 swallow the first frame received on a fresh TCP
// connection, so we burn that slot on a harmless request.
func (t *rtuTCP) warmup() {
	frame := []byte{t.unitID, 0x04, 0x00, 0x00, 0x00, 0x01}
	c := crc16(frame)
	frame = append(frame, byte(c&0xFF), byte(c>>8))
	t.conn.SetWriteDeadline(time.Now().Add(t.timeout))
	if _, err := t.conn.Write(frame); err != nil {
		return
	}
	t.conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	buf := make([]byte, 64)
	t.conn.Read(buf) // response or timeout — either is fine
}

func (t *rtuTCP) Close() error {
	if t.conn != nil {
		return t.conn.Close()
	}
	return nil
}

func crc16(data []byte) uint16 {
	crc := uint16(0xFFFF)
	for _, b := range data {
		crc ^= uint16(b)
		for i := 0; i < 8; i++ {
			if crc&1 != 0 {
				crc = crc>>1 ^ 0xA001
			} else {
				crc >>= 1
			}
		}
	}
	return crc
}

// roundTrip sends one PDU and returns the response PDU (without unit id
// and CRC). One retry with a fresh connection on transport errors.
func (t *rtuTCP) roundTrip(pdu []byte) ([]byte, error) {
	resp, err := t.tryRoundTrip(pdu)
	if err == nil {
		return resp, nil
	}
	// Reconnect once: the serial server may have dropped the idle
	// socket, or WiFi hiccuped mid-frame.
	t.Close()
	if cerr := t.connect(); cerr != nil {
		return nil, err
	}
	return t.tryRoundTrip(pdu)
}

func (t *rtuTCP) tryRoundTrip(pdu []byte) ([]byte, error) {
	adu := make([]byte, 0, len(pdu)+3)
	adu = append(adu, t.unitID)
	adu = append(adu, pdu...)
	c := crc16(adu)
	adu = append(adu, byte(c&0xFF), byte(c>>8))

	deadline := time.Now().Add(t.timeout)
	t.conn.SetWriteDeadline(deadline)
	if _, err := t.conn.Write(adu); err != nil {
		return nil, fmt.Errorf("write: %w", err)
	}

	t.conn.SetReadDeadline(deadline)
	// Header: unit id + function code.
	head := make([]byte, 2)
	if _, err := io.ReadFull(t.conn, head); err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	if head[0] != t.unitID {
		return nil, fmt.Errorf("response from unexpected unit %d", head[0])
	}
	fc := head[1]
	var rest []byte
	switch {
	case fc&0x80 != 0: // exception: code + CRC
		rest = make([]byte, 3)
	case fc == 0x03 || fc == 0x04: // byte count + data + CRC
		bc := make([]byte, 1)
		if _, err := io.ReadFull(t.conn, bc); err != nil {
			return nil, fmt.Errorf("read len: %w", err)
		}
		data := make([]byte, int(bc[0])+2)
		if _, err := io.ReadFull(t.conn, data); err != nil {
			return nil, fmt.Errorf("read data: %w", err)
		}
		full := append(append(head, bc[0]), data...)
		return verifyCRC(full)
	case fc == 0x06 || fc == 0x10: // addr + value/qty + CRC
		rest = make([]byte, 6)
	default:
		return nil, fmt.Errorf("unexpected function code 0x%02X", fc)
	}
	if _, err := io.ReadFull(t.conn, rest); err != nil {
		return nil, fmt.Errorf("read tail: %w", err)
	}
	return verifyCRC(append(head, rest...))
}

// verifyCRC checks the trailing CRC and returns the PDU (frame without
// unit id and CRC). Exceptions are turned into errors.
func verifyCRC(frame []byte) ([]byte, error) {
	n := len(frame)
	want := binary.LittleEndian.Uint16(frame[n-2:])
	if crc16(frame[:n-2]) != want {
		return nil, fmt.Errorf("CRC mismatch")
	}
	pdu := frame[1 : n-2]
	if pdu[0]&0x80 != 0 {
		return nil, fmt.Errorf("modbus exception 0x%02X for function 0x%02X", pdu[1], pdu[0]&0x7F)
	}
	return pdu, nil
}

func (t *rtuTCP) readRegisters(fc byte, addr, count uint16) ([]uint16, error) {
	pdu := []byte{fc, byte(addr >> 8), byte(addr), byte(count >> 8), byte(count)}
	resp, err := t.roundTrip(pdu)
	if err != nil {
		return nil, err
	}
	if len(resp) < 2 || int(resp[1]) != int(count)*2 || len(resp) < 2+int(resp[1]) {
		return nil, fmt.Errorf("short response: %d bytes for %d regs", len(resp), count)
	}
	out := make([]uint16, count)
	for i := range out {
		out[i] = binary.BigEndian.Uint16(resp[2+i*2:])
	}
	return out, nil
}

func (t *rtuTCP) ReadInputs(addr, count uint16) ([]uint16, error) {
	return t.readRegisters(0x04, addr, count)
}

func (t *rtuTCP) ReadHoldings(addr, count uint16) ([]uint16, error) {
	return t.readRegisters(0x03, addr, count)
}

func (t *rtuTCP) WriteRegister(addr, value uint16) error {
	pdu := []byte{0x06, byte(addr >> 8), byte(addr), byte(value >> 8), byte(value)}
	_, err := t.roundTrip(pdu)
	return err
}

func (t *rtuTCP) WriteRegisters(addr uint16, values []uint16) error {
	pdu := []byte{0x10, byte(addr >> 8), byte(addr),
		byte(len(values) >> 8), byte(len(values)), byte(len(values) * 2)}
	for _, v := range values {
		pdu = append(pdu, byte(v>>8), byte(v))
	}
	_, err := t.roundTrip(pdu)
	return err
}
