variable "prefix" {
  type = string
  validation {
    condition     = can(regex("^kal10b-[a-z0-9]{4,16}$", var.prefix))
    error_message = "Use an exclusive kal10b- project prefix."
  }
}
variable "storage_root" {
  type = string
  validation {
    condition     = can(regex("^[A-Za-z]:/[^.]+", var.storage_root)) && !strcontains(var.storage_root, "..") && !endswith(var.storage_root, "/")
    error_message = "Use an absolute Windows directory with forward slashes and no parent traversal."
  }
}
variable "ssh_user" {
  type = string
}
variable "ssh_port" {
  type    = number
  default = 22
}
variable "ssh_private_key_path" {
  type = string
}
variable "management_switch" {
  type    = string
  default = "Default Switch"
}
variable "mode" {
  type    = string
  default = "probe"
  validation {
    condition     = contains(["probe", "cluster"], var.mode)
    error_message = "Mode must be probe or cluster."
  }
}
variable "start_vms" {
  type    = bool
  default = false
}
variable "attach_media" {
  type    = bool
  default = true
}
variable "installer_iso" {
  type    = string
  default = ""
}
variable "seed_directory" {
  type    = string
  default = ""
}
