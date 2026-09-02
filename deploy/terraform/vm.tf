data "google_compute_image" "ubuntu" {
  family  = "ubuntu-2404-lts-amd64"
  project = "ubuntu-os-cloud"
}

locals {
  # Terraform owns the local tunnel ports because loom's origin allowlist has
  # to contain the port the browser will actually use. The required explicit
  # base is the collision-free option when several stacks share a workstation.
  tunnel_ui_port      = var.tunnel_port_base
  tunnel_api_port     = local.tunnel_ui_port + 1
  tunnel_fleetdb_port = local.tunnel_ui_port + 2

  cloud_init = templatefile("${path.module}/templates/cloud-init.yaml.tftpl", {
    compose_file = base64encode(templatefile("${path.module}/templates/docker-compose.yml.tftpl", {
      fleetdb_image  = var.fleetdb_image
      loom_image     = var.loom_image
      ui_image       = var.ui_image
      redis_image    = var.redis_image
      loom_workspace = var.loom_workspace
      bucket         = google_storage_bucket.workspace_files.name
      region         = var.region
      fleetdb_port   = var.iap_web_ports[0]
      api_port       = var.iap_web_ports[1]
      ui_port        = var.iap_web_ports[2]
      ui_local_port  = local.tunnel_ui_port
      codex_enabled  = local.codex_enabled
      # --wait returns before the daemon spawns agents, so the plan role is
      # relaxed by a sidecar in the stack itself rather than from systemd.
      plan_role_read_only = var.plan_role_read_only
    }))
    redis_secret                 = google_secret_manager_secret.redis_password.secret_id
    workspace_token_secret       = google_secret_manager_secret.workspace_file_token.secret_id
    run_token_signing_key_secret = google_secret_manager_secret.run_token_signing_key.secret_id
    s3_access_id_secret          = google_secret_manager_secret.s3_access_id.secret_id
    s3_secret_secret             = google_secret_manager_secret.s3_secret.secret_id
    project_id                   = var.project_id
    registry_host                = coalesce(var.registry_host, "${var.region}-docker.pkg.dev")
    codex_secret                 = var.codex_auth_secret
    plan_role_read_only          = var.plan_role_read_only
    fleetdb_port                 = var.iap_web_ports[0]
    loom_workspace               = var.loom_workspace
  })
}

resource "google_compute_instance" "stack" {
  name         = var.name
  project      = var.project_id
  zone         = var.zone
  machine_type = var.machine_type
  tags         = [var.name]
  labels       = var.labels

  boot_disk {
    initialize_params {
      image = data.google_compute_image.ubuntu.self_link
      size  = var.boot_disk_gb
      type  = "pd-balanced"
    }
  }

  network_interface {
    network    = google_compute_network.stack.id
    subnetwork = google_compute_subnetwork.stack.id

    # An access_config block at all is what assigns a public IP; omitting it
    # leaves the VM reachable only through IAP.
    dynamic "access_config" {
      for_each = var.enable_external_ip ? [1] : []
      content {}
    }
  }

  service_account {
    email = google_service_account.vm.email
    # cloud-platform plus narrow IAM roles is the supported combination;
    # legacy per-API scopes are not how Secret Manager access is granted.
    scopes = ["https://www.googleapis.com/auth/cloud-platform"]
  }

  metadata = {
    # OS Login ties SSH to IAM instead of metadata keys, so revoking a person's
    # IAM access revokes their shell.
    enable-oslogin = "TRUE"

    user-data = local.cloud_init
  }

  # Without NAT and without a public IP the startup script cannot fetch
  # anything, so make that ordering explicit rather than racy.
  depends_on = [
    google_compute_router_nat.nat,
    google_secret_manager_secret_version.redis_password,
    google_secret_manager_secret_version.workspace_file_token,
    google_secret_manager_secret_version.run_token_signing_key,
    google_secret_manager_secret_version.s3_access_id,
    google_secret_manager_secret_version.s3_secret,
    google_secret_manager_secret_iam_member.accessor,
    google_storage_bucket_iam_member.workspace_files,
    google_secret_manager_secret_iam_member.codex_auth,
    # Image pulls happen in cloud-init, seconds after boot. Without this the
    # VM can come up before the reader binding on a brand-new service account
    # exists, and every pull 403s.
    google_artifact_registry_repository_iam_member.artifact_reader,
  ]

  # cloud-init runs ONCE per instance. Metadata is mutable, so without this the
  # apply that changes an image tag, a port, or a codex setting would update
  # metadata in place, report success, and deploy nothing -- while wait-healthy
  # happily checks the old stack. Replacing the VM is the redeploy.
  lifecycle {
    replace_triggered_by = [terraform_data.boot_config]
  }

  # The stack lives on the boot disk; replacing the VM is how you redeploy.
  allow_stopping_for_update = true
}

# Sole purpose: give the instance something to key `replace_triggered_by` on.
# Any change to the rendered boot config -- which embeds the compose file, and
# so every image tag and port -- changes this hash and forces a fresh VM.
resource "terraform_data" "boot_config" {
  input = sha256(local.cloud_init)
}
