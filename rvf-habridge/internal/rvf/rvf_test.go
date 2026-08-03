package rvf

import "testing"

func ptr[T any](v T) *T { return &v }

// Control word layout: [15:14 on/off][13:9 mode][8:5 fan][4:0 temp-15]
func word(power, mode, fan, temp uint16) uint16 {
	return power<<14 | mode<<9 | fan<<5 | (temp - 15)
}

func TestDecodeControlWord(t *testing.T) {
	s := &IDUStatus{}
	// on, cool(1), fan high(4), 24degC
	DecodeIDUHoldings(s, []uint16{word(2, 1, 4, 24), 0})
	if !s.Power || s.Mode != "cool" || s.FanSet != "high" || s.Setpoint != 24 {
		t.Errorf("got power=%v mode=%s fan=%s temp=%d", s.Power, s.Mode, s.FanSet, s.Setpoint)
	}
	// off, heat(8), fan auto(8), 21degC
	DecodeIDUHoldings(s, []uint16{word(1, 8, 8, 21), 0})
	if s.Power || s.Mode != "heat" || s.FanSet != "auto" || s.Setpoint != 21 {
		t.Errorf("got power=%v mode=%s fan=%s temp=%d", s.Power, s.Mode, s.FanSet, s.Setpoint)
	}
}

func TestDecodeLocksWord(t *testing.T) {
	s := &IDUStatus{}
	// off-lock, heat mode lock(2), temp lock 24(2), controller lock,
	// fresh air, sleep, heater, h-swing, v-swing auto(6)
	l := uint16(1<<15 | 2<<13 | 2<<11 | 1<<10 | 1<<8 | 1<<7 | 1<<6 | 1<<3 | 6)
	DecodeIDUHoldings(s, []uint16{0, l})
	if !s.OffLock || s.ModeLock != "heat" || s.TempLock != "24" ||
		!s.ControllerLock || !s.FreshAir || !s.SleepMode || !s.HeaterSet ||
		!s.HSwing || s.VSwing != 6 {
		t.Errorf("locks decode mismatch: %+v", s)
	}
}

func TestDecodeInputs(t *testing.T) {
	s := &IDUStatus{}
	regs := []uint16{
		235,    // 23.5 degC return
		0xFFF6, // -1.0 degC evap inlet (int16 -10)
		180,    // 18.0
		56,     // 5.6 HP
		123,    // demand 12.3
		// fan medium(3), heater on, pump off, EXV 512
		3<<13 | 1<<12 | 512,
		0x00E4,    // error E4
		0b0100<<12 | 1<<7, // duct unit, extended range
		0,
	}
	DecodeIDUInputs(s, regs)
	if s.ReturnAirTemp != 23.5 || s.EvapInletTemp != -1.0 || s.EvapMidTemp != 18.0 {
		t.Errorf("temps: %v %v %v", s.ReturnAirTemp, s.EvapInletTemp, s.EvapMidTemp)
	}
	if s.CapacityHP != 5.6 || s.PowerDemand != 12.3 {
		t.Errorf("cap/demand: %v %v", s.CapacityHP, s.PowerDemand)
	}
	if s.FanActual != "medium" || !s.HeaterOn || s.WaterPumpOn || s.EXVOpening != 512 {
		t.Errorf("fan state: %+v", s)
	}
	if s.Error == "" || s.Error[:2] != "E4" {
		t.Errorf("error: %q", s.Error)
	}
	if s.UnitType != "duct" || !s.ExtendedRange {
		t.Errorf("model: %q ext=%v", s.UnitType, s.ExtendedRange)
	}
}

func TestApplyControlRMW(t *testing.T) {
	// Current: on, cool, low fan, 23degC. Change only the setpoint.
	cur := word(2, 1, 1, 23)
	req := SetRequest{Temp: ptr(26)}
	got, err := req.applyControl(cur)
	if err != nil {
		t.Fatal(err)
	}
	if want := word(2, 1, 1, 26); got != want {
		t.Errorf("got 0x%04X want 0x%04X", got, want)
	}

	// Change only mode; power/fan/temp must survive.
	req = SetRequest{Mode: ptr(ModeHeat)}
	got, err = req.applyControl(cur)
	if err != nil {
		t.Fatal(err)
	}
	if want := word(2, 8, 1, 23); got != want {
		t.Errorf("got 0x%04X want 0x%04X", got, want)
	}
}

