package anvil

import (
	"github.com/anvil-dev/anvil/internal/settings"
)

// mergeMCPFromSources loads file MCP layers and merges with inline MCPServers.
// Precedence (high → low): inline Create options → project → user.
func mergeMCPFromSources(cwd string, opts Options) (Options, error) {
	merged := map[string]MCPServerConfig{}
	if opts.Local.HasSettingSource(SettingSourceUser) {
		path, err := settings.UserMCPPath()
		if err != nil {
			return opts, newError("resolve user mcp path", false, err)
		}
		servers, err := settings.LoadMCPJSON(path)
		if err != nil {
			return opts, newError("load user mcp.json", false, err)
		}
		for name, s := range servers {
			merged[name] = mcpFromSettings(s)
		}
	}
	if opts.Local.HasSettingSource(SettingSourceProject) {
		servers, err := settings.LoadMCPJSON(settings.ProjectMCPPath(cwd))
		if err != nil {
			return opts, newError("load project mcp.json", false, err)
		}
		for name, s := range servers {
			merged[name] = mcpFromSettings(s)
		}
	}
	for name, cfg := range opts.MCPServers {
		merged[name] = cfg
	}
	opts.MCPServers = merged
	return opts, nil
}

func mcpFromSettings(s settings.MCPServer) MCPServerConfig {
	cfg := MCPServerConfig{
		Command: s.Command,
		Args:    s.Args,
		Env:     settings.EnvSlice(s.Env),
		URL:     s.URL,
		Headers: s.Headers,
	}
	if cfg.URL != "" && cfg.Command == "" {
		cfg.Transport = "http"
	}
	return cfg
}
