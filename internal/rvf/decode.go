package rvf

import "fmt"

// IDUStatus is the fully decoded state of one indoor unit: input
// (monitoring) registers plus holding (control) registers.
type IDUStatus struct {
	ID     int    `json:"id"`
	Name   string `json:"name,omitempty"`
	Online bool   `json:"online"`

	// Input registers (monitoring).
	ReturnAirTemp  float64 `json:"return_air_temp"` // degC (T1)
	EvapInletTemp  float64 `json:"evap_inlet_temp"` // degC (T2A)
	EvapMidTemp    float64 `json:"evap_mid_temp"`   // degC (T2B)
	CapacityHP     float64 `json:"capacity_hp"`
	PowerDemand    float64 `json:"power_demand"` // raw*0.1, unit per manual
	FanActual      string  `json:"fan_actual"`
	FanActualRaw   uint16  `json:"fan_actual_raw"`
	HeaterOn       bool    `json:"heater_on"`
	WaterPumpOn    bool    `json:"water_pump_on"`
	EXVOpening     uint16  `json:"exv_opening"` // 0-2000
	ErrorRaw       uint16  `json:"error_raw"`
	Error          string  `json:"error"` // "" when OK
	UnitType       string  `json:"unit_type"`
	UnitTypeRaw    uint16  `json:"unit_type_raw"`
	ExtendedRange  bool    `json:"extended_temp_range"` // false: 17-30, true: 16-32 degC
	ModelWord      uint16  `json:"model_word"`
	FanStateWord   uint16  `json:"fan_state_word"`
	PowerDemandRaw uint16  `json:"power_demand_raw"`

	// Holding registers (settings).
	Power       bool   `json:"power"`
	Mode        string `json:"mode"`
	ModeRaw     uint16 `json:"mode_raw"`
	FanSet      string `json:"fan_set"`
	FanSetRaw   uint16 `json:"fan_set_raw"`
	Setpoint    int    `json:"setpoint"` // degC
	ControlWord uint16 `json:"control_word"`

	OffLock        bool   `json:"off_lock"`
	ModeLock       string `json:"mode_lock"` // "", "cool", "heat"
	TempLock       string `json:"temp_lock"` // "", "22", "24", "26"
	ControllerLock bool   `json:"controller_lock"`
	FreshAir       bool   `json:"fresh_air"`
	SleepMode      bool   `json:"sleep_mode"`
	HeaterSet      bool   `json:"heater_set"`
	HSwing         bool   `json:"h_swing"`
	VSwing         uint16 `json:"v_swing"` // 0-5 fixed/off, 6 = auto swing
	LocksWord      uint16 `json:"locks_word"`
}

func temp01(v uint16) float64 { return float64(int16(v)) / 10.0 }

// DecodeIDUInputs decodes the 9 input registers of one IDU.
func DecodeIDUInputs(s *IDUStatus, regs []uint16) {
	if len(regs) < IDUInputStride {
		return
	}
	s.ReturnAirTemp = temp01(regs[iduRegReturnAir])
	s.EvapInletTemp = temp01(regs[iduRegEvapInlet])
	s.EvapMidTemp = temp01(regs[iduRegEvapMid])
	s.CapacityHP = float64(regs[iduRegCapacity]) / 10.0
	s.PowerDemandRaw = regs[iduRegPowerDemand]
	s.PowerDemand = float64(regs[iduRegPowerDemand]) / 10.0

	w := regs[iduRegFanState]
	s.FanStateWord = w
	s.FanActualRaw = w >> 13
	s.FanActual = name(fanActualNames, s.FanActualRaw)
	s.HeaterOn = w&(1<<12) != 0
	s.WaterPumpOn = w&(1<<11) != 0
	s.EXVOpening = w & 0x07FF

	s.ErrorRaw = regs[iduRegErrorCode]
	s.Error = decodeErr(s.ErrorRaw, 'E', iduErrorDesc)

	m := regs[iduRegModel]
	s.ModelWord = m
	s.UnitTypeRaw = m >> 12
	s.UnitType = name(iduTypeNames, s.UnitTypeRaw)
	s.ExtendedRange = m&(1<<7) != 0
}

// DecodeIDUHoldings decodes the 2 holding registers of one IDU.
func DecodeIDUHoldings(s *IDUStatus, regs []uint16) {
	if len(regs) < IDUHoldingStride {
		return
	}
	c := regs[iduHoldControl]
	s.ControlWord = c
	s.Power = c>>14 == 2 // 1 = off, 2 = on
	s.ModeRaw = (c >> 9) & 0x1F
	s.Mode = name(modeNames, Mode(s.ModeRaw))
	s.FanSetRaw = (c >> 5) & 0x0F
	s.FanSet = name(fanNames, FanSpeed(s.FanSetRaw))
	s.Setpoint = int(c&0x1F) + 15

	l := regs[iduHoldLocks]
	s.LocksWord = l
	s.OffLock = l&(1<<15) != 0
	switch (l >> 13) & 0x3 {
	case 1:
		s.ModeLock = "cool"
	case 2:
		s.ModeLock = "heat"
	default:
		s.ModeLock = ""
	}
	switch (l >> 11) & 0x3 {
	case 1:
		s.TempLock = "22"
	case 2:
		s.TempLock = "24"
	case 3:
		s.TempLock = "26"
	default:
		s.TempLock = ""
	}
	s.ControllerLock = l&(1<<10) != 0
	s.FreshAir = l&(1<<8) != 0
	s.SleepMode = l&(1<<7) != 0
	s.HeaterSet = l&(1<<6) != 0
	s.HSwing = l&(1<<3) != 0
	s.VSwing = l & 0x7
}