func TestApplyControlPowerOnValidation(t *testing.T) {
	// Word never initialised: turning on without mode/fan/temp must fail.
	req := SetRequest{Power: ptr(true)}
	if _, err := req.applyControl(0); err == nil {
		t.Error("expected error when turning on an uninitialised unit")
	}
	// Providing everything must succeed.
	req = SetRequest{Power: ptr(true), Mode: ptr(ModeCool), Fan: ptr(FanAuto), Temp: ptr(24)}
	got, err := req.applyControl(0)
	if err != nil {
		t.Fatal(err)
	}
	if want := word(2, 1, 8, 24); got != want {
		t.Errorf("got 0x%04X want 0x%04X", got, want)
	}
}

func TestApplyControlTempRange(t *testing.T) {
	for _, bad := range []int{15, 33, 0} {
		req := SetRequest{Temp: ptr(bad)}
		if _, err := req.applyControl(word(2, 1, 1, 23)); err == nil {
			t.Errorf("temp %d should be rejected", bad)
		}
	}
}

func TestApplyLocksRMW(t *testing.T) {
	cur := uint16(1<<15 | 6) // off-lock set, v-swing auto
	req := SetRequest{HSwing: ptr(true), VSwing: ptr(uint16(3))}
	got, err := req.applyLocks(cur)
	if err != nil {
		t.Fatal(err)
	}
	if want := uint16(1<<15 | 1<<3 | 3); got != want {
		t.Errorf("got 0x%04X want 0x%04X", got, want)
	}
}

func TestDecodeOnlineBitmap(t *testing.T) {
	online := DecodeOnlineBitmap([]uint16{0b101, 0, 1 << 15, 0})
	for i, want := range map[int]bool{0: true, 1: false, 2: true, 47: true, 48: false} {
		if online[i] != want {
			t.Errorf("IDU %d: got %v want %v", i, online[i], want)
		}
	}
}

func TestDecodeODUStatusCodes(t *testing.T) {
	regs := make([]uint16, ODUReadCount)
	regs[oduRegStatus] = 0xF1E4 // protection P1, error E4
	regs[oduRegHighPressure] = 2540
	regs[oduRegAmbient] = 0xFFCE // -5.0
	regs[oduRegTypeInfo] = 0xF000 | 1<<11 | 1<<8
	regs[oduRegCapacity] = 22
	s := DecodeODU(0, regs)
	if s.Protection[:2] != "P1" || s.Error[:2] != "E4" {
		t.Errorf("codes: %q / %q", s.Protection, s.Error)
	}
	if s.HighPressure != 2.540 || s.AmbientTemp != -5.0 {
		t.Errorf("pressure/ambient: %v %v", s.HighPressure, s.AmbientTemp)
	}
	if !s.ThreePhase || s.CompType != "inverter" || s.CapacityHP != 22 {
		t.Errorf("type: %+v", s)
	}
}

func TestDecodeErrZero(t *testing.T) {
	if got := decodeErr(0, 'E', iduErrorDesc); got != "" {
		t.Errorf("zero must decode to empty, got %q", got)
	}
	if got := decodeErr(0xE7, 'E', iduErrorDesc); got[:2] != "E7" {
		t.Errorf("0xE7: %q", got)
	}
}

func TestSystemMode(t *testing.T) {
	if got := SystemModeName(1 << 14); got != "cooling" {
		t.Errorf("got %q", got)
	}
	if got := SystemModeName(3 << 14); got != "offline" {
		t.Errorf("got %q", got)
	}
}

func TestConfirmedBy(t *testing.T) {
	s := &IDUStatus{Power: true, ModeRaw: 1, FanSetRaw: 8, Setpoint: 24, VSwing: 6}
	req := SetRequest{Power: ptr(true), Temp: ptr(24)}
	if !req.ConfirmedBy(s) {
		t.Error("matching fields should confirm")
	}
	req = SetRequest{Temp: ptr(22)}
	if req.ConfirmedBy(s) {
		t.Error("mismatched setpoint must not confirm")
	}
	req = SetRequest{Mode: ptr(ModeHeat)}
	if req.ConfirmedBy(s) {
		t.Error("mismatched mode must not confirm")
	}
}

func TestMergeRequests(t *testing.T) {
	r := SetRequest{Power: ptr(true), Temp: ptr(22)}
	r.Merge(SetRequest{Temp: ptr(25), Fan: ptr(FanHigh)})
	if *r.Temp != 25 || *r.Fan != FanHigh || !*r.Power {
		t.Errorf("merge result wrong: %+v", r)
	}
}
