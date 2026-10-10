// Package sysinfo reads the media server Pi's own health for the web app's
// Info page: hardware, temperature, memory, storage and network.
//
// Every reading degrades to nil when unavailable (e.g. on a dev machine or in
// the Docker container, which has no Pi thermal zone or vcgencmd), so the page
// shows what it can instead of failing outright.
package sysinfo

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Root is prepended to every /proc and /sys path so tests can point it at a
// fake tree.
var Root = "/"

var startedAt = time.Now()

type Memory struct {
	TotalMB     int `json:"total"`
	AvailableMB int `json:"available"`
}

type Disk struct {
	Label   string  `json:"label"`
	Path    string  `json:"path"`
	TotalGB float64 `json:"total"`
	FreeGB  float64 `json:"free"`
}

type Address struct {
	Interface string `json:"interface"`
	Address   string `json:"address"`
}

type Software struct {
	Commit    string `json:"commit,omitempty"`
	Date      string `json:"date,omitempty"`
	Modified  bool   `json:"modified,omitempty"`
	GoVersion string `json:"go_version"`
}

type Snapshot struct {
	Hostname       string    `json:"hostname"`
	MDNSName       string    `json:"mdns_name"`
	Model          *string   `json:"model"`
	OS             *string   `json:"os"`
	Kernel         *string   `json:"kernel"`
	Arch           string    `json:"arch"`
	CPUCount       int       `json:"cpu_count"`
	Addresses      []Address `json:"addresses"`
	UptimeSeconds  *int64    `json:"uptime_seconds"`
	ServerUptime   int64     `json:"server_uptime_seconds"`
	LoadAverage    []float64 `json:"load_average"`
	CPUTemperature *float64  `json:"cpu_temperature_celsius"`
	UnderVoltage   *bool     `json:"under_voltage"`
	Throttled      *bool     `json:"throttled"`
	Memory         *Memory   `json:"memory_mb"`
	Swap           *Memory   `json:"swap_mb"`
	Disks          []Disk    `json:"disks"`
	Software       Software  `json:"software"`
}

// Dir is a directory whose filesystem's free space the page should show.
type Dir struct {
	Label string
	Path  string
}

// Collect gathers a snapshot. Directories on the same filesystem as an
// earlier one are listed once, under the earlier label.
func Collect(ctx context.Context, dirs []Dir) Snapshot {
	host, _ := os.Hostname()
	s := Snapshot{
		Hostname:       host,
		MDNSName:       strings.SplitN(host, ".", 2)[0] + ".local",
		Model:          readString("proc/device-tree/model"),
		OS:             osName(),
		Kernel:         readString("proc/sys/kernel/osrelease"),
		Arch:           runtime.GOARCH,
		CPUCount:       runtime.NumCPU(),
		Addresses:      addresses(),
		UptimeSeconds:  uptime(),
		ServerUptime:   int64(time.Since(startedAt).Seconds()),
		LoadAverage:    loadAverage(),
		CPUTemperature: cpuTemperature(),
		Disks:          disks(dirs),
		Software:       software(),
	}
	s.Memory, s.Swap = memory()
	s.UnderVoltage, s.Throttled = throttle(ctx)
	return s
}

func path(rel string) string { return filepath.Join(Root, rel) }

func readString(rel string) *string {
	data, err := os.ReadFile(path(rel))
	if err != nil {
		return nil
	}
	text := strings.Trim(string(data), "\x00\n ")
	if text == "" {
		return nil
	}
	return &text
}

func osName() *string {
	data, err := os.ReadFile(path("etc/os-release"))
	if err != nil {
		return nil
	}
	for _, line := range strings.Split(string(data), "\n") {
		if value, ok := strings.CutPrefix(line, "PRETTY_NAME="); ok {
			value = strings.Trim(value, `"`)
			return &value
		}
	}
	return nil
}

func uptime() *int64 {
	text := readString("proc/uptime")
	if text == nil {
		return nil
	}
	seconds, err := strconv.ParseFloat(strings.Fields(*text)[0], 64)
	if err != nil {
		return nil
	}
	whole := int64(seconds)
	return &whole
}

