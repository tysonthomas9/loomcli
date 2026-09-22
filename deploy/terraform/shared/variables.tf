variable "project_id" {
  description = "GCP project that owns the shared network resources."
  type        = string
}

variable "region" {
  description = "Region for the shared subnet, router, and NAT."
  type        = string
  default     = "us-central1"
}

variable "network_name" {
  description = "Name of the shared custom-mode VPC."
  type        = string
  default     = "loom"
}

variable "subnet_cidr" {
  description = "CIDR range for the shared regional subnet."
  type        = string
  default     = "10.90.0.0/20"
}
