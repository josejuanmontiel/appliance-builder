#!/usr/bin/env bash
set -e

# ==============================================================================
# Alpine Appliance Builder — Orquestador Principal
# ==============================================================================

SCRIPT_DIR="$(cd "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")" && pwd)"
WORK_DIR="${SCRIPT_DIR}/work"
OUTPUT_DIR="${SCRIPT_DIR}/output"
CACHE_DIR="${SCRIPT_DIR}/cache"
PAYLOAD_DIR="${SCRIPT_DIR}/app-payload"

CONFIG_FILE="${SCRIPT_DIR}/config.default.env"
PAYLOAD_CONFIG="${PAYLOAD_DIR}/config/wifi.env"

# Cargar configuraciones base
[ -f "$CONFIG_FILE" ] && source "$CONFIG_FILE"
[ -f "$PAYLOAD_CONFIG" ] && source "$PAYLOAD_CONFIG"

# Parsear argumentos de línea de comandos
REBUILD_BUILDER=false
RUN_IN_DOCKER=true

while [[ $# -gt 0 ]]; do
    case "$1" in
        --payload)
            PAYLOAD_DIR="$(readlink -f "$2")"
            shift 2
            ;;
        --wifi-ssid)
            WIFI_SSID="$2"
            shift 2
            ;;
        --wifi-pass)
            WIFI_PASS="$2"
            shift 2
            ;;
        --ip)
            STATIC_IP="$2"
            shift 2
            ;;
        --netmask)
            STATIC_NETMASK="$2"
            shift 2
            ;;
        --gateway)
            STATIC_GATEWAY="$2"
            shift 2
            ;;
        --root-pass)
            ROOT_PASSWORD="$2"
            shift 2
            ;;
        --name)
            APPLIANCE_NAME="$2"
            shift 2
            ;;
        --rebuild)
            REBUILD_BUILDER=true
            shift
            ;;
        --no-docker)
            RUN_IN_DOCKER=false
            shift
            ;;
        --help|-h)
            echo "Uso: $0 [opciones]"
            echo ""
            echo "Opciones del Payload:"
            echo "  --payload <DIR>           Directorio del payload personalizado (def: ./app-payload)"
            echo ""
            echo "Opciones de Red y Acceso:"
            echo "  --wifi-ssid <SSID>        Nombre de la red WiFi (def: '${WIFI_SSID}')"
            echo "  --wifi-pass <PASSWORD>    Contraseña WiFi"
            echo "  --ip <IP_ESTATICA>        Dirección IP estática (def: ${STATIC_IP})"
            echo "  --netmask <MASCARA>       Máscara de red (def: ${STATIC_NETMASK})"
            echo "  --gateway <PUERTA_ENLACE> Gateway/Router (def: ${STATIC_GATEWAY})"
            echo "  --root-pass <PASSWORD>    Contraseña para el usuario root"
            echo ""
            echo "Opciones del Appliance:"
            echo "  --name <NOMBRE>           Nombre del artefacto resultante (def: ${APPLIANCE_NAME})"
            echo "  --rebuild                 Reconstruir imagen Docker del builder"
            echo "  --no-docker               Ejecutar directamente en el host (requiere Alpine)"
            echo ""
            exit 0
            ;;
        *)
            echo "Opción desconocida: $1 (usa --help para ver las opciones)"
            exit 1
            ;;
    esac
done

if [ ! -d "$PAYLOAD_DIR" ]; then
    echo "ERROR: Directorio de payload no encontrado: ${PAYLOAD_DIR}" >&2
    exit 1
fi

echo "============================================================"
echo "  Alpine Appliance Builder                                  "
echo "  Appliance: ${APPLIANCE_NAME} | Target: ${TARGET_BOARD}"
echo "  Payload:   ${PAYLOAD_DIR}"
echo "============================================================"

