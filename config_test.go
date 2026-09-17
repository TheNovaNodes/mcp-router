package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestConfigValidation_AuthAndTLS(t *testing.T) {
	t.Run("auth enabled without bearer token", func(t *testing.T) {
		cfg := &Config{
			Auth: AuthConfig{Enabled: true, BearerToken: "  "},
			Servers: map[string]ServerConfig{
				"s1": {Transport: "stdio", Command: "echo"},
			},
		}
		if err := cfg.Validate(); err == nil {
			t.Errorf("expected error when auth is enabled with empty token")
		}
	})

	t.Run("tls enabled without cert or key", func(t *testing.T) {
		cfg := &Config{
			TLS: TLSConfig{Enabled: true, CertFile: "", KeyFile: "key.pem"},
			Servers: map[string]ServerConfig{
				"s1": {Transport: "stdio", Command: "echo"},
			},
		}
		if err := cfg.Validate(); err == nil {
			t.Errorf("expected error when tls is enabled without cert file")
		}
	})

	t.Run("empty servers map", func(t *testing.T) {
		cfg := &Config{
			Servers: map[string]ServerConfig{},
		}
		if err := cfg.Validate(); err == nil {
			t.Errorf("expected error when no servers defined")
		}
	})

	t.Run("http transport without url", func(t *testing.T) {
		cfg := &Config{
			Servers: map[string]ServerConfig{
				"h1": {Transport: "http", URL: "", Prefix: "h1__"},
			},
		}
		if err := cfg.Validate(); err == nil {
			t.Errorf("expected error when http transport has no url")
		}
	})

	t.Run("tls enabled without key file", func(t *testing.T) {
		cfg := &Config{
			TLS: TLSConfig{Enabled: true, CertFile: "cert.pem", KeyFile: "  "},
			Servers: map[string]ServerConfig{
				"s1": {Transport: "stdio", Command: "echo", Prefix: "s1__"},
			},
		}
		if err := cfg.Validate(); err == nil {
			t.Errorf("expected error when tls is enabled without key file")
		}
	})

	t.Run("empty server name", func(t *testing.T) {
		cfg := &Config{
			Servers: map[string]ServerConfig{
				"   ": {Transport: "stdio", Command: "echo", Prefix: "s1__"},
			},
		}
		if err := cfg.Validate(); err == nil {
			t.Errorf("expected error for whitespace server name")
		}
	})

	t.Run("empty transport field", func(t *testing.T) {
		cfg := &Config{
			Servers: map[string]ServerConfig{
				"s1": {Transport: "   ", Command: "echo", Prefix: "s1__"},
			},
		}
		if err := cfg.Validate(); err == nil {
			t.Errorf("expected error for empty transport")
		}
	})

	t.Run("invalid transport field", func(t *testing.T) {
		cfg := &Config{
			Servers: map[string]ServerConfig{
				"s1": {Transport: "grpc", Command: "echo", Prefix: "s1__"},
			},
		}
		if err := cfg.Validate(); err == nil {
			t.Errorf("expected error for invalid transport")
		}
	})

	t.Run("stdio without command", func(t *testing.T) {
		cfg := &Config{
			Servers: map[string]ServerConfig{
				"s1": {Transport: "stdio", Command: "   ", Prefix: "s1__"},
			},
		}
		if err := cfg.Validate(); err == nil {
			t.Errorf("expected error when stdio transport has no command")
		}
	})

	t.Run("valid http and stdio config", func(t *testing.T) {
		cfg := &Config{
			Auth: AuthConfig{Enabled: true, BearerToken: "secret"},
			TLS:  TLSConfig{Enabled: true, CertFile: "cert.pem", KeyFile: "key.pem"},
			Servers: map[string]ServerConfig{
				"s1": {Transport: "stdio", Command: "ls", Prefix: "s1__"},
				"h1": {Transport: "http", URL: "http://localhost:8080", Prefix: "h1__"},
			},
		}
		if err := cfg.Validate(); err != nil {
			t.Errorf("unexpected validation error: %v", err)
		}
	})

	t.Run("streamable_http without url", func(t *testing.T) {
		cfg := &Config{
			Servers: map[string]ServerConfig{
				"s1": {Transport: "streamable_http", URL: "  ", Prefix: "s1__"},
			},
		}
		if err := cfg.Validate(); err == nil {
			t.Errorf("expected error when streamable_http transport has no url")
		}
	})

	t.Run("valid streamable_http and sse config", func(t *testing.T) {
		cfg := &Config{
			Servers: map[string]ServerConfig{
				"s1": {Transport: "streamable_http", URL: "https://mcp.context7.com/mcp", Prefix: "s1__"},
				"s2": {Transport: "sse", URL: "https://api.example.com/sse", Prefix: "s2__"},
			},
		}
		if err := cfg.Validate(); err != nil {
			t.Errorf("unexpected validation error: %v", err)
		}
	})
}

