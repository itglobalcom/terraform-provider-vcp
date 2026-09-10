# Takes a snapshot of the server's disks. A snapshot cannot be renamed, so
# changing `name` replaces it: the old snapshot is deleted and a new one is
# taken of the server as it is now.
resource "vcp_server_snapshot" "before_upgrade" {
  server_id = vcp_server.example.id
  name      = "before-upgrade"
}
