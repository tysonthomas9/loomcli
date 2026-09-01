# The stack is reached through IAP TCP forwarding, not a public IP. Google
# brokers those connections from a fixed range, so the allow rules open the
# ports to that range only.
#
# An allow rule alone does not close anything, though -- GCP firewall rules are
# additive and the default VPC ships its own permissive ones. The deny rule
# below is what actually makes "IAP only" true; read it before loosening any of
# this.
locals {
  iap_source_range = "35.235.240.0/20"

  # Lower number = higher precedence. The gap is deliberate; see deny_non_iap.
  allow_priority = 1000
  deny_priority  = 1100
}

data "google_compute_network" "default" {
  name    = "default"
  project = var.project_id
}

data "google_compute_subnetwork" "default" {
  name    = "default"
  region  = var.region
  project = var.project_id
}

resource "google_compute_firewall" "iap_ssh" {
  name        = "${var.name}-allow-iap-ssh"
  project     = var.project_id
  network     = data.google_compute_network.default.id
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
  network     = data.google_compute_network.default.id
  description = "Stack HTTP ports, reachable only through an IAP tunnel."

  allow {
    protocol = "tcp"
    ports    = var.iap_web_ports
  }

  priority      = local.allow_priority
  source_ranges = [local.iap_source_range]
  target_tags   = [var.name]
}

# The allow rules above are ADDITIVE, not an isolation boundary. A stock default
# VPC also carries `default-allow-internal` (priority 65534), which lets any
# other VM, VPN or interconnect peer in the network reach these ports -- and
# fleet-db runs with --auth-dev-mode and --authz-enabled=false, so "reachable"
# means "fully readable and writable". "IAP only" was not true without this.
#
# Sitting between the two priorities is what makes it work: the IAP allows at
# 1000 still win, the broad internal rule at 65534 loses, and a deliberate
# higher-precedence allow (priority < 1100) that an operator adds on purpose
# still takes effect.
resource "google_compute_firewall" "deny_non_iap" {
  name        = "${var.name}-deny-non-iap"
  project     = var.project_id
  network     = data.google_compute_network.default.id
  description = "Everything to the stack ports that did not come through IAP."

  deny {
    protocol = "tcp"
    ports    = concat(["22"], var.iap_web_ports)
  }

  priority      = local.deny_priority
  source_ranges = ["0.0.0.0/0"]
  target_tags   = [var.name]
}

# A VM with no external IP has no route off the network. Without NAT the
# startup script hangs on the first apt or docker pull, which surfaces much
# later as an empty stack rather than an obvious network error.
resource "google_compute_router" "nat" {
  count   = var.enable_cloud_nat && !var.enable_external_ip ? 1 : 0
  name    = "${var.name}-router"
  project = var.project_id
  region  = var.region
  network = data.google_compute_network.default.id
}

resource "google_compute_router_nat" "nat" {
  count                              = var.enable_cloud_nat && !var.enable_external_ip ? 1 : 0
  name                               = "${var.name}-nat"
  project                            = var.project_id
  region                             = var.region
  router                             = google_compute_router.nat[0].name
  nat_ip_allocate_option             = "AUTO_ONLY"
  source_subnetwork_ip_ranges_to_nat = "ALL_SUBNETWORKS_ALL_IP_RANGES"

  log_config {
    enable = false
    filter = "ERRORS_ONLY"
  }
}
