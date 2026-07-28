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
  description = "Location ID where the network and server will be created"
  type        = string
  default     = "ca"
}

variable "image_id" {
  description = "OS image ID for the server (must exist in var.location_id)"
  type        = string
  default     = "Ubuntu-24.04.4-X64"
}