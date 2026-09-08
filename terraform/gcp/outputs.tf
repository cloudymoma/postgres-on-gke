output "cluster_name" {
  value = google_container_cluster.pg.name
}

output "backup_bucket" {
  value = google_storage_bucket.backups.url
}

output "backup_gsa_email" {
  value = google_service_account.pg_backup.email
}

output "get_credentials" {
  value = "gcloud container clusters get-credentials ${google_container_cluster.pg.name} --project ${var.project_id} --region ${var.region}"
}
