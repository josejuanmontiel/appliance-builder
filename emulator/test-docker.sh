#!/usr/bin/env bash
set -e

# ==============================================================================
# Simulador Docker para Alpine Appliance
# ==============================================================================

SCRIPT_DIR="$(cd "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")" && pwd)"
BUILDER_DIR="$(dirname "$SCRIPT_DIR")"
OUTPUT_DIR="${BUILDER_DIR}/output"

TARBALL=$(ls "${OUTPUT_DIR}"/*.tar.gz 2>/dev/null | head -n 1)

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
    -p 9000:9000 \
    -p 3478:3478/udp \
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
    tar -xzf /media/mmcblk0p1/localhost.apkovl.tar.gz -C / 2>/dev/null || true
    
    echo '  [3/4] Instalando paquetes APK offline...'
    apk add --allow-untrusted /media/mmcblk0p1/apks/armhf/*.apk >/dev/null 2>&1 || true
    
    echo '  [4/4] Inicializando entorno OpenRC...'
    mkdir -p /run/openrc /var/log
    touch /run/openrc/softlevel
"

echo ">> Ejecutando script de arranque y persistencia..."
docker exec "$CONTAINER_NAME" /bin/bash -c "
    /etc/init.d/appliance-setup start 2>/dev/null || true
"

echo ">> Iniciando servicios OpenRC del payload..."
docker exec "$CONTAINER_NAME" /bin/bash -c "
    for svc in /etc/init.d/*; do
        svc_name=\$(basename \"\$svc\")
        case \"\$svc_name\" in
            appliance-setup|networking|wpa_supplicant|modloop|hwdrivers|devfs|dmesg|mdev|bootmisc|hostname|syslog|swap)
                ;;
            *)
                echo \"Iniciando \$svc_name...\"
                \"\$svc\" start 2>/dev/null || true
                ;;
        esac
    done
"

echo ">> Esperando 5 segundos para inicialización de servicios..."
sleep 5

echo -e "\n============================================================"
echo "  Estado de los Procesos en el Simulador:                   "
echo "============================================================"
docker exec "$CONTAINER_NAME" ps aux

echo -e "\n============================================================"
echo "  Probando Endpoint HTTP (Port 9000):                       "
echo "============================================================"
docker exec "$CONTAINER_NAME" curl -k -s -o /dev/null -w "HTTP Status: %{http_code}\n" http://127.0.0.1:9000/ || echo "Servicio no iniciado o no escuchando en :9000"

echo -e "\n✔ Simulación completada."
