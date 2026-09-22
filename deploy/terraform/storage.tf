resource "random_id" "bucket_suffix" {
  byte_length = 4
}

# Workspace file trees are content-addressed: every object is named by its own
# SHA-256 and never rewritten. Versioning would therefore only accumulate cost,
# and uniform access keeps the bucket on IAM alone (no per-object ACLs).
resource "google_storage_bucket" "workspace_files" {
  name                        = "${var.project_id}-${var.name}-${random_id.bucket_suffix.hex}"
  project                     = var.project_id
  location                    = var.region
  storage_class               = "STANDARD"
  uniform_bucket_level_access = true
  # Test stacks are disposable and their objects are reproducible build
  # artifacts, so `terraform destroy` must not wedge on a non-empty bucket.
  # Set ephemeral = false for anything you would be sad to lose.
  force_destroy = var.ephemeral
  labels        = var.labels

  lifecycle {
    precondition {
      condition     = length(var.project_id) + length(var.name) + 10 <= 63
      error_message = "project_id plus name is too long for the generated GCS bucket name (maximum 63 characters)."
    }
  }

  versioning {
    enabled = false
  }
}

# fleet-db talks to object storage over the S3 API, so GCS is reached through
# its S3 interoperability endpoint, which authenticates with HMAC keys rather
# than the VM's Google credentials. The key belongs to this service account and
# is scoped by the bucket IAM binding above.
#
# fleet-db's signer strips Accept-Encoding, Amz-Sdk-Invocation-Id and
# Amz-Sdk-Request before signing, because GCS omits them from its canonical
# request (internal/storage/blob/s3_store.go). That fix is required for this
# endpoint to work at all.
#
# Managing the key here is also what makes teardown complete. A hand-rolled
# stack leaves the HMAC key active after the bucket and VM are gone -- live
# credentials with no stack attached. The provider deactivates the key before
# deleting it, so `terraform destroy` leaves nothing behind.
resource "google_storage_hmac_key" "workspace_files" {
  project               = var.project_id
  service_account_email = google_service_account.vm.email
}
