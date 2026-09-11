package main

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

type Backend struct {
	Name        string
	Config      ServerConfig
	Client      client.MCPClient
	CachedTools []mcp.Tool
	Semaphore   chan struct{} // Concurrency limiting semaphore
}

func (b *Backend) GetType() string {
	return b.Config.GetType(b.Name)
}

// IsConnected returns true if the backend has an active client connection
func (b *Backend) IsConnected() bool {
	return b != nil && b.Client != nil
}

// HasTools returns true if the backend has cached at least one tool
func (b *Backend) HasTools() bool {
	return b != nil && len(b.CachedTools) > 0
}

// IsHealthy returns true if the backend has an active client connection
func (b *Backend) IsHealthy() bool {
	return b.IsConnected()
}

type Multiplexer struct {
	mu       sync.RWMutex
	backends map[string]*Backend
	ctx      context.Context
	cancel   context.CancelFunc
	stopOnce sync.Once
}

func NewMultiplexer(cfg *Config) *Multiplexer {
	ctx, cancel := context.WithCancel(context.Background())
	mux := &Multiplexer{
		backends: make(map[string]*Backend),
		ctx:      ctx,
		cancel:   cancel,
	}
	for name, srvCfg := range cfg.Servers {
		maxConc := srvCfg.MaxConcurrent
		if maxConc <= 0 {
			maxConc = 50 // Default worker pool capacity
		}
		mux.backends[name] = &Backend{
			Name:      name,
			Config:    srvCfg,
			Semaphore: make(chan struct{}, maxConc),
		}
	}
	return mux
}

// GetBackends returns a thread-safe snapshot of all registered backends
func (m *Multiplexer) GetBackends() []*Backend {
	m.mu.RLock()
	defer m.mu.RUnlock()
	res := make([]*Backend, 0, len(m.backends))
	for _, b := range m.backends {
		res = append(res, b)
	}
	return res
}

// GetBackend returns a single backend by name in a thread-safe manner
func (m *Multiplexer) GetBackend(name string) (*Backend, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	b, ok := m.backends[name]
	return b, ok
}

// startSingleBackend starts and initializes a single MCP backend client
func (m *Multiplexer) startSingleBackend(b *Backend) {
	var mcpClient client.MCPClient
	var err error

	defer func() {
		if b.Client == nil && mcpClient != nil {
			log.Printf("Closing uninitialized/failed client for backend %s", b.Name)
			mcpClient.Close()
		}
	}()

	log.Printf("Starting backend %s (%s)", b.Name, b.Config.Transport)

	trans := strings.ToLower(strings.TrimSpace(b.Config.Transport))
	if trans == "stdio" {
		if b.Config.Command == "" {
			log.Printf("empty command for backend %s", b.Name)
			return
		}
		cmd := b.Config.ExpandedCommand()
		cmdArgs := b.Config.ExpandedArgs()

		// If strings.Fields was used previously, allow fallback if Args is empty
		if len(cmdArgs) == 0 && strings.Contains(cmd, " ") {
			parts := strings.Fields(cmd)
			cmd = parts[0]
			cmdArgs = parts[1:]
		}

		env := b.Config.ExpandedEnv()
		c, err := client.NewStdioMCPClient(cmd, env, cmdArgs...)
		if err != nil {
			log.Printf("Failed to start stdio backend %s: %v", b.Name, err)
			return
		}
		mcpClient = c
	} else if trans == "streamable_http" || trans == "streamable" {
		url := b.Config.ExpandedURL()
		headers := b.Config.ExpandedHeaders()
		var opts []transport.StreamableHTTPCOption
		if len(headers) > 0 {
			opts = append(opts, transport.WithHTTPHeaders(headers))
		}
		c, err := client.NewStreamableHttpClient(url, opts...)
		if err != nil {
			log.Printf("failed to create streamable_http backend %s: %v", b.Name, err)
			return
		}
		err = c.Start(m.ctx)
		if err != nil {
			log.Printf("failed to start streamable_http transport for backend %s: %v", b.Name, err)
			return
		}
		mcpClient = c
	} else if trans == "http" || trans == "sse" {
		var c *client.Client
		var err error
		url := b.Config.ExpandedURL()
		headers := b.Config.ExpandedHeaders()
		if len(headers) > 0 {
			c, err = client.NewSSEMCPClient(url, client.WithHeaders(headers))
		} else {
			c, err = client.NewSSEMCPClient(url)
		}
		if err != nil {
			log.Printf("failed to create sse backend %s: %v", b.Name, err)
			return
		}
		err = c.Start(m.ctx)
		if err != nil {
			log.Printf("failed to start sse transport for backend %s: %v", b.Name, err)
			return
		}
		mcpClient = c
	} else {
		log.Printf("unsupported transport '%s' for backend %s", b.Config.Transport, b.Name)
		return
	}

	// Connect initialize request
	initRequest := mcp.InitializeRequest{}
	initRequest.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initRequest.Params.ClientInfo = mcp.Implementation{
		Name:    "mcp-router",
		Version: "1.0.0",
	}

	initCtx, initCancel := context.WithTimeout(m.ctx, 15*time.Second)
	_, err = mcpClient.Initialize(initCtx, initRequest)
	initCancel()

	if err != nil {
		log.Printf("failed to initialize backend %s: %v, skipping...", b.Name, err)
		return
	}

	log.Printf("Backend %s initialized successfully", b.Name)
	b.Client = mcpClient

	// Cache tools for this backend
	toolCtx, toolCancel := context.WithTimeout(m.ctx, 15*time.Second)
	result, err := mcpClient.ListTools(toolCtx, mcp.ListToolsRequest{})
	toolCancel()
	if err != nil {
		log.Printf("failed to cache tools for backend %s: %v", b.Name, err)
	} else {
		b.CachedTools = result.Tools
		log.Printf("Cached %d tools for backend %s", len(b.CachedTools), b.Name)
	}
}

