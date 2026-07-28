terraform {
  required_providers {
    vcp = {
      source  = "itglobalcom/vcp"
      version = ">= 0.1.0"
    }
    # Used to generate a throwaway SSH key pair for the example.
    tls = {
      source  = "hashicorp/tls"
      version = "~> 4.0"
    }
  }
}

provider "vcp" {
  key  = var.api_key
  host = var.endpoint
}

# Isolated network the server will live on.
resource "vcp_network" "example_network" {
  name           = "example_network"
  location_id    = var.location_id
  description    = "Production environment network"
  network_prefix = "10.0.0.0"
  mask           = 24

  tags = ["example", "terraform"]
}

# Random SSH key pair generated on apply — no hardcoded public key.
# The private key is exposed as a sensitive output (see outputs below).
resource "tls_private_key" "example" {
  algorithm = "ED25519"
}

resource "vcp_ssh_key" "test" {
  name = "example-key"
  # trimspace() drops the trailing newline that public_key_openssh appends.
  public_key = trimspace(tls_private_key.example.public_key_openssh)
}

resource "vcp_server" "my_server" {
  cpu         = 2
  image_id    = var.image_id
  location_id = var.location_id # must match the network's location
  name        = "test-server"
  ram_mb      = 2048
  ssh_key_ids = [vcp_ssh_key.test.id]
  volumes = [
    {
      name    = "boot" # required volume
      number  = 0
      size_mb = 51200 # 30720 or more
    },
  ]
  tags = ["terraform"]
}

resource "vcp_server_public_interface" "my_server_pub" {
  server_id      = vcp_server.my_server.id
  bandwidth_mbps = 50
}

# Connect the server to the isolated network. Modelled as a separate resource so
# Terraform detaches the NIC before deleting either endpoint.
resource "vcp_server_network_attachment" "my_server_net" {
  server_id  = vcp_server.my_server.id
  network_id = vcp_network.example_network.id
}

# ============================================
# Outputs
# ============================================
output "server_id" {
  description = "ID of the created server"
  value       = vcp_server.my_server.id
}

output "server_private_ip" {
  description = "Server IP address on the isolated network"
  value       = vcp_server_network_attachment.my_server_net.ip_address
}

output "ssh_private_key" {
  description = "Private key for the generated SSH key pair (OpenSSH format)"
  value       = tls_private_key.example.private_key_openssh
  sensitive   = true
}
