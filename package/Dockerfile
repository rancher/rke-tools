FROM nginx:1.13.6-alpine

LABEL maintainer "Rancher Labs <support@rancher.com>"
RUN apk -U --no-cache add bash curl wget ca-certificates \
    && mkdir -p /opt/rke/bin /etc/confd \
    && curl -sLf https://github.com/kelseyhightower/confd/releases/download/v0.12.0-alpha3/confd-0.12.0-alpha3-linux-amd64 > /usr/bin/confd \
    && chmod +x /usr/bin/confd \
    && curl -sLf https://get.docker.com/builds/Linux/x86_64/docker-1.12.3.tgz | tar xvzf - -C /opt/rke/bin --strip-components=1 docker/docker \
    && chmod +x /opt/rke/bin/docker \
    && apk del curl

RUN mkdir -p /opt/cni/bin
RUN wget -q -O - https://github.com/containernetworking/cni/releases/download/v0.4.0/cni-amd64-v0.4.0.tgz | tar xzf - -C /tmp

RUN wget -q -O - https://github.com/coreos/etcd/releases/download/v3.0.17/etcd-v3.0.17-linux-amd64.tar.gz | tar xzf - -C /tmp && \
    mv /tmp/etcd-*/etcdctl /usr/local/bin/etcdctl && \
    rm -rf /tmp/etcd-* && rm -f /etcd-*.tar.gz && \
    apk del wget

COPY templates /etc/confd/templates/
COPY conf.d /etc/confd/conf.d/
COPY cert-deployer nginx-proxy /usr/bin/
COPY entrypoint.sh cloud-provider.sh weave-loopback-cni.sh /opt/rke/
COPY rke-etcd-backup /opt/rke

VOLUME /opt/rke
CMD ["/bin/bash"]
