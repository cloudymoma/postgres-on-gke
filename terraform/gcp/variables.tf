variable "project_id" {
  description = "GCP project ID"
  type        = string
}

variable "region" {
  description = "Region for the regional GKE cluster and backup bucket"
  type        = string
  default     = "us-central1"
}

variable "cluster_name" {
  description = "GKE cluster name"
  type        = string
  default     = "pg-gke"
}

variable "machine_type" {
  description = "Node machine type"
  type        = string
  default     = "c4-highmem-4"
}

variable "nodes_per_zone" {
  description = "Nodes per zone (regional cluster spans 3 zones)"
  type        = number
  default     = 1
}

variable "pg_namespace" {
  description = "Kubernetes namespace of the Postgres cluster (must match config.sh NAMESPACE)"
  type        = string
  default     = "pg"
}

variable "pg_cluster_name" {
  description = "CloudNativePG Cluster name (must match config.sh PG_CLUSTER)"
  type        = string
  default     = "pg-main"
}
