# Components

## Entrypoint for each Kubernetes container

Each node in an RKE cluster gets a container named `service-sidekick` created. It will stay in `Created` state, it will not show up in `docker ps` output (only in `docker ps -a`). The purpose of this container is to share its volume to RKE Kubernetes containers. Each RKE Kubernetes container is started with `--volumes from=service-sidekick` so each container can use files from that volume. The volume defined in the `Dockerfile` (`package/Dockerfile`) is `/opt/rke-tools`. The default entrypoint for RKE Kubernetes containers is `/opt/rke-tools/entrypoint.sh` (https://github.com/rancher/rke/blob/v1.4.8/cluster/plan.go#L46).

The comments in `entrypoint.sh` should explain what is being done. This also includes the use of `cloud-provider.sh`.

## etcd snapshots

The compiled binary from `main.go` is `rke-etcd-backup`. This binary is used to operate etcd snapshots, used by the RKE etcd containers on nodes with the etcd role.

The binary has one subcommand (`etcd-backup`) with multiple options, which are described below.

### save

Used in container to create snapshots in interval (`etcd-rolling-snapshots`) or during ad-hoc snapshots (`etcd-snapshot-once`) using the `--once` flag.

### delete

Used to delete created snapshots locally or uploaded to S3

### download

Used to download snapshots from S3 or download snapshots from other etcd nodes. Each node takes its own snapshot but only one node's snapshot is selected for restore. The selected node's snapshot is served in a container for the remaining etcd nodes to download, to make sure they are all using the exact same snapshot source.

### serve

Used to serve the selected snapshot for restore to the other etcd nodes. This will create an HTTPS endpoint for the other nodes to download the snapshot archive that can be used for the restore.

### extractstatefile

Used to extract the RKE statefile from an etcd snapshot archive. Starting with RKE v1.1.4, the statefile got included in the snapshot archive to make sure the correct information was available to restore (like Kubernetes certificates, reference: https://github.com/rancher/rke/issues/1336). This is used when a restore is requested and the statefile is needed.

## Container to run the proxy between `kubelet` and `kube-apiserver` in RKE clusters

The kubelet connects to the `kube-apiserver` using a container named `nginx-proxy`. The `nginx-proxy` container runs on the host network and nginx listens on port 6443. The `nginx-proxy` container is configured with the environment variable `CP_HOSTS` which contains the IP addresses of all controlplane nodes in the cluster. The container will use `confd` to dynamically generate the `/etc/nginx/nginx.conf` before starting nginx itself. The file used by confd can be found in `conf.d/nginx.toml`, the template used by confd can be found in `templates/nginx.tmpl`.

## Container to deploy Kubernetes certificates needed by the nodes in RKE clusters

`cert-deployer` is a bash script helper to deploy certificates to RKE Kubernetes nodes using environment variables. RKE will set the environment variables and create the container with `cert-deployer` as command. The container will create the certificate files in the correct location.

## Container to deploy portmap CNI plugin for Weave

Weave needs the `portmap` CNI plugin starting with v2.5.0, `weave-plugins-cni.sh` is a bash script helper to deploy this CNI plugin

## Container to make mounts shared (DEPRECATED)

`share-mnt` was created to make mounts shared, mostly used to make RKE/Rancher compatible with running on boot2docker. Error shown was `[workerPlane] Failed to bring up Worker Plane: Failed to start [kubelet] container on host [192.168.59.104]: Error response from daemon: linux mounts: Path /var/lib/kubelet is mounted on / but it is not a shared mount`. See https://github.com/rancher/rancher/issues/12687 for more details. This is no longer in use.
