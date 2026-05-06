# CAPLV Demo Setup

This demo uses three Kubernetes operators working together to
schedule ephemeral libvirt VMs as CAPI worker nodes.

## Components

### CAPLV (Cluster API Provider Libvirt)
- **LibvirtHost** CR: points to a libvirt hypervisor over SSH
- **LibvirtMachine** CR: defines a VM (vCPUs, memory, disk, network)
- The controller provisions/destroys VMs on the referenced host

### 5-Spot (Scheduled Machine Controller)
- **ScheduledMachine** CR: wraps a CAPI Machine with a time-based schedule
- Creates infrastructure + bootstrap + Machine resources when in-schedule
- Drains and deletes them when out-of-schedule
- Supports day-of-week, hour-of-day, timezone, priority, and kill switch

### capi-bootstrap-ignition
- **IgnitionConfig** CR: references a Secret containing Ignition/Butane config
- Generates bootstrap data consumed by CAPLV to configure the VM on first boot

## Flow

1. User creates a **ScheduledMachine** in the `default` namespace
2. 5-Spot evaluates the schedule and, when active, creates a
   **LibvirtMachine**, **IgnitionConfig**, and CAPI **Machine**
3. CAPLV provisions the VM on the target **LibvirtHost**
4. The VM boots with Ignition config and joins the cluster as a worker
5. When the schedule window closes, 5-Spot drains and deletes the machine
