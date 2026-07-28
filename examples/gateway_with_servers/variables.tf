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
  description = "Location ID"
  type        = string
  default     = "kz"
}

variable "image_id" {
  description = "OS image ID for the servers"
  type        = string
  default     = "Ubuntu-22.04-X64"
}
