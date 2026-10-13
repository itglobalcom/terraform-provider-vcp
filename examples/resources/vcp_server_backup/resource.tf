data "vcp_server_backup_storages" "example" {
  server_id = vcp_server.example.id
}

# Copies at 02:30: a week of daily ones, a month of Sunday ones and a year of
# copies taken on the last day of each month. Destroying this resource disables
# the backup service and deletes the server's copies.
resource "vcp_server_backup" "example" {
  server_id = vcp_server.example.id
  hour      = 2
  minute    = 30

  daily = {
    keep              = 7
    backup_storage_id = data.vcp_server_backup_storages.example.storages[0].id
  }

  weekly = {
    keep              = 4
    backup_storage_id = data.vcp_server_backup_storages.example.storages[0].id
    weekday           = "sunday"
  }

  monthly = {
    keep              = 12
    backup_storage_id = data.vcp_server_backup_storages.example.storages[0].id
    day_of_month      = "last"
  }
}
