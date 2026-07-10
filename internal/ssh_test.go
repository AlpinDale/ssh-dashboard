package internal

import (
	"io"
	"net"
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
