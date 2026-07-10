package internal

import (
	"bufio"
	"context"
	"fmt"
	"math"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"
)

const (
	sshTimeoutEnv              = "SSH_DASHBOARD_TIMEOUT"
	defaultSSHTimeout          = 10 * time.Second
	minSSHTimeout              = 10 * time.Millisecond
	maxSSHTimeout              = time.Hour
	maxProxyTargetDialAttempts = 3
	proxyTargetRetryDelay      = 200 * time.Millisecond
)

type SSHHost struct {
	Name         string
	Hostname     string
	User         string
	Port         string
	IdentityFile string
	ProxyJump    string
	configPath   string
}

type SSHClient struct {
	client           *ssh.Client
	config           *SSHHost
	releaseJumpChain func()
	closeOnce        sync.Once
	closeErr         error
}

type sharedJumpChain struct {
	ready    chan struct{}
	clients  []*ssh.Client
	err      error
	refs     int
	complete bool
}

type SSHConnector struct {
	mu         sync.Mutex
	jumpChains map[string]*sharedJumpChain
}

var defaultSSHConnector = NewSSHConnector()

func NewSSHConnector() *SSHConnector {
	return &SSHConnector{jumpChains: make(map[string]*sharedJumpChain)}
}

func ParseSSHConfig(configPath string) ([]SSHHost, error) {
	if configPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		configPath = filepath.Join(home, ".ssh", "config")
	}

	visited := make(map[string]bool)
	hosts, err := parseSSHConfigRecursive(configPath, visited)
	if err != nil {
		return nil, err
	}

	for i := range hosts {
		hosts[i].configPath = configPath
	}

	return hosts, nil
}

func parseSSHConfigRecursive(configPath string, visited map[string]bool) ([]SSHHost, error) {
	absPath, err := filepath.Abs(configPath)
	if err != nil {
		return nil, err
	}

	if visited[absPath] {
		return nil, nil
	}
	visited[absPath] = true

	file, err := os.Open(configPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var hosts []SSHHost
	var currentHost *SSHHost

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}

		key := strings.ToLower(parts[0])
		value := strings.Join(parts[1:], " ")

		if key == "include" {
			includePath := expandPath(value)

			if !filepath.IsAbs(includePath) {
				configDir := filepath.Dir(configPath)
				includePath = filepath.Join(configDir, includePath)
			}

			matches, err := filepath.Glob(includePath)
			if err != nil {
				continue
			}

			for _, match := range matches {
				includedHosts, err := parseSSHConfigRecursive(match, visited)
				if err != nil {
					continue
				}
				hosts = append(hosts, includedHosts...)
			}
			continue
		}

		if key == "host" {
			hosts = appendConcreteSSHHosts(hosts, currentHost)

			currentHost = &SSHHost{
				Name: value,
				Port: "22",
			}
		} else if currentHost != nil {
			switch key {
			case "hostname":
				currentHost.Hostname = value
			case "user":
				currentHost.User = value
			case "port":
				currentHost.Port = value
			case "identityfile":
				currentHost.IdentityFile = expandPath(value)
			case "proxyjump":
				currentHost.ProxyJump = value
			}
		}
	}

	hosts = appendConcreteSSHHosts(hosts, currentHost)

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return hosts, nil
}

func appendConcreteSSHHosts(hosts []SSHHost, host *SSHHost) []SSHHost {
	if host == nil {
		return hosts
	}

	for _, name := range strings.Fields(host.Name) {
		if strings.HasPrefix(name, "!") || strings.ContainsAny(name, "*?") {
			continue
		}
		configuredHost := *host
		configuredHost.Name = name
		hosts = append(hosts, configuredHost)
	}

	return hosts
}

