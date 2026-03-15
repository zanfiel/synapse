package ssh

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	cryptossh "golang.org/x/crypto/ssh"
)

// Pool manages reusable SSH connections.
type Pool struct {
	conns   map[string]*cryptossh.Client
	mu      sync.Mutex
	timeout time.Duration
}

func NewPool(timeout time.Duration) *Pool {
	return &Pool{
		conns:   make(map[string]*cryptossh.Client),
		timeout: timeout,
	}
}

// Exec runs a command on a remote host. target format: user@host or user@host:port
func (p *Pool) Exec(target, keyPath, command string) (string, error) {
	client, err := p.getOrDial(target, keyPath)
	if err != nil {
		return "", err
	}

	session, err := client.NewSession()
	if err != nil {
		// Connection might be stale — reconnect
		p.mu.Lock()
		delete(p.conns, target)
		p.mu.Unlock()

		client, err = p.getOrDial(target, keyPath)
		if err != nil {
			return "", err
		}
		session, err = client.NewSession()
		if err != nil {
			return "", fmt.Errorf("ssh session: %w", err)
		}
	}
	defer session.Close()

	var stdout, stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr

	err = session.Run(command)
	output := stdout.String()
	if stderr.Len() > 0 && stdout.Len() == 0 {
		output = stderr.String()
	}

	if err != nil {
		return output, fmt.Errorf("ssh exec: %w\nstderr: %s", err, stderr.String())
	}
	return output, nil
}

func (p *Pool) getOrDial(target, keyPath string) (*cryptossh.Client, error) {
	p.mu.Lock()
	if c, ok := p.conns[target]; ok {
		p.mu.Unlock()
		// Test if alive
		_, _, err := c.SendRequest("keepalive@openssh.com", true, nil)
		if err == nil {
			return c, nil
		}
		p.mu.Lock()
		delete(p.conns, target)
	}
	p.mu.Unlock()

	// Parse target
	user, host, port := parseTarget(target)

	// Resolve key path
	if keyPath == "" {
		keyPath = defaultKeyPath()
	}

	keyData, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("read key %s: %w", keyPath, err)
	}

	signer, err := cryptossh.ParsePrivateKey(keyData)
	if err != nil {
		return nil, fmt.Errorf("parse key: %w", err)
	}

	config := &cryptossh.ClientConfig{
		User: user,
		Auth: []cryptossh.AuthMethod{
			cryptossh.PublicKeys(signer),
		},
		HostKeyCallback: cryptossh.InsecureIgnoreHostKey(),
		Timeout:         p.timeout,
	}

	addr := net.JoinHostPort(host, port)
	client, err := cryptossh.Dial("tcp", addr, config)
	if err != nil {
		return nil, fmt.Errorf("ssh dial %s: %w", addr, err)
	}

	p.mu.Lock()
	p.conns[target] = client
	p.mu.Unlock()

	return client, nil
}

// Close shuts down all connections.
func (p *Pool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.conns {
		c.Close()
	}
	p.conns = make(map[string]*cryptossh.Client)
}

func parseTarget(target string) (user, host, port string) {
	port = "22"
	parts := strings.SplitN(target, "@", 2)
	if len(parts) == 2 {
		user = parts[0]
		host = parts[1]
	} else {
		user = "root"
		host = parts[0]
	}

	if h, p, err := net.SplitHostPort(host); err == nil {
		host = h
		port = p
	}

	return
}

func defaultKeyPath() string {
	home, _ := os.UserHomeDir()
	if runtime.GOOS == "windows" {
		return filepath.Join(home, ".ssh", "id_rsa")
	}
	return filepath.Join(home, ".ssh", "id_rsa")
}
