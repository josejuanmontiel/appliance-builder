FROM alpine:3.19

# Instalar herramientas necesarias para manipular imágenes de disco, squashfs y particiones
RUN apk add --no-cache \
    bash \
    curl \
    tar \
    gzip \
    squashfs-tools \
    dosfstools \
    mtools \
    e2fsprogs \
    parted \
    util-linux \
    qemu-arm \
    openssl \
    ca-certificates \
    apk-tools-static

WORKDIR /workspace
