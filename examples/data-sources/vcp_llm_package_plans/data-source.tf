data "vcp_llm_package_plans" "all" {}

output "llm_package_plans" {
  value = {
    for p in data.vcp_llm_package_plans.all.plans : p.tech_title => "${p.price} ${p.currency} for ${p.volume_credits} credits"
  }
}
