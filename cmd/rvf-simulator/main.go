// rvf-simulator emulates a Rotenso RVF Modbus Box over Modbus TCP so
// the CLI (and later the HA bridge) can be exercised without hardware.
//
//	rvf-simulator --listen tcp://127.0.0.1:5502 --idus 5
//	rvf-habridge scan --conn tcp://127.0.0.1:5502
package main

import (
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/simonvetter/modbus"
)

type simulator struct {
	mu       sync.Mutex
	start    time.Time
	iduCount int
	holdings map[uint16]uint16
}

func newSimulator(iduCount int) *simulator {
	s := &simulator{start: time.Now(), iduCount: iduCount, holdings: map[uint16]uint16{}}
	for n := 0; n < iduCount; n++ {
		// on/off=on(2), mode=cool(1), fan=low(1), setpoint 23degC (8)
		s.holdings[uint16(n*2)] = 2<<14 | 1<<9 | 1<<5 | 8
		s.holdings[uint16(n*2+1)] = 6 // v-swing auto
	}
	s.holdings[2000] = 0 // parity: none
	return s
}

func (s *simulator) HandleHoldingRegisters(req *modbus.HoldingRegistersRequest) ([]uint16, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if req.IsWrite {
		for i, v := range req.Args {
			addr := req.Addr + uint16(i)
			s.holdings[addr] = v
			log.Printf("write holding %d = 0x%04X (unit %d)", addr, v, req.UnitId)
			// Group control: fan out to all IDUs.
			if addr == 3840 || addr == 3841 {
				off := addr - 3840
				for n := 0; n < s.iduCount; n++ {
					s.holdings[uint16(n*2)+off] = v
				}
			}
		}
		return req.Args, nil
	}
	out := make([]uint16, req.Quantity)
	for i := range out {
		out[i] = s.holdings[req.Addr+uint16(i)]
	}
	return out, nil
}

func (s *simulator) HandleInputRegisters(req *modbus.InputRegistersRequest) ([]uint16, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]uint16, req.Quantity)
	for i := range out {
		out[i] = s.input(req.Addr + uint16(i))
	}
	return out, nil
}

// input computes the value of one input register on the fly.
func (s *simulator) input(addr uint16) uint16 {
	t := time.Since(s.start).Seconds()
	wobble := func(n int, amp float64) int16 {
		return int16(amp * math.Sin(t/60+float64(n)))
	}
	switch {
	case addr < uint16(s.iduCount*9): // IDU blocks
		n, reg := int(addr/9), int(addr%9)
		ctrl := s.holdings[uint16(n*2)]
		on := ctrl>>14 == 2
		setp := int16(ctrl&0x1F) + 15
		switch reg {
		case 0: // return air: drifts toward setpoint when on
			base := int16(260) + wobble(n, 15)
			if on {
				base = setp*10 + wobble(n, 8)
			}
			return uint16(base)
		case 1: // evap inlet
			if on {
				return uint16(80 + wobble(n, 20))
			}
			return uint16(230 + wobble(n, 10))
		case 2: // evap mid
			if on {
				return uint16(110 + wobble(n, 20))
			}
			return uint16(230 + wobble(n, 10))
		case 3:
			return uint16(28 + n*14) // 2.8, 4.2, 5.6... HP
		case 4:
			if on {
				return uint16(120 + n*10)
			}
			return 0
		case 5: // fan actual mirrors the set fan when on
			if !on {
				return 0
			}
			fanSet := (ctrl >> 5) & 0xF
			var actual uint16
			switch fanSet {
			case 1:
				actual = 2
			case 2:
				actual = 3
			case 4, 8:
				actual = 4
			}
			exv := uint16(400 + 100*n)
			return actual<<13 | exv
		case 6:
			if n == s.iduCount-1 && int(t/120)%2 == 1 {
				return 0xE4 // periodic fake T2B sensor fault on the last unit
			}
			return 0
		case 7: // types: wall, cassette, duct, ...
			types := []uint16{0b0001, 0b0011, 0b0100, 0b0101, 0b0010}
			return types[n%len(types)]<<12 | 1<<7
		}
		return 0
	case addr == 2560: // system mode: cooling if any IDU on
		for n := 0; n < s.iduCount; n++ {
			if s.holdings[uint16(n*2)]>>14 == 2 {
				return 1 << 14
			}
		}
		return 0
	case addr >= 2572 && addr <= 2575: // online bitmap
		if addr == 2572 {
			return uint16(1<<s.iduCount - 1)
		}
		return 0
	case addr >= 2048 && addr < 2048+49: // ODU 0
		switch int(addr - 2048) {
		case 2:
			return 0 // no protection/error
		case 6:
			return uint16(2650 + wobble(9, 100)) // 2.65 MPa
		case 12:
			return uint16(315 + wobble(1, 20)) // ambient 31.5degC
		case 16:
			return uint16(720 + wobble(2, 30))
		case 18:
			return uint16(705 + wobble(3, 30))
		case 24:
			return 1450
		case 25:
			return 0
		case 30:
			return 180
		case 31:
			return uint16(420 + wobble(4, 40)) // 42 rps
		case 35:
			return 0
		case 39:
			return 0xF000 | 1<<11 | 1<<8 // 3-phase inverter
		case 40:
			return 16 // 16 HP
		}
		return 0
	}
	return 0
}

func (s *simulator) HandleCoils(req *modbus.CoilsRequest) ([]bool, error) {
	return nil, modbus.ErrIllegalFunction
}

func (s *simulator) HandleDiscreteInputs(req *modbus.DiscreteInputsRequest) ([]bool, error) {
	return nil, modbus.ErrIllegalFunction
}

func main() {
	listen := flag.String("listen", "tcp://127.0.0.1:5502", "listen URL")
	idus := flag.Int("idus", 5, "number of simulated indoor units (1-16)")
	flag.Parse()
	if *idus < 1 || *idus > 16 {
		log.Fatal("--idus must be 1-16")
	}

	server, err := modbus.NewServer(&modbus.ServerConfiguration{
		URL:        *listen,
		Timeout:    30 * time.Second,
		MaxClients: 5,
	}, newSimulator(*idus))
	if err != nil {
		log.Fatal(err)
	}
	if err := server.Start(); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("rvf-simulator: %d IDUs + 1 ODU on %s (Ctrl-C to stop)\n", *idus, *listen)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	server.Stop()
}
