package agent

import (
	"bufio"
	"bytes"
	"sort"
	"strings"

	"github.com/Jackolix/Lotse/internal/protocol"
	"github.com/Jackolix/Lotse/internal/version"
)

// listServices reads the jobs launchd runs in the system domain.
func listServices() protocol.ServiceList {
	out, err := runServiceCommand("launchctl", "list")
	if err != nil {
		return protocol.ServiceList{Services: []protocol.Service{}, Error: err.Error()}
	}
	list := []protocol.Service{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Scan() // header: PID Status Label
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) != 3 {
			continue
		}
		pid, status, label := f[0], f[1], f[2]
		s := protocol.Service{Name: label, State: "stopped"}
		switch {
		case pid != "-":
			s.State, s.Detail = "running", "pid "+pid
		case status != "0":
			s.State, s.Detail = "failed", "last exit status "+status
		}
		list = append(list, s)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	return protocol.ServiceList{Services: list}
}

func controlService(name, action string) error {
	if err := checkServiceAction(name, action, version.AgentName); err != nil {
		return err
	}
	target := "system/" + name
	var err error
	switch action {
	case "start":
		_, err = runServiceCommand("launchctl", "kickstart", target)
	case "restart":
		_, err = runServiceCommand("launchctl", "kickstart", "-k", target)
	case "stop":
		_, err = runServiceCommand("launchctl", "kill", "SIGTERM", target)
	}
	return err
}
