package collect

import (
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/Jackolix/Lotse/internal/protocol"
)

var procGetSystemPowerStatus = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetSystemPowerStatus")

// systemPowerStatus is SYSTEM_POWER_STATUS.
type systemPowerStatus struct {
	ACLineStatus        byte
	BatteryFlag         byte
	BatteryLifePercent  byte
	SystemStatusFlag    byte
	BatteryLifeTime     uint32
	BatteryFullLifeTime uint32
}

func battery() *protocol.Battery {
	var s systemPowerStatus
	if r, _, _ := procGetSystemPowerStatus.Call(uintptr(unsafe.Pointer(&s))); r == 0 {
		return nil
	}
	// Flag 128 means no system battery; 255 stands for "unknown" in both fields.
	if s.BatteryFlag&128 != 0 || s.BatteryFlag == 255 || s.BatteryLifePercent > 100 {
		return nil
	}
	b := &protocol.Battery{Percent: float64(s.BatteryLifePercent), State: "idle"}
	switch {
	case s.BatteryFlag&8 != 0:
		b.State = "charging"
	case s.ACLineStatus == 0:
		b.State = "discharging"
	}
	return b
}
