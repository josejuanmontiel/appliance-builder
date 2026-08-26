#!/usr/bin/env bash
set -e

# ==============================================================================
# Alpine Appliance Builder — Orquestador Declarativo y Modular
# ==============================================================================

SCRIPT_DIR="$(cd "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")" && pwd)"
WORK_DIR="${SCRIPT_DIR}/work"
OUTPUT_DIR="${SCRIPT_DIR}/output"
CACHE_DIR="${SCRIPT_DIR}/cache"
PAYLOAD_DIR="${SCRIPT_DIR}/app-payload"
CUSTOM_APKS_DIR="${WORK_DIR}/custom-apks"

CONFIG_FILE="${SCRIPT_DIR}/config.default.env"
PAYLOAD_CONFIG="${PAYLOAD_DIR}/config/wifi.env"

# Cargar configuraciones base
[ -f "$CONFIG_FILE" ] && source "$CONFIG_FILE"
[ -f "$PAYLOAD_CONFIG" ] && source "$PAYLOAD_CONFIG"

mkdir -p "${WORK_DIR}" "${OUTPUT_DIR}" "${CACHE_DIR}" "${CUSTOM_APKS_DIR}"

# Parsear argumentos de línea de comandos
REBUILD_BUILDER=false
RUN_IN_DOCKER=true
RECIPE_FILE=""
CLI_APKS=()

while [[ $# -gt 0 ]]; do
    case "$1" in
        --recipe)
            RECIPE_FILE="$(readlink -f "$2")"
            shift 2
            ;;
        --apk)
            CLI_APKS+=("$2")
            shift 2
            ;;
        --board)
            TARGET_BOARD="$2"
            shift 2
            ;;
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
            echo "Opciones Declarativas y de Paquetes:"
            echo "  --recipe <RECIPE.yaml>    Construir a partir de una receta declarativa"
            echo "  --apk <RUTA_O_URL>        Inyectar un paquete APK (puede repetirse)"
            echo "  --payload <DIR>           Directorio de payload personalizado (def: ./app-payload)"
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

# Si se pasó una receta declarativa YAML, procesarla
if [ -n "$RECIPE_FILE" ] && [ -f "$RECIPE_FILE" ]; then
    echo ">> Procesando receta declarativa: ${RECIPE_FILE}..."
    
    python3 - <<EOF
import yaml, os, urllib.request, shutil

with open("${RECIPE_FILE}", "r") as f:
    recipe = yaml.safe_load(f) or {}

app = recipe.get("appliance", {})
pkgs = recipe.get("packages", {})
net = recipe.get("network", {})

# Escribir variables de entorno de la receta
env_lines = []
if "name" in app: env_lines.append(f'APPLIANCE_NAME="{app["name"]}"')
if "board" in app: env_lines.append(f'TARGET_BOARD="{app["board"]}"')
if "hostname" in app: env_lines.append(f'HOSTNAME="{app["hostname"]}"')
if "boot_label" in app: env_lines.append(f'BOOT_LABEL="{app["boot_label"]}"')
if "ip" in net: env_lines.append(f'STATIC_IP="{net["ip"]}"')
if "netmask" in net: env_lines.append(f'STATIC_NETMASK="{net["netmask"]}"')
if "gateway" in net: env_lines.append(f'STATIC_GATEWAY="{net["gateway"]}"')
if "dns" in net: env_lines.append(f'STATIC_DNS="{net["dns"]}"')

with open("${WORK_DIR}/recipe.env", "w") as f:
    f.write("\n".join(env_lines) + "\n")

# Escribir paquetes alpine adicionales
alpine_pkgs = pkgs.get("alpine", [])
if alpine_pkgs:
    with open("${WORK_DIR}/extra-apks.txt", "w") as f:
        f.write("\n".join(alpine_pkgs) + "\n")

# Procesar paquetes APK (locales o URLs)
apk_list = pkgs.get("apks", [])
recipe_dir = os.path.dirname(os.path.abspath("${RECIPE_FILE}"))
for item in apk_list:
    item = item.strip()
    if not item: continue
    if item.startswith("http://") or item.startswith("https://"):
        filename = os.path.basename(item.split("?")[0])
        dest = os.path.join("${CUSTOM_APKS_DIR}", filename)
        print(f"  Descargando APK: {item} -> {filename}")
        urllib.request.urlretrieve(item, dest)
    else:
        target_path = item
        if not os.path.isabs(target_path):
            candidate = os.path.normpath(os.path.join(recipe_dir, item))
            if os.path.isfile(candidate):
                target_path = candidate
        if os.path.isfile(target_path):
            print(f"  Copiando APK local: {target_path}")
            shutil.copy(target_path, "${CUSTOM_APKS_DIR}")
        else:
            print(f"  AVISO: Archivo APK local no encontrado: {target_path}")
EOF

    [ -f "${WORK_DIR}/recipe.env" ] && source "${WORK_DIR}/recipe.env"
fi

# Procesar APKs pasados por CLI
for apk in "${CLI_APKS[@]}"; do
    if [[ "$apk" =~ ^https?:// ]]; then
        fname=$(basename "${apk%%\?*}")
        echo "  Descargando APK CLI: $apk -> $fname"
        curl -fSL -o "${CUSTOM_APKS_DIR}/$fname" "$apk"
    elif [ -f "$apk" ]; then
        echo "  Copiando APK CLI: $apk"
        cp "$apk" "${CUSTOM_APKS_DIR}/"
    fi
done

echo "============================================================"
echo "  Alpine Appliance Builder (Motor Declarativo)              "
echo "  Appliance: ${APPLIANCE_NAME} | Target: ${TARGET_BOARD}"
echo "  APKs custom detectados: $(ls "${CUSTOM_APKS_DIR}"/*.apk 2>/dev/null | wc -l)"
echo "============================================================"

# Crear archivo de configuración runtime
RUNTIME_ENV="${WORK_DIR}/config.runtime.env"

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
