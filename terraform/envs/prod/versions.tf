terraform {
  required_version = ">= 1.9"
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 8.12"
    }
  }

  # State lives in the bucket terraform/bootstrap made. The bucket's name
  # contains the project id, so it isn't written here; give it at init:
  #   terraform init -backend-config="bucket=<project>-tfstate"
  # The GCS backend locks the state while a plan or apply runs.
  backend "gcs" {
    prefix = "envs/prod"
  }
}

provider "google" {
  project = var.project
  region  = var.region
}
