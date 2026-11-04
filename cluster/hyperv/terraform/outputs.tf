output "ownership" {
  value = {
    prefix    = var.prefix
    mode      = var.mode
    directory = local.directory
    switch    = { id = hyperv_network_switch.lab.id, name = hyperv_network_switch.lab.name }
    machines  = { for key, node in hyperv_machine_instance.node : key => {
      id   = node.id
      name = node.name
      disk = hyperv_vhd.node[key].path
    } }
  }
}
output "bootstrap" {
  value = { for key, node in local.nodes : key => {
    hostname       = "${var.prefix}-${key}"
    address        = "10.77.10.${node.index}"
    cluster_mac    = lower(join(":", regexall("..", "${local.mac_prefix}${format("%02x", node.index)}")))
    management_mac = lower(join(":", regexall("..", "${local.mac_prefix}${format("%02x", node.index + 32)}")))
  } }
}
