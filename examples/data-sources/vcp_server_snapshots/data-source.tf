# List the snapshots of one server, including those taken in the panel.
data "vcp_server_snapshots" "example" {
  server_id = vcp_server.example.id
}

# The most recent snapshot, whoever took it.
output "latest_snapshot" {
  value = try(reverse(sort([for s in data.vcp_server_snapshots.example.snapshots : s.created]))[0], null)
}
