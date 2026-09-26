package agent

import "testing"

func TestParseSystemd(t *testing.T) {
	units := []byte(`accounts-daemon.service loaded active running Accounts Service
nginx.service           loaded failed failed  A high performance web server
ghost.service           not-found inactive dead ghost.service
oneshot.service         loaded active exited  Runs once
`)
	files := []byte(`accounts-daemon.service enabled enabled
nginx.service enabled enabled
getty@.service enabled enabled
docker.service disabled enabled
`)
	got := parseSystemd(units, files)
	want := map[string][3]string{ // state, detail, start type
		"accounts-daemon.service": {"running", "active (running)", "enabled"},
		"nginx.service":           {"failed", "failed (failed)", "enabled"},
		"oneshot.service":         {"running", "active (exited)", ""},
		"docker.service":          {"stopped", "inactive (dead)", "disabled"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d services: %+v", len(got), got)
	}
	for _, s := range got {
		w, ok := want[s.Name]
		if !ok || s.State != w[0] || s.Detail != w[1] || s.StartType != w[2] {
			t.Errorf("%s: %+v, want %v", s.Name, s, w)
		}
	}
	if got[0].Name != "accounts-daemon.service" || got[0].Description != "Accounts Service" {
		t.Errorf("first service: %+v", got[0])
	}
}

func TestServiceActionsAreValidated(t *testing.T) {
	for _, tc := range []struct{ name, action string }{
		{"nginx", "reload"},
		{"--help", "start"},
		{"../etc/passwd", "stop"},
		{"lotse-agent", "stop"},
		{"", "start"},
		{"*", "stop"}, // would stop every loaded service, the agent included
		{"lotse-agen?", "stop"},
		{"ssh[d]", "restart"},
	} {
		if err := checkServiceAction(tc.name, tc.action, "lotse-agent"); err == nil {
			t.Errorf("%s %q was accepted", tc.action, tc.name)
		}
	}
	if err := checkServiceAction("nginx.service", "restart"); err != nil {
		t.Errorf("valid action refused: %v", err)
	}
}
