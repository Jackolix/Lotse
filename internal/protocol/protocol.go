// Package protocol defines how the hub and agents talk to each other.
//
// Transport: the agent dials the hub's WebSocket endpoint (AgentPath). Over that
// stream the agent runs an SSH server and the hub acts as the SSH client. Each side
// pins the other's Ed25519 key, so the link is mutually authenticated and encrypted
// even over plain ws://. The messages below travel as SSH global requests with JSON
// payloads; later phases add SSH channels for shells and file transfer.
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
)

type Hello struct {
	Protocol     int        `json:"protocol"`
	AgentVersion string     `json:"agent_version"`
	Token        string     `json:"token,omitempty"` // enrollment token, only needed until the hub knows this agent's key
	Info         SystemInfo `json:"info"`
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
