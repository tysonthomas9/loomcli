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
# (source 35.235.240.0/20 only), the absence of an external IP, and the fact
# that this VPC belongs to this stack alone and carries no permissive default
# rules. That last part matters: fleet-db runs with auth in dev mode, so
# anything that CAN reach the port has full read/write.
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

# The service ports the containers publish, exported so the health gate and the
# smoke gate probe what this stack actually listens on. They used to be
# hardcoded 8280/8282/8283 in both scripts, which meant overriding
# iap_web_ports produced a 15-minute wait-healthy timeout against a stack that
# was perfectly healthy -- the same drift that once broke the tunnel ports.
output "fleetdb_port" {
  value = var.iap_web_ports[0]
}

output "api_port" {
  value = var.iap_web_ports[1]
}

output "ui_port" {
  value = var.iap_web_ports[2]
}

# What smoke should EXPECT of the plan role, read from the deployed stack
# rather than reconstructed from Make arguments. `make up CODEX=1` followed by
# the documented `make smoke NAME=...` used to assert read_only=true against a
# stack deliberately deployed with read_only=false, failing a healthy stack for
# saying exactly what it was configured to say.
output "plan_role_read_only" {
  description = "Whether this stack left the plan role read-only (false under CODEX=1)."
  value       = var.plan_role_read_only
}
