# What is switched on. Committed (no secrets here), so GitHub Actions plans
# against the same switches, and turning something on or off is a commit.
#
#   gke_enabled   the cluster, network, Gateway IP, certificates, alerts
#                 (about $50 a month while on; $0 off)
#   mesh_enabled  Cloud Service Mesh on that cluster (about $0.07 an hour)
#   node_count    spot nodes (about $0.04 an hour each)
#   fanout_nodes  most fan-out worker nodes, 8 vCPU spot, autoscaled from
#                 zero (only while worker pods wait; about $0.07 an hour each)
gke_enabled  = false
mesh_enabled = false
node_count   = 1
fanout_nodes = 3
