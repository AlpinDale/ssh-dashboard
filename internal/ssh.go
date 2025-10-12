package internal

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"
)

type SSHHost struct {
	Name         string
	Hostname     string
	User         string
	Port         string
	IdentityFile string
}

type SSHClient struct {
	client *ssh.Client
	config *SSHHost
}

func ParseSSHConfig(configPath string) ([]SSHHost, error) {
	if configPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		configPath = filepath.Join(home, ".ssh", "config")
	}

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

		if key == "host" {
			if currentHost != nil && !strings.Contains(currentHost.Name, "*") && !strings.Contains(currentHost.Name, "?") {
				hosts = append(hosts, *currentHost)
			}

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
			}
		}
	}

	if currentHost != nil && !strings.Contains(currentHost.Name, "*") && !strings.Contains(currentHost.Name, "?") {
		hosts = append(hosts, *currentHost)
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return hosts, nil
}

func expandPath(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return filepath.Join(home, path[2:])
	}
	return path
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

func NewSSHClient(host SSHHost) (*SSHClient, error) {
	// Set defaults
	if host.Hostname == "" {
		host.Hostname = host.Name
	}
	if host.User == "" {
		host.User = os.Getenv("USER")
	}
	if host.Port == "" {
		host.Port = "22"
	}

	var authMethods []ssh.AuthMethod

	agentAuth, agentErr := sshAgentAuth()
	if agentErr == nil {
		authMethods = append(authMethods, agentAuth)
	} else {
		if host.IdentityFile != "" {
			if keyAuth, err := publicKeyAuth(host.IdentityFile); err == nil {
				authMethods = append(authMethods, keyAuth)
			}
		}

		home, err := os.UserHomeDir()
		if err == nil {
			defaultKeys := []string{
				filepath.Join(home, ".ssh", "id_rsa"),
				filepath.Join(home, ".ssh", "id_ed25519"),
				filepath.Join(home, ".ssh", "id_ecdsa"),
			}
			for _, keyPath := range defaultKeys {
				if keyAuth, err := publicKeyAuth(keyPath); err == nil {
					authMethods = append(authMethods, keyAuth)
				}
			}
		}
	}

	if len(authMethods) == 0 {
		return nil, fmt.Errorf("no authentication methods available")
	}

	hostKeyCallback, err := getHostKeyCallback()
	if err != nil {
		return nil, fmt.Errorf("failed to setup host key verification: %w", err)
	}

	config := &ssh.ClientConfig{
		User:            host.User,
		Auth:            authMethods,
		HostKeyCallback: hostKeyCallback,
		Timeout:         10 * time.Second,
	}

	addr := fmt.Sprintf("%s:%s", host.Hostname, host.Port)
	client, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to %s: %w", addr, err)
	}

	return &SSHClient{
		client: client,
		config: &host,
	}, nil
}

func publicKeyAuth(keyPath string) (ssh.AuthMethod, error) {
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

	return ssh.PublicKeys(signer), nil
}

func sshAgentAuth() (ssh.AuthMethod, error) {
	socket := os.Getenv("SSH_AUTH_SOCK")
	if socket == "" {
		return nil, fmt.Errorf("SSH_AUTH_SOCK not set")
	}

	conn, err := net.Dial("unix", socket)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to SSH agent: %w", err)
	}

	agentClient := agent.NewClient(conn)

	return ssh.PublicKeysCallback(agentClient.Signers), nil
}

func (c *SSHClient) ExecuteCommand(cmd string) (string, error) {
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
	if c.client != nil {
		return c.client.Close()
	}
	return nil
}
