// Package protocol defines how the hub and agents talk to each other.
//
// Transport: the agent dials the hub's WebSocket endpoint (AgentPath). Over that
// stream the agent runs an SSH server and the hub acts as the SSH client. Each side
// pins the other's Ed25519 key, so the link is mutually authenticated and encrypted
// even over plain ws://. The messages below travel as SSH global requests with JSON
// payloads. Remote shells use standard SSH "session" channels (RFC 4254).
package protocol

// Version is bumped on incompatible changes to the messages below.
const Version = 1

// AgentPath is the hub endpoint agents connect to.
const AgentPath = "/api/agent/connect"

// SSH global request types.
const (
	// ReqHello is the agent's first request (want reply). Payload Hello, reply HelloReply.
	ReqHello = "hello"
	// ReqMetrics carries one Metrics sample (want reply, which doubles as a liveness check).
	ReqMetrics = "metrics"
	// ReqInterval tells the agent how often to report (hub to agent). Payload IntervalMsg.
	ReqInterval = "interval"
	// ReqWake asks the agent to broadcast Wake-on-LAN magic packets (hub to agent, want reply). Payload WakeMsg.
	ReqWake = "wake"
	// ReqProcesses asks for the busiest processes (hub to agent, want reply). Payload
	// ProcessQuery, reply ProcessList.
	ReqProcesses = "processes"
	// ReqSignal stops a process (hub to agent, want reply). Payload SignalMsg, reply
	// ActionReply. Agents only accept it with FeatureShell.
	ReqSignal = "signal"
	// ReqPower reboots or shuts down the machine (hub to agent, want reply). Payload
	// PowerMsg, reply ActionReply. Agents only accept it with FeatureShell.
	ReqPower = "power"
	// ReqServices lists the system services (hub to agent, want reply). Reply ServiceList.
	ReqServices = "services"
	// ReqService starts, stops or restarts a service (hub to agent, want reply). Payload
	// ServiceMsg, reply ActionReply. Agents only accept it with FeatureShell.
	ReqService = "service"
)

// Channels and channel requests beyond RFC 4254. The agent accepts "session"
// channels only with FeatureShell; on them it serves a PTY shell, the SFTP subsystem
// (files) or ReqScript.
const (
	// SubsystemSFTP is the standard SFTP subsystem name on a "session" channel.
	SubsystemSFTP = "sftp"
	// ReqScript runs a script on a "session" channel without a PTY. Payload JSON
	// ScriptMsg. Output (stdout and stderr merged) is the channel data, followed by
	// "exit-status".
	ReqScript = "script@lotse"
	// ChanUpdate carries a new agent binary (hub to agent). Extra data: the JSON
	// update.Manifest, whose signature the agent checks before accepting. The hub
	// writes the binary and closes its side; the agent answers with an UpdateResult.
	ChanUpdate = "update@lotse"
)

// Optional agent features, announced in Hello.Features.
const (
	// FeatureShell: the agent accepts "session" channels with a PTY. Off unless the
	// machine's owner installed the agent with --allow-shell.
	FeatureShell = "shell"
	// FeatureWake: the agent can relay Wake-on-LAN packets into its networks.
	FeatureWake = "wake"
	// FeatureUpdate: the agent installs signed updates sent over ChanUpdate.
	FeatureUpdate = "update"
)

type Hello struct {
	Protocol     int        `json:"protocol"`
	AgentVersion string     `json:"agent_version"`
	Token        string     `json:"token,omitempty"` // enrollment token, only needed until the hub knows this agent's key
	Info         SystemInfo `json:"info"`
	Features     []string   `json:"features,omitempty"`
}

type HelloReply struct {
	OK       bool   `json:"ok"`
	Error    string `json:"error,omitempty"`
	Interval int    `json:"interval,omitempty"` // seconds between metric reports
}

// SystemInfo is static host information sent once per connection.
type SystemInfo struct {
	Hostname        string `json:"hostname"`
	OS              string `json:"os"` // runtime.GOOS
	Platform        string `json:"platform"`
	PlatformVersion string `json:"platform_version"`
	Kernel          string `json:"kernel"`
	Arch            string `json:"arch"`
	CPUModel        string `json:"cpu_model"`
	Cores           int    `json:"cores"`
	MemTotal        uint64 `json:"mem_total"`

	// Interfaces are the physical network interfaces; the hub keeps them to wake
	// the machine once it is offline.
	Interfaces []NetInterface `json:"interfaces,omitempty"`
}

type NetInterface struct {
	Name  string   `json:"name"`
	MAC   string   `json:"mac"`
	Addrs []string `json:"addrs"` // CIDR notation, e.g. 192.168.1.20/24
}