func loadAverage() []float64 {
	text := readString("proc/loadavg")
	if text == nil {
		return nil
	}
	fields := strings.Fields(*text)
	if len(fields) < 3 {
		return nil
	}
	load := make([]float64, 0, 3)
	for _, field := range fields[:3] {
		value, err := strconv.ParseFloat(field, 64)
		if err != nil {
			return nil
		}
		load = append(load, value)
	}
	return load
}

// cpuTemperature reads the SoC sensor (millidegrees C), the same value
// `vcgencmd measure_temp` reports.
func cpuTemperature() *float64 {
	text := readString("sys/class/thermal/thermal_zone0/temp")
	if text == nil {
		return nil
	}
	milli, err := strconv.Atoi(*text)
	if err != nil {
		return nil
	}
	celsius := float64(milli) / 1000
	return &celsius
}

func memory() (*Memory, *Memory) {
	text := readString("proc/meminfo")
	if text == nil {
		return nil, nil
	}
	values := map[string]int{}
	for _, line := range strings.Split(*text, "\n") {
		key, rest, ok := strings.Cut(line, ":")
		fields := strings.Fields(rest)
		if !ok || len(fields) == 0 {
			continue
		}
		if kb, err := strconv.Atoi(fields[0]); err == nil {
			values[key] = kb / 1024
		}
	}
	var mem, swap *Memory
	if total, ok := values["MemTotal"]; ok {
		if available, ok := values["MemAvailable"]; ok {
			mem = &Memory{TotalMB: total, AvailableMB: available}
		}
	}
	if total := values["SwapTotal"]; total > 0 {
		swap = &Memory{TotalMB: total, AvailableMB: values["SwapFree"]}
	}
	return mem, swap
}

func disks(dirs []Dir) []Disk {
	const gb = 1 << 30
	seen := map[uint64]bool{}
	found := []Disk{}
	for _, dir := range dirs {
		var st syscall.Statfs_t
		if dir.Path == "" || syscall.Statfs(dir.Path, &st) != nil {
			continue
		}
		var info syscall.Stat_t
		if syscall.Stat(dir.Path, &info) == nil {
			if seen[uint64(info.Dev)] {
				continue
			}
			seen[uint64(info.Dev)] = true
		}
		found = append(found, Disk{
			Label:   dir.Label,
			Path:    dir.Path,
			TotalGB: round1(float64(st.Blocks) * float64(st.Bsize) / gb),
			FreeGB:  round1(float64(st.Bavail) * float64(st.Bsize) / gb),
		})
	}
	return found
}

func round1(v float64) float64 { return float64(int64(v*10+0.5)) / 10 }

func addresses() []Address {
	found := []Address{}
	ifaces, err := net.Interfaces()
	if err != nil {
		return found
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, addr := range addrs {
			if ipNet, ok := addr.(*net.IPNet); ok && ipNet.IP.To4() != nil {
				found = append(found, Address{Interface: iface.Name, Address: ipNet.IP.String()})
			}
		}
	}
	return found
}

// throttle runs `vcgencmd get_throttled`: bit 0 is under-voltage now, bit 2
// is throttled now. Both nil off Pi hardware, or when the service user
// can't reach /dev/vchiq.
func throttle(ctx context.Context) (*bool, *bool) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "vcgencmd", "get_throttled").Output()
	if err != nil {
		return nil, nil
	}
	return parseThrottled(string(out))
}

func parseThrottled(out string) (*bool, *bool) {
	hex, ok := strings.CutPrefix(strings.TrimSpace(out), "throttled=0x")
	if !ok {
		return nil, nil
	}
	bits, err := strconv.ParseUint(hex, 16, 32)
	if err != nil {
		return nil, nil
	}
	under, throttled := bits&0x1 != 0, bits&0x4 != 0
	return &under, &throttled
}

// software reports the commit Go stamped into the binary. Builds from an
// rsynced checkout without .git (scripts/pi-publish.sh) carry none.
func software() Software {
	sw := Software{GoVersion: runtime.Version()}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return sw
	}
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			sw.Commit = setting.Value
			if len(sw.Commit) > 7 {
				sw.Commit = sw.Commit[:7]
			}
		case "vcs.time":
			sw.Date = setting.Value
		case "vcs.modified":
			sw.Modified = setting.Value == "true"
		}
	}
	return sw
}
