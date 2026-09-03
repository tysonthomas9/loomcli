output "network" {
  description = "Self link of the shared VPC."
  value       = google_compute_network.shared.self_link
}

output "subnetwork" {
  description = "Self link of the shared regional subnet."
  value       = google_compute_subnetwork.shared.self_link
}

output "network_name" {
  description = "Name of the shared VPC."
  value       = google_compute_network.shared.name
}
