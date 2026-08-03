// rvf-habridge is a standalone CLI for the Rotenso RVF Modbus Box:
// scanning, decoding and controlling indoor/outdoor units. It is the
// test bench for the future Home Assistant bridge (MQTT discovery).
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"rotenso-rvf-habridge/internal/config"
	"rotenso-rvf-habridge/internal/rvf"
)

var version = "0.1.0-dev"

func usage() {
	fmt.Fprintf(os.Stderr, `rvf-habridge %s — Rotenso RVF Modbus Box tool

Usage: rvf-habridge <command> [flags]

Commands:
  scan      detect online IDUs, show system mode and unit identity
  status    full decoded status of IDUs (all online, or --idu N)
  odu       outdoor unit diagnostics (--odu N, default all configured)
  watch     poll and print changes — use it to map IDU ids to rooms:
            poke a unit with its remote and watch which id reacts
  set       change settings of one IDU (read-modify-write)
  all       group (broadcast) control of all IDUs
  raw       raw register access (read-input / read-holding / write)
  version   print version

Connection flags (every command): --config, --conn, --unit, --timeout
Config file (default %s):
  connection: {url: "tcp://192.168.1.50:502", unit_id: 1}
  idus: {0: Salon, 1: Sypialnia}
  odu_count: 1
`, version, config.DefaultPath)
	os.Exit(2)
}

// commonFlags registers connection flags shared by all subcommands and
// returns a resolver that merges them with the config file.
func commonFlags(fs *flag.FlagSet) func() (*config.Config, *rvf.Client) {
	cfgPath := fs.String("config", "", "config file path (default "+config.DefaultPath+")")
	conn := fs.String("conn", "", "connection URL: tcp://host:502 or rtu:///dev/ttyUSB0 (overrides config)")
	unit := fs.Uint("unit", 0, "Modbus slave id (overrides config)")
	timeout := fs.Duration("timeout", 0, "per-request timeout (overrides config, default 2s)")
	jsonOut = fs.Bool("json", false, "output as JSON")

	return func() (*config.Config, *rvf.Client) {
		cfg, err := config.Load(*cfgPath)
		if err != nil {
			fatal("config: %v", err)
		}
		cc := rvf.ConnConfig{
			URL:      cfg.Connection.URL,
			UnitID:   cfg.Connection.UnitID,
			Timeout:  cfg.Connection.Timeout,
			Speed:    cfg.Connection.Speed,
			Parity:   cfg.Connection.Parity,
			StopBits: cfg.Connection.StopBits,
		}
		if *conn != "" {
			cc.URL = *conn
		}
		if *unit != 0 {
			cc.UnitID = uint8(*unit)
		}
		if *timeout != 0 {
			cc.Timeout = *timeout
		}
		if cc.URL == "" {
			fatal("no connection URL: pass --conn tcp://host:502 (or rtu:///dev/tty...) or set connection.url in %s", config.DefaultPath)
		}
		if cc.UnitID == 0 {
			cc.UnitID = 1
		}
		client, err := rvf.Dial(cc)
		if err != nil {
			fatal("%v", err)
		}
		return cfg, client
	}
}

var jsonOut *bool

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "rvf-habridge: "+format+"\n", args...)
	os.Exit(1)
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	cmd, args := os.Args[1], os.Args[2:]
	switch cmd {
	case "scan":
		cmdScan(args)
	case "status":
		cmdStatus(args)
	case "odu":
		cmdODU(args)
	case "watch":
		cmdWatch(args)
	case "set":
		cmdSet(args)
	case "all":
		cmdAll(args)
	case "raw":
		cmdRaw(args)
	case "version":
		fmt.Println(version)
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", cmd)
		usage()
	}
}

