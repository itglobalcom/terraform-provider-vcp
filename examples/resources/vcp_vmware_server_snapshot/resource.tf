# A VMware server holds one snapshot at a time, addressed by the server rather
# than by an id of its own — so declare at most one of these per machine.
#
# The snapshot cannot be renamed: changing `name` deletes this one and takes a
# new snapshot of the machine as it is then.
resource "vcp_vmware_server_snapshot" "before_upgrade" {
  server_id = vcp_vmware_server.web.id
  name      = "before-upgrade"
}
