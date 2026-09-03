variable "project_id" {
  description = "GCP project that owns every resource in this module."
  type        = string
}

variable "region" {
  description = "Region for the bucket, the subnet, and (if enabled) Cloud NAT."
  type        = string
  default     = "us-central1"
}

variable "zone" {
  description = "Zone for the VM. Must be inside var.region."
  type        = string
  default     = "us-central1-a"
}

variable "name" {
  description = <<-EOT
    Prefix for every resource name. Also the network tag the firewall rules
    target, so two stacks with different names cannot reach each other's ports.
  EOT
  type        = string
  default     = "loom-stack"

  validation {
    condition     = can(regex("^[a-z][-a-z0-9]{4,28}[a-z0-9]$", var.name))
    error_message = "name must be 6-30 characters, lowercase, start with a letter, and end with a letter or digit."
  }
}

variable "machine_type" {
  description = <<-EOT
    e2-standard-2 (2 vCPU / 8 GB, ~$49/mo running) is the measured-good size:
    the loom daemon spawns agent processes and tmux sessions alongside fleet-db,
    Redis and Caddy. e2-medium (~$24/mo) fits a fleet-db-only stack.
  EOT
  type        = string
  default     = "e2-standard-2"
}

variable "boot_disk_gb" {
  description = "Boot disk size. Container images plus agent worktrees are the bulk of it."
  type        = number
  default     = 50
}

variable "enable_external_ip" {
  description = <<-EOT
    Give the VM a public IP. false is the recommended posture: reach the stack
    over IAP instead (see the iap_tunnel_* outputs). Turning it off means the VM
    has no route to the internet, so enable_cloud_nat must be true or image
    pulls and apt will hang.
  EOT
  type        = bool
  default     = false
}

variable "enable_cloud_nat" {
  description = "Create a Cloud Router + NAT so a VM with no external IP can still reach the internet."
  type        = bool
  default     = true
}

variable "subnet_cidr" {
  description = <<-EOT
    Range for this stack's subnet. Every stack builds its own VPC, so stacks
    cannot see each other and the SAME range is correct for all of them --
    there is nothing to allocate and nothing to collide. Only change this if
    the stack has to be peered with something that already uses this range.
  EOT
  type        = string
  default     = "10.90.0.0/24"
}

variable "iap_web_ports" {
  description = <<-EOT
    TCP ports reachable through IAP tunnels, on top of SSH, in the order
    [fleet-db, loom API, Caddy-served UI]. Defaults match the local-mode
    stack. wait-healthy and smoke read these back out of Terraform outputs
    rather than hardcoding them, so overriding this does not strand the
    health gate probing a port nothing is listening on.
  EOT
  type        = list(string)
  default     = ["8280", "8282", "8283"]

  validation {
    condition = (length(var.iap_web_ports) == 3 &&
      alltrue([
        for port in var.iap_web_ports :
        can(tonumber(port)) &&
        tonumber(port) >= 1 &&
        tonumber(port) <= 65535 &&
        tonumber(port) != 22 &&
        floor(tonumber(port)) == tonumber(port)
      ]) &&
      length(distinct([
        for port in var.iap_web_ports : can(tonumber(port)) ? tonumber(port) : 0
    ])) == 3)
    error_message = "iap_web_ports must contain exactly three unique integer TCP ports from 1 through 65535, excluding SSH port 22, in [fleet-db, loom API, UI] order."
  }
}

variable "tunnel_port_base" {
  description = <<-EOT
    Required local port for the UI IAP tunnel. The API and fleet-db tunnels
    use this port plus one and plus two. Set a unique value for each stack
    tunnelled from the same workstation; Terraform does not attempt to act as
    a global local-port allocator.
  EOT
  type        = number

  validation {
    condition     = var.tunnel_port_base >= 1024 && var.tunnel_port_base <= 65533 && floor(var.tunnel_port_base) == var.tunnel_port_base
    error_message = "tunnel_port_base must be an integer from 1024 through 65533 so the three tunnel ports fit in the TCP range."
  }
}