// ODUStatus is the decoded state of one outdoor unit.
type ODUStatus struct {
	ID int `json:"id"`

	Protection    string  `json:"protection"` // "" when none
	ProtectionRaw byte    `json:"protection_raw"`
	Error         string  `json:"error"` // "" when OK
	ErrorRaw      byte    `json:"error_raw"`
	StatusWord    uint16  `json:"status_word"`
	HighPressure  float64 `json:"high_pressure_mpa"`
	AmbientTemp   float64 `json:"ambient_temp"`      // degC (T4)
	DischargeA1   float64 `json:"discharge_temp_a1"` // degC
	DischargeB1   float64 `json:"discharge_temp_b1"` // degC (reserved)
	DischargeA2   float64 `json:"discharge_temp_a2"` // degC
	EXV1Opening   uint16  `json:"exv1_opening"`
	EXV2Opening   uint16  `json:"exv2_opening"`
	FanSpeed      uint16  `json:"fan_speed"`       // 0-255
	Comp1SpeedRPS float64 `json:"comp1_speed_rps"` // rps
	Comp2SpeedRPS float64 `json:"comp2_speed_rps"` // rps
	ThreePhase    bool    `json:"three_phase"`
	CompType      string  `json:"compressor_type"`
	CompTypeRaw   uint16  `json:"compressor_type_raw"`
	CapacityHP    float64 `json:"capacity_hp"`
	TypeWord      uint16  `json:"type_word"`
}

// DecodeODU decodes the ODUReadCount input registers of one ODU
// (block starting at odu*49+2048).
func DecodeODU(id int, regs []uint16) ODUStatus {
	s := ODUStatus{ID: id}
	if len(regs) < ODUReadCount {
		return s
	}
	s.StatusWord = regs[oduRegStatus]
	s.ProtectionRaw = byte(s.StatusWord >> 8)
	s.ErrorRaw = byte(s.StatusWord & 0xFF)
	s.Protection = decodeErr(uint16(s.ProtectionRaw), 'P', oduProtectionDesc)
	s.Error = decodeErr(uint16(s.ErrorRaw), 'E', oduErrorDesc)

	s.HighPressure = float64(regs[oduRegHighPressure]) / 1000.0
	s.AmbientTemp = temp01(regs[oduRegAmbient])
	s.DischargeA1 = temp01(regs[oduRegDischargeA1])
	s.DischargeB1 = temp01(regs[oduRegDischargeB1])
	s.DischargeA2 = temp01(regs[oduRegDischargeA2])
	s.EXV1Opening = regs[oduRegEXV1]
	s.EXV2Opening = regs[oduRegEXV2]
	s.FanSpeed = regs[oduRegFanSpeed]
	s.Comp1SpeedRPS = float64(regs[oduRegComp1Speed]) / 10.0
	s.Comp2SpeedRPS = float64(regs[oduRegComp2Speed]) / 10.0

	t := regs[oduRegTypeInfo]
	s.TypeWord = t
	s.ThreePhase = t&(1<<11) != 0
	s.CompTypeRaw = (t >> 8) & 0x7
	s.CompType = name(oduTypeNames, s.CompTypeRaw)
	s.CapacityHP = float64(regs[oduRegCapacity] & 0xFF)
	return s
}

// decodeErr renders an error/protection register value as a display
// code like "E4 (E4: T2B sensor fault)". Zero means OK and yields "".
// Values 0xE0-0xEF / 0xF0-0xFF carry the code in the low nibble; small
// raw values (0-15) are treated as the code index directly.
func decodeErr(v uint16, letter byte, desc map[byte]string) string {
	if v == 0 {
		return ""
	}
	var nib byte
	switch {
	case v >= 0xE0 && v <= 0xFF:
		nib = byte(v & 0xF)
	case v <= 0xF:
		nib = byte(v)
	default:
		return fmt.Sprintf("unknown (0x%04X)", v)
	}
	code := fmt.Sprintf("%c%X", letter, nib)
	if d, ok := desc[nib]; ok {
		return fmt.Sprintf("%s (%s)", code, d)
	}
	return code
}

// name looks up a display name in a map keyed by raw value, falling
// back to the raw number so unknown values stay visible.
func name[K ~uint16](m map[K]string, k K) string {
	if s, ok := m[k]; ok {
		return s
	}
	return fmt.Sprintf("unknown(%d)", uint16(k))
}

// DecodeOnlineBitmap turns the 4 system registers (addr 2572..2575)
// into a per-IDU online flag. Bit i of register k covers IDU k*16+i.
func DecodeOnlineBitmap(regs []uint16) [MaxIDUs]bool {
	var online [MaxIDUs]bool
	for k, r := range regs {
		if k >= 4 {
			break
		}
		for i := 0; i < 16; i++ {
			online[k*16+i] = r&(1<<i) != 0
		}
	}
	return online
}

// SystemModeName decodes input register 2560.
func SystemModeName(v uint16) string {
	return name(systemModeNames, v>>14)
}