func cmdScan(args []string) {
	fs := flag.NewFlagSet("scan", flag.ExitOnError)
	connect := commonFlags(fs)
	fs.Parse(args)
	cfg, client := connect()
	defer client.Close()

	mode, raw, err := client.SystemMode()
	if err != nil {
		fatal("%v", err)
	}
	_, ids, err := client.OnlineIDUs()
	if err != nil {
		fatal("%v", err)
	}
	statuses, err := client.ReadIDUs(ids)
	if err != nil {
		fatal("%v", err)
	}
	fillNames(cfg, statuses)

	if *jsonOut {
		printJSON(map[string]any{
			"system_mode": mode, "system_mode_raw": raw,
			"online_ids": ids, "idus": sortedStatuses(statuses),
		})
		return
	}
	fmt.Printf("System mode: %s   Online IDUs: %d\n\n", mode, len(ids))
	if len(ids) == 0 {
		fmt.Println("No indoor units online. Check XYE wiring, slave id and connection.")
		return
	}
	fmt.Printf("%-4s %-18s %-20s %-6s %-8s %s\n", "ID", "NAME", "TYPE", "CAP", "POWER", "ERROR")
	for _, s := range sortedStatuses(statuses) {
		fmt.Printf("%-4d %-18s %-20s %-6s %-8s %s\n",
			s.ID, orDash(s.Name), s.UnitType,
			fmt.Sprintf("%.1fHP", s.CapacityHP),
			onOff(s.Power), orDash(s.Error))
	}
	unnamed := 0
	for _, s := range statuses {
		if s.Name == "" {
			unnamed++
		}
	}
	if unnamed > 0 {
		fmt.Printf("\n%d unit(s) unnamed — run `rvf-habridge watch`, poke each unit with its remote,\nnote which ID reacts, then add it under `idus:` in %s.\n", unnamed, config.DefaultPath)
	}
}

func cmdStatus(args []string) {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	connect := commonFlags(fs)
	idu := fs.Int("idu", -1, "single IDU id (default: all online)")
	fs.Parse(args)
	cfg, client := connect()
	defer client.Close()

	ids, err := resolveIDs(client, *idu)
	if err != nil {
		fatal("%v", err)
	}
	statuses, err := client.ReadIDUs(ids)
	if err != nil {
		fatal("%v", err)
	}
	fillNames(cfg, statuses)

	if *jsonOut {
		printJSON(sortedStatuses(statuses))
		return
	}
	for _, s := range sortedStatuses(statuses) {
		printIDU(s)
	}
}

func printIDU(s *rvf.IDUStatus) {
	title := fmt.Sprintf("IDU %d", s.ID)
	if s.Name != "" {
		title += " (" + s.Name + ")"
	}
	fmt.Printf("=== %s — %s, %.1f HP ===\n", title, s.UnitType, s.CapacityHP)
	fmt.Printf("  Power: %-4s  Mode: %-10s  Setpoint: %d°C  Fan set: %s (actual: %s)\n",
		onOff(s.Power), s.Mode, s.Setpoint, s.FanSet, s.FanActual)
	fmt.Printf("  Temps: return %.1f°C  evap-in %.1f°C  evap-mid %.1f°C\n",
		s.ReturnAirTemp, s.EvapInletTemp, s.EvapMidTemp)
	fmt.Printf("  EXV: %d/2000  Heater: %s  Pump: %s  Power demand: %.1f (raw %d)\n",
		s.EXVOpening, onOff(s.HeaterOn), onOff(s.WaterPumpOn), s.PowerDemand, s.PowerDemandRaw)
	fmt.Printf("  Swing: vertical %s  horizontal %s   Sleep: %s  Fresh air: %s\n",
		vswingStr(s.VSwing), onOff(s.HSwing), onOff(s.SleepMode), onOff(s.FreshAir))
	locks := []string{}
	if s.OffLock {
		locks = append(locks, "off-lock")
	}
	if s.ModeLock != "" {
		locks = append(locks, "mode-lock:"+s.ModeLock)
	}
	if s.TempLock != "" {
		locks = append(locks, "temp-lock:"+s.TempLock+"°C")
	}
	if s.ControllerLock {
		locks = append(locks, "controller-lock")
	}
	fmt.Printf("  Locks: %s\n", orDash(strings.Join(locks, ", ")))
	fmt.Printf("  Error: %s   [ctrl=0x%04X locks=0x%04X fan=0x%04X model=0x%04X]\n\n",
		orDash(s.Error), s.ControlWord, s.LocksWord, s.FanStateWord, s.ModelWord)
}

