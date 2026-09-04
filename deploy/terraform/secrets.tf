# Generated, never hand-written. Each value goes straight into Secret Manager
# and is fetched by the VM at boot; none is baked into the image or the
# compose file.
resource "random_password" "redis" {
  length  = 40
  special = false
}

# fleet-db requires this to be base64url of at least 32 bytes, and silently
# DISABLES the workspace file API when it is missing or malformed -- the only
# symptom is a "workspace file API disabled" line in its startup log.
resource "random_bytes" "workspace_file_token" {
  length = 32
}

# loom signs run tokens with this key. It must outlive a serve restart or every
# in-flight agent run becomes unverifiable when the API process comes back.
resource "random_id" "run_token_signing_key" {
  byte_length = 32
}

locals {
  stack_secrets = {
    redis_password = {
      secret_id = google_secret_manager_secret.redis_password.secret_id
    }
    workspace_file_token = {
      secret_id = google_secret_manager_secret.workspace_file_token.secret_id
    }
    run_token_signing_key = {
      secret_id = google_secret_manager_secret.run_token_signing_key.secret_id
    }
    s3_access_id = {
      secret_id = google_secret_manager_secret.s3_access_id.secret_id
    }
    s3_secret = {
      secret_id = google_secret_manager_secret.s3_secret.secret_id
    }
  }
}

resource "google_secret_manager_secret" "redis_password" {
  secret_id = "${var.name}-redis-password"
  project   = var.project_id
  labels    = var.labels

  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "redis_password" {
  secret      = google_secret_manager_secret.redis_password.id
  secret_data = random_password.redis.result
}

resource "google_secret_manager_secret" "workspace_file_token" {
  secret_id = "${var.name}-workspace-file-token"
  project   = var.project_id
  labels    = var.labels

  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "workspace_file_token" {
  secret = google_secret_manager_secret.workspace_file_token.id
  # The random provider only emits standard base64. fleet-db validates
  # base64url specifically, so translate the alphabet and drop the padding --
  # feeding it standard base64 disables the workspace file API silently.
  secret_data = replace(replace(replace(random_bytes.workspace_file_token.base64, "=", ""), "+", "-"), "/", "_")
}

resource "google_secret_manager_secret" "run_token_signing_key" {
  secret_id = "${var.name}-run-token-signing-key"
  project   = var.project_id
  labels    = var.labels

  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "run_token_signing_key" {
  secret      = google_secret_manager_secret.run_token_signing_key.id
  secret_data = random_id.run_token_signing_key.hex
}

resource "google_secret_manager_secret" "s3_access_id" {
  secret_id = "${var.name}-s3-access-id"
  project   = var.project_id
  labels    = var.labels

  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "s3_access_id" {
  secret      = google_secret_manager_secret.s3_access_id.id
  secret_data = google_storage_hmac_key.workspace_files.access_id
}

resource "google_secret_manager_secret" "s3_secret" {
  secret_id = "${var.name}-s3-secret"
  project   = var.project_id
  labels    = var.labels

  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "s3_secret" {
  secret      = google_secret_manager_secret.s3_secret.id
  secret_data = google_storage_hmac_key.workspace_files.secret
}
