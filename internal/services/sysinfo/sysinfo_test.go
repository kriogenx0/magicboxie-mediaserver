package sysinfo

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, root, rel, body string) {
	t.Helper()
	full := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCollectReadsProcAndSys(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "proc/device-tree/model", "Raspberry Pi 4 Model B Rev 1.4\x00")
	writeFile(t, root, "proc/uptime", "3725.41 12000.00\n")
	writeFile(t, root, "proc/loadavg", "0.52 0.40 0.31 1/123 4567\n")
	writeFile(t, root, "proc/meminfo", "MemTotal:        3884120 kB\nMemFree:  100 kB\nMemAvailable:    2048000 kB\nSwapTotal:        102396 kB\nSwapFree:          51200 kB\n")
	writeFile(t, root, "sys/class/thermal/thermal_zone0/temp", "48312\n")
	writeFile(t, root, "etc/os-release", "NAME=\"Debian\"\nPRETTY_NAME=\"Debian GNU/Linux 12 (bookworm)\"\n")
	old := Root
	Root = root
	defer func() { Root = old }()

	dir := t.TempDir()
	s := Collect(context.Background(), []Dir{{Label: "Movies", Path: dir}, {Label: "Same disk", Path: dir}, {Label: "Missing", Path: filepath.Join(dir, "nope")}})

	if s.Model == nil || *s.Model != "Raspberry Pi 4 Model B Rev 1.4" {
		t.Errorf("model = %v", s.Model)
	}
	if s.OS == nil || *s.OS != "Debian GNU/Linux 12 (bookworm)" {
		t.Errorf("os = %v", s.OS)
	}
	if s.UptimeSeconds == nil || *s.UptimeSeconds != 3725 {
		t.Errorf("uptime = %v", s.UptimeSeconds)
	}
	if len(s.LoadAverage) != 3 || s.LoadAverage[0] != 0.52 {
		t.Errorf("load = %v", s.LoadAverage)
	}
	if s.CPUTemperature == nil || *s.CPUTemperature != 48.312 {
		t.Errorf("temp = %v", s.CPUTemperature)
	}
	if s.Memory == nil || s.Memory.TotalMB != 3793 || s.Memory.AvailableMB != 2000 {
		t.Errorf("memory = %+v", s.Memory)
	}
	if s.Swap == nil || s.Swap.TotalMB != 99 || s.Swap.AvailableMB != 50 {
		t.Errorf("swap = %+v", s.Swap)
	}
	if len(s.Disks) != 1 || s.Disks[0].Label != "Movies" {
		t.Errorf("disks = %+v", s.Disks)
	}
}

func TestCollectWithoutPiFiles(t *testing.T) {
	old := Root
	Root = t.TempDir()
	defer func() { Root = old }()

	s := Collect(context.Background(), nil)
	if s.Model != nil || s.CPUTemperature != nil || s.Memory != nil || s.UptimeSeconds != nil || s.LoadAverage != nil {
		t.Errorf("expected nil readings, got %+v", s)
	}
}

func TestParseThrottled(t *testing.T) {
	for _, tc := range []struct {
		out              string
		under, throttled bool
	}{
		{"throttled=0x0\n", false, false},
		{"throttled=0x50005\n", true, true},
		{"throttled=0x50000\n", false, false},
	} {
		under, throttled := parseThrottled(tc.out)
		if under == nil || *under != tc.under || *throttled != tc.throttled {
			t.Errorf("%q -> %v %v", tc.out, under, throttled)
		}
	}
	if under, _ := parseThrottled("garbage"); under != nil {
		t.Error("garbage should parse to nil")
	}
}