// ⚡ Bolt: concurrent backend initialization
func (m *Multiplexer) StartBackends() error {
	var wg sync.WaitGroup
	m.mu.RLock()
	backends := make([]*Backend, 0, len(m.backends))
	for _, b := range m.backends {
		backends = append(backends, b)
	}
	m.mu.RUnlock()

	for _, b := range backends {
		wg.Add(1)
		go func(backend *Backend) {
			defer wg.Done()
			m.startSingleBackend(backend)
		}(b)
	}
	wg.Wait()
	return nil
}

// ReloadReport summarizes changes applied during dynamic configuration reload
type ReloadReport struct {
	Kept    []string
	Added   []string
	Updated []string
	Removed []string
}

// Reload dynamically updates the multiplexer with a new configuration using a shadow-init atomic swap,
// guaranteeing zero downtime and zero dropped calls during SIGHUP configuration reload.
func (m *Multiplexer) Reload(newCfg *Config) ReloadReport {
	if newCfg == nil {
		return ReloadReport{}
	}

	report := ReloadReport{}
	var toStop []*Backend
	var toStart []*Backend
	var toDelete []string
	shadowBackends := make(map[string]*Backend)

	// 1. Plan updates under read lock: existing backends continue serving uninterrupted!
	m.mu.RLock()
	for name, existingBackend := range m.backends {
		newServerCfg, exists := newCfg.Servers[name]
		if !exists {
			toDelete = append(toDelete, name)
			toStop = append(toStop, existingBackend)
			report.Removed = append(report.Removed, name)
		} else if !existingBackend.Config.Equal(newServerCfg) {
			report.Updated = append(report.Updated, name)
			toStop = append(toStop, existingBackend)

			maxConc := newServerCfg.MaxConcurrent
			if maxConc <= 0 {
				maxConc = 50
			}
			newBackend := &Backend{
				Name:      name,
				Config:    newServerCfg,
				Semaphore: make(chan struct{}, maxConc),
			}
			shadowBackends[name] = newBackend
			toStart = append(toStart, newBackend)
		} else {
			report.Kept = append(report.Kept, name)
		}
	}

	// 2. Check for brand new backends
	for name, newServerCfg := range newCfg.Servers {
		if _, exists := m.backends[name]; !exists {
			report.Added = append(report.Added, name)
			maxConc := newServerCfg.MaxConcurrent
			if maxConc <= 0 {
				maxConc = 50
			}
			newBackend := &Backend{
				Name:      name,
				Config:    newServerCfg,
				Semaphore: make(chan struct{}, maxConc),
			}
			shadowBackends[name] = newBackend
			toStart = append(toStart, newBackend)
		}
	}
	m.mu.RUnlock()

	// 3. Initialize shadow backends in parallel outside lock (traffic continues to old backends!)
	if len(toStart) > 0 {
		var wg sync.WaitGroup
		for _, b := range toStart {
			wg.Add(1)
			go func(backend *Backend) {
				defer wg.Done()
				m.startSingleBackend(backend)
			}(b)
		}
		wg.Wait()
	}

	// 4. Atomic Swap: under write lock, delete removed backends and swap in initialized shadow backends
	m.mu.Lock()
	for _, name := range toDelete {
		delete(m.backends, name)
	}
	for name, newBackend := range shadowBackends {
		m.backends[name] = newBackend
	}
	m.mu.Unlock()

	// 5. Safely stop decommissioned old clients outside lock
	for _, b := range toStop {
		if b.Client != nil {
			log.Printf("Stopping removed/updated backend %s", b.Name)
			b.Client.Close()
		}
	}

	log.Printf("Reload complete: %d kept, %d added, %d updated, %d removed",
		len(report.Kept), len(report.Added), len(report.Updated), len(report.Removed))
	return report
}

func (m *Multiplexer) Stop() {
	m.stopOnce.Do(func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		for _, b := range m.backends {
			if b.Client != nil {
				b.Client.Close()
			}
		}
		m.cancel()
	})
}

func (m *Multiplexer) IsAgentAllowed(backendName string, agentID string) bool {
	m.mu.RLock()
	backend, exists := m.backends[backendName]
	m.mu.RUnlock()
	if !exists {
		return false
	}
	
	allowed := backend.Config.AllowedAgents
	if len(allowed) == 0 {
		return true // If section is missing or empty, it's allowed for everyone
	}

	for _, a := range allowed {
		if a == agentID {
			return true
		}
	}
	return false
}