func expandPath(path string) string {
	if path == "" {
		return ""
	}

	path = filepath.Clean(path)

	if strings.Contains(path, "..") {
		return ""
	}

	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		expandedPath := filepath.Join(home, path[2:])

		absHome, err := filepath.Abs(home)
		if err != nil {
			return ""
		}
		absPath, err := filepath.Abs(expandedPath)
		if err != nil {
			return ""
		}

		if !strings.HasPrefix(absPath, absHome) {
			return ""
		}

		return expandedPath
	}

	if filepath.IsAbs(path) {
		home, _ := os.UserHomeDir()
		sshDir := filepath.Join(home, ".ssh")

		absPath, err := filepath.Abs(path)
		if err != nil {
			return ""
		}

		if strings.HasPrefix(absPath, sshDir) || strings.HasPrefix(absPath, "/etc/ssh") {
			return path
		}

		return ""
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".ssh", path)
}

func getHostKeyCallback() (ssh.HostKeyCallback, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("unable to get user home directory: %w", err)
	}

	knownHostsPath := filepath.Join(home, ".ssh", "known_hosts")

	if _, err := os.Stat(knownHostsPath); os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(knownHostsPath), 0700); err != nil {
			return nil, fmt.Errorf("unable to create .ssh directory: %w", err)
		}
		if _, err := os.Create(knownHostsPath); err != nil {
			return nil, fmt.Errorf("unable to create known_hosts file: %w", err)
		}
	}

	hostKeyCallback, err := knownhosts.New(knownHostsPath)
	if err != nil {
		return nil, fmt.Errorf("unable to load known_hosts: %w", err)
	}

	return ssh.HostKeyCallback(func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		err := hostKeyCallback(hostname, remote, key)
		if err != nil {
			if keyErr, ok := err.(*knownhosts.KeyError); ok && len(keyErr.Want) > 0 {
				return fmt.Errorf("host key verification failed: host key has changed for %s. Remove the old key from %s if you trust this connection", hostname, knownHostsPath)
			} else if keyErr, ok := err.(*knownhosts.KeyError); ok && len(keyErr.Want) == 0 {
				return fmt.Errorf("host key verification failed: %s is not in known_hosts. Add the host key to %s or run 'ssh %s' first to accept the host key", hostname, knownHostsPath, hostname)
			}
			return fmt.Errorf("host key verification failed: %w", err)
		}
		return nil
	}), nil
}

func getValidatedUsername() string {
	user := os.Getenv("USER")
	if user == "" {
		return ""
	}

	if len(user) > 32 {
		return ""
	}

	for _, char := range user {
		if !((char >= 'a' && char <= 'z') ||
			(char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') ||
			char == '_' || char == '-' || char == '.') {
			return ""
		}
	}

	return user
}

func getValidatedSSHAuthSock() string {
	socket := os.Getenv("SSH_AUTH_SOCK")
	if socket == "" {
		return ""
	}

	if !filepath.IsAbs(socket) {
		return ""
	}

	cleanSocket := filepath.Clean(socket)
	if strings.Contains(cleanSocket, "..") {
		return ""
	}

	validPrefixes := []string{
		"/tmp/",
		"/var/run/",
		"/run/",
	}

	if tmpDir := os.Getenv("TMPDIR"); tmpDir != "" {
		validPrefixes = append(validPrefixes, tmpDir)
	}

	for _, prefix := range validPrefixes {
		if strings.HasPrefix(cleanSocket, prefix) {
			return socket
		}
	}

	if info, err := os.Stat(socket); err == nil {
		if info.Mode()&os.ModeSocket != 0 {
			return socket
		}
	}

	return ""
}

func getSSHTimeout() time.Duration {
	value := os.Getenv(sshTimeoutEnv)
	if value == "" {
		return defaultSSHTimeout
	}

	seconds, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
		return defaultSSHTimeout
	}

	timeout := time.Duration(seconds * float64(time.Second))
	if timeout < minSSHTimeout || timeout > maxSSHTimeout {
		return defaultSSHTimeout
	}

	return timeout
}

