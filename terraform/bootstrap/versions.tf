terraform {
  required_version = ">= 1.9"
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 8.12"
    }
  }
  # No backend block: this folder creates the bucket every other folder keeps
  # its state in, so its own state stays on the laptop (terraform.tfstate,
  # gitignored). It changes rarely; keep that file safe.
}

provider "google" {
  project = var.project
  region  = var.region
}
