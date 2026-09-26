# The state bucket and prefix are supplied by `make shared` so the project can
# be selected without interpolating variables in this backend block.
terraform {
  backend "gcs" {}
}
