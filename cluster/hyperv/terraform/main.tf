locals {
  cluster_nodes = {
    cp = { disk_gib = 30, index = 11 }
    w1 = { disk_gib = 35, index = 12 }
    w2 = { disk_gib = 35, index = 13 }
  }
  nodes     = var.mode == "probe" ? { probe = { disk_gib = 1, index = 21 } } : local.cluster_nodes
  directory = "${var.storage_root}/${var.prefix}"
  # Locally administered MACs, unique per project prefix and node.
  mac_prefix = "02${substr(sha256(var.prefix), 0, 8)}"
}

resource "hyperv_network_switch" "lab" {
  name                = "${var.prefix}-net"
  switch_type         = "Private"
  allow_management_os = false
  notes               = "Owned by kubernetes-application-lab ${var.prefix}"
}

resource "hyperv_vhd" "node" {
  for_each = local.nodes
  path     = "${local.directory}/${var.prefix}-${each.key}.vhdx"
  size     = each.value.disk_gib * 1024 * 1024 * 1024
  vhd_type = "Dynamic"
}

resource "hyperv_machine_instance" "node" {
  for_each               = local.nodes
  name                   = "${var.prefix}-${each.key}"
  path                   = local.directory
  generation             = 2
  processor_count        = var.mode == "probe" ? 1 : 2
  static_memory          = true
  memory_startup_bytes   = (var.mode == "probe" ? 1 : 4) * 1024 * 1024 * 1024
  state                  = var.start_vms ? "Running" : "Off"
  automatic_start_action = "Nothing"
  automatic_stop_action  = "ShutDown"
  checkpoint_type        = "Disabled"
  notes                  = "Owned by kubernetes-application-lab ${var.prefix}"

  network_adaptors {
    name                = "cluster"
    switch_name         = hyperv_network_switch.lab.name
    dynamic_mac_address = false
    static_mac_address  = upper("${local.mac_prefix}${format("%02x", each.value.index)}")
    wait_for_ips        = false
  }

  dynamic "network_adaptors" {
    for_each = var.mode == "cluster" ? [1] : []
    content {
      name                = "management"
      switch_name         = var.management_switch
      dynamic_mac_address = false
      static_mac_address  = upper("${local.mac_prefix}${format("%02x", each.value.index + 32)}")
      wait_for_ips        = false
    }
  }

  hard_disk_drives {
    controller_number   = 0
    controller_location = 0
    path                = hyperv_vhd.node[each.key].path
  }

  dynamic "dvd_drives" {
    for_each = var.mode == "cluster" && var.attach_media ? { 1 = var.installer_iso, 2 = "${var.seed_directory}/${var.prefix}-${each.key}.iso" } : {}
    content {
      controller_number   = 0
      controller_location = tonumber(dvd_drives.key)
      path                = dvd_drives.value
    }
  }

  vm_firmware {
    enable_secure_boot   = "On"
    secure_boot_template = "MicrosoftUEFICertificateAuthority"
    boot_order {
      boot_type           = "HardDiskDrive"
      controller_number   = 0
      controller_location = 0
    }
    dynamic "boot_order" {
      for_each = var.mode == "cluster" && var.attach_media ? [1] : []
      content {
        boot_type           = "DvdDrive"
        controller_number   = 0
        controller_location = 1
      }
    }
  }

  lifecycle {
    precondition {
      condition     = var.mode == "probe" || (var.installer_iso != "" && var.seed_directory != "")
      error_message = "Cluster mode requires a verified autoinstall ISO and per-node seed ISOs."
    }
    precondition {
      condition     = var.mode != "probe" || !var.start_vms
      error_message = "The provider probe creates an unbooted VM only."
    }
  }
}
