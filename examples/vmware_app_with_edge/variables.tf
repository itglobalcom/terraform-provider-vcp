variable "api_key" {
  description = "VStack Cloud Panel API token (or set VCP_API_TOKEN)"
  type        = string
  default     = null
  sensitive   = true
}

variable "endpoint" {
  description = "VStack Cloud Panel API endpoint (or set VCP_API_URL)"
  type        = string
  default     = null
}

variable "location_id" {
  description = "VMware Cloud location ID — see the vcp_vmware_locations data source"
  type        = number
}

variable "image_id" {
  description = "OS image ID — see the vcp_vmware_images data source"
  type        = number
}

variable "office_cidr" {
  description = "The network SSH is allowed from"
  type        = string
  default     = "203.0.113.0/24"
}