func TestLoadConfig_FileErrors(t *testing.T) {
	t.Run("non-existent file", func(t *testing.T) {
		_, err := LoadConfig("/non/existent/path/config.yaml")
		if err == nil {
			t.Errorf("expected error reading non-existent file")
		}
	})

	t.Run("invalid yaml syntax", func(t *testing.T) {
		tmpDir := t.TempDir()
		tmpFile := filepath.Join(tmpDir, "invalid.yaml")
		os.WriteFile(tmpFile, []byte("servers: [invalid_yaml"), 0644)

		_, err := LoadConfig(tmpFile)
		if err == nil {
			t.Errorf("expected error parsing invalid YAML")
		}
	})

	t.Run("unknown yaml fields rejected by KnownFields", func(t *testing.T) {
		tmpDir := t.TempDir()
		tmpFile := filepath.Join(tmpDir, "unknown_field.yaml")
		content := `
unknown_key: "disallowed"
servers:
  s1:
    transport: stdio
    command: echo
`
		os.WriteFile(tmpFile, []byte(content), 0644)

		_, err := LoadConfig(tmpFile)
		if err == nil {
			t.Errorf("expected error for unknown YAML field")
		}
	})
}

func TestBackend_GetType(t *testing.T) {
	explicitConfig := ServerConfig{Type: "github", Prefix: "custom__"}
	if explicitConfig.GetType("my-server") != "github" {
		t.Errorf("expected explicit type 'github', got '%s'", explicitConfig.GetType("my-server"))
	}

	autoPrefixConfig := ServerConfig{Prefix: "gh-custom__"}
	if autoPrefixConfig.GetType("random-name") != "github" {
		t.Errorf("expected auto-detected type 'github' from prefix, got '%s'", autoPrefixConfig.GetType("random-name"))
	}

	autoNameConfig := ServerConfig{Prefix: "mcp__"}
	if autoNameConfig.GetType("github-doctormes") != "github" {
		t.Errorf("expected auto-detected type 'github' from server name, got '%s'", autoNameConfig.GetType("github-doctormes"))
	}

	defaultConfig := ServerConfig{Prefix: "searxng__"}
	if defaultConfig.GetType("searxng-control") != "default" {
		t.Errorf("expected 'default' type, got '%s'", defaultConfig.GetType("searxng-control"))
	}
}

func TestMultiplexer_Semaphore(t *testing.T) {
	cfg := &Config{
		Servers: map[string]ServerConfig{
			"limited": {
				MaxConcurrent: 5,
				Transport:     "stdio",
				Command:       "echo",
				Prefix:        "lim__",
			},
			"default_capacity": {
				Transport: "stdio",
				Command:   "echo",
				Prefix:    "def__",
			},
		},
	}

	mux := NewMultiplexer(cfg)
	if cap(mux.backends["limited"].Semaphore) != 5 {
		t.Errorf("expected limited backend semaphore capacity 5, got %d", cap(mux.backends["limited"].Semaphore))
	}

	if cap(mux.backends["default_capacity"].Semaphore) != 50 {
		t.Errorf("expected default backend semaphore capacity 50, got %d", cap(mux.backends["default_capacity"].Semaphore))
	}
}

func TestServerConfig_EnvironmentExpansion(t *testing.T) {
	t.Setenv("TEST_ROUTER_CMD", "python3")
	t.Setenv("TEST_ROUTER_ARG", "--verbose")
	t.Setenv("TEST_ROUTER_ENV_VAL", "secret123")
	t.Setenv("TEST_ROUTER_URL", "https://api.example.com/mcp")
	t.Setenv("TEST_ROUTER_HEADER_VAL", "Bearer token999")

	cfg := ServerConfig{
		Command: "${TEST_ROUTER_CMD}",
		Args:    []string{"-m", "${TEST_ROUTER_ARG}"},
		Env: map[string]string{
			"API_KEY": "${TEST_ROUTER_ENV_VAL}",
		},
		URL: "${TEST_ROUTER_URL}",
		Headers: map[string]string{
			"Authorization": "${TEST_ROUTER_HEADER_VAL}",
		},
	}

	if cmd := cfg.ExpandedCommand(); cmd != "python3" {
		t.Errorf("expected expanded command 'python3', got '%s'", cmd)
	}

	args := cfg.ExpandedArgs()
	if len(args) != 2 || args[1] != "--verbose" {
		t.Errorf("expected expanded args ['-m', '--verbose'], got %v", args)
	}

	envList := cfg.ExpandedEnv()
	foundEnv := false
	for _, e := range envList {
		if e == "API_KEY=secret123" {
			foundEnv = true
			break
		}
	}
	if !foundEnv {
		t.Errorf("expected expanded env to contain 'API_KEY=secret123'")
	}

	if url := cfg.ExpandedURL(); url != "https://api.example.com/mcp" {
		t.Errorf("expected expanded URL 'https://api.example.com/mcp', got '%s'", url)
	}

	headers := cfg.ExpandedHeaders()
	if headers["Authorization"] != "Bearer token999" {
		t.Errorf("expected expanded Authorization header 'Bearer token999', got '%s'", headers["Authorization"])
	}
}

