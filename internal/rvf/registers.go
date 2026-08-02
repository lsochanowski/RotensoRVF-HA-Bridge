// Package rvf implements the Modbus register map of the Rotenso RVF
// Modbus Box (SP-D168, code 810055200026), per service manual
// RO_RVF_ALL_BMSMB3_SM_PL_20260520.
//
// All addresses below are raw protocol addresses (0-based), i.e. the
// "Adres odczytu" column of the manual, not the 3xxxx/4xxxx notation.
package rvf

// Per-IDU input registers (function 0x04): block of 9 registers per unit,
// base address = idu*9.
const (
	IDUInputStride = 9

	iduRegReturnAir   = 0 // T1 return air temp, 0.1 degC, signed
	iduRegEvapInlet   = 1 // T2A evaporator inlet temp, 0.1 degC, signed
	iduRegEvapMid     = 2 // T2B evaporator mid temp, 0.1 degC, signed
	iduRegCapacity    = 3 // rated capacity, value*0.1 HP
	iduRegPowerDemand = 4 // power demand, value*0.1, unsigned
	iduRegFanState    = 5 // bitfield: fan/heater/pump/EXV
	iduRegErrorCode   = 6 // error code, 0 = OK
	iduRegModel       = 7 // bitfield: unit type / features
	// register 8 is unused (padding)
)

// Per-IDU holding registers (functions 0x03/0x06/0x10): block of 2
// registers per unit, base address = idu*2.
const (
	IDUHoldingStride = 2

	iduHoldControl = 0 // on/off, mode, fan, setpoint
	iduHoldLocks   = 1 // locks, sleep, fresh air, heater, louver
)

// System registers.
const (
	AddrParity = 2000 // holding 42001: 0=none, 2=odd, 3=even

	AddrGroupControl = 3840 // holding 43841: group (broadcast) control word
	AddrGroupLocks   = 3841 // holding 43842: group locks/louver word

	AddrSystemMode   = 2560 // input 32561: bits 15..14 system mode
	AddrOnlineBitmap = 2572 // input 32573..32576: IDU online bits, 4 regs
)

// Per-ODU input registers (function 0x04): block of 49 registers per
// outdoor unit, base address = odu*49 + 2048. Up to 4 ODUs.
const (
	ODUInputBase   = 2048
	ODUInputStride = 49
	MaxODUs        = 4

	oduRegStatus       = 2  // high byte: protection F0-FF, low byte: error E0-EF
	oduRegHighPressure = 6  // 0.001 MPa, unsigned
	oduRegAmbient      = 12 // T4 ambient temp, 0.1 degC, signed
	oduRegDischargeA1  = 16 // compressor A1 discharge temp, 0.1 degC
	oduRegDischargeB1  = 17 // compressor B1 discharge temp (reserved), 0.1 degC
	oduRegDischargeA2  = 18 // compressor A2 discharge temp, 0.1 degC
	oduRegEXV1         = 24 // EXV1 opening, 0-2000
	oduRegEXV2         = 25 // EXV2 opening, 0-2000
	oduRegFanSpeed     = 30 // 0-255
	oduRegComp1Speed   = 31 // compressor 1 speed, 0.1 rps
	oduRegComp2Speed   = 35 // compressor 2 speed, 0.1 rps
	oduRegTypeInfo     = 39 // bitfield: phase / compressor type
	oduRegCapacity     = 40 // low byte: capacity, HP
	// registers span 0..40 within the block; read 41 regs per ODU
	ODUReadCount = 41
)

// MaxIDUs is the systems's addressing limit.
const MaxIDUs = 64

// Mode values carried in bits 13..9 of the IDU control word.
type Mode uint16

const (
	ModeCool           Mode = 1
	ModeDry            Mode = 2
	ModeFan            Mode = 4
	ModeHeat           Mode = 8
	ModeAuto           Mode = 16
	ModeFloorHeat      Mode = 9
	ModeSmartFloorHeat Mode = 10
)

var modeNames = map[Mode]string{
	ModeCool: "cool", ModeDry: "dry", ModeFan: "fan_only",
	ModeHeat: "heat", ModeAuto: "auto",
	ModeFloorHeat: "floor_heat", ModeSmartFloorHeat: "smart_floor_heat",
}

var modeByName = map[string]Mode{
	"cool": ModeCool, "dry": ModeDry, "fan": ModeFan, "fan_only": ModeFan,
	"heat": ModeHeat, "auto": ModeAuto,
	"floor_heat": ModeFloorHeat, "smart_floor_heat": ModeSmartFloorHeat,
}

