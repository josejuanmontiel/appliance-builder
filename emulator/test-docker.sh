#!/usr/bin/env bash
set -e

# ==============================================================================
# Simulador Docker para Alpine Appliance
# ==============================================================================

SCRIPT_DIR="$(cd "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")" && pwd)"
BUILDER_DIR="$(dirname "$SCRIPT_DIR")"
OUTPUT_DIR="${BUILDER_DIR}/output"

TARBALL="${1:-$(ls "${OUTPUT_DIR}"/*-box-rpi-zero.tar.gz 2>/dev/null | head -n 1)}"

if [ -z "$TARBALL" ] || [ ! -f "$TARBALL" ]; then
    echo "Error: No se encontró ningún tarball en ${OUTPUT_DIR}/"
    echo "Ejecuta primero ./build.sh para generar el appliance."
    exit 1
fi

echo "============================================================"
echo "  Simulador de Servicios Alpine Appliance (Docker)          "
echo "  Tarball: $(basename "$TARBALL")"
echo "============================================================"

# Registrar binfmt para emular binarios ARMv6/ARMv7
docker run --rm --privileged multiarch/qemu-user-static --reset -p yes >/dev/null 2>&1 || true

CONTAINER_NAME="alpine-appliance-sim-$$"

echo ">> Preparando contenedor de simulación..."
docker run --name "$CONTAINER_NAME" -d \
    --privileged \
    -p 8443:443 \
    -p 9000:9000 \
    -p 3478:3478/udp \
    --platform linux/arm/v6 \
    alpine:3.19 \
    tail -f /dev/null

cleanup() {
    echo ">> Deteniendo simulador..."
    docker rm -f "$CONTAINER_NAME" >/dev/null 2>&1 || true
}
trap cleanup EXIT

echo ">> Inyectando sistema, modloop y overlay en el simulador..."
docker exec "$CONTAINER_NAME" mkdir -p /appliance /media/mmcblk0p1 /mnt/data
docker cp "$TARBALL" "${CONTAINER_NAME}:/appliance/appliance.tar.gz"

docker exec "$CONTAINER_NAME" sh -c "
    set -e
    apk add --no-cache squashfs-tools openrc bash curl >/dev/null 2>&1
    tar -xzf /appliance/appliance.tar.gz -C /media/mmcblk0p1
    
    echo '  [1/4] Extrayendo modloop SquashFS...'
    unsquashfs -d /modloop /media/mmcblk0p1/boot/modloop-rpi >/dev/null 2>&1
    cp -r /modloop/* / 2>/dev/null || true
    
    echo '  [2/4] Extrayendo overlay de configuración...'
    mkdir -p /stage_apkovl
    tar -xzf /media/mmcblk0p1/localhost.apkovl.tar.gz -C /stage_apkovl
    cp -a /stage_apkovl/usr/. /usr/ 2>/dev/null || true
    cp -a /stage_apkovl/etc/. /etc/ 2>/dev/null || true
    rm -rf /stage_apkovl
    
    echo '  [3/4] Instalando paquetes APK offline...'
    apk add --allow-untrusted /media/mmcblk0p1/apks/armhf/*.apk >/dev/null 2>&1 || true
    echo '  [4/4] Inicializando entorno OpenRC...'
    mkdir -p /run/openrc /var/log
    touch /run/openrc/softlevel
"

# Inyectar ddns.txt si existe localmente
if [ -f "$BUILDER_DIR/ddns.txt" ]; then
    echo "  [+] Inyectando drop-in ddns.txt en /media/mmcblk0p1/..."
    docker cp "$BUILDER_DIR/ddns.txt" "${CONTAINER_NAME}:/media/mmcblk0p1/ddns.txt"
fi

echo ">> Ejecutando script de arranque y persistencia..."
docker exec "$CONTAINER_NAME" /bin/sh -c '
    BOOT_FAT="/media/mmcblk0p1"
    if [ -f "$BOOT_FAT/ddns.txt" ]; then
        echo "Leyendo $BOOT_FAT/ddns.txt..."
        DDNS_ID=$(grep -i "^ID=" "$BOOT_FAT/ddns.txt" | cut -d= -f2- | tr -d "\r\"" | sed "s/^[[:space:]]*//;s/[[:space:]]*$//")
        DDNS_TOKEN=$(grep -i "^TOKEN=" "$BOOT_FAT/ddns.txt" | cut -d= -f2- | tr -d "\r\"" | sed "s/^[[:space:]]*//;s/[[:space:]]*$//")
        DDNS_HUB=$(grep -i "^HUB=" "$BOOT_FAT/ddns.txt" | cut -d= -f2- | tr -d "\r\"" | sed "s/^[[:space:]]*//;s/[[:space:]]*$//")
        touch /etc/p2pt.env
        sed -i "/^DDNS_/d" /etc/p2pt.env
        echo "DDNS_APPLIANCE_ID=$DDNS_ID" >> /etc/p2pt.env
        echo "DDNS_SECRET_TOKEN=$DDNS_TOKEN" >> /etc/p2pt.env
        [ -n "$DDNS_HUB" ] && echo "DDNS_HUB_ENDPOINT=$DDNS_HUB" >> /etc/p2pt.env
        echo "Configurado DDNS para $DDNS_ID en /etc/p2pt.env"
    fi
'

# Inyectar binario p2pt-server actualizado si existe
if [ -f "$BUILDER_DIR/app-payload/bin/p2pt-server" ]; then
    echo "  [+] Inyectando binario ARM p2pt-server actualizado..."
    docker cp "$BUILDER_DIR/app-payload/bin/p2pt-server" "${CONTAINER_NAME}:/usr/bin/p2pt-server"
    docker exec "$CONTAINER_NAME" chmod +x /usr/bin/p2pt-server
fi

echo ">> Iniciando servicio p2pt..."
docker exec "$CONTAINER_NAME" /bin/sh -c '
    set -a
    [ -f /etc/p2pt.env ] && source /etc/p2pt.env
    set +a
    /usr/bin/p2pt-server &
'

sleep 8

echo -e "\n============================================================"
echo "  Estado de los Procesos en el Simulador:                   "
echo "============================================================"
docker exec "$CONTAINER_NAME" ps aux

echo -e "\n============================================================"
echo "  Logs de p2pt y appliance-setup:                           "
echo "============================================================"
docker exec "$CONTAINER_NAME" cat /media/mmcblk0p1/logs/p2pt.log 2>/dev/null || true
docker exec "$CONTAINER_NAME" cat /var/log/p2pt.log 2>/dev/null || true
docker exec "$CONTAINER_NAME" cat /var/log/p2pt.err 2>/dev/null || true
docker exec "$CONTAINER_NAME" cat /etc/p2pt.env 2>/dev/null || true

echo -e "\n✔ Simulación completada."
