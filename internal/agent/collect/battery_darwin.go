package collect

import (
	"sync"

	"github.com/ebitengine/purego"

	"github.com/Jackolix/Lotse/internal/protocol"
)

// iops is IOKit's power source API, called through purego (as gopsutil does) so the
// agent still builds without cgo. Nothing is spawned, so it is fine on every sample.
var iops struct {
	once sync.Once
	ok   bool

	copyInfo    func() uintptr
	copyList    func(blob uintptr) uintptr
	description func(blob, source uintptr) uintptr

	arrayCount  func(array uintptr) int
	arrayValue  func(array uintptr, i int) uintptr
	dictValue   func(dict, key uintptr) uintptr
	newString   func(alloc uintptr, s string, encoding uint32) uintptr
	equal       func(a, b uintptr) bool
	numberValue func(number uintptr, kind int, out *int64) bool
	boolValue   func(b uintptr) bool
	release     func(ref uintptr)

	// Dictionary keys and values, created once and kept.
	keyType, keyPresent, keyCurrent, keyMax, keyCharging, keyState uintptr
	internalBattery, acPower                                       uintptr
}

func loadIOPS() {
	const flags = purego.RTLD_LAZY | purego.RTLD_GLOBAL
	cf, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", flags)
	if err != nil {
		return
	}
	iokit, err := purego.Dlopen("/System/Library/Frameworks/IOKit.framework/IOKit", flags)
	if err != nil {
		return
	}
	// RegisterLibFunc panics on a missing symbol; these have all been there since 10.3.
	defer func() { iops.ok = recover() == nil }()
	purego.RegisterLibFunc(&iops.copyInfo, iokit, "IOPSCopyPowerSourcesInfo")
	purego.RegisterLibFunc(&iops.copyList, iokit, "IOPSCopyPowerSourcesList")
	purego.RegisterLibFunc(&iops.description, iokit, "IOPSGetPowerSourceDescription")
	purego.RegisterLibFunc(&iops.arrayCount, cf, "CFArrayGetCount")
	purego.RegisterLibFunc(&iops.arrayValue, cf, "CFArrayGetValueAtIndex")
	purego.RegisterLibFunc(&iops.dictValue, cf, "CFDictionaryGetValue")
	purego.RegisterLibFunc(&iops.newString, cf, "CFStringCreateWithCString")
	purego.RegisterLibFunc(&iops.equal, cf, "CFEqual")
	purego.RegisterLibFunc(&iops.numberValue, cf, "CFNumberGetValue")
	purego.RegisterLibFunc(&iops.boolValue, cf, "CFBooleanGetValue")
	purego.RegisterLibFunc(&iops.release, cf, "CFRelease")

	const utf8 = 0x08000100 // kCFStringEncodingUTF8
	str := func(s string) uintptr { return iops.newString(0, s, utf8) }
	iops.keyType, iops.internalBattery = str("Type"), str("InternalBattery")
	iops.keyPresent = str("Is Present")
	iops.keyCurrent, iops.keyMax = str("Current Capacity"), str("Max Capacity")
	iops.keyCharging = str("Is Charging")
	iops.keyState, iops.acPower = str("Power Source State"), str("AC Power")
}

func battery() *protocol.Battery {
	iops.once.Do(loadIOPS)
	if !iops.ok {
		return nil
	}
	blob := iops.copyInfo()
	if blob == 0 {
		return nil
	}
	defer iops.release(blob)
	list := iops.copyList(blob)
	if list == 0 {
		return nil
	}
	defer iops.release(list)

	// CoreFoundation crashes on NULL, so every lookup is checked before use.
	is := func(dict, key, want uintptr) bool {
		v := iops.dictValue(dict, key)
		return v != 0 && iops.equal(v, want)
	}
	flag := func(dict, key uintptr, missing bool) bool {
		if v := iops.dictValue(dict, key); v != 0 {
			return iops.boolValue(v)
		}
		return missing
	}
	number := func(dict, key uintptr) int64 {
		const sint64 = 4 // kCFNumberSInt64Type
		var n int64
		if v := iops.dictValue(dict, key); v == 0 || !iops.numberValue(v, sint64, &n) {
			return 0
		}
		return n
	}

	var current, capacity int64
	charging, external := false, false
	for i := range iops.arrayCount(list) {
		// The description belongs to the blob; it is not released here.
		d := iops.description(blob, iops.arrayValue(list, i))
		// A UPS is a power source too, but not this machine's battery.
		if d == 0 || !is(d, iops.keyType, iops.internalBattery) || !flag(d, iops.keyPresent, true) {
			continue
		}
		full := number(d, iops.keyMax)
		if full <= 0 {
			continue
		}
		current += number(d, iops.keyCurrent)
		capacity += full
		charging = charging || flag(d, iops.keyCharging, false)
		external = external || is(d, iops.keyState, iops.acPower)
	}
	if capacity == 0 {
		return nil
	}
	b := &protocol.Battery{Percent: min(100, float64(current)/float64(capacity)*100), State: "discharging"}
	switch {
	case charging:
		b.State = "charging"
	case external:
		b.State = "idle"
	}
	return b
}
