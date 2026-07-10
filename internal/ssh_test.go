package internal

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestGetSSHTimeout(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  time.Duration
	}{
		{name: "unset uses default", want: defaultSSHTimeout},
		{name: "whole seconds", value: "3", want: 3 * time.Second},
		{name: "decimal seconds", value: "0.25", want: 250 * time.Millisecond},
		{name: "invalid value uses default", value: "soon", want: defaultSSHTimeout},
		{name: "too small uses default", value: "0.001", want: defaultSSHTimeout},
		{name: "too large uses default", value: "3601", want: defaultSSHTimeout},
		{name: "not a number uses default", value: "NaN", want: defaultSSHTimeout},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(sshTimeoutEnv, tt.value)
			if got := getSSHTimeout(); got != tt.want {
				t.Fatalf("getSSHTimeout() = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestDialSSHTimesOutDuringHandshake(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	accepted := make(chan struct{})
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		close(accepted)
		defer conn.Close()
		_, _ = io.Copy(io.Discard, conn)
	}()

	timeout := 50 * time.Millisecond
	started := time.Now()
	client, err := dialSSH(listener.Addr().String(), &ssh.ClientConfig{
		User:            "test",
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}, timeout)
	elapsed := time.Since(started)

	if client != nil {
		client.Close()
		t.Fatal("dialSSH() unexpectedly connected")
	}
	if err == nil {
		t.Fatal("dialSSH() returned no error")
	}
	if elapsed < timeout/2 {
		t.Fatalf("dialSSH() returned too early after %s", elapsed)
	}
	if elapsed > time.Second {
		t.Fatalf("dialSSH() exceeded timeout by too much: %s", elapsed)
	}

	select {
	case <-accepted:
	case <-time.After(time.Second):
		t.Fatal("server never accepted the connection")
	}
}

func TestParseSSHConfigResolvesProxyJumpChain(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config")
	config := `Host edge
    HostName 192.0.2.10
    User edge-user
    Port 2222

Host jump jump-alias
    HostName 192.0.2.20
    User jump-user
    ProxyJump edge

Host target
    HostName 10.0.0.20
    User target-user
    ProxyJump override@jump:2200
`
	if err := os.WriteFile(configPath, []byte(config), 0600); err != nil {
		t.Fatalf("write SSH config: %v", err)
	}

	hosts, err := ParseSSHConfig(configPath)
	if err != nil {
		t.Fatalf("ParseSSHConfig() error: %v", err)
	}

	var target SSHHost
	for _, host := range hosts {
		if host.Name == "target" {
			target = host
			break
		}
	}
	if target.ProxyJump != "override@jump:2200" {
		t.Fatalf("target ProxyJump = %q", target.ProxyJump)
	}
	var foundJumpAlias bool
	for _, host := range hosts {
		if host.Name == "jump-alias" {
			foundJumpAlias = true
			if host.Hostname != "192.0.2.20" || host.User != "jump-user" {
				t.Fatalf("jump alias = %+v", host)
			}
		}
	}
	if !foundJumpAlias {
		t.Fatal("second alias from Host block was not parsed")
	}

	chain, err := proxyJumpHosts(target)
	if err != nil {
		t.Fatalf("proxyJumpHosts() error: %v", err)
	}
	if len(chain) != 2 {
		t.Fatalf("proxyJumpHosts() returned %d hosts, want 2", len(chain))
	}
	if got := chain[0]; got.Name != "edge" || got.Hostname != "192.0.2.10" || got.User != "edge-user" || got.Port != "2222" {
		t.Fatalf("first jump host = %+v", got)
	}
	if got := chain[1]; got.Name != "jump" || got.Hostname != "192.0.2.20" || got.User != "override" || got.Port != "2200" {
		t.Fatalf("second jump host = %+v", got)
	}
}

func TestResolveProxyJumpChainRejectsCycle(t *testing.T) {
	aliases := map[string]SSHHost{
		"jump-a": {Name: "jump-a", ProxyJump: "jump-b"},
		"jump-b": {Name: "jump-b", ProxyJump: "jump-a"},
	}

	_, err := resolveProxyJumpChain("jump-a", aliases, make(map[string]bool), make(map[string]bool))
	if err == nil {
		t.Fatal("resolveProxyJumpChain() accepted a cycle")
	}
}

func TestDialSSHThroughProxy(t *testing.T) {
	targetAddr := startTestSSHServer(t, func(channel ssh.NewChannel) {
		_ = channel.Reject(ssh.UnknownChannelType, "unsupported channel")
	})

	jumpAddr := startTestSSHServer(t, forwardDirectTCPIP)
	config := &ssh.ClientConfig{
		User:            "test",
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	jumpClient, err := dialSSH(jumpAddr, config, time.Second)
	if err != nil {
		t.Fatalf("connect to jump server: %v", err)
	}
	defer jumpClient.Close()

	targetClient, err := dialSSHWithDeadline(targetAddr, config, jumpClient, time.Now().Add(time.Second))
	if err != nil {
		t.Fatalf("connect to target through jump server: %v", err)
	}
	targetClient.Close()
}

func TestDialSSHThroughProxyTimesOutDuringHandshake(t *testing.T) {
	targetListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for target: %v", err)
	}
	defer targetListener.Close()

	targetAccepted := make(chan struct{})
	go func() {
		conn, err := targetListener.Accept()
		if err != nil {
			return
		}
		close(targetAccepted)
		defer conn.Close()
		_, _ = io.Copy(io.Discard, conn)
	}()

	jumpAddr := startTestSSHServer(t, forwardDirectTCPIP)
	config := &ssh.ClientConfig{
		User:            "test",
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}
	jumpClient, err := dialSSH(jumpAddr, config, time.Second)
	if err != nil {
		t.Fatalf("connect to jump server: %v", err)
	}
	defer jumpClient.Close()

	timeout := 50 * time.Millisecond
	started := time.Now()
	client, err := dialSSHWithDeadline(targetListener.Addr().String(), config, jumpClient, time.Now().Add(timeout))
	elapsed := time.Since(started)
	if client != nil {
		client.Close()
		t.Fatal("dialSSHWithDeadline() unexpectedly connected")
	}
	if err == nil {
		t.Fatal("dialSSHWithDeadline() returned no error")
	}
	if elapsed < timeout/2 || elapsed > time.Second {
		t.Fatalf("tunneled handshake timed out after %s, want approximately %s", elapsed, timeout)
	}

	select {
	case <-targetAccepted:
	case <-time.After(time.Second):
		t.Fatal("target never accepted the tunneled connection")
	}
}

func TestNewSSHClientConfigTriesAllDefaultKeys(t *testing.T) {
	home := t.TempDir()
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0700); err != nil {
		t.Fatalf("create .ssh directory: %v", err)
	}

	_, rejectedKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate rejected key: %v", err)
	}
	_, acceptedKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate accepted key: %v", err)
	}
	writeTestPrivateKey(t, filepath.Join(sshDir, "id_rsa"), rejectedKey)
	writeTestPrivateKey(t, filepath.Join(sshDir, "id_ed25519"), acceptedKey)

	acceptedSigner, err := ssh.NewSignerFromKey(acceptedKey)
	if err != nil {
		t.Fatalf("create accepted signer: %v", err)
	}
	serverConfig := &ssh.ServerConfig{
		PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if !bytes.Equal(key.Marshal(), acceptedSigner.PublicKey().Marshal()) {
				return nil, fmt.Errorf("unknown key")
			}
			return nil, nil
		},
	}
	serverAddr := startTestSSHServerWithConfig(t, serverConfig, func(channel ssh.NewChannel) {
		_ = channel.Reject(ssh.UnknownChannelType, "unsupported channel")
	})

	t.Setenv("HOME", home)
	t.Setenv("SSH_AUTH_SOCK", "")
	config, closeAuth, err := newSSHClientConfig(SSHHost{Name: "target", User: "test"}, time.Second)
	if err != nil {
		t.Fatalf("newSSHClientConfig() error: %v", err)
	}
	defer closeAuth()
	config.HostKeyCallback = ssh.InsecureIgnoreHostKey()

	client, err := dialSSH(serverAddr, config, time.Second)
	if err != nil {
		t.Fatalf("dialSSH() did not try the accepted fallback key: %v", err)
	}
	client.Close()
}

