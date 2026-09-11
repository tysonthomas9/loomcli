# The state includes Secret Manager versions and HMAC credentials. Keep it in
# the project-owned, versioned GCS backend rather than leaving plaintext
# credentials in the local checkout's terraform.tfstate files.
terraform {
  backend "gcs" {
    # The bucket and prefix are supplied by `make state` so the project can be
    # selected without interpolating variables in this backend block.
  }
}
