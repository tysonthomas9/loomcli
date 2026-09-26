locals {
  codex_enabled              = var.codex_auth_secret != ""
  artifact_registry_project  = coalesce(var.registry_project, var.project_id)
  artifact_registry_location = coalesce(var.registry_location, var.region)
}

# One service account for the VM. It is deliberately narrow: read the stack's
# own secrets, read/write the stack's own bucket, read the shared image
# repository, and ship logs and metrics. It has no project-wide storage or
# secret access.
resource "google_service_account" "vm" {
  account_id   = var.name
  project      = var.project_id
  display_name = "Loom + fleet-db stack VM"
  description  = "Runtime identity for the ${var.name} VM. Managed by Terraform."
}

# The Artifact Registry repository is a shared bootstrap resource created by
# `make preflight`, because images must be pushed before the first Terraform
# apply. Scope the VM binding to that repository instead of the whole project.
# The repository intentionally survives `make down` and is documented as such.
resource "google_artifact_registry_repository_iam_member" "artifact_reader" {
  project    = local.artifact_registry_project
  location   = local.artifact_registry_location
  repository = var.registry_repository
  role       = "roles/artifactregistry.reader"
  member     = "serviceAccount:${google_service_account.vm.email}"
}

resource "google_project_iam_member" "logging" {
  project = var.project_id
  role    = "roles/logging.logWriter"
  member  = "serviceAccount:${google_service_account.vm.email}"
}

resource "google_project_iam_member" "monitoring" {
  project = var.project_id
  role    = "roles/monitoring.metricWriter"
  member  = "serviceAccount:${google_service_account.vm.email}"
}

# Bucket-scoped, not project-scoped: the VM can only touch workspace files for
# this stack.
resource "google_storage_bucket_iam_member" "workspace_files" {
  bucket = google_storage_bucket.workspace_files.name
  role   = "roles/storage.objectAdmin"
  member = "serviceAccount:${google_service_account.vm.email}"
}

# Secret-scoped for the same reason. Each binding names one secret.
resource "google_secret_manager_secret_iam_member" "accessor" {
  for_each = local.stack_secrets

  project   = var.project_id
  secret_id = each.value.secret_id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.vm.email}"
}

# The codex secret is created outside Terraform (it holds a personal
# credential), so bind to it by name rather than by resource reference.
resource "google_secret_manager_secret_iam_member" "codex_auth" {
  count = local.codex_enabled ? 1 : 0

  project   = var.project_id
  secret_id = var.codex_auth_secret
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.vm.email}"
}
