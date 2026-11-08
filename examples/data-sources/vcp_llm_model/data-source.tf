data "vcp_llm_models" "catalog" {}

data "vcp_llm_model" "first" {
  id = data.vcp_llm_models.catalog.models[0].id
}

output "llm_model_prices" {
  value = {
    for p in data.vcp_llm_model.first.prices : p.unit => p.credits
  }
}
