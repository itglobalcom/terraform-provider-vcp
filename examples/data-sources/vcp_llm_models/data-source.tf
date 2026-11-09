data "vcp_llm_models" "catalog" {}

# Inference address of every model the groups may call, by model name.
output "llm_endpoints" {
  value = {
    for m in data.vcp_llm_models.catalog.models : m.tech_title => m.endpoint
    if m.is_available_to_groups
  }
}