func dialSSH(addr string, config *ssh.ClientConfig, timeout time.Duration) (*ssh.Client, error) {
	return dialSSHWithDeadline(addr, config, nil, time.Now().Add(timeout))
}

func dialSSHWithDeadline(addr string, config *ssh.ClientConfig, proxy *ssh.Client, deadline time.Time) (*ssh.Client, error) {
	if time.Until(deadline) <= 0 {
		return nil, context.DeadlineExceeded
	}

	var conn net.Conn
	var err error
	if proxy == nil {
		dialer := net.Dialer{Deadline: deadline}
		conn, err = dialer.Dial("tcp", addr)
	} else {
		ctx, cancel := context.WithDeadline(context.Background(), deadline)
		defer cancel()
		conn, err = proxy.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return nil, err
	}

	deadlineSet := conn.SetDeadline(deadline) == nil
	var deadlineTimer *time.Timer
	if !deadlineSet {
		deadlineTimer = time.AfterFunc(time.Until(deadline), func() {
			conn.Close()
		})
	}

	clientConn, channels, requests, err := ssh.NewClientConn(conn, addr, config)
	if err != nil {
		if deadlineTimer != nil {
			deadlineTimer.Stop()
		}
		conn.Close()
		return nil, err
	}

	if deadlineTimer != nil {
		if !deadlineTimer.Stop() {
			clientConn.Close()
			return nil, context.DeadlineExceeded
		}
	} else {
		if err := conn.SetDeadline(time.Time{}); err != nil {
			clientConn.Close()
			return nil, err
		}
	}

	return ssh.NewClient(clientConn, channels, requests), nil
}

func dialSSHWithRetry(addr string, config *ssh.ClientConfig, proxy *ssh.Client, deadline time.Time) (*ssh.Client, error) {
	attempts := 1
	if proxy != nil {
		attempts = maxProxyTargetDialAttempts
	}

	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		client, err := dialSSHWithDeadline(addr, config, proxy, deadline)
		if err == nil {
			return client, nil
		}
		lastErr = err

		if attempt == attempts {
			break
		}
		remaining := time.Until(deadline)
		delay := time.Duration(attempt) * proxyTargetRetryDelay
		if remaining <= delay {
			break
		}
		timer := time.NewTimer(delay)
		<-timer.C
	}

	return nil, lastErr
}

func normalizeSSHHost(host SSHHost) SSHHost {
	if host.Hostname == "" {
		host.Hostname = host.Name
	}
	if host.User == "" {
		host.User = getValidatedUsername()
	}
	if host.Port == "" {
		host.Port = "22"
	}
	return host
}

func newSSHClientConfig(host SSHHost, timeout time.Duration) (*ssh.ClientConfig, func(), error) {
	host = normalizeSSHHost(host)

	var signers []ssh.Signer
	var agentConn net.Conn

	// First, try the specific identity file from SSH config (highest priority)
	if host.IdentityFile != "" {
		if signer, err := publicKeySigner(host.IdentityFile); err == nil {
			signers = append(signers, signer)
		}
	}

	// Second, try SSH agent (if available)
	if agentSigners, conn, agentErr := sshAgentSigners(); agentErr == nil {
		signers = append(signers, agentSigners...)
		agentConn = conn
	}

	// Finally, try default key files as fallback
	home, err := os.UserHomeDir()
	if err == nil {
		defaultKeys := []string{
			filepath.Join(home, ".ssh", "id_rsa"),
			filepath.Join(home, ".ssh", "id_ed25519"),
			filepath.Join(home, ".ssh", "id_ecdsa"),
		}
		for _, keyPath := range defaultKeys {
			// Skip if this is the same as the identity file we already tried
			if keyPath != host.IdentityFile {
				if signer, err := publicKeySigner(keyPath); err == nil {
					signers = append(signers, signer)
				}
			}
		}
	}

	signers = uniqueSSHSigners(signers)
	if len(signers) == 0 {
		if agentConn != nil {
			agentConn.Close()
		}
		return nil, nil, fmt.Errorf("no authentication methods available for %s", host.Name)
	}

	hostKeyCallback, err := getHostKeyCallback()
	if err != nil {
		if agentConn != nil {
			agentConn.Close()
		}
		return nil, nil, fmt.Errorf("failed to setup host key verification: %w", err)
	}

	closeAuth := func() {
		if agentConn != nil {
			agentConn.Close()
		}
	}

	return &ssh.ClientConfig{
		User:            host.User,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signers...)},
		HostKeyCallback: hostKeyCallback,
		Timeout:         timeout,
	}, closeAuth, nil
}

