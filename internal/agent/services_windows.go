package agent

import (
	"errors"
	"sort"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"

	"github.com/Jackolix/Lotse/internal/protocol"
	"github.com/Jackolix/Lotse/internal/version"
)

// listServices reads the Windows services from the service control manager.
func listServices() protocol.ServiceList {
	m, err := mgr.Connect()
	if err != nil {
		return protocol.ServiceList{Services: []protocol.Service{}, Error: err.Error()}
	}
	defer m.Disconnect()
	names, err := m.ListServices()
	if err != nil {
		return protocol.ServiceList{Services: []protocol.Service{}, Error: err.Error()}
	}
	list := make([]protocol.Service, 0, len(names))
	for _, name := range names {
		s, err := m.OpenService(name)
		if err != nil {
			continue
		}
		entry := protocol.Service{Name: name, State: "stopped"}
		if st, err := s.Query(); err == nil {
			entry.State, entry.Detail = windowsState(st.State)
		}
		if cfg, err := s.Config(); err == nil {
			entry.Description = cfg.DisplayName
			entry.StartType = startType(cfg)
		}
		s.Close()
		list = append(list, entry)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	return protocol.ServiceList{Services: list}
}

func windowsState(s svc.State) (state, detail string) {
	switch s {
	case svc.Running:
		return "running", "running"
	case svc.StartPending:
		return "starting", "start pending"
	case svc.StopPending:
		return "stopping", "stop pending"
	case svc.Paused, svc.PausePending, svc.ContinuePending:
		return "running", "paused"
	}
	return "stopped", "stopped"
}

func startType(cfg mgr.Config) string {
	switch cfg.StartType {
	case mgr.StartAutomatic:
		if cfg.DelayedAutoStart {
			return "automatic (delayed)"
		}
		return "automatic"
	case mgr.StartManual:
		return "manual"
	case mgr.StartDisabled:
		return "disabled"
	}
	return "system"
}

func controlService(name, action string) error {
	if err := checkServiceAction(name, action, version.AgentName); err != nil {
		return err
	}
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	s, err := m.OpenService(name)
	if err != nil {
		m.Disconnect()
		return err
	}
	switch action {
	case "start":
		err = s.Start()
	case "stop":
		_, err = s.Control(svc.Stop)
	case "restart":
		// Stopping can take a while; finish in the background so the hub gets an
		// answer right away.
		if _, err = s.Control(svc.Stop); err != nil && !errors.Is(err, windows.ERROR_SERVICE_NOT_ACTIVE) {
			break
		}
		go func() {
			defer m.Disconnect()
			defer s.Close()
			for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(250 * time.Millisecond) {
				if st, err := s.Query(); err != nil || st.State == svc.Stopped {
					break
				}
			}
			s.Start()
		}()
		return nil
	}
	s.Close()
	m.Disconnect()
	return err
}
