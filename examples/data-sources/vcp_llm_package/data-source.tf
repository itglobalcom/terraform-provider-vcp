data "vcp_llm_package" "current" {}

output "llm_credits_left" {
  value = (
    data.vcp_llm_package.current.package == null
    ? 0
    : data.vcp_llm_package.current.package.volume_credits - data.vcp_llm_package.current.usage.in_package_credits
  )
}
