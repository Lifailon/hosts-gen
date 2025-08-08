# hosts-gen

Standalone template engine for synchronizing hosts of any DNS server (with support for custom lists, e.g. [pi-hole](https://github.com/pi-hole/pi-hole) or [coredns](https://github.com/coredns/coredns)) and Proxy server in a stack with [docker-gen](https://github.com/nginx-proxy/docker-gen).

## How does this work?

When using [Nginx Proxy](https://github.com/nginx-proxy/nginx-proxy) with docker-gen, the proxy server configuration is updated automatically to forward requests to the Docker containers by the hostname specified in `VIRTUAL_HOST`. hosts-gen works in the same way, getting a list of all `VIRTUAL_HOST` from containers on remote servers where Proxy is running and updating the host list when there are changes. For this to work, the host list must be synchronized between the DNS server and hosts-gen using the volume mount mechanism. To apply changes to the DNS server (if necessary), you can specify an arbitrary command in `POST_COMMAND`.

## Launch

- Download a basic template to run a stack of DNS server (pi-hole) and Nginx Proxy:

```bash
cd $HOME
git clone https://github.com/Lifailon/hosts-gen dns-stack
cd dns-stack
```

- Fill in the list of variables in the [.env](/.env) file:

```bash
# List of proxy hosts with docker (must specify IP addresses separated by commas)
PROXY_IP_LIST=192.168.3.105,lifailon@192.168.3.106:2121
# Specify the proxy address if it is used to forward to all hosts with Docker (by default, each host is also a proxy)
PROXY_IP_ENDPOINT=
# Global parameters for ssh connecting to proxy hosts (lower priority)
SSH_USERNAME=lifailon
SSH_PASSWORD=
SSH_PORT=2121
# Host polling interval
UPDATE_INTERVAL=10s
# Path to hosts file inside container
DNS_HOSTS_PATH=/hostsfile
# The command to be executed after updating the host list
POST_COMMAND="docker exec pihole-server pihole reloaddns"
```

- Start the stack:

```bash
docker-compose up -d
```

> [!NOTE]
> To run the DNS server, you need to disable the built-in `DNSStubListener` in `/etc/systemd/resolved.conf` (used by default in Ubuntu).

## Example of use

- On the remote host on the network where the Nginx proxy is running, add the VIRTUAL_HOST variable to any (if the proxy uses `network_mode: "host"`) or the current stack:

```yml
nginx:
  image: nginx
  environment:
    - VIRTUAL_HOST=nginx.local
```

- Update the stack to update or start a new container:

```bash
docker-compose up -d
```

Within n-seconds (specified in the `UPDATE_INTERVAL` variable), a new record will be automatically added to the DNS server using `hosts-gen` and the Nginx configuration will be updated using `docker-gen`.

- Now on any host that is a client of the DNS server, go to the address `http://nginx.local`.