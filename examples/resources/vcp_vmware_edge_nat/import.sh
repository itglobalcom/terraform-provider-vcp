# The NAT rule set is imported by the numeric ID of its network.
#
# The rules land in state in the order the API lists them, and that is the order
# the configuration has to repeat: an entry is paired with an existing rule by
# position, so a differently ordered configuration rewrites rules instead of
# adopting them. Run `terraform plan` right after importing and reorder the
# configuration until the plan is empty.
terraform import vcp_vmware_edge_nat.app 2479
