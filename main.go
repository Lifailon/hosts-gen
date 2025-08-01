package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"golang.org/x/crypto/ssh"
)

var (
	PROXY_HOSTS_ADDR = "lifailon@192.168.3.105:2121"
	SSH_USERNAME     = "lifailon"
	SSH_PORT         = "2121"
	SSH_PASSWORD     = ""
	SSH_KEY_PATH     = ""
)

func loadPrivateKey() ssh.AuthMethod {
	// Get private key
	homeDir, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("Failed to get home directory: %v", err)
	}
	keyPath := filepath.Join(homeDir, ".ssh", "id_rsa")

	// Read private key
	keyData, err := os.ReadFile(keyPath)
	if err != nil {
		log.Fatalf("Failed to read private key: %v", err)
	}

	// Get signer from private key
	signer, err := ssh.ParsePrivateKey(keyData)
	if err != nil {
		log.Fatalf("Failed to parse private key: %v", err)
	}

	return ssh.PublicKeys(signer)
}

func main() {
	USERNAME := strings.Split(PROXY_HOSTS_ADDR, "@")

	// ssh Configuration
	sshConfig := &ssh.ClientConfig{
		User:            USERNAME[0],
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
		Auth: []ssh.AuthMethod{
			loadPrivateKey(),
			ssh.Password(SSH_PASSWORD),
		},
	}

	// SSH Connection
	sshClient, err := ssh.Dial("tcp", USERNAME[1], sshConfig)
	if err != nil {
		panic(err)
	}
	defer sshClient.Close()

	// Create local TCP listener
	localListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	defer localListener.Close()

	go func() {
		for {
			localConn, err := localListener.Accept()
			if err != nil {
				return
			}

			// Connection to remote Docker Socket
			remoteConn, err := sshClient.Dial("unix", "/var/run/docker.sock")
			if err != nil {
				localConn.Close()
				continue
			}

			// Copy data between connections
			go func() {
				defer localConn.Close()
				defer remoteConn.Close()
				io.Copy(localConn, remoteConn)
			}()
			go func() {
				defer localConn.Close()
				defer remoteConn.Close()
				io.Copy(remoteConn, localConn)
			}()
		}
	}()

	// Create Docker client for local socket
	dockerClient, err := client.NewClientWithOpts(
		client.WithHost("tcp://"+localListener.Addr().String()),
		client.WithAPIVersionNegotiation(),
	)
	if err != nil {
		panic(err)
	}
	defer dockerClient.Close()

	// Get container list
	containers, err := dockerClient.ContainerList(context.Background(), container.ListOptions{All: true})
	if err != nil {
		panic(err)
	}

	// Run docker inspect from containers
	for _, c := range containers {
		inspect, _ := dockerClient.ContainerInspect(context.Background(), c.ID)
		envArr := inspect.Config.Env
		for _, e := range envArr {
			fmt.Println(e)
		}
	}

}