// FanSpeed values carried in bits 8..5 of the IDU control word (setting).
type FanSpeed uint16

const (
	FanLow  FanSpeed = 1
	FanMed  FanSpeed = 2
	FanHigh FanSpeed = 4
	FanAuto FanSpeed = 8
)

var fanNames = map[FanSpeed]string{
	FanLow: "low", FanMed: "medium", FanHigh: "high", FanAuto: "auto",
}

var fanByName = map[string]FanSpeed{
	"low": FanLow, "medium": FanMed, "med": FanMed,
	"high": FanHigh, "auto": FanAuto,
}

// Actual fan speed reported in bits 15..13 of input register 5.
var fanActualNames = map[uint16]string{
	0: "off", 1: "breeze", 2: "low", 3: "medium", 4: "high", 5: "super",
}

// IDU unit types, bits 15..12 of input register 7.
var iduTypeNames = map[uint16]string{
	0b0001: "wall_mounted",
	0b0010: "floor_standing",
	0b0011: "cassette",
	0b0100: "duct",
	0b0101: "floor_ceiling",
	0b1001: "auxiliary",
	0b1010: "multi_split",
	0b1100: "inverter_compressor",
	0b1110: "digital_scroll",
}

// System mode, bits 15..14 of input register 2560.
var systemModeNames = map[uint16]string{
	0: "off", 1: "cooling", 2: "heating", 3: "offline",
}

// IDU error code descriptions (codes E0..EF as displayed on the unit).
// The manual's two tables differ slightly; this follows the per-IDU
// table (page 9) with page-7 details where the former says "reserved".
var iduErrorDesc = map[byte]string{
	0x0: "E0: wrong phase order / missing phase",
	0x1: "E1: communication error",
	0x2: "E2: T1 sensor fault",
	0x3: "E3: T2A sensor fault",
	0x4: "E4: T2B sensor fault",
	0x5: "E5: outdoor unit fault",
	0x6: "E6: zero-crossing / zero-speed protection",
	0x7: "E7: EEPROM error",
	0x8: "E8: fan speed out of control",
	0x9: "E9: main board <-> wired controller communication error",
	0xA: "EA: reserved",
	0xB: "EB: IPM module protection (reserved)",
	0xC: "EC: fresh air function fault (reserved)",
	0xD: "ED: outdoor unit protection (reserved)",
	0xE: "EE: water level alarm",
	0xF: "EF: other fault",
}

// ODU error code descriptions (low byte of ODU status register).
var oduErrorDesc = map[byte]string{
	0x0: "E0: communication error between outdoor units / IPM B fault",
	0x1: "E1: wrong phase order or missing phase",
	0x2: "E2: ODU <-> IDU communication error",
	0x3: "E3: reserved",
	0x4: "E4: T4 sensor fault",
	0x5: "E5: reserved",
	0x6: "E6: T3 sensor fault",
	0x7: "E7: reserved",
	0x8: "E8: ODU address error",
	0x9: "E9: voltage error",
	0xA: "EA (H0): DSP <-> main board (0537) communication error",
	0xB: "EB (H1): communication error 0537 <-> 0547",
	0xC: "EC (H2): decrease of outdoor unit count",
	0xD: "ED (H3): increase of outdoor unit count",
	0xE: "EE (H7): decrease of indoor unit count",
	0xF: "EF: other fault",
}

// ODU protection code descriptions (high byte of ODU status register).
var oduProtectionDesc = map[byte]string{
	0x0: "P0: compressor top / overheat protection",
	0x1: "P1: high discharge pressure protection",
	0x2: "P2: low discharge pressure protection",
	0x3: "P3: inverter overcurrent protection",
	0x4: "P4: high discharge temperature protection",
	0x5: "P5: heat exchanger high temperature protection (T3)",
	0x6: "P6: IPM A module protection",
	0x7: "P7: module B overcurrent protection (2P3)",
	0x8: "P8: compressor 2 current protection",
	0x9: "P9: DC fan module protection",
	0xA: "PA: defrost/anti-icing protection",
	0xB: "PB: reserved",
	0xC: "PC: reserved",
	0xD: "PD: oil return",
	0xE: "PE: oil balance",
	0xF: "PF: other protection",
}

// ODU compressor types, bits 10..8 of type-info register.
var oduTypeNames = map[uint16]string{
	0: "on_off", 1: "inverter", 2: "digital_scroll", 3: "chiller",
}
