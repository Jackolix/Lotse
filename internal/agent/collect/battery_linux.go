package collect

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Jackolix/Lotse/internal/protocol"
)

func battery() *protocol.Battery {
	return batteryFrom("/sys/class/power_supply")
}

// batteryFrom reads the batteries below a power_supply directory. Laptops with two
// batteries get one value, weighted by capacity.
func batteryFrom(root string) *protocol.Battery {
	dirs, _ := filepath.Glob(filepath.Join(root, "*"))
	var sum, weight float64
	state := "idle"
	for _, dir := range dirs {
		read := func(name string) string {
			b, _ := os.ReadFile(filepath.Join(dir, name))
			return strings.TrimSpace(string(b))
		}
		// Scope "Device" is the battery of a mouse, keyboard or headset.
		if read("type") != "Battery" || read("scope") == "Device" || read("present") == "0" {
			continue
		}
		percent, err := strconv.ParseFloat(read("capacity"), 64)
		if err != nil {
			continue
		}
		w := 1.0
		for _, name := range []string{"energy_full", "charge_full"} {
			if full, err := strconv.ParseFloat(read(name), 64); err == nil && full > 0 {
				w = full
				break
			}
		}
		sum += percent * w
		weight += w
		switch read("status") {
		case "Charging":
			state = "charging"
		case "Discharging":
			if state != "charging" {
				state = "discharging"
			}
		}
	}
	if weight == 0 {
		return nil
	}
	return &protocol.Battery{Percent: min(100, sum/weight), State: state}
}
