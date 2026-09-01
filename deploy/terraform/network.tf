# Each stack gets its OWN VPC, not a slice of the project's default network.
#
# That is what makes concurrent named stacks work. Cloud NAT refuses two
# gateways whose subnet coverage overlaps in the same network and region, so
# when every stack put an ALL_SUBNETWORKS gateway on the shared default VPC,
# the first `make up` succeeded and the second failed on:
#
#   Error 400: Invalid value for field 'resource.natIpAllocateOption' ...
#   NAT gateway <a>-nat and <b>-nat cannot have overlapping subnetwork ranges
#
# Per-stack subnets on a shared VPC would also satisfy NAT, but then every
# stack needs a distinct non-overlapping CIDR out of one address space, and
# picking those from a stack name is a collision waiting to happen. Separate
# networks make the CIDR a constant instead: they cannot see each other, so
# every stack can use the same range.
#
# The cost is quota. A project allows 5 VPC networks by default (one being
# `default`), so ~4 concurrent stacks before you need a quota bump. That is a
# clear error at apply time, unlike a silent NAT collision.
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

resource "google_compute_network" "stack" {
  name    = var.name
  project = var.project_id
  # Auto mode would create a subnet in every region and re-introduce exactly
  # the overlap this network exists to avoid.
  auto_create_subnetworks = false
  description             = "Isolated network for the ${var.name} Loom stack."
}

resource "google_compute_subnetwork" "stack" {
  name          = var.name
  project       = var.project_id
  region        = var.region
  network       = google_compute_network.stack.id
  ip_cidr_range = var.subnet_cidr

  # Lets the VM reach Secret Manager, Artifact Registry and the metadata
  # server over Google's internal path. NAT covers this too, but this keeps
  # boot working if NAT is disabled and is the cheaper route regardless.
  private_ip_google_access = true
}

resource "google_compute_firewall" "iap_ssh" {
  name        = "${var.name}-allow-iap-ssh"
  project     = var.project_id
  network     = google_compute_network.stack.id
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
  network     = google_compute_network.stack.id
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
# A custom-mode VPC ships no permissive defaults (unlike `default`, which
# carries `default-allow-internal` at priority 65534), so this rule is no
# longer the only thing standing between the stack and every other VM in the
# project. It stays because it is cheap and the exposure it prevents is total:
# fleet-db runs with --auth-dev-mode and --authz-enabled=false, so anything
# that can reach the port owns the data. It still bites if this network is
# ever peered, or if someone adds a broad allow while debugging.
#
# Sitting between the two priorities is what makes it work: the IAP allows at
# 1000 still win, a broad low-precedence rule loses, and a deliberate
# higher-precedence allow (priority < 1100) that an operator adds on purpose
# still takes effect.
resource "google_compute_firewall" "deny_non_iap" {
  name        = "${var.name}-deny-non-iap"
  project     = var.project_id
  network     = google_compute_network.stack.id
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
  network = google_compute_network.stack.id
}

resource "google_compute_router_nat" "nat" {
  count                  = var.enable_cloud_nat && !var.enable_external_ip ? 1 : 0
  name                   = "${var.name}-nat"
  project                = var.project_id
  region                 = var.region
  router                 = google_compute_router.nat[0].name
  nat_ip_allocate_option = "AUTO_ONLY"
  # Safe here only because this network belongs to this stack alone. On a
  # shared network this is the setting that made the second stack unappliable.
  source_subnetwork_ip_ranges_to_nat = "ALL_SUBNETWORKS_ALL_IP_RANGES"

  log_config {
    enable = false
    filter = "ERRORS_ONLY"
  }
}
