# Network, subnet, router, and NAT are shared project-bootstrap resources owned
# by the module in shared/. This stack only looks them up and installs three
# tag-scoped firewall rules. Isolation between stacks is the deny rule at
# priority 1100: round 1 measured peer traffic to 8280/8282/8283/22 blocked.
# Round 2's per-stack VPCs avoided the NAT collision but hit the project's
# five-network quota at four stacks, so networks cannot be stack resources.
#
# The stack is reached through IAP TCP forwarding, not a public IP. Google
# brokers those connections from a fixed range, so the allow rules open the
# ports to that range only.
locals {
  iap_source_range = "35.235.240.0/20"

  # Lower number = higher precedence. The gap is deliberate; see deny_non_iap.
  allow_priority = 1000
  deny_priority  = 1100
}

data "google_compute_network" "shared" {
  name    = var.network_name
  project = var.project_id
}

data "google_compute_subnetwork" "shared" {
  name    = "${var.network_name}-${var.region}"
  project = var.project_id
  region  = var.region
}

resource "google_compute_firewall" "iap_ssh" {
  name        = "${var.name}-allow-iap-ssh"
  project     = var.project_id
  network     = data.google_compute_network.shared.id
  description = "SSH from Google's IAP forwarding range only."

  allow {
    protocol = "tcp"
    ports    = ["22"]
  }

  priority      = local.allow_priority
  source_ranges = [local.iap_source_range]
  target_tags   = [var.name]
}

resource "google_compute_firewall" "iap_web" {
  name        = "${var.name}-allow-iap-web"
  project     = var.project_id
  network     = data.google_compute_network.shared.id
  description = "Stack HTTP ports, reachable only through an IAP tunnel."

  allow {
    protocol = "tcp"
    ports    = var.iap_web_ports
  }

  priority      = local.allow_priority
  source_ranges = [local.iap_source_range]
  target_tags   = [var.name]
}

# Firewall rules are ADDITIVE: the allows above open ports, they close nothing.
# The VPC is shared by all stacks, so this tag-scoped deny is the isolation
# boundary for SSH and the stack ports. It prevents peer VMs from reaching a
# fleet-db that runs with --auth-dev-mode and --authz-enabled=false; anything
# that can reach that port owns the data.
#
# Sitting between the two priorities is what makes it work: the IAP allows at
# 1000 still win, a broad low-precedence rule loses, and a deliberate
# higher-precedence allow (priority < 1100) that an operator adds on purpose
# still takes effect.
resource "google_compute_firewall" "deny_non_iap" {
  name        = "${var.name}-deny-non-iap"
  project     = var.project_id
  network     = data.google_compute_network.shared.id
  description = "Everything to the stack ports that did not come through IAP."

  deny {
    protocol = "tcp"
    ports    = concat(["22"], var.iap_web_ports)
  }

  priority      = local.deny_priority
  source_ranges = ["0.0.0.0/0"]
  target_tags   = [var.name]
}