func uniqueSSHSigners(signers []ssh.Signer) []ssh.Signer {
	seen := make(map[string]bool, len(signers))
	unique := make([]ssh.Signer, 0, len(signers))
	for _, signer := range signers {
		fingerprint := ssh.FingerprintSHA256(signer.PublicKey())
		if seen[fingerprint] {
			continue
		}
		seen[fingerprint] = true
		unique = append(unique, signer)
	}
	return unique
}

func sshAddress(host SSHHost) string {
	return net.JoinHostPort(strings.Trim(host.Hostname, "[]"), host.Port)
}

func connectSSHHost(host SSHHost, proxy *ssh.Client, deadline time.Time) (*ssh.Client, error) {
	host = normalizeSSHHost(host)
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return nil, context.DeadlineExceeded
	}

	config, closeAuth, err := newSSHClientConfig(host, remaining)
	if err != nil {
		return nil, err
	}
	defer closeAuth()

	addr := sshAddress(host)
	client, err := dialSSHWithRetry(addr, config, proxy, deadline)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to %s: %w", addr, err)
	}

	return client, nil
}

func parseProxyJumpSpec(spec string) (name, user, port string, err error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return "", "", "", fmt.Errorf("empty ProxyJump host")
	}

	if at := strings.LastIndex(spec, "@"); at >= 0 {
		user = spec[:at]
		spec = spec[at+1:]
		if user == "" || spec == "" {
			return "", "", "", fmt.Errorf("invalid ProxyJump host")
		}
	}

	if strings.HasPrefix(spec, "[") {
		end := strings.Index(spec, "]")
		if end < 0 {
			return "", "", "", fmt.Errorf("invalid bracketed ProxyJump host")
		}
		name = spec[1:end]
		remainder := spec[end+1:]
		if remainder != "" {
			if !strings.HasPrefix(remainder, ":") || len(remainder) == 1 {
				return "", "", "", fmt.Errorf("invalid ProxyJump port")
			}
			port = remainder[1:]
		}
	} else if strings.Count(spec, ":") == 1 {
		name, port, _ = strings.Cut(spec, ":")
	} else {
		name = spec
	}

	if name == "" {
		return "", "", "", fmt.Errorf("empty ProxyJump host")
	}
	if port != "" {
		value, parseErr := strconv.ParseUint(port, 10, 16)
		if parseErr != nil || value == 0 {
			return "", "", "", fmt.Errorf("invalid ProxyJump port %q", port)
		}
	}

	return name, user, port, nil
}

func resolveProxyJumpHost(spec string, aliases map[string]SSHHost) (SSHHost, error) {
	name, user, port, err := parseProxyJumpSpec(spec)
	if err != nil {
		return SSHHost{}, err
	}

	host, found := aliases[name]
	if !found {
		host = SSHHost{Name: name, Hostname: name, Port: "22"}
	}
	if user != "" {
		host.User = user
	}
	if port != "" {
		host.Port = port
	}

	return normalizeSSHHost(host), nil
}