func cmdODU(args []string) {
	fs := flag.NewFlagSet("odu", flag.ExitOnError)
	connect := commonFlags(fs)
	odu := fs.Int("odu", -1, "single ODU id 0-3 (default: all configured)")
	fs.Parse(args)
	cfg, client := connect()
	defer client.Close()

	var ids []int
	if *odu >= 0 {
		ids = []int{*odu}
	} else {
		for i := 0; i < cfg.ODUCount; i++ {
			ids = append(ids, i)
		}
	}
	var all []rvf.ODUStatus
	for _, id := range ids {
		s, err := client.ReadODU(id)
		if err != nil {
			fatal("%v", err)
		}
		all = append(all, s)
	}
	if *jsonOut {
		printJSON(all)
		return
	}
	for _, s := range all {
		fmt.Printf("=== ODU %d — %s, %.0f HP, %s ===\n",
			s.ID, s.CompType, s.CapacityHP, phase(s.ThreePhase))
		fmt.Printf("  Ambient: %.1f°C   High pressure: %.3f MPa\n", s.AmbientTemp, s.HighPressure)
		fmt.Printf("  Discharge temps: A1 %.1f°C  A2 %.1f°C  (B1 %.1f°C)\n",
			s.DischargeA1, s.DischargeA2, s.DischargeB1)
		fmt.Printf("  Compressors: %.1f rps / %.1f rps   Fan: %d/255\n",
			s.Comp1SpeedRPS, s.Comp2SpeedRPS, s.FanSpeed)
		fmt.Printf("  EXV1: %d/2000  EXV2: %d/2000\n", s.EXV1Opening, s.EXV2Opening)
		fmt.Printf("  Protection: %s   Error: %s   [status=0x%04X type=0x%04X]\n\n",
			orDash(s.Protection), orDash(s.Error), s.StatusWord, s.TypeWord)
	}
}

func cmdWatch(args []string) {
	fs := flag.NewFlagSet("watch", flag.ExitOnError)
	connect := commonFlags(fs)
	interval := fs.Duration("interval", 3*time.Second, "poll interval")
	idu := fs.Int("idu", -1, "watch a single IDU id (default: all online)")
	fs.Parse(args)
	cfg, client := connect()
	defer client.Close()

	ids, err := resolveIDs(client, *idu)
	if err != nil {
		fatal("%v", err)
	}
	if len(ids) == 0 {
		fatal("no online IDUs to watch")
	}
	fmt.Printf("Watching %d IDU(s) every %s — poke a unit with its remote to identify it. Ctrl-C to stop.\n",
		len(ids), interval)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	tick := time.NewTicker(*interval)
	defer tick.Stop()

	prev := map[int]*rvf.IDUStatus{}
	for {
		statuses, err := client.ReadIDUs(ids)
		if err != nil {
			fmt.Printf("%s read error: %v\n", time.Now().Format("15:04:05"), err)
		} else {
			fillNames(cfg, statuses)
			for _, s := range sortedStatuses(statuses) {
				reportDiff(prev[s.ID], s)
				prev[s.ID] = s
			}
		}
		select {
		case <-tick.C:
		case <-sig:
			fmt.Println("\nbye")
			return
		}
	}
}

// reportDiff prints changed fields between two polls of one IDU.
func reportDiff(old, new_ *rvf.IDUStatus) {
	ts := time.Now().Format("15:04:05")
	label := fmt.Sprintf("IDU %d", new_.ID)
	if new_.Name != "" {
		label += " (" + new_.Name + ")"
	}
	if old == nil {
		fmt.Printf("%s %s: baseline — power %s, mode %s, set %d°C, fan %s, return %.1f°C%s\n",
			ts, label, onOff(new_.Power), new_.Mode, new_.Setpoint, new_.FanSet,
			new_.ReturnAirTemp, errSuffix(new_.Error))
		return
	}
	var changes []string
	add := func(cond bool, format string, args ...any) {
		if cond {
			changes = append(changes, fmt.Sprintf(format, args...))
		}
	}
	add(old.Power != new_.Power, "power %s→%s", onOff(old.Power), onOff(new_.Power))
	add(old.Mode != new_.Mode, "mode %s→%s", old.Mode, new_.Mode)
	add(old.Setpoint != new_.Setpoint, "setpoint %d→%d°C", old.Setpoint, new_.Setpoint)
	add(old.FanSet != new_.FanSet, "fan %s→%s", old.FanSet, new_.FanSet)
	add(old.FanActual != new_.FanActual, "fan-actual %s→%s", old.FanActual, new_.FanActual)
	add(old.ReturnAirTemp != new_.ReturnAirTemp, "return %.1f→%.1f°C", old.ReturnAirTemp, new_.ReturnAirTemp)
	add(old.EvapInletTemp != new_.EvapInletTemp, "evap-in %.1f→%.1f°C", old.EvapInletTemp, new_.EvapInletTemp)
	add(old.EvapMidTemp != new_.EvapMidTemp, "evap-mid %.1f→%.1f°C", old.EvapMidTemp, new_.EvapMidTemp)
	add(old.EXVOpening != new_.EXVOpening, "EXV %d→%d", old.EXVOpening, new_.EXVOpening)
	add(old.HeaterOn != new_.HeaterOn, "heater %s→%s", onOff(old.HeaterOn), onOff(new_.HeaterOn))
	add(old.WaterPumpOn != new_.WaterPumpOn, "pump %s→%s", onOff(old.WaterPumpOn), onOff(new_.WaterPumpOn))
	add(old.VSwing != new_.VSwing, "v-swing %s→%s", vswingStr(old.VSwing), vswingStr(new_.VSwing))
	add(old.HSwing != new_.HSwing, "h-swing %s→%s", onOff(old.HSwing), onOff(new_.HSwing))
	add(old.Error != new_.Error, "error %q→%q", old.Error, new_.Error)
	add(old.PowerDemandRaw != new_.PowerDemandRaw, "power-demand %d→%d", old.PowerDemandRaw, new_.PowerDemandRaw)
	if len(changes) > 0 {
		fmt.Printf("%s %s: %s\n", ts, label, strings.Join(changes, ", "))
	}
}