// Metrics is one sample. JSON keys match the hub's metric column names so the web UI
// can treat live samples and stored history the same way.
type Metrics struct {
	Time      int64   `json:"t"`   // agent clock, unix ms (informational; the hub timestamps on receipt)
	CPU       float64 `json:"cpu"` // percent, 0-100
	MemUsed   uint64  `json:"mem_used"`
	MemTotal  uint64  `json:"mem_total"`
	SwapUsed  uint64  `json:"swap_used"`
	SwapTotal uint64  `json:"swap_total"`
	DiskUsed  uint64  `json:"disk_used"` // primary filesystem ("/" or the system drive)
	DiskTotal uint64  `json:"disk_total"`
	DiskRead  float64 `json:"disk_read"` // bytes/s
	DiskWrite float64 `json:"disk_write"`
	NetRx     float64 `json:"net_rx"` // bytes/s, physical interfaces only
	NetTx     float64 `json:"net_tx"`
	Load1     float64 `json:"load1"` // always 0 on Windows
	Load5     float64 `json:"load5"`
	Load15    float64 `json:"load15"`
	Uptime    uint64  `json:"uptime"` // seconds

	Filesystems []Filesystem `json:"fs,omitempty"`
	Containers  []Container  `json:"containers,omitempty"` // when Docker or Podman runs on the host
}

// Container is one Docker/Podman container. Stats are zero unless it is running.
type Container struct {
	ID       string  `json:"id"` // short form
	Name     string  `json:"name"`
	Image    string  `json:"image"`
	State    string  `json:"state"`  // running, exited, paused, ...
	Status   string  `json:"status"` // e.g. "Up 3 hours (healthy)"
	CPU      float64 `json:"cpu"`    // percent of the whole machine
	Mem      uint64  `json:"mem"`
	MemLimit uint64  `json:"mem_limit"`
	NetRx    float64 `json:"net_rx"` // bytes/s
	NetTx    float64 `json:"net_tx"`
}

type ProcessQuery struct {
	Limit int `json:"limit"`
}

type Process struct {
	PID     int32   `json:"pid"`
	Name    string  `json:"name"`
	User    string  `json:"user,omitempty"`
	CPU     float64 `json:"cpu"` // percent of the whole machine
	Mem     uint64  `json:"mem"` // resident bytes
	Started int64   `json:"started,omitempty"`
	Command string  `json:"cmd,omitempty"` // only sent by agents that allow the shell
}

type ProcessList struct {
	Processes []Process `json:"processes"`
	Total     int       `json:"total"`
}

type SignalMsg struct {
	PID    int32  `json:"pid"`
	Signal string `json:"signal"` // "terminate" (SIGTERM; on Windows the same as kill) or "kill"
}

// ActionReply answers requests that change something on the machine.
type ActionReply struct {
	Error string `json:"error,omitempty"`
}

type PowerMsg struct {
	Action string `json:"action"` // "reboot" or "shutdown"
}

// Service is a system service: a systemd unit, launchd job or Windows service.
type Service struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	State       string `json:"state"`                // running, stopped, failed, starting, stopping
	Detail      string `json:"detail,omitempty"`     // the service manager's own state, e.g. "active (exited)"
	StartType   string `json:"start_type,omitempty"` // enabled, disabled, manual, static, ...
}

type ServiceList struct {
	Services []Service `json:"services"`
	Error    string    `json:"error,omitempty"` // e.g. no supported service manager
}

type ServiceMsg struct {
	Name   string `json:"name"`
	Action string `json:"action"` // start, stop, restart
}

// ScriptMsg asks the agent to run a script. Shell is sh, bash, powershell or cmd.
type ScriptMsg struct {
	Shell   string `json:"shell"`
	Script  string `json:"script"`
	Timeout int    `json:"timeout"` // seconds; the agent stops the script after this long
}

// UpdateResult is the agent's answer on a ChanUpdate channel.
type UpdateResult struct {
	Error string `json:"error,omitempty"`
}

type Filesystem struct {
	Mount  string `json:"mount"`
	Device string `json:"device"`
	Type   string `json:"type"`
	Used   uint64 `json:"used"`
	Total  uint64 `json:"total"`
}

type IntervalMsg struct {
	Seconds int `json:"seconds"`
}

// WakeMsg asks a relay agent to send magic packets for MACs to a broadcast address.
type WakeMsg struct {
	MACs      []string `json:"macs"`
	Broadcast string   `json:"broadcast"` // e.g. 192.168.1.255
}

// SSH payloads for "session" channels, encoded with ssh.Marshal (RFC 4254 section 6).
type (
	PtyRequest struct {
		Term    string
		Columns uint32
		Rows    uint32
		Width   uint32
		Height  uint32
		Modes   string
	}
	WindowChange struct {
		Columns uint32
		Rows    uint32
		Width   uint32
		Height  uint32
	}
	ExitStatus struct {
		Status uint32
	}
)
