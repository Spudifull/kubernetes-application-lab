terraform {
  required_version = "= 1.13.5"
  required_providers {
    hyperv = {
      source  = "Bafbi/hyperv"
      version = "= 1.5.4"
    }
  }
  backend "local" {}
}

provider "hyperv" {
  ssh                  = true
  ssh_host             = "127.0.0.1"
  ssh_port             = var.ssh_port
  ssh_user             = var.ssh_user
  ssh_private_key_path = var.ssh_private_key_path
}
