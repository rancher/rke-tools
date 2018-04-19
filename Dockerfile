FROM nginx:1.13.6-alpine

LABEL maintainer "Rancher Labs <support@rancher.com>"
RUN apk -U --no-cache add bash curl \
    && mkdir -p /opt/rke /etc/confd \
    && curl -sLf https://github.com/kelseyhightower/confd/releases/download/v0.12.0-alpha3/confd-0.12.0-alpha3-linux-amd64 > /usr/bin/confd \
    && chmod +x /usr/bin/confd \
    && apk del curl

COPY templates /etc/confd/templates/
COPY conf.d /etc/confd/conf.d/
COPY cert-deployer nginx-proxy /usr/bin/
COPY entrypoint.sh cloud-provider.sh /opt/rke/

VOLUME /opt/rke
CMD ["/bin/bash"]
