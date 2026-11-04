# What the project has been charged this month so far, per service.
data "vcp_project_consumption" "this_month" {}

# A closed period: is_final is true once the period ends before today.
data "vcp_project_consumption" "q3" {
  from = "2026-07-01"
  to   = "2026-09-30"
}

output "vstack_this_month" {
  value = one([
    for s in data.vcp_project_consumption.this_month.services : s.total if s.service == "vstack"
  ])
}
