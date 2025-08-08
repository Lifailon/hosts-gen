package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"golang.org/x/crypto/ssh"
)

type Params struct {
	SSH_USERNAME string
	SSH_IP       string
	SSH_PORT     string
}

func (sshParams *Params) parseParams(host string) *Params {
	var userName, port string
	// Get username
	if strings.Contains(host, "@") {
		hostSplit := strings.Split(host, "@")
		userName = hostSplit[0]
		host = hostSplit[1]
	} else {
		userName = sshParams.SSH_USERNAME
	}
	// Get ip and port
	if strings.Contains(host, ":") {
		hostSplit := strings.Split(host, ":")
		host = hostSplit[0]
		port = hostSplit[1]
	} else {
		port = sshParams.SSH_PORT
	}
	return &Params{
		SSH_USERNAME: userName,
		SSH_IP:       host,
		SSH_PORT:     port,
	}
}

func loadPrivateKey() ssh.AuthMethod {
	// Get private key path
	homeDir, err := os.UserHomeDir()
	if err != nil {
		fmt.Printf("[ERROR] Failed to get home directory: %v\n", err)
		return nil
	}
	keyPath := filepath.Join(homeDir, ".ssh", "id_rsa")

	// Read private key
	keyData, err := os.ReadFile(keyPath)
	if err != nil {
		fmt.Printf("[ERROR] Failed to read private key: %v\n", err)
		return nil
	}

	// Get signer from private key
	signer, err := ssh.ParsePrivateKey(keyData)
	if err != nil {
		fmt.Printf("[ERROR] Failed to parse private key: %v\n", err)
		return nil
	}

	return ssh.PublicKeys(signer)
}

func getVirtualHosts(sshGloabalParams Params, PROXY_IP, SSH_PASSWORD string, sshKey ssh.AuthMethod) []string {
	// Array for return
	var virtualHosts []string

	// Get ssh params for current host
	sshParams := sshGloabalParams.parseParams(PROXY_IP)

	// ssh configuration
	sshConfig := &ssh.ClientConfig{
		User:            sshParams.SSH_USERNAME,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
		Auth: []ssh.AuthMethod{
			ssh.Password(SSH_PASSWORD),
		},
	}

	// Add private key to ssh configuration
	if sshKey != nil {
		sshConfig.Auth = append(sshConfig.Auth, sshKey)
	}

	// SSH Connection
	sshClient, err := ssh.Dial("tcp", sshParams.SSH_IP+":"+sshParams.SSH_PORT, sshConfig)
	if err != nil {
		fmt.Printf("[ERROR] Failed to connect to %v via SSH: %v\n", sshParams.SSH_IP, err)
		return virtualHosts
	}
	defer sshClient.Close()

	// Create local TCP listener
	localListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Printf("[ERROR] Failed to create local listener: %v\n", err)
		return virtualHosts
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

	// Create Docker client for local socket from ssh
	dockerClient, err := client.NewClientWithOpts(
		client.WithHost("tcp://"+localListener.Addr().String()),
		client.WithAPIVersionNegotiation(),
	)
	if err != nil {
		fmt.Printf("[ERROR] Failed to create Docker client: %v\n", err)
		return virtualHosts
	}
	defer dockerClient.Close()

	// Get container list
	containers, err := dockerClient.ContainerList(context.Background(), container.ListOptions{All: true})
	if err != nil {
		fmt.Printf("[ERROR] Failed to get container list: %v\n", err)
		return virtualHosts
	}

	// Extracting variable list from all containers
	for _, c := range containers {
		inspect, _ := dockerClient.ContainerInspect(context.Background(), c.ID)
		envArr := inspect.Config.Env
		// Find VIRTUAL_HOST
		for _, e := range envArr {
			if strings.Contains(e, "VIRTUAL_HOST=") {
				vh := strings.ReplaceAll(e, "VIRTUAL_HOST=", "")
				virtualHosts = append(virtualHosts, sshParams.SSH_IP+" "+vh)
			}
		}
	}

	return virtualHosts
}