func writeTestPrivateKey(t *testing.T, path string, key ed25519.PrivateKey) {
	t.Helper()
	block, err := ssh.MarshalPrivateKey(key, "test key")
	if err != nil {
		t.Fatalf("marshal private key: %v", err)
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0600); err != nil {
		t.Fatalf("write private key: %v", err)
	}
}

func startTestSSHServer(t *testing.T, handleChannel func(ssh.NewChannel)) string {
	t.Helper()
	return startTestSSHServerWithConfig(t, &ssh.ServerConfig{NoClientAuth: true}, handleChannel)
}

func startTestSSHServerWithConfig(t *testing.T, serverConfig *ssh.ServerConfig, handleChannel func(ssh.NewChannel)) string {
	t.Helper()

	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate host key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatalf("create host signer: %v", err)
	}

	serverConfig.AddHostKey(signer)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		serverConn, channels, requests, err := ssh.NewServerConn(conn, serverConfig)
		if err != nil {
			return
		}
		defer serverConn.Close()
		go ssh.DiscardRequests(requests)

		for channel := range channels {
			go handleChannel(channel)
		}
	}()

	t.Cleanup(func() {
		listener.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("test SSH server did not stop")
		}
	})

	return listener.Addr().String()
}

func forwardDirectTCPIP(newChannel ssh.NewChannel) {
	if newChannel.ChannelType() != "direct-tcpip" {
		_ = newChannel.Reject(ssh.UnknownChannelType, "unsupported channel")
		return
	}

	var request struct {
		Host       string
		Port       uint32
		OriginHost string
		OriginPort uint32
	}
	if err := ssh.Unmarshal(newChannel.ExtraData(), &request); err != nil {
		_ = newChannel.Reject(ssh.ConnectionFailed, "invalid forwarding request")
		return
	}

	addr := net.JoinHostPort(request.Host, strconv.Itoa(int(request.Port)))
	upstream, err := net.Dial("tcp", addr)
	if err != nil {
		_ = newChannel.Reject(ssh.ConnectionFailed, err.Error())
		return
	}

	channel, requests, err := newChannel.Accept()
	if err != nil {
		upstream.Close()
		return
	}
	go ssh.DiscardRequests(requests)

	go func() {
		_, _ = io.Copy(channel, upstream)
		_ = channel.CloseWrite()
	}()
	go func() {
		_, _ = io.Copy(upstream, channel)
		upstream.Close()
		channel.Close()
	}()
}