func TestServerConfig_EnvironmentExpansion_NilOrEmpty(t *testing.T) {
	cfg := ServerConfig{}
	if cfg.ExpandedArgs() != nil {
		t.Errorf("expected nil for empty Args")
	}
	if cfg.ExpandedHeaders() != nil {
		t.Errorf("expected nil for empty Headers")
	}
}

func TestServerConfig_ExpandedEnv_SecretIsolation(t *testing.T) {
	// Set an unrelated daemon secret in the host process environment
	t.Setenv("MCP_ROUTER_BEARER_TOKEN", "super-secret-daemon-token-xyz")
	t.Setenv("HOST_UNRELATED_SECRET", "do-not-leak-this")
	t.Setenv("PATH", "/usr/local/bin:/usr/bin:/bin")
	t.Setenv("MY_APP_SECRET", "declared-secret-123")

	cfg := ServerConfig{
		Env: map[string]string{
			"APP_ALLOWED_SECRET": "${MY_APP_SECRET}",
			"STATIC_VAR":         "static-val",
		},
	}

	envList := cfg.ExpandedEnv()

	// Verify safe baseline is inherited and undeclared daemon secrets are isolated
	hasPath := false
	hasAllowedSecret := false
	hasStaticVar := false
	hasLeakedBearerToken := false
	hasLeakedHostSecret := false

	for _, e := range envList {
		if strings.HasPrefix(e, "PATH=") {
			hasPath = true
		}
		if e == "APP_ALLOWED_SECRET=declared-secret-123" {
			hasAllowedSecret = true
		}
		if e == "STATIC_VAR=static-val" {
			hasStaticVar = true
		}
		if strings.Contains(e, "super-secret-daemon-token-xyz") || strings.HasPrefix(e, "MCP_ROUTER_BEARER_TOKEN=") {
			hasLeakedBearerToken = true
		}
		if strings.Contains(e, "do-not-leak-this") || strings.HasPrefix(e, "HOST_UNRELATED_SECRET=") {
			hasLeakedHostSecret = true
		}
	}

	if !hasPath {
		t.Errorf("expected PATH from safe baseline to be present in ExpandedEnv")
	}
	if !hasAllowedSecret {
		t.Errorf("expected explicitly declared and expanded APP_ALLOWED_SECRET to be present")
	}
	if !hasStaticVar {
		t.Errorf("expected static variable to be present")
	}
	if hasLeakedBearerToken {
		t.Errorf("SECURITY VULNERABILITY: daemon MCP_ROUTER_BEARER_TOKEN leaked into child process environment")
	}
	if hasLeakedHostSecret {
		t.Errorf("SECURITY VULNERABILITY: host secret HOST_UNRELATED_SECRET leaked into child process environment")
	}
}

func TestServerConfig_ExpandedEnv_DropsHomeAndUserByDefault(t *testing.T) {
	// Issue #41: HOME, USER, and LOGNAME must NOT be inherited by default
	t.Setenv("HOME", "/root")
	t.Setenv("USER", "root")
	t.Setenv("LOGNAME", "root")
	t.Setenv("PATH", "/usr/bin:/bin")

	cfg := ServerConfig{
		Command: "dummy",
	}

	envList := cfg.ExpandedEnv()
	for _, e := range envList {
		if strings.HasPrefix(e, "HOME=") {
			t.Errorf("SECURITY VULNERABILITY (Issue #41): HOME leaked into default stdio environment: %s", e)
		}
		if strings.HasPrefix(e, "USER=") {
			t.Errorf("SECURITY VULNERABILITY (Issue #41): USER leaked into default stdio environment: %s", e)
		}
		if strings.HasPrefix(e, "LOGNAME=") {
			t.Errorf("SECURITY VULNERABILITY (Issue #41): LOGNAME leaked into default stdio environment: %s", e)
		}
	}
}

