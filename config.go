package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type ServerConfig struct {
	Type          string            `yaml:"type,omitempty"`           // e.g. "github", "searxng", etc.
	MaxConcurrent int               `yaml:"max_concurrent,omitempty"` // max concurrent tool executions (default 50)
	Timeout       int               `yaml:"timeout,omitempty"`        // tool execution timeout in seconds (default 60s)
	Transport     string            `yaml:"transport"`                // "stdio" or "http"
	Command       string            `yaml:"command,omitempty"`
	Args          []string          `yaml:"args,omitempty"`
	Env           map[string]string `yaml:"env,omitempty"`
	EnvInherit    []string          `yaml:"env_inherit,omitempty"` // Explicitly inherited host env vars (e.g. HOME, USER)
	Headers       map[string]string `yaml:"headers,omitempty"`
	URL           string            `yaml:"url,omitempty"`
	Prefix        string            `yaml:"prefix"`
	AllowedAgents []string          `yaml:"allowed_agents,omitempty"`
	ToolsAllowlist      []string            `yaml:"tools_allowlist,omitempty"`
	AgentToolsAllowlist map[string][]string `yaml:"agent_tools_allowlist,omitempty"`
}

// GetType returns the explicit backend type or auto-detects from prefix/name
func (s *ServerConfig) GetType(serverName string) string {
	if s.Type != "" {
		return strings.ToLower(strings.TrimSpace(s.Type))
	}
	// Fallback auto-detection
	prefix := strings.ToLower(s.Prefix)
	name := strings.ToLower(serverName)
	if strings.HasPrefix(prefix, "gh-") || strings.Contains(name, "github") {
		return "github"
	}
	return "default"
}

// ExpandedCommand returns Command with environment variables expanded
func (s *ServerConfig) ExpandedCommand() string {
	return os.ExpandEnv(s.Command)
}

// ExpandedArgs returns Args with environment variables expanded
func (s *ServerConfig) ExpandedArgs() []string {
	if s.Args == nil {
		return nil
	}
	res := make([]string, len(s.Args))
	for i, arg := range s.Args {
		res[i] = os.ExpandEnv(arg)
	}
	return res
}

// ExpandedEnv returns sanitized process environment variables merged with ServerConfig.Env (expanded).
// It selectively inherits minimal safe baseline system variables (PATH, TMPDIR, etc.) without HOME or USER
// to prevent leaking user credentials (~/.ssh, ~/.aws, ~/.netrc) into child processes.
func (s *ServerConfig) ExpandedEnv() []string {
	safeBaseline := []string{
		"PATH", "TMPDIR", "TEMP", "TMP", "LANG", "LC_ALL", "TZ",
	}

	var env []string
	seen := make(map[string]bool)

	for _, key := range safeBaseline {
		if val, exists := os.LookupEnv(key); exists {
			env = append(env, fmt.Sprintf("%s=%s", key, val))
			seen[key] = true
		}
	}

	// Inherit any explicitly requested host variables
	for _, key := range s.EnvInherit {
		key = strings.TrimSpace(key)
		if key != "" && !seen[key] {
			if val, exists := os.LookupEnv(key); exists {
				env = append(env, fmt.Sprintf("%s=%s", key, val))
				seen[key] = true
			}
		}
	}

	for k, v := range s.Env {
		env = append(env, fmt.Sprintf("%s=%s", k, os.ExpandEnv(v)))
	}
	return env
}

// ExpandedURL returns URL with environment variables expanded
func (s *ServerConfig) ExpandedURL() string {
	return os.ExpandEnv(s.URL)
}

// ExpandedHeaders returns Headers with environment variables expanded for keys and values
func (s *ServerConfig) ExpandedHeaders() map[string]string {
	if s.Headers == nil {
		return nil
	}
	res := make(map[string]string, len(s.Headers))
	for k, v := range s.Headers {
		res[os.ExpandEnv(k)] = os.ExpandEnv(v)
	}
	return res
}

// GetCallTimeout returns the configured tool execution timeout or default 60s
func (s *ServerConfig) GetCallTimeout() time.Duration {
	if s.Timeout > 0 {
		return time.Duration(s.Timeout) * time.Second
	}
	return 60 * time.Second
}

// Equal checks whether two ServerConfig configurations are deeply equal
func (s *ServerConfig) Equal(other ServerConfig) bool {
	if s.Type != other.Type ||
		s.MaxConcurrent != other.MaxConcurrent ||
		s.Timeout != other.Timeout ||
		s.Transport != other.Transport ||
		s.Command != other.Command ||
		s.URL != other.URL ||
		s.Prefix != other.Prefix {
		return false
	}
	if len(s.Args) != len(other.Args) {
		return false
	}
	for i := range s.Args {
		if s.Args[i] != other.Args[i] {
			return false
		}
	}
	if len(s.Env) != len(other.Env) {
		return false
	}
	for k, v := range s.Env {
		if other.Env[k] != v {
			return false
		}
	}
	if len(s.Headers) != len(other.Headers) {
		return false
	}
	for k, v := range s.Headers {
		if other.Headers[k] != v {
			return false
		}
	}
	if len(s.EnvInherit) != len(other.EnvInherit) {
		return false
	}
	for i := range s.EnvInherit {
		if s.EnvInherit[i] != other.EnvInherit[i] {
			return false
		}
	}
	if len(s.AllowedAgents) != len(other.AllowedAgents) {
		return false
	}
	for i := range s.AllowedAgents {
		if s.AllowedAgents[i] != other.AllowedAgents[i] {
			return false
		}
	}
	if len(s.ToolsAllowlist) != len(other.ToolsAllowlist) {
		return false
	}
	for i := range s.ToolsAllowlist {
		if s.ToolsAllowlist[i] != other.ToolsAllowlist[i] {
			return false
		}
	}
	if len(s.AgentToolsAllowlist) != len(other.AgentToolsAllowlist) {
		return false
	}
	for agent, list := range s.AgentToolsAllowlist {
		otherList, ok := other.AgentToolsAllowlist[agent]
		if !ok || len(list) != len(otherList) {
			return false
		}
		for i := range list {
			if list[i] != otherList[i] {
				return false
			}
		}
	}
	return true
}

