# List the backup storages a server's copies can be kept in, and the limits a
# schedule has to fit into.
data "vcp_server_backup_storages" "example" {
  server_id = vcp_server.example.id
}

# The cheapest storage, for the rules of vcp_server_backup.
output "cheapest_backup_storage_id" {
  value = try([for s in data.vcp_server_backup_storages.example.storages : s.id if s.price_per_gb == min(data.vcp_server_backup_storages.example.storages[*].price_per_gb...)][0], null)
}

# How many daily copies the partner allows.
output "max_daily_copies" {
  value = try(data.vcp_server_backup_storages.example.limits.daily.max_keep, null)
}