func cmdSet(args []string) {
	fs := flag.NewFlagSet("set", flag.ExitOnError)
	connect := commonFlags(fs)
	idu := fs.Int("idu", -1, "IDU id (required)")
	req, parse := setFlags(fs)
	fs.Parse(args)
	if *idu < 0 {
		fatal("set: --idu is required")
	}
	cfg, client := connect()
	defer client.Close()

	if err := parse(); err != nil {
		fatal("set: %v", err)
	}
	if err := client.SetIDU(*idu, *req); err != nil {
		fatal("set IDU %d: %v", *idu, err)
	}
	// Read back for confirmation.
	statuses, err := client.ReadIDUs([]int{*idu})
	if err != nil {
		fmt.Println("written OK (read-back failed:", err, ")")
		return
	}
	fillNames(cfg, statuses)
	if *jsonOut {
		printJSON(statuses[*idu])
		return
	}
	fmt.Println("written OK, current state:")
	printIDU(statuses[*idu])
}

func cmdAll(args []string) {
	fs := flag.NewFlagSet("all", flag.ExitOnError)
	connect := commonFlags(fs)
	req, parse := setFlags(fs)
	fs.Parse(args)
	_, client := connect()
	defer client.Close()

	if err := parse(); err != nil {
		fatal("all: %v", err)
	}
	if err := client.SetGroup(*req); err != nil {
		fatal("group write: %v", err)
	}
	fmt.Println("group command written OK")
}

// setFlags registers the state-change flags shared by `set` and `all`.
// The returned parse func must run after fs.Parse.
func setFlags(fs *flag.FlagSet) (*rvf.SetRequest, func() error) {
	power := fs.String("power", "", "on | off")
	mode := fs.String("mode", "", "cool | dry | fan | heat | auto | floor_heat | smart_floor_heat")
	fan := fs.String("fan", "", "low | medium | high | auto")
	temp := fs.Int("temp", 0, "setpoint °C (16-32)")
	vswing := fs.Int("vswing", -1, "vertical louver: 0-5 fixed/off, 6 = auto swing")
	hswing := fs.String("hswing", "", "horizontal louver: on | off")
	offLock := fs.String("off-lock", "", "on | off")
	modeLock := fs.String("mode-lock", "", "none | cool | heat")
	tempLock := fs.String("temp-lock", "", "none | 22 | 24 | 26")
	ctrlLock := fs.String("controller-lock", "", "on | off (blocks wired/IR controllers)")

	req := &rvf.SetRequest{}
	parse := func() error {
		if *power != "" {
			b, err := parseOnOff(*power)
			if err != nil {
				return err
			}
			req.Power = &b
		}
		if *mode != "" {
			m, err := rvf.ParseMode(*mode)
			if err != nil {
				return err
			}
			req.Mode = &m
		}
		if *fan != "" {
			f, err := rvf.ParseFan(*fan)
			if err != nil {
				return err
			}
			req.Fan = &f
		}
		if *temp != 0 {
			req.Temp = temp
		}
		if *vswing >= 0 {
			v := uint16(*vswing)
			req.VSwing = &v
		}
		if *hswing != "" {
			b, err := parseOnOff(*hswing)
			if err != nil {
				return err
			}
			req.HSwing = &b
		}
		if *offLock != "" {
			b, err := parseOnOff(*offLock)
			if err != nil {
				return err
			}
			req.OffLock = &b
		}
		if *modeLock != "" {
			var v uint16
			switch *modeLock {
			case "none":
				v = 0
			case "cool":
				v = 1
			case "heat":
				v = 2
			default:
				return fmt.Errorf("mode-lock must be none, cool or heat")
			}
			req.ModeLock = &v
		}
		if *tempLock != "" {
			var v uint16
			switch *tempLock {
			case "none":
				v = 0
			case "22":
				v = 1
			case "24":
				v = 2
			case "26":
				v = 3
			default:
				return fmt.Errorf("temp-lock must be none, 22, 24 or 26")
			}
			req.TempLock = &v
		}
		if *ctrlLock != "" {
			b, err := parseOnOff(*ctrlLock)
			if err != nil {
				return err
			}
			req.ControllerLock = &b
		}
		empty := rvf.SetRequest{}
		if *req == empty {
			return fmt.Errorf("nothing to do — pass at least one of --power/--mode/--fan/--temp/--vswing/...")
		}
		return nil
	}
	return req, parse
}