func TestServerConfig_ExpandedEnv_AllowsEnvInherit(t *testing.T) {
	t.Setenv("HOME", "/custom/home")
	t.Setenv("CUSTOM_HOST_VAR", "inherited_val")
	t.Setenv("DO_NOT_INHERIT", "stay_here")

	cfg := ServerConfig{
		Command:    "dummy",
		EnvInherit: []string{"HOME", "CUSTOM_HOST_VAR"},
	}

	envList := cfg.ExpandedEnv()
	hasHome := false
	hasCustom := false
	hasDoNotInherit := false

	for _, e := range envList {
		if e == "HOME=/custom/home" {
			hasHome = true
		}
		if e == "CUSTOM_HOST_VAR=inherited_val" {
			hasCustom = true
		}
		if strings.HasPrefix(e, "DO_NOT_INHERIT=") {
			hasDoNotInherit = true
		}
	}

	if !hasHome {
		t.Errorf("expected explicitly inherited HOME to be present")
	}
	if !hasCustom {
		t.Errorf("expected explicitly inherited CUSTOM_HOST_VAR to be present")
	}
	if hasDoNotInherit {
		t.Errorf("expected unlisted DO_NOT_INHERIT to remain isolated")
	}
}

func TestLoadConfig_EnvironmentExpansion(t *testing.T) {
	t.Setenv("TEST_BEARER_TOKEN", "secure-token-abc")
	t.Setenv("TEST_CERT_FILE", "/path/to/cert.pem")
	t.Setenv("TEST_KEY_FILE", "/path/to/key.pem")

	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config.yaml")

	yamlContent := `
auth:
  enabled: true
  bearer_token: "${TEST_BEARER_TOKEN}"
tls:
  enabled: true
  cert_file: "${TEST_CERT_FILE}"
  key_file: "${TEST_KEY_FILE}"
servers:
  s1:
    transport: stdio
    command: echo
    prefix: s1__
`
	if err := os.WriteFile(configFile, []byte(yamlContent), 0644); err != nil {
		t.Fatalf("failed to write test config file: %v", err)
	}

	cfg, err := LoadConfig(configFile)
	if err != nil {
		t.Fatalf("unexpected error loading config: %v", err)
	}

	if cfg.Auth.BearerToken != "secure-token-abc" {
		t.Errorf("expected Auth.BearerToken to be expanded to 'secure-token-abc', got '%s'", cfg.Auth.BearerToken)
	}
	if cfg.TLS.CertFile != "/path/to/cert.pem" {
		t.Errorf("expected TLS.CertFile to be expanded to '/path/to/cert.pem', got '%s'", cfg.TLS.CertFile)
	}
	if cfg.TLS.KeyFile != "/path/to/key.pem" {
		t.Errorf("expected TLS.KeyFile to be expanded to '/path/to/key.pem', got '%s'", cfg.TLS.KeyFile)
	}
}

func TestLoadConfig_ProductionAndExampleFiles(t *testing.T) {
	t.Setenv("MCP_ROUTER_BEARER_TOKEN", "ci-test-token")
	t.Setenv("MCP_ROUTER_SECRET_TOKEN", "ci-test-token")
	configs := []string{"config.example.yaml"}
	if _, err := os.Stat("config.yaml"); err == nil {
		configs = append(configs, "config.yaml")
	}
	for _, f := range configs {
		t.Run(f, func(t *testing.T) {
			cfg, err := LoadConfig(f)
			if err != nil {
				t.Fatalf("failed to load %s: %v", f, err)
			}
			if len(cfg.Servers) == 0 {
				t.Fatalf("expected servers in %s", f)
			}
		})
	}
}

func TestCanonicalConfig_CleanEcosystemBackends(t *testing.T) {
	t.Setenv("MCP_ROUTER_BEARER_TOKEN", "ci-test-token")
	configs := []string{"config.example.yaml"}
	if _, err := os.Stat("config.yaml"); err == nil {
		configs = append(configs, "config.yaml")
	}

	expectedBackends := []string{
		"nova-anythingllm-mcp",
		"nova-searxng-gateway",
		"mailru",
		"nextcloud-gateway",
		"github-doctormes",
		"github-novanodes",
		"dynadot",
		"google-jules-doctormes",
		"google-jules-novanodes",
		"context7-node-1",
		"google-stitch-node-1",
		"grizzly-sms",
		"manus-gateway",
		"nova-devops-mcp",
	}

	for _, cfgFile := range configs {
		t.Run(cfgFile, func(t *testing.T) {
			cfg, err := LoadConfig(cfgFile)
			if err != nil {
				t.Fatalf("failed to load %s: %v", cfgFile, err)
			}

			if len(cfg.Servers) != len(expectedBackends) {
				t.Errorf("expected exactly %d backends in %s, got %d", len(expectedBackends), cfgFile, len(cfg.Servers))
			}

			for _, expected := range expectedBackends {
				if _, ok := cfg.Servers[expected]; !ok {
					t.Errorf("expected backend '%s' not found in %s", expected, cfgFile)
				}
			}
		})
	}
}