variable "fleetdb_image" {
  description = "fleet-db container image. Pin by digest or commit tag, never :latest."
  type        = string
}

variable "loom_image" {
  description = "loom container image (the local-mode build). Pin by digest or commit tag."
  type        = string
}

variable "ui_image" {
  description = <<-EOT
    Image that serves the built SPA and proxies /api, /sse and /ws-tab to loom.

    Required, with no default. `loom serve` answers only the API paths -- the
    frontend is a separate static bundle, which the repo's compose file mounts
    from a local checkout. A provisioned VM has no checkout, so the bundle has
    to travel inside an image. Build one with deploy/terraform/ui/Dockerfile
    after `make build-frontend`.
  EOT
  type        = string
}

variable "redis_image" {
  type = string
  # The VM image is amd64; pin the architecture-specific digest so Docker
  # cannot select an incompatible arm64 image when given a digest reference.
  default = "docker.io/library/redis:7-alpine@sha256:1db42ccef14898aa29bae778452d567534b59c107129cbc1163fb552de184d3c"
}

variable "loom_workspace" {
  description = "Workspace key the stack seeds on first boot."
  type        = string
  default     = "LOOMGCP"
}

variable "labels" {
  description = "Labels applied to every resource that supports them."
  type        = map(string)
  default     = {}
}

variable "ephemeral" {
  description = <<-EOT
    Mark the stack disposable. Lets `terraform destroy` remove a bucket that
    still has objects in it, which is what you want for a test stack and not
    what you want for anything real.
  EOT
  type        = bool
  default     = true
}

variable "registry_host" {
  description = <<-EOT
    Artifact Registry host the VM authenticates docker against, e.g.
    us-central1-docker.pkg.dev. Defaults to the stack's region.
  EOT
  type        = string
  default     = ""
}

variable "registry_project" {
  description = "Artifact Registry project. Empty uses project_id."
  type        = string
  default     = ""
}

variable "registry_location" {
  description = "Artifact Registry location. Empty uses region."
  type        = string
  default     = ""
}

variable "registry_repository" {
  description = "Artifact Registry repository containing the fleet-db, loom, and UI images."
  type        = string
  default     = "loom"
}

variable "codex_auth_secret" {
  description = <<-EOT
    Name of an EXISTING Secret Manager secret holding a codex `auth.json`.
    Empty (the default) runs the stack on the deterministic `localdogfood`
    backend with no AI credentials anywhere.

    Terraform does not create this secret: it holds a personal OpenAI
    credential, so you create it deliberately and it is never in state.

      gcloud secrets create loom-codex-auth --replication-policy=automatic \
        --data-file="$HOME/.codex/auth.json"

    Setting it switches LOOM_BACKEND to codex, which is what makes the
    entrypoint's prepare_codex_credentials() run at all. It also requires a
    loom image built with INSTALL_CODEX=true (`make images CODEX=1`).
  EOT
  type        = string
  default     = ""
}

variable "plan_role_read_only" {
  description = <<-EOT
    Keep the built-in `plan` role read-only.

    Inert on localdogfood, so true is correct there. Under codex it selects a
    real `--sandbox read-only`, which is bubblewrap -- and bubblewrap does not
    run inside a stock Docker container. Measured on this stack, in order:

      default            bwrap: No permissions to create a new namespace
                         (Docker's seccomp profile denies clone/unshare with
                         CLONE_NEWUSER unless the container has CAP_SYS_ADMIN)
      seccomp relaxed    bwrap: Failed to make / slave: Permission denied
                         (AppArmor's docker-default profile denies mount)

    Clearing both would mean stripping the container's real isolation to enable
    a weaker sandbox inside it. This stack does not make that trade, especially
    since codex's read-only sandbox also carries --unshare-net while the planner
    reaches Loom over HTTP.

    The failure is silent: the agent exits 0 having done nothing and the daemon
    retries it forever while the stack stays green. So set FALSE whenever the
    backend is codex; `make up CODEX=1` does it for you.
  EOT
  type        = bool
  default     = true
}
