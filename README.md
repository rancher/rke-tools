# rke-tools

## About

The container image `rancher/rke-tools` is used in Kubernetes clusters built by RKE (`rancher/rke`) as:

- Entrypoint for each k8s container created by RKE (`entrypoint.sh`, `cloud-provider.sh`)
- Container to operate etcd snapshots in RKE clusters (create/remove/restore) (`main.go`)
- Container to run the proxy between `kubelet` and `kube-apiserver` in RKE clusters (`nginx-proxy`, `conf.d/nginx.toml`)
- Container to deploy Kubernetes certificates needed by the nodes in RKE clusters (`cert-deployer`)
- Container to deploy Weave loopback/portmap plugin (`weave-plugins-cni.sh`)
- Container to make mounts shared (`share-root.sh`, deprecated, used for boot2docker)

See [components.md](./docs/components.md) for a more detailed explanation of each component.

## Building

Running `make` should run the default target (`ci`), which runs all the scripts needed to built a binary and container. It uses `rancher/dapper` as build wrapper. You can run each steps separately if you want to skip some of the defaults, for example `make build`.

## Testing

To test your newly built image, you need to make the image available on a Docker registry that is available to your RKE cluster nodes or import the image manually to your RKE cluster nodes (RKE will look for images locally available before pulling from the registry). Now you can use the following configuration in your `cluster.yml` file to use your new image in testing:

```yaml
system_images:
  alpine: your_name/rke-tools:your_tag
  nginx_proxy: your_name/rke-tools:your_tag
  cert_downloader: your_name/rke-tools:your_tag
  kubernetes_services_sidecar: your_name/rke-tools:your_tag
```
