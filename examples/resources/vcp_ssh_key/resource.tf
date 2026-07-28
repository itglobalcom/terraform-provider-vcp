resource "vcp_ssh_key" "example" {
  name       = "example-key"
  public_key = file(pathexpand("~/.ssh/id_ed25519.pub"))
}
