# Alternative IaC path: GKE cluster + backup bucket + Workload Identity
# wiring. Equivalent to `./bin/gke.sh create` + `./bin/gcs_backup.sh setup`.
# The CloudNativePG operator and Cluster are still applied with the scripts
# (or your GitOps tool of choice) after `terraform apply`.

terraform {
  required_version = ">= 1.5"
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = ">= 6.0"
    }
  }
}

provider "google" {
  project = var.project_id
  region  = var.region
}

resource "google_container_cluster" "pg" {
  name     = var.cluster_name
  location = var.region

  initial_node_count       = 1
  remove_default_node_pool = true

  workload_identity_config {
    workload_pool = "${var.project_id}.svc.id.goog"
  }

  ip_allocation_policy {}

  deletion_protection = false
}

resource "google_container_node_pool" "pg_nodes" {
  name     = "pg-nodes"
  cluster  = google_container_cluster.pg.id
  location = var.region

  # Per-zone count; a regional cluster spans 3 zones.
  node_count = var.nodes_per_zone

  node_config {
    machine_type = var.machine_type
    workload_metadata_config {
      mode = "GKE_METADATA"
    }
    oauth_scopes = ["https://www.googleapis.com/auth/cloud-platform"]
  }
}

resource "google_storage_bucket" "backups" {
  name                        = "${var.project_id}-pg-backups"
  location                    = var.region
  uniform_bucket_level_access = true
  force_destroy               = false

  versioning {
    enabled = true
  }

  lifecycle_rule {
    action {
      type = "Delete"
    }
    condition {
      days_since_noncurrent_time = 35
      with_state                 = "ARCHIVED"
    }
  }
}

resource "google_service_account" "pg_backup" {
  account_id   = "pg-backup"
  display_name = "CloudNativePG backups"
}

resource "google_storage_bucket_iam_member" "backup_writer" {
  bucket = google_storage_bucket.backups.name
  role   = "roles/storage.objectAdmin"
  member = "serviceAccount:${google_service_account.pg_backup.email}"
}

# Lets the cluster's Kubernetes SA (<namespace>/<cluster-name>) impersonate
# the GSA — the other half is the annotation in templates/pg.prod.yml.
resource "google_service_account_iam_member" "workload_identity" {
  service_account_id = google_service_account.pg_backup.name
  role               = "roles/iam.workloadIdentityUser"
  member             = "serviceAccount:${var.project_id}.svc.id.goog[${var.pg_namespace}/${var.pg_cluster_name}]"
}