func TestCanonicalConfig_AgentACLMatrix(t *testing.T) {
	t.Setenv("MCP_ROUTER_BEARER_TOKEN", "ci-test-token")
	configs := []string{"config.example.yaml"}
	if _, err := os.Stat("config.yaml"); err == nil {
		configs = append(configs, "config.yaml")
	}

	for _, cfgFile := range configs {
		t.Run(cfgFile, func(t *testing.T) {
			cfg, err := LoadConfig(cfgFile)
			if err != nil {
				t.Fatalf("failed to load %s: %v", cfgFile, err)
			}

	// Helper to check if an agent is allowed in a backend
	isAllowed := func(backendName, agentID string) bool {
		server, ok := cfg.Servers[backendName]
		if !ok {
			return false
		}
		if len(server.AllowedAgents) == 0 {
			return true // open to all
		}
		for _, a := range server.AllowedAgents {
			if a == agentID {
				return true
			}
		}
		return false
	}

	countAllowedBackends := func(agentID string) int {
		count := 0
		for name := range cfg.Servers {
			if isAllowed(name, agentID) {
				count++
			}
		}
		return count
	}

	// 1. Base stack: open to everyone (no allowed_agents specified)
	baseBackends := []string{"nova-anythingllm-mcp", "nova-searxng-gateway", "context7-node-1"}
	for _, b := range baseBackends {
		if len(cfg.Servers[b].AllowedAgents) != 0 {
			t.Errorf("expected base backend %s to have empty allowed_agents, got %v", b, cfg.Servers[b].AllowedAgents)
		}
	}

	// 2. Prometheus gets exactly 6 backends: 3 base + github-novanodes + manus-gateway + nova-devops-mcp
	if count := countAllowedBackends("prometheus_brobot"); count != 6 {
		t.Errorf("expected prometheus_brobot to have access to 6 backends, got %d", count)
	}
	if !isAllowed("nova-devops-mcp", "prometheus_brobot") {
		t.Errorf("expected prometheus_brobot to have access to nova-devops-mcp")
	}
	if !isAllowed("github-novanodes", "prometheus_brobot") {
		t.Errorf("expected prometheus_brobot to have access to github-novanodes")
	}
	if !isAllowed("manus-gateway", "prometheus_brobot") {
		t.Errorf("expected prometheus_brobot to have access to manus-gateway")
	}
	if isAllowed("github-doctormes", "prometheus_brobot") {
		t.Errorf("expected prometheus_brobot to NOT have access to github-doctormes")
	}

	// 3. Kairos gets exactly 9 backends: 3 base + 4 office + github-novanodes + manus-gateway
	if count := countAllowedBackends("kairos_brobot"); count != 9 {
		t.Errorf("expected kairos_brobot to have access to 9 backends, got %d", count)
	}
	if !isAllowed("manus-gateway", "kairos_brobot") {
		t.Errorf("expected kairos_brobot to have access to manus-gateway")
	}
	if isAllowed("nova-devops-mcp", "kairos_brobot") {
		t.Errorf("expected kairos_brobot to NOT have access to nova-devops-mcp")
	}
	officeBackends := []string{"dynadot", "mailru", "nextcloud-gateway", "grizzly-sms"}
	for _, b := range officeBackends {
		if !isAllowed(b, "kairos_brobot") {
			t.Errorf("expected kairos_brobot to have access to office backend %s", b)
		}
		if isAllowed(b, "prometheus_brobot") {
			t.Errorf("expected prometheus_brobot to NOT have access to office backend %s", b)
		}
	}

	// 4. Doctormes contour: exactly 5 backends (3 base + github-doctormes + manus-gateway)
	doctormesAgents := []string{"Caduceus_brobot", "Dartanyan_brobot", "Marla_Singer_gobot"}
	for _, agent := range doctormesAgents {
		if count := countAllowedBackends(agent); count != 5 {
			t.Errorf("expected %s to have access to 5 backends, got %d", agent, count)
		}
		if !isAllowed("manus-gateway", agent) {
			t.Errorf("expected %s to have access to manus-gateway", agent)
		}
		if !isAllowed("github-doctormes", agent) {
			t.Errorf("expected %s to have access to github-doctormes", agent)
		}
		if isAllowed("github-novanodes", agent) {
			t.Errorf("expected %s to NOT have access to github-novanodes", agent)
		}
		if isAllowed("nova-devops-mcp", agent) {
			t.Errorf("expected %s to NOT have access to nova-devops-mcp", agent)
		}
	}

	// 5. Tyler gets 8 backends: 3 base + github-novanodes + 3 quarantine (stitch, jules-novanodes, jules-doctormes) + manus-gateway
	if count := countAllowedBackends("Tyler_Durden_gobot"); count != 8 {
		t.Errorf("expected Tyler_Durden_gobot to have access to 8 backends, got %d", count)
	}
	if !isAllowed("manus-gateway", "Tyler_Durden_gobot") {
		t.Errorf("expected Tyler_Durden_gobot to have access to manus-gateway")
	}
	if isAllowed("nova-devops-mcp", "Tyler_Durden_gobot") {
		t.Errorf("expected Tyler_Durden_gobot to NOT have access to nova-devops-mcp")
	}
	quarantineBackends := []string{"google-stitch-node-1", "google-jules-novanodes", "google-jules-doctormes"}
	for _, b := range quarantineBackends {
		if !isAllowed(b, "Tyler_Durden_gobot") {
			t.Errorf("expected Tyler_Durden_gobot to have access to quarantine backend %s", b)
		}
		if isAllowed(b, "prometheus_brobot") {
			t.Errorf("expected prometheus_brobot to NOT have access to quarantine backend %s", b)
		}
	}

	// 6. Orchestration agents (trickster_gobot, toomynamea_brobot, NovaNodes_brobot): exactly 5 backends (3 base + github-novanodes + manus-gateway)
	orchestrationAgents := []string{"trickster_gobot", "toomynamea_brobot", "NovaNodes_brobot"}
	for _, agent := range orchestrationAgents {
		if count := countAllowedBackends(agent); count != 5 {
			t.Errorf("expected %s to have access to 5 backends, got %d", agent, count)
		}
		if !isAllowed("manus-gateway", agent) {
			t.Errorf("expected %s to have access to manus-gateway", agent)
		}
		if isAllowed("nova-devops-mcp", agent) {
			t.Errorf("expected %s to NOT have access to nova-devops-mcp", agent)
		}
	}

	// 7. OpenClaw agents (bahus, main): exactly 4 backends (3 base + github-novanodes)
	openclawAgents := []string{"bahus", "main"}
	for _, agent := range openclawAgents {
		if count := countAllowedBackends(agent); count != 4 {
			t.Errorf("expected %s to have access to 4 backends, got %d", agent, count)
		}
		if !isAllowed("github-novanodes", agent) {
			t.Errorf("expected %s to have access to github-novanodes", agent)
		}
		if isAllowed("github-doctormes", agent) {
			t.Errorf("expected %s to NOT have access to github-doctormes", agent)
		}
		if isAllowed("dynadot", agent) {
			t.Errorf("expected %s to NOT have access to dynadot", agent)
		}
		if isAllowed("nova-devops-mcp", agent) {
			t.Errorf("expected %s to NOT have access to nova-devops-mcp", agent)
		}
	}
		})
	}
}