// GetToolsAllowlistForAgent returns the effective tools allowlist for a specific agent.
// If an agent-specific allowlist is configured, it takes precedence.
// Otherwise, it falls back to the backend-level ToolsAllowlist.
func (s *ServerConfig) GetToolsAllowlistForAgent(agentID string) []string {
	if s.AgentToolsAllowlist != nil {
		if list, ok := s.AgentToolsAllowlist[agentID]; ok {
			return list
		}
	}
	return s.ToolsAllowlist
}

type AuditConfig struct {
	Path         string `yaml:"path,omitempty"`
	FallbackPath string `yaml:"fallback_path,omitempty"`
	Required     bool   `yaml:"required,omitempty"`
}

type AuthConfig struct {
	Enabled         bool   `yaml:"enabled"`
	BearerToken     string `yaml:"bearer_token,omitempty"`
	AllowQueryToken bool   `yaml:"allow_query_token,omitempty"`
}

type TLSConfig struct {
	Enabled  bool   `yaml:"enabled"`
	CertFile string `yaml:"cert_file,omitempty"`
	KeyFile  string `yaml:"key_file,omitempty"`
}

type Config struct {
	Audit   AuditConfig             `yaml:"audit,omitempty"`
	Auth    AuthConfig              `yaml:"auth,omitempty"`
	TLS     TLSConfig               `yaml:"tls,omitempty"`
	Servers map[string]ServerConfig `yaml:"servers"`
}

// Validate checks the configuration for strict errors and fail-fast criteria
func (c *Config) Validate() error {
	if c.Auth.Enabled && strings.TrimSpace(c.Auth.BearerToken) == "" {
		return fmt.Errorf("configuration error: auth is enabled but 'bearer_token' is empty")
	}

	if c.TLS.Enabled {
		if strings.TrimSpace(c.TLS.CertFile) == "" || strings.TrimSpace(c.TLS.KeyFile) == "" {
			return fmt.Errorf("configuration error: tls is enabled but 'cert_file' or 'key_file' is missing")
		}
	}
	if len(c.Servers) == 0 {
		return fmt.Errorf("configuration error: no servers defined in 'servers' section")
	}

	prefixSeen := make(map[string]string)
	for name, srv := range c.Servers {
		name = strings.TrimSpace(name)
		if name == "" {
			return fmt.Errorf("configuration error: server name cannot be empty")
		}

		prefix := strings.TrimSpace(srv.Prefix)
		if prefix == "" {
			return fmt.Errorf("configuration error for server '%s': prefix is required and must be non-empty", name)
		}
		if !strings.HasSuffix(prefix, "_") && !strings.HasSuffix(prefix, "__") {
			return fmt.Errorf("configuration error for server '%s': prefix '%s' must end with '_' or '__' to avoid tool name collisions", name, prefix)
		}
		if other, exists := prefixSeen[prefix]; exists {
			return fmt.Errorf("configuration error: prefix '%s' is used by both server '%s' and server '%s'", prefix, other, name)
		}
		prefixSeen[prefix] = name

		transport := strings.ToLower(strings.TrimSpace(srv.Transport))
		if transport == "" {
			return fmt.Errorf("configuration error for server '%s': transport field is required", name)
		}

		if srv.Timeout < 0 {
			return fmt.Errorf("configuration error for server '%s': timeout cannot be negative", name)
		}

		switch transport {
		case "stdio":
			if strings.TrimSpace(srv.Command) == "" {
				return fmt.Errorf("configuration error for server '%s': command is required for 'stdio' transport", name)
			}
		case "http", "sse", "streamable_http", "streamable":
			if strings.TrimSpace(srv.URL) == "" {
				return fmt.Errorf("configuration error for server '%s': url is required for '%s' transport", name, srv.Transport)
			}
		default:
			return fmt.Errorf("configuration error for server '%s': invalid transport '%s' (must be 'stdio', 'http', 'sse', or 'streamable_http')", name, srv.Transport)
		}
	}

	return nil
}

func LoadConfig(filename string) (*Config, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file '%s': %w", filename, err)
	}

	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	decoder.KnownFields(true) // Fail fast on typos in YAML keys!

	var cfg Config
	if err := decoder.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("failed to parse YAML config '%s': %w", filename, err)
	}

	cfg.Audit.Path = os.ExpandEnv(cfg.Audit.Path)
	cfg.Audit.FallbackPath = os.ExpandEnv(cfg.Audit.FallbackPath)
	cfg.Auth.BearerToken = os.ExpandEnv(cfg.Auth.BearerToken)
	cfg.TLS.CertFile = os.ExpandEnv(cfg.TLS.CertFile)
	cfg.TLS.KeyFile = os.ExpandEnv(cfg.TLS.KeyFile)

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}
