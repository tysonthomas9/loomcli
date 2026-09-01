output "instance_name" {
  description = "VM name, for gcloud commands."
  value       = google_compute_instance.stack.name
}

output "zone" {
  value = google_compute_instance.stack.zone
}

output "service_account_email" {
  value = google_service_account.vm.email
}

output "bucket" {
  description = "Bucket holding content-addressed workspace file objects."
  value       = google_storage_bucket.workspace_files.name
}

output "external_ip" {
  description = "Public IP, or null when the VM is IAP-only (the default)."
  value       = var.enable_external_ip ? google_compute_instance.stack.network_interface[0].access_config[0].nat_ip : null
}

output "ssh_command" {
  value = "gcloud compute ssh ${google_compute_instance.stack.name} --project ${var.project_id} --zone ${var.zone} --tunnel-through-iap"
}

# Ports publish on ALL interfaces on the VM -- IAP forwards to the NIC, so a
# loopback bind breaks the tunnel. What keeps them closed is the firewall
# (source 35.235.240.0/20 only) plus the absence of an external IP. Note that
# on a stock default VPC `default-allow-internal` still lets other VMs in the
# network reach these ports, and fleet-db runs with auth in dev mode.
output "iap_tunnel_ui" {
  description = "Run this, then open http://localhost:8283 in a browser."
  value       = "gcloud compute start-iap-tunnel ${google_compute_instance.stack.name} ${var.iap_web_ports[2]} --local-host-port=localhost:${var.iap_web_ports[2]} --project ${var.project_id} --zone ${var.zone}"
}

output "iap_tunnel_api" {
  value = "gcloud compute start-iap-tunnel ${google_compute_instance.stack.name} ${var.iap_web_ports[1]} --local-host-port=localhost:${var.iap_web_ports[1]} --project ${var.project_id} --zone ${var.zone}"
}

output "iap_tunnel_fleetdb" {
  value = "gcloud compute start-iap-tunnel ${google_compute_instance.stack.name} ${var.iap_web_ports[0]} --local-host-port=localhost:${var.iap_web_ports[0]} --project ${var.project_id} --zone ${var.zone}"
}

output "hmac_access_id" {
  description = "GCS S3-interop access id. The matching secret is in Secret Manager, never in state output."
  value       = google_storage_hmac_key.workspace_files.access_id
}

# Local ports `make tunnel` opens. Derived from the stack name so concurrent
# stacks do not collide, and exported rather than recomputed in bash: loom's
# origin allowlist has to agree with them, and two independent derivations
# drifted apart once already.
output "tunnel_ui_port" {
  value = local.tunnel_ui_port
}

output "tunnel_api_port" {
  value = local.tunnel_api_port
}

output "tunnel_fleetdb_port" {
  value = local.tunnel_fleetdb_port
}

output "loom_workspace" {
  description = "Workspace key the stack seeded, for URL construction."
  value       = var.loom_workspace
}