func resolveProxyJumpChain(proxyJump string, aliases map[string]SSHHost, visiting, added map[string]bool) ([]SSHHost, error) {
	if proxyJump == "" || strings.EqualFold(strings.TrimSpace(proxyJump), "none") {
		return nil, nil
	}

	var chain []SSHHost
	for _, spec := range strings.Split(proxyJump, ",") {
		jumpHost, err := resolveProxyJumpHost(spec, aliases)
		if err != nil {
			return nil, fmt.Errorf("invalid ProxyJump %q: %w", spec, err)
		}

		key := jumpHost.Name
		if visiting[key] {
			return nil, fmt.Errorf("ProxyJump cycle involving %s", key)
		}
		if added[key] {
			continue
		}

		visiting[key] = true
		nested, err := resolveProxyJumpChain(jumpHost.ProxyJump, aliases, visiting, added)
		delete(visiting, key)
		if err != nil {
			return nil, err
		}

		chain = append(chain, nested...)
		chain = append(chain, jumpHost)
		added[key] = true
	}

	return chain, nil
}

func proxyJumpHosts(host SSHHost) ([]SSHHost, error) {
	if host.ProxyJump == "" || strings.EqualFold(strings.TrimSpace(host.ProxyJump), "none") {
		return nil, nil
	}

	configPath := host.configPath
	hosts, err := ParseSSHConfig(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve ProxyJump hosts: %w", err)
	}

	aliases := make(map[string]SSHHost, len(hosts))
	for _, configuredHost := range hosts {
		aliases[configuredHost.Name] = configuredHost
	}

	visiting := map[string]bool{host.Name: true}
	return resolveProxyJumpChain(host.ProxyJump, aliases, visiting, make(map[string]bool))
}

func closeSSHClients(clients []*ssh.Client) {
	for i := len(clients) - 1; i >= 0; i-- {
		clients[i].Close()
	}
}

func jumpChainKey(hosts []SSHHost) string {
	var key strings.Builder
	for _, host := range hosts {
		host = normalizeSSHHost(host)
		fmt.Fprintf(&key, "%q\x00%q\x00%q\x00%q\x00%q\x00", host.Name, host.Hostname, host.User, host.Port, host.IdentityFile)
	}
	return key.String()
}

func connectJumpChain(hosts []SSHHost, deadline time.Time) ([]*ssh.Client, error) {
	var clients []*ssh.Client
	var proxy *ssh.Client
	for _, host := range hosts {
		client, err := connectSSHHost(host, proxy, deadline)
		if err != nil {
			closeSSHClients(clients)
			return nil, fmt.Errorf("failed to connect through jump host %s: %w", host.Name, err)
		}
		clients = append(clients, client)
		proxy = client
	}
	return clients, nil
}

func (c *SSHConnector) releaseJumpChain(key string, chain *sharedJumpChain) {
	c.mu.Lock()
	if chain.refs > 0 {
		chain.refs--
	}
	shouldClose := chain.refs == 0 && chain.complete
	if shouldClose {
		if current := c.jumpChains[key]; current == chain {
			delete(c.jumpChains, key)
		}
	}
	clients := chain.clients
	c.mu.Unlock()

	if shouldClose {
		closeSSHClients(clients)
	}
}

