// Package version holds build metadata shared by the hub and the agent.
package version

// Name prefixes service names, config directories and binary names.
const Name = "lotse"

// AgentName is the agent's binary, service and config directory name.
const AgentName = Name + "-agent"

// Version is set at build time with -ldflags "-X github.com/Jackolix/Lotse/internal/version.Version=...".
var Version = "dev"
