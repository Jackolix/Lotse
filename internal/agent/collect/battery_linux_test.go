package collect

import (
	"os"
	"path/filepath"
	"testing"
)

func writeSupply(t *testing.T, root, name string, files map[string]string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for f, v := range files {
		if err := os.WriteFile(filepath.Join(dir, f), []byte(v+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestBatteryNone(t *testing.T) {
	root := t.TempDir()
	if b := batteryFrom(root); b != nil {
		t.Fatalf("empty directory: got %+v", b)
	}
	writeSupply(t, root, "AC", map[string]string{"type": "Mains", "online": "1"})
	writeSupply(t, root, "hidpp_battery_0", map[string]string{
		"type": "Battery", "scope": "Device", "capacity": "55", "status": "Discharging",
	})
	writeSupply(t, root, "BAT1", map[string]string{"type": "Battery", "present": "0", "capacity": "0"})
	if b := batteryFrom(root); b != nil {
		t.Fatalf("mains, a mouse and an empty bay: got %+v", b)
	}
}

func TestBatterySingle(t *testing.T) {
	for status, want := range map[string]string{
		"Charging": "charging", "Discharging": "discharging", "Full": "idle", "Not charging": "idle", "Unknown": "idle",
	} {
		root := t.TempDir()
		writeSupply(t, root, "BAT0", map[string]string{
			"type": "Battery", "present": "1", "capacity": "64", "status": status, "energy_full": "50000000",
		})
		b := batteryFrom(root)
		if b == nil || b.Percent != 64 || b.State != want {
			t.Errorf("status %q: got %+v, want 64%% %s", status, b, want)
		}
	}
}

func TestBatteryTwo(t *testing.T) {
	root := t.TempDir()
	writeSupply(t, root, "BAT0", map[string]string{
		"type": "Battery", "capacity": "100", "status": "Full", "energy_full": "20000000",
	})
	writeSupply(t, root, "BAT1", map[string]string{
		"type": "Battery", "capacity": "40", "status": "Charging", "energy_full": "60000000",
	})
	b := batteryFrom(root)
	if b == nil || b.Percent != 55 || b.State != "charging" {
		t.Fatalf("got %+v, want 55%% charging", b)
	}
}