func (c *SSHConnector) acquireJumpChain(hosts []SSHHost, deadline time.Time) (*ssh.Client, func(), error) {
	if len(hosts) == 0 {
		return nil, nil, nil
	}

	key := jumpChainKey(hosts)
	c.mu.Lock()
	chain, found := c.jumpChains[key]
	if found {
		chain.refs++
	} else {
		chain = &sharedJumpChain{ready: make(chan struct{}), refs: 1}
		c.jumpChains[key] = chain
	}
	c.mu.Unlock()

	release := sync.OnceFunc(func() {
		c.releaseJumpChain(key, chain)
	})

	if !found {
		clients, err := connectJumpChain(hosts, deadline)

		c.mu.Lock()
		chain.clients = clients
		chain.err = err
		chain.complete = true
		close(chain.ready)
		shouldClose := chain.refs == 0
		if shouldClose {
			if current := c.jumpChains[key]; current == chain {
				delete(c.jumpChains, key)
			}
		}
		c.mu.Unlock()

		if shouldClose {
			closeSSHClients(clients)
		}
	} else {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			release()
			return nil, nil, context.DeadlineExceeded
		}
		timer := time.NewTimer(remaining)
		defer timer.Stop()
		select {
		case <-chain.ready:
		case <-timer.C:
			release()
			return nil, nil, context.DeadlineExceeded
		}
	}

	if chain.err != nil {
		release()
		return nil, nil, chain.err
	}
	if len(chain.clients) == 0 {
		release()
		return nil, nil, fmt.Errorf("jump chain connected without a client")
	}

	return chain.clients[len(chain.clients)-1], release, nil
}

func NewSSHClient(host SSHHost) (*SSHClient, error) {
	return defaultSSHConnector.NewSSHClient(host)
}

func (c *SSHConnector) NewSSHClient(host SSHHost) (*SSHClient, error) {
	host = normalizeSSHHost(host)
	timeout := getSSHTimeout()
	deadline := time.Now().Add(timeout)

	jumpHosts, err := proxyJumpHosts(host)
	if err != nil {
		return nil, err
	}

	proxy, releaseJumpChain, err := c.acquireJumpChain(jumpHosts, deadline)
	if err != nil {
		return nil, err
	}

	client, err := connectSSHHost(host, proxy, deadline)
	if err != nil {
		if releaseJumpChain != nil {
			releaseJumpChain()
		}
		return nil, err
	}

	return &SSHClient{
		client:           client,
		config:           &host,
		releaseJumpChain: releaseJumpChain,
	}, nil
}

func publicKeySigner(keyPath string) (ssh.Signer, error) {
	key, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, err
	}

	signer, err := ssh.ParsePrivateKey(key)
	if err != nil {
		// if it's an encrypted key error, we can't handle it without a passphrase
		// just return the error - SSH agent should handle these keys
		return nil, err
	}

	return signer, nil
}

func sshAgentSigners() ([]ssh.Signer, net.Conn, error) {
	socket := getValidatedSSHAuthSock()
	if socket == "" {
		return nil, nil, fmt.Errorf("SSH_AUTH_SOCK not set or invalid")
	}

	conn, err := net.Dial("unix", socket)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to connect to SSH agent: %w", err)
	}

	agentClient := agent.NewClient(conn)
	signers, err := agentClient.Signers()
	if err != nil {
		conn.Close()
		return nil, nil, fmt.Errorf("failed to list SSH agent identities: %w", err)
	}

	return signers, conn, nil
}

func isAllowedCommand(cmd string) bool {
	allowedPrefixes := []string{
		"lscpu ",
		"top -",
		"which ",
		"nvidia-smi ",
		"amd-smi ",
		"rocm-smi ",
		"free -",
		"df -",
	}

	cmd = strings.TrimSpace(cmd)
	for _, prefix := range allowedPrefixes {
		if strings.HasPrefix(cmd, prefix) {
			return true
		}
	}

	return false
}

func (c *SSHClient) ExecuteCommand(cmd string) (string, error) {
	if !isAllowedCommand(cmd) {
		return "", fmt.Errorf("command not in allowed list: %s", cmd)
	}

	session, err := c.client.NewSession()
	if err != nil {
		return "", err
	}
	defer session.Close()

	output, err := session.CombinedOutput(cmd)
	if err != nil {
		return string(output), err
	}

	return string(output), nil
}

func (c *SSHClient) Close() error {
	c.closeOnce.Do(func() {
		if c.client != nil {
			c.closeErr = c.client.Close()
		}
		if c.releaseJumpChain != nil {
			c.releaseJumpChain()
		}
	})
	return c.closeErr
}
