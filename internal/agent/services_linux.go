package agent

import (
	"bufio"
	"bytes"
	"os"
	"sort"
	"strings"

	"github.com/Jackolix/Lotse/internal/protocol"
	"github.com/Jackolix/Lotse/internal/version"
)

// listServices reads systemd's service units, including installed ones that are
// not loaded (usually disabled and stopped).
func listServices() protocol.ServiceList {
	if _, err := os.Stat("/run/systemd/system"); err != nil {
		return protocol.ServiceList{Services: []protocol.Service{}, Error: "service management needs systemd"}
	}
	units, err := runServiceCommand("systemctl", "list-units", "--type=service", "--all", "--no-legend", "--no-pager", "--plain")
	if err != nil {
		return protocol.ServiceList{Services: []protocol.Service{}, Error: err.Error()}
	}
	files, _ := runServiceCommand("systemctl", "list-unit-files", "--type=service", "--no-legend", "--no-pager")
	return protocol.ServiceList{Services: parseSystemd(units, files)}
}

// parseSystemd merges the output of "systemctl list-units" and "list-unit-files".
func parseSystemd(units, files []byte) []protocol.Service {
	byName := map[string]*protocol.Service{}
	sc := bufio.NewScanner(bytes.NewReader(units))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 4 || f[1] == "not-found" {
			continue
		}
		name, active, sub := f[0], f[2], f[3]
		byName[name] = &protocol.Service{
			Name:        name,
			Description: strings.Join(f[4:], " "),
			State:       systemdState(active, sub),
			Detail:      active + " (" + sub + ")",
		}
	}
	sc = bufio.NewScanner(bytes.NewReader(files))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 2 || strings.Contains(f[0], "@.") { // templates cannot run by themselves
			continue
		}
		s := byName[f[0]]
		if s == nil {
			s = &protocol.Service{Name: f[0], State: "stopped", Detail: "inactive (dead)"}
			byName[f[0]] = s
		}
		s.StartType = f[1]
	}
	out := make([]protocol.Service, 0, len(byName))
	for _, s := range byName {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func systemdState(active, sub string) string {
	switch active {
	case "active", "reloading":
		return "running"
	case "activating":
		return "starting"
	case "deactivating":
		return "stopping"
	case "failed":
		return "failed"
	}
	return "stopped"
}

func controlService(name, action string) error {
	if err := checkServiceAction(name, action, version.AgentName, version.AgentName+".service"); err != nil {
		return err
	}
	if !strings.Contains(name, ".") {
		name += ".service"
	}
	_, err := runServiceCommand("systemctl", "--no-block", action, "--", name)
	return err
}
