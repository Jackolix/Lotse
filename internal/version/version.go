// Package version holds build metadata shared by the hub and the agent.
package version

import "strings"

// Name prefixes service names, config directories and binary names.
const Name = "lotse"

// AgentName is the agent's binary, service and config directory name.
const AgentName = Name + "-agent"

// Repo is the GitHub repository that publishes releases and agent binaries.
const Repo = "Jackolix/Lotse"

// Version is set at build time with -ldflags "-X github.com/Jackolix/Lotse/internal/version.Version=...".
var Version = "dev"

// ReleaseAssetURL is where a release build publishes a file, or "" for dev builds.
func ReleaseAssetURL(file string) string {
	if Version == "" || Version == "dev" || !strings.ContainsAny(Version[:1], "0123456789") {
		return ""
	}
	return "https://github.com/" + Repo + "/releases/download/v" + Version + "/" + file
}
