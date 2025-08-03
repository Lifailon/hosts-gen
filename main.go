package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
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

func getVirtualHosts(sshGloabalParams Params, PROXY_IP, SSH_PASSWORD string) []string {
	// Array for return
	var virtualHosts []string

	// Get ssh params for current host
	sshParams := sshGloabalParams.parseParams(PROXY_IP)

	// ssh Configuration
	sshConfig := &ssh.ClientConfig{
		User:            sshParams.SSH_USERNAME,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
		Auth: []ssh.AuthMethod{
			loadPrivateKey(),
			ssh.Password(SSH_PASSWORD),
		},
	}

	// SSH Connection
	sshClient, err := ssh.Dial("tcp", sshParams.SSH_IP+":"+sshParams.SSH_PORT, sshConfig)
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

	// Create Docker client for local socket from ssh
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

func updateHostsFile(virtualHosts []string, DNS_HOSTS_PATH string) {
	// Sort env data
	sort.Strings(virtualHosts)
	// Check file
	_, err := os.Stat(DNS_HOSTS_PATH)
	if err != nil {
		fmt.Println("[INFO] Creating a hosts file")
		content := strings.Join(virtualHosts, "\n")
		fmt.Printf("[INFO] Number of hosts: %v\n", len(virtualHosts))
		err := os.WriteFile(DNS_HOSTS_PATH, []byte(content), 0644)
		if err != nil {
			panic(err)
		}
	} else {
		data, err := os.ReadFile(DNS_HOSTS_PATH)
		if err != nil {
			panic(err)
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
				fmt.Printf("[INFO] :: %v\n", vh)
			}
			content := strings.Join(virtualHosts, "\n")
			err := os.WriteFile(DNS_HOSTS_PATH, []byte(content), 0644)
			if err != nil {
				panic(err)
			}
		} else {
			fmt.Println("[INFO] No changes found")
		}
	}
}

func main() {
	// Get environments from system
	PROXY_IP_LIST := "lifailon@192.168.3.105:2121,lifailon@192.168.3.106:2121" // os.Getenv("PROXY_HOSTS")
	SSH_USERNAME := os.Getenv("SSH_USERNAME")
	SSH_PORT := os.Getenv("SSH_PORT")
	SSH_PASSWORD := os.Getenv("SSH_PASSWORD")
	DNS_HOSTS_PATH := "proxylist"

	// Fill in global ssh params or use default
	sshGloabalParams := &Params{}

	sshGloabalParams.SSH_USERNAME = SSH_USERNAME
	if sshGloabalParams.SSH_USERNAME == "" {
		sshGloabalParams.SSH_USERNAME = "root"
	}
	sshGloabalParams.SSH_PORT = SSH_PORT
	if sshGloabalParams.SSH_PORT == "" {
		sshGloabalParams.SSH_PORT = "22"
	}

	// Get hosts array from string
	PROXY_IP_ARRAY := strings.Split(PROXY_IP_LIST, ",")

	// Get environments from docker containers
	var virtualHosts []string
	for _, PROXY_IP := range PROXY_IP_ARRAY {
		vh := getVirtualHosts(*sshGloabalParams, PROXY_IP, SSH_PASSWORD)
		virtualHosts = append(virtualHosts, vh...)
	}

	// Compare the number of hosts from the environment and update the hosts file
	updateHostsFile(virtualHosts, DNS_HOSTS_PATH)
}