func updateHostsFile(virtualHosts []string, DNS_HOSTS_PATH, POST_COMMAND string) {
	// Sort env data
	sort.Strings(virtualHosts)
	// Check file
	_, err := os.Stat(DNS_HOSTS_PATH)
	if err != nil {
		fmt.Println("[INFO] Creating a hosts file")
		for _, vh := range virtualHosts {
			fmt.Printf("[DEBUG] %v\n", vh)
		}
		content := strings.Join(virtualHosts, "\n")
		fmt.Printf("[INFO] Number of hosts: %v\n", len(virtualHosts))
		err := os.WriteFile(DNS_HOSTS_PATH, []byte(content), 0644)
		if err != nil {
			fmt.Printf("[ERROR] Failed to write hosts file: %v\n", err)
			return
		}
		if POST_COMMAND != "" {
			cmdParts := strings.Fields(POST_COMMAND)
			cmd := exec.Command(cmdParts[0], cmdParts[1:]...)
			_, err := cmd.Output()
			if err != nil {
				fmt.Printf("[ERROR] Error execution post command: %v\n", err)
			} else {
				fmt.Printf("[INFO] Execution post command: %v\n", POST_COMMAND)
			}
		}
	} else {
		data, err := os.ReadFile(DNS_HOSTS_PATH)
		if err != nil {
			fmt.Printf("[ERROR] Failed to read hosts file: %v\n", err)
			return
		}
		hostData := strings.Split(string(data), "\n")
		// Sort file data
		sort.Strings(hostData)
		// Diff env and file
		if !reflect.DeepEqual(hostData, virtualHosts) {
			fmt.Println("[INFO] Changes detected in host list")
			fmt.Printf("[INFO] Number of hosts before: %v\n", len(hostData))
			fmt.Printf("[INFO] Number of hosts after: %v\n", len(virtualHosts))
			for _, vh := range virtualHosts {
				fmt.Printf("[DEBUG] %v\n", vh)
			}
			content := strings.Join(virtualHosts, "\n")
			err := os.WriteFile(DNS_HOSTS_PATH, []byte(content), 0644)
			if err != nil {
				fmt.Printf("[ERROR] Failed to write hosts file: %v\n", err)
				return
			}
			if POST_COMMAND != "" {
				cmdParts := strings.Fields(POST_COMMAND)
				cmd := exec.Command(cmdParts[0], cmdParts[1:]...)
				_, err := cmd.Output()
				if err != nil {
					fmt.Printf("[ERROR] Error execution post command: %v\n", err)
				} else {
					fmt.Printf("[INFO] Execution post command: %v\n", POST_COMMAND)
				}
			}
		}
	}
}

func main() {
	fmt.Println("[INFO] Starting hosts-gen")

	// Get environments from system
	PROXY_IP_LIST := os.Getenv("PROXY_IP_LIST")
	SSH_USERNAME := os.Getenv("SSH_USERNAME")
	SSH_PASSWORD := os.Getenv("SSH_PASSWORD")
	SSH_PORT := os.Getenv("SSH_PORT")
	UPDATE_INTERVAL := os.Getenv("UPDATE_INTERVAL")
	DNS_HOSTS_PATH := os.Getenv("DNS_HOSTS_PATH")
	POST_COMMAND := os.Getenv("POST_COMMAND")

	// Global ssh params
	sshGloabalParams := &Params{}

	// Check env and use default
	sshGloabalParams.SSH_USERNAME = SSH_USERNAME
	if sshGloabalParams.SSH_USERNAME == "" {
		sshGloabalParams.SSH_USERNAME = "root"
	}
	sshGloabalParams.SSH_PORT = SSH_PORT
	if sshGloabalParams.SSH_PORT == "" {
		sshGloabalParams.SSH_PORT = "22"
	}
	if UPDATE_INTERVAL == "" {
		UPDATE_INTERVAL = "30s"
	}
	// Convert string to int for interval (timeout)
	updateInterval, err := time.ParseDuration(UPDATE_INTERVAL)
	if err != nil {
		fmt.Printf("[ERROR] Interval format error: %v\n", err)
		updateInterval, _ = time.ParseDuration("30s")
	}

	// Get private key
	sshKey := loadPrivateKey()

	// Get hosts array from string
	PROXY_IP_ARRAY := strings.Split(PROXY_IP_LIST, ",")

	fmt.Println("[INFO] Proxy hosts:")
	for _, proxy_ip := range PROXY_IP_ARRAY {
		fmt.Printf("[INFO] - %v\n", proxy_ip)
	}

	// Signal channel for graceful shutdown
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	for {
		// Get environments from docker containers
		var virtualHosts []string
		for _, PROXY_IP := range PROXY_IP_ARRAY {
			vh := getVirtualHosts(*sshGloabalParams, PROXY_IP, SSH_PASSWORD, sshKey)
			virtualHosts = append(virtualHosts, vh...)
		}

		// Compare the number of hosts from the environment and update the hosts file
		updateHostsFile(virtualHosts, DNS_HOSTS_PATH, POST_COMMAND)

		select {
		case <-stop:
			fmt.Println("[INFO] Stopping hosts-gen")
			return
		case <-time.After(updateInterval):
		}
	}
}
