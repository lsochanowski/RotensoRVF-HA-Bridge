package rvf

import "fmt"

// SetRequest describes a partial change to one IDU (or the group
// registers). Nil fields are left untouched — the writer performs a
// read-modify-write on the packed holding registers.
type SetRequest struct {
	Power *bool
	Mode  *Mode
	Fan   *FanSpeed
	Temp  *int // degC, 16-32

	VSwing *uint16 // 0-5 fixed/off, 6 = auto swing
	HSwing *bool

	OffLock        *bool
	ModeLock       *uint16 // 0 unlock, 1 cool lock, 2 heat lock
	TempLock       *uint16 // 0 unlock, 1=22degC, 2=24degC, 3=26degC
	ControllerLock *bool
}

// ApplyOptimistic merges the request into a decoded status, mirroring
// what the unit will report once the write settles. Used by the MQTT
// bridge for instant UI feedback.
func (r *SetRequest) ApplyOptimistic(s *IDUStatus) {
	if r.Power != nil {
		s.Power = *r.Power
	}
	if r.Mode != nil {
		s.ModeRaw = uint16(*r.Mode)
		s.Mode = name(modeNames, *r.Mode)
	}
	if r.Fan != nil {
		s.FanSetRaw = uint16(*r.Fan)
		s.FanSet = name(fanNames, *r.Fan)
	}
	if r.Temp != nil {
		s.Setpoint = *r.Temp
	}
	if r.VSwing != nil {
		s.VSwing = *r.VSwing
	}
	if r.HSwing != nil {
		s.HSwing = *r.HSwing
	}
	if r.OffLock != nil {
		s.OffLock = *r.OffLock
	}
	if r.ControllerLock != nil {
		s.ControllerLock = *r.ControllerLock
	}
}

// ParseMode maps a CLI/user string to a Mode value.
func ParseMode(s string) (Mode, error) {
	if m, ok := modeByName[s]; ok {
		return m, nil
	}
	return 0, fmt.Errorf("unknown mode %q (cool, dry, fan, heat, auto, floor_heat, smart_floor_heat)", s)
}

// ParseFan maps a CLI/user string to a FanSpeed value.
func ParseFan(s string) (FanSpeed, error) {
	if f, ok := fanByName[s]; ok {
		return f, nil
	}
	return 0, fmt.Errorf("unknown fan speed %q (low, medium, high, auto)", s)
}

// touchesControl reports whether the request modifies the control word.
func (r *SetRequest) touchesControl() bool {
	return r.Power != nil || r.Mode != nil || r.Fan != nil || r.Temp != nil
}

// touchesLocks reports whether the request modifies the locks word.
func (r *SetRequest) touchesLocks() bool {
	return r.VSwing != nil || r.HSwing != nil || r.OffLock != nil ||
		r.ModeLock != nil || r.TempLock != nil || r.ControllerLock != nil
}

// applyControl merges the request into an existing control word.
func (r *SetRequest) applyControl(cur uint16) (uint16, error) {
	w := cur
	if r.Power != nil {
		w &^= 0x3 << 14
		if *r.Power {
			w |= 2 << 14
		} else {
			w |= 1 << 14
		}
	}
	if r.Mode != nil {
		w &^= 0x1F << 9
		w |= uint16(*r.Mode&0x1F) << 9
	}
	if r.Fan != nil {
		w &^= 0xF << 5
		w |= uint16(*r.Fan&0xF) << 5
	}
	if r.Temp != nil {
		if *r.Temp < 16 || *r.Temp > 32 {
			return 0, fmt.Errorf("setpoint %d degC out of range 16-32", *r.Temp)
		}
		w &^= 0x1F
		w |= uint16(*r.Temp - 15)
	}
	// The manual requires mode, temperature and fan speed to be valid
	// when starting a unit; after RMW all fields are present unless the
	// stored word was never initialised.
	if r.Power != nil && *r.Power {
		if (w>>9)&0x1F == 0 || (w>>5)&0xF == 0 || w&0x1F == 0 {
			return 0, fmt.Errorf("turning on requires mode, fan and temp to be set (current word 0x%04X has empty fields — pass --mode/--fan/--temp)", cur)
		}
	}
	return w, nil
}

// applyLocks merges the request into an existing locks/louver word.
func (r *SetRequest) applyLocks(cur uint16) (uint16, error) {
	w := cur
	setBit := func(bit uint, on bool) {
		if on {
			w |= 1 << bit
		} else {
			w &^= 1 << bit
		}
	}
	if r.OffLock != nil {
		setBit(15, *r.OffLock)
	}
	if r.ModeLock != nil {
		if *r.ModeLock > 2 {
			return 0, fmt.Errorf("mode lock must be 0 (unlock), 1 (cool) or 2 (heat)")
		}
		w &^= 0x3 << 13
		w |= *r.ModeLock << 13
	}
	if r.TempLock != nil {
		if *r.TempLock > 3 {
			return 0, fmt.Errorf("temp lock must be 0 (unlock), 1 (22degC), 2 (24degC) or 3 (26degC)")
		}
		w &^= 0x3 << 11
		w |= *r.TempLock << 11
	}
	if r.ControllerLock != nil {
		setBit(10, *r.ControllerLock)
	}
	if r.HSwing != nil {
		setBit(3, *r.HSwing)
	}
	if r.VSwing != nil {
		if *r.VSwing > 6 {
			return 0, fmt.Errorf("vertical swing must be 0-5 (fixed/off) or 6 (auto)")
		}
		w &^= 0x7
		w |= *r.VSwing
	}
	return w, nil
}
