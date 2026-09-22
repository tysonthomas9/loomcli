resource "google_compute_network" "shared" {
  name                    = var.network_name
  project                 = var.project_id
  auto_create_subnetworks = false
  description             = "Shared network for parallel Loom stacks."
}

resource "google_compute_subnetwork" "shared" {
  name                     = "${var.network_name}-${var.region}"
  project                  = var.project_id
  region                   = var.region
  network                  = google_compute_network.shared.id
  ip_cidr_range            = var.subnet_cidr
  private_ip_google_access = true
}

resource "google_compute_router" "shared" {
  name    = "${var.network_name}-${var.region}"
  project = var.project_id
  region  = var.region
  network = google_compute_network.shared.id
}

resource "google_compute_router_nat" "shared" {
  name                               = "${var.network_name}-${var.region}"
  project                            = var.project_id
  region                             = var.region
  router                             = google_compute_router.shared.name
  nat_ip_allocate_option             = "AUTO_ONLY"
  source_subnetwork_ip_ranges_to_nat = "ALL_SUBNETWORKS_ALL_IP_RANGES"

  log_config {
    enable = false
    filter = "ERRORS_ONLY"
  }
}