func TestServerConfig_GetCallTimeout(t *testing.T) {
	defaultCfg := ServerConfig{}
	if defaultCfg.GetCallTimeout() != 60*time.Second {
		t.Errorf("expected default timeout 60s, got %v", defaultCfg.GetCallTimeout())
	}

	customCfg := ServerConfig{Timeout: 15}
	if customCfg.GetCallTimeout() != 15*time.Second {
		t.Errorf("expected custom timeout 15s, got %v", customCfg.GetCallTimeout())
	}
}

func TestConfigValidation_NegativeTimeout(t *testing.T) {
	cfg := &Config{
		Servers: map[string]ServerConfig{
			"s1": {
				Transport: "stdio",
				Command:   "echo",
				Timeout:   -5,
				Prefix:    "s1__",
			},
		},
	}
	if err := cfg.Validate(); err == nil {
		t.Errorf("expected error when server timeout is negative")
	}
}

func TestValidate_RejectsEmptyPrefix(t *testing.T) {
	cfg := &Config{
		Servers: map[string]ServerConfig{
			"s1": {Transport: "stdio", Command: "echo", Prefix: "  "},
		},
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatalf("expected error for empty prefix")
	}
	if !strings.Contains(err.Error(), "prefix is required and must be non-empty") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestValidate_RejectsDuplicatePrefix(t *testing.T) {
	cfg := &Config{
		Servers: map[string]ServerConfig{
			"s1": {Transport: "stdio", Command: "echo", Prefix: "dup__"},
			"s2": {Transport: "stdio", Command: "ls", Prefix: "dup__"},
		},
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatalf("expected error for duplicate prefix")
	}
	if !strings.Contains(err.Error(), "is used by both") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestValidate_RejectsPrefixWithoutSeparator(t *testing.T) {
	cfg := &Config{
		Servers: map[string]ServerConfig{
			"s1": {Transport: "stdio", Command: "echo", Prefix: "gh"},
		},
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatalf("expected error for prefix without separator")
	}
	if !strings.Contains(err.Error(), "must end with '_' or '__'") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestServerConfig_Equal(t *testing.T) {
	base := ServerConfig{
		Type:          "stdio",
		Command:       "node",
		Args:          []string{"dist/index.js", "--flag"},
		Env:           map[string]string{"FOO": "bar", "BAZ": "qux"},
		URL:           "",
		Headers:       map[string]string{"Auth": "tok"},
		Prefix:        "test__",
		AllowedAgents: []string{"agent-1", "agent-2"},
		ToolsAllowlist: []string{"tool1", "tool2"},
		AgentToolsAllowlist: map[string][]string{
			"agent-1": {"tool1", "tool2"},
		},
		Transport:     "stdio",
		MaxConcurrent: 10,
		Timeout:       45,
	}

	// Identical copy
	same := base
	same.Args = []string{"dist/index.js", "--flag"}
	same.Env = map[string]string{"FOO": "bar", "BAZ": "qux"}
	same.Headers = map[string]string{"Auth": "tok"}
	same.AllowedAgents = []string{"agent-1", "agent-2"}
	same.ToolsAllowlist = []string{"tool1", "tool2"}
	same.AgentToolsAllowlist = map[string][]string{
		"agent-1": {"tool1", "tool2"},
	}
	if !base.Equal(same) {
		t.Errorf("expected base.Equal(same) to be true")
	}

	testCases := []struct {
		name   string
		modify func(c *ServerConfig)
	}{
		{"type", func(c *ServerConfig) { c.Type = "changed" }},
		{"maxConcurrent", func(c *ServerConfig) { c.MaxConcurrent = 99 }},
		{"timeout", func(c *ServerConfig) { c.Timeout = 999 }},
		{"transport", func(c *ServerConfig) { c.Transport = "http" }},
		{"command", func(c *ServerConfig) { c.Command = "python3" }},
		{"url", func(c *ServerConfig) { c.URL = "http://localhost:8080" }},
		{"prefix", func(c *ServerConfig) { c.Prefix = "new__" }},
		{"args_len", func(c *ServerConfig) { c.Args = []string{"dist/index.js"} }},
		{"args_elem", func(c *ServerConfig) { c.Args = []string{"dist/index.js", "--other"} }},
		{"env_len", func(c *ServerConfig) { c.Env = map[string]string{"FOO": "bar"} }},
		{"env_val", func(c *ServerConfig) { c.Env = map[string]string{"FOO": "diff", "BAZ": "qux"} }},
		{"env_key", func(c *ServerConfig) { c.Env = map[string]string{"FOO": "bar", "OTHER": "qux"} }},
		{"headers_len", func(c *ServerConfig) { c.Headers = map[string]string{} }},
		{"headers_val", func(c *ServerConfig) { c.Headers = map[string]string{"Auth": "diff"} }},
		{"headers_key", func(c *ServerConfig) { c.Headers = map[string]string{"NewKey": "tok"} }},
		{"agents_len", func(c *ServerConfig) { c.AllowedAgents = []string{"agent-1"} }},
		{"agents_elem", func(c *ServerConfig) { c.AllowedAgents = []string{"agent-1", "agent-diff"} }},
		{"env_inherit_len", func(c *ServerConfig) { c.EnvInherit = []string{"HOME"} }},
		{"env_inherit_elem", func(c *ServerConfig) { c.EnvInherit = []string{"USER", "OTHER"} }},
		{"tools_allowlist_len", func(c *ServerConfig) { c.ToolsAllowlist = []string{"tool1"} }},
		{"tools_allowlist_elem", func(c *ServerConfig) { c.ToolsAllowlist = []string{"tool1", "diff"} }},
		{"agent_tools_allowlist_len", func(c *ServerConfig) { c.AgentToolsAllowlist = map[string][]string{} }},
		{"agent_tools_allowlist_val", func(c *ServerConfig) { c.AgentToolsAllowlist = map[string][]string{"agent-1": {"tool1", "diff"}} }},
		{"agent_tools_allowlist_key", func(c *ServerConfig) { c.AgentToolsAllowlist = map[string][]string{"agent-other": {"tool1", "tool2"}} }},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mod := base
			mod.Args = append([]string{}, base.Args...)
			mod.Env = make(map[string]string)
			for k, v := range base.Env {
				mod.Env[k] = v
			}
			mod.Headers = make(map[string]string)
			for k, v := range base.Headers {
				mod.Headers[k] = v
			}
			mod.AllowedAgents = append([]string{}, base.AllowedAgents...)
			mod.EnvInherit = append([]string{}, base.EnvInherit...)
			mod.ToolsAllowlist = append([]string{}, base.ToolsAllowlist...)
			mod.AgentToolsAllowlist = make(map[string][]string)
			for k, v := range base.AgentToolsAllowlist {
				mod.AgentToolsAllowlist[k] = append([]string{}, v...)
			}

			tc.modify(&mod)

			if base.Equal(mod) {
				t.Errorf("expected base.Equal(mod) to be false for mutation in %s", tc.name)
			}
		})
	}
}

func TestServerConfig_GetToolsAllowlistForAgent(t *testing.T) {
	cfg := ServerConfig{
		ToolsAllowlist: []string{"global_tool1", "global_tool2"},
		AgentToolsAllowlist: map[string][]string{
			"kairos_brobot": {"kairos_tool1", "kairos_tool2"},
		},
	}

	// 1. Agent with specific allowlist
	kairosList := cfg.GetToolsAllowlistForAgent("kairos_brobot")
	if len(kairosList) != 2 || kairosList[0] != "kairos_tool1" || kairosList[1] != "kairos_tool2" {
		t.Errorf("unexpected allowlist for kairos_brobot: %v", kairosList)
	}

	// 2. Agent without specific entry falls back to global ToolsAllowlist
	otherList := cfg.GetToolsAllowlistForAgent("other_agent")
	if len(otherList) != 2 || otherList[0] != "global_tool1" || otherList[1] != "global_tool2" {
		t.Errorf("unexpected allowlist for other_agent: %v", otherList)
	}

	// 3. No global allowlist and no agent allowlist -> empty
	emptyCfg := ServerConfig{}
	emptyList := emptyCfg.GetToolsAllowlistForAgent("kairos_brobot")
	if len(emptyList) != 0 {
		t.Errorf("expected empty allowlist, got: %v", emptyList)
	}
}

func TestLoadConfig_WithAuditAndQueryToken(t *testing.T) {
	yamlContent := `
audit:
  path: "/var/log/custom-audit.jsonl"
  fallback_path: "/tmp/custom-fallback.jsonl"
  required: true

auth:
  enabled: true
  bearer_token: "secret-token"
  allow_query_token: true

servers:
  srv1:
    transport: stdio
    command: python3
    prefix: srv1__
    env_inherit:
      - HOME
      - USER
`
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "audit_cfg.yaml")
	if err := os.WriteFile(cfgPath, []byte(yamlContent), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("failed to load config with audit: %v", err)
	}

	if cfg.Audit.Path != "/var/log/custom-audit.jsonl" {
		t.Errorf("expected audit path '/var/log/custom-audit.jsonl', got '%s'", cfg.Audit.Path)
	}
	if cfg.Audit.FallbackPath != "/tmp/custom-fallback.jsonl" {
		t.Errorf("expected fallback path '/tmp/custom-fallback.jsonl', got '%s'", cfg.Audit.FallbackPath)
	}
	if !cfg.Audit.Required {
		t.Errorf("expected audit required to be true")
	}
	if !cfg.Auth.AllowQueryToken {
		t.Errorf("expected allow_query_token to be true")
	}
	if len(cfg.Servers["srv1"].EnvInherit) != 2 || cfg.Servers["srv1"].EnvInherit[0] != "HOME" {
		t.Errorf("expected EnvInherit ['HOME', 'USER'], got %v", cfg.Servers["srv1"].EnvInherit)
	}
}

func TestProductionConfig_AnythingLLMBaseURL(t *testing.T) {
	t.Setenv("MCP_ROUTER_BEARER_TOKEN", "ci-test-token")
	configs := []string{"config.example.yaml"}
	if _, err := os.Stat("config.yaml"); err == nil {
		configs = append(configs, "config.yaml")
	}
	for _, cfgFile := range configs {
		t.Run(cfgFile, func(t *testing.T) {
			cfg, err := LoadConfig(cfgFile)
			if err != nil {
				t.Fatalf("failed to load %s: %v", cfgFile, err)
			}
			server, ok := cfg.Servers["nova-anythingllm-mcp"]
			if !ok {
				t.Fatalf("nova-anythingllm-mcp not found in %s", cfgFile)
			}
			almBase, ok := server.Env["MG_ALM_BASE"]
			if !ok {
				t.Fatalf("MG_ALM_BASE not defined in %s for nova-anythingllm-mcp", cfgFile)
			}
			expected := "http://127.0.0.1:3002/api/v1"
			if almBase != expected {
				t.Errorf("expected MG_ALM_BASE to be %q in %s, got %q", expected, cfgFile, almBase)
			}
		})
	}
}

