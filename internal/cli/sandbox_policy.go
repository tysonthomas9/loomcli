package cli

// openPolicyYAML is the default permissive sandbox policy for network: "open".
// It allows the sandbox to reach common ports (HTTPS, HTTP, SSH) and provides
// read-write access to the sandbox workspace and temp directories.
const openPolicyYAML = `version: 1
filesystem_policy:
  include_workdir: true
  read_only: [/usr, /lib, /etc, /proc, /dev/urandom, /opt, /var/log]
  read_write: [/sandbox, /tmp, /dev/null, /home]
network_policies:
  allow_all:
    endpoints:
      - { host: "**", ports: [443, 80, 22] }
    binaries:
      - { path: "/**" }
`