# Crear archivo de configuración runtime
RUNTIME_ENV="${WORK_DIR}/config.runtime.env"
mkdir -p "${WORK_DIR}" "${OUTPUT_DIR}" "${CACHE_DIR}"

cat <<EOF > "${RUNTIME_ENV}"
ALPINE_VERSION="${ALPINE_VERSION}"
ALPINE_BRANCH="${ALPINE_BRANCH}"
ALPINE_ARCH="${ALPINE_ARCH}"
ALPINE_MIRROR="${ALPINE_MIRROR}"
APPLIANCE_NAME="${APPLIANCE_NAME}"
TARGET_BOARD="${TARGET_BOARD}"
WIFI_SSID="${WIFI_SSID}"
WIFI_PASS="${WIFI_PASS}"
STATIC_IP="${STATIC_IP}"
STATIC_NETMASK="${STATIC_NETMASK}"
STATIC_GATEWAY="${STATIC_GATEWAY}"
STATIC_DNS="${STATIC_DNS}"
HOSTNAME="${HOSTNAME}"
ROOT_PASSWORD="${ROOT_PASSWORD}"
BOOT_LABEL="${BOOT_LABEL:-P2PT_BOOT}"
BOOT_PART_MB="${BOOT_PART_MB}"
DATA_PART_MB="${DATA_PART_MB}"
GPU_MEM="${GPU_MEM}"
EOF

if [ "$RUN_IN_DOCKER" = true ]; then
    BUILDER_IMAGE="alpine-appliance-builder:latest"
    
    if [ "$REBUILD_BUILDER" = true ] || [ -z "$(docker images -q "$BUILDER_IMAGE" 2>/dev/null)" ]; then
        echo ">> Construyendo imagen Docker del builder..."
        docker build -t "$BUILDER_IMAGE" -f "${SCRIPT_DIR}/Dockerfile.builder" "${SCRIPT_DIR}"
    fi

    # Registrar binfmt para emulación ARM si es necesario
    docker run --rm --privileged multiarch/qemu-user-static --reset -p yes >/dev/null 2>&1 || true

    echo ">> Ejecutando construcción dentro del contenedor..."
    docker run --rm \
        -v "${SCRIPT_DIR}:/workspace" \
        -v "${PAYLOAD_DIR}:/payload:ro" \
        -e CONFIG_ENV="/workspace/work/config.runtime.env" \
        "$BUILDER_IMAGE" \
        /bin/bash -c "
            set -e
            chmod +x /workspace/core/*.sh
            /workspace/core/01_fetch_alpine_base.sh /workspace/work /workspace/output /workspace/cache /workspace/work/config.runtime.env /payload
            /workspace/core/02_build_modloop.sh /workspace/work /payload /workspace/work/config.runtime.env
            /workspace/core/03_build_apkovl.sh /workspace/work /payload /workspace/work/config.runtime.env
            /workspace/core/04_build_disk_image.sh /workspace/work /workspace/output /workspace/work/config.runtime.env
        "
else
    chmod +x "${SCRIPT_DIR}"/core/*.sh
    "${SCRIPT_DIR}/core/01_fetch_alpine_base.sh" "${WORK_DIR}" "${OUTPUT_DIR}" "${CACHE_DIR}" "${RUNTIME_ENV}" "${PAYLOAD_DIR}"
    "${SCRIPT_DIR}/core/02_build_modloop.sh" "${WORK_DIR}" "${PAYLOAD_DIR}" "${RUNTIME_ENV}"
    "${SCRIPT_DIR}/core/03_build_apkovl.sh" "${WORK_DIR}" "${PAYLOAD_DIR}" "${RUNTIME_ENV}"
    "${SCRIPT_DIR}/core/04_build_disk_image.sh" "${WORK_DIR}" "${OUTPUT_DIR}" "${RUNTIME_ENV}"
fi

echo -e "\n✔ Construcción finalizada con éxito. Artefactos disponibles en: ${OUTPUT_DIR}/"
ls -lh "${OUTPUT_DIR}"
