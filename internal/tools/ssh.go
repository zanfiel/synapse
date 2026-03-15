package tools

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

func SSHTool() *ToolDef {
	return &ToolDef{
		Name:        "ssh",
		Description: "Execute a command on a remote server via SSH. Uses key-based authentication.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"host": map[string]interface{}{
					"type":        "string",
					"description": "SSH host (user@host or host). Can include port as user@host:port",
				},
				"command": map[string]interface{}{
					"type":        "string",
					"description": "Command to execute on the remote server",
				},
				"key": map[string]interface{}{
					"type":        "string",
					"description": "Path to SSH private key (default: ~/.ssh/id_rsa)",
				},
				"timeout": map[string]interface{}{
					"type":        "number",
					"description": "Timeout in seconds (default 30)",
				},
			},
			"required": []string{"host", "command"},
		},
		Execute: func(args map[string]interface{}) (string, error) {
			host := getStr(args, "host")
			command := getStr(args, "command")
			keyPath := getStr(args, "key")
			timeout := getInt(args, "timeout")

			if host == "" || command == "" {
				return "", fmt.Errorf("host and command are required")
			}

			if timeout <= 0 {
				timeout = 30
			}

			// Parse host string
			user := "root"
			port := "22"
			hostname := host

			if strings.Contains(host, "@") {
				parts := strings.SplitN(host, "@", 2)
				user = parts[0]
				hostname = parts[1]
			}
			if strings.Contains(hostname, ":") {
				parts := strings.SplitN(hostname, ":", 2)
				hostname = parts[0]
				port = parts[1]
			}

			// Find SSH key
			if keyPath == "" {
				home, _ := os.UserHomeDir()
				if runtime.GOOS == "windows" {
					keyPath = filepath.Join(home, ".ssh", "id_rsa")
				} else {
					keyPath = filepath.Join(home, ".ssh", "id_rsa")
				}
			}

			keyData, err := os.ReadFile(keyPath)
			if err != nil {
				return "", fmt.Errorf("read key %s: %w", keyPath, err)
			}

			signer, err := ssh.ParsePrivateKey(keyData)
			if err != nil {
				return "", fmt.Errorf("parse key: %w", err)
			}

			config := &ssh.ClientConfig{
				User: user,
				Auth: []ssh.AuthMethod{
					ssh.PublicKeys(signer),
				},
				HostKeyCallback: ssh.InsecureIgnoreHostKey(),
				Timeout:         time.Duration(timeout) * time.Second,
			}

			addr := net.JoinHostPort(hostname, port)
			client, err := ssh.Dial("tcp", addr, config)
			if err != nil {
				return "", fmt.Errorf("ssh connect %s: %w", addr, err)
			}
			defer client.Close()

			session, err := client.NewSession()
			if err != nil {
				return "", fmt.Errorf("ssh session: %w", err)
			}
			defer session.Close()

			var stdout, stderr bytes.Buffer
			session.Stdout = &stdout
			session.Stderr = &stderr

			err = session.Run(command)

			var result strings.Builder
			if stdout.Len() > 0 {
				result.WriteString(stdout.String())
			}
			if stderr.Len() > 0 {
				if result.Len() > 0 {
					result.WriteString("\n")
				}
				result.WriteString(stderr.String())
			}

			output := result.String()

			// Truncate
			if len(output) > maxOutputBytes {
				output = output[len(output)-maxOutputBytes:]
				output = "... (truncated)\n" + output
			}

			if err != nil {
				return fmt.Sprintf("%s\n\nSSH command failed: %s", output, err), nil
			}

			return output, nil
		},
	}
}