func cmdRaw(args []string) {
	if len(args) < 1 {
		fatal("raw: subcommand required: read-input | read-holding | write")
	}
	sub, rest := args[0], args[1:]
	fs := flag.NewFlagSet("raw "+sub, flag.ExitOnError)
	connect := commonFlags(fs)
	addr := fs.Uint("addr", 0, "register address (protocol address, 0-based)")
	count := fs.Uint("count", 1, "number of registers (reads)")
	value := fs.String("value", "", "value(s) to write, comma-separated, decimal or 0x hex")
	fs.Parse(rest)
	_, client := connect()
	defer client.Close()

	switch sub {
	case "read-input", "read-holding":
		var regs []uint16
		var err error
		if sub == "read-input" {
			regs, err = client.ReadInputs(uint16(*addr), uint16(*count))
		} else {
			regs, err = client.ReadHoldings(uint16(*addr), uint16(*count))
		}
		if err != nil {
			fatal("%v", err)
		}
		if *jsonOut {
			printJSON(regs)
			return
		}
		for i, r := range regs {
			fmt.Printf("%5d: %6d  0x%04X  0b%016b\n", int(*addr)+i, r, r, r)
		}
	case "write":
		if *value == "" {
			fatal("raw write: --value required")
		}
		var vals []uint16
		for _, p := range strings.Split(*value, ",") {
			v, err := strconv.ParseUint(strings.TrimSpace(p), 0, 16)
			if err != nil {
				fatal("raw write: bad value %q: %v", p, err)
			}
			vals = append(vals, uint16(v))
		}
		var err error
		if len(vals) == 1 {
			err = client.WriteRegister(uint16(*addr), vals[0])
		} else {
			err = client.WriteRegisters(uint16(*addr), vals)
		}
		if err != nil {
			fatal("%v", err)
		}
		fmt.Println("written OK")
	default:
		fatal("raw: unknown subcommand %q", sub)
	}
}

// --- helpers ---

func resolveIDs(client *rvf.Client, single int) ([]int, error) {
	if single >= 0 {
		return []int{single}, nil
	}
	_, ids, err := client.OnlineIDUs()
	return ids, err
}

func fillNames(cfg *config.Config, statuses map[int]*rvf.IDUStatus) {
	for id, s := range statuses {
		s.Name = cfg.Name(id)
	}
}

func sortedStatuses(m map[int]*rvf.IDUStatus) []*rvf.IDUStatus {
	out := make([]*rvf.IDUStatus, 0, len(m))
	for _, s := range m {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func printJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(v)
}

func parseOnOff(s string) (bool, error) {
	switch strings.ToLower(s) {
	case "on", "1", "true":
		return true, nil
	case "off", "0", "false":
		return false, nil
	}
	return false, fmt.Errorf("expected on/off, got %q", s)
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func errSuffix(e string) string {
	if e == "" {
		return ""
	}
	return ", ERROR: " + e
}

func vswingStr(v uint16) string {
	if v == 6 {
		return "auto"
	}
	return fmt.Sprintf("pos%d", v)
}

func phase(three bool) string {
	if three {
		return "3-phase"
	}
	return "1-phase"
}
