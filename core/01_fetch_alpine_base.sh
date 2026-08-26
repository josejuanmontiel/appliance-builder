#!/usr/bin/env bash
set -e

# ==============================================================================
# Paso 1: Descargar y preparar la base oficial certificada de Alpine Linux RPi
# ==============================================================================

WORKDIR="${1:-/workspace/work}"
OUTPUT_DIR="${2:-/workspace/output}"
CACHE_DIR="${3:-/workspace/cache}"
CONFIG_ENV="${4:-/workspace/config.env}"
PAYLOAD_DIR="${5:-/workspace/app-payload}"

[ -f "$CONFIG_ENV" ] && source "$CONFIG_ENV"

BOOTFS="${WORKDIR}/bootfs"
mkdir -p "${BOOTFS}" "${CACHE_DIR}"

BASE_TARBALL="alpine-rpi-${ALPINE_VERSION}-${ALPINE_ARCH}.tar.gz"
BASE_URL="${ALPINE_MIRROR}/${ALPINE_BRANCH}/releases/${ALPINE_ARCH}/${BASE_TARBALL}"

echo ">> [1/4] Obteniendo release oficial de Alpine Linux (${BASE_TARBALL})..."

if [ ! -f "${CACHE_DIR}/${BASE_TARBALL}" ]; then
    echo "  Descargando desde ${BASE_URL}..."
    curl -fSL "${BASE_URL}" -o "${CACHE_DIR}/${BASE_TARBALL}" || {
        echo "Error: No se pudo descargar el release oficial de Alpine."
        exit 1
    }
else
    echo "  Usando archivo en caché: ${CACHE_DIR}/${BASE_TARBALL}"
fi

echo "  Extrayendo sistema base en ${BOOTFS}..."
# Limpiar bootfs para asegurar que el APKINDEX original firmado de Alpine
# siempre prevalezca sobre cualquier index regenerado de builds anteriores.
rm -rf "${BOOTFS}"
mkdir -p "${BOOTFS}"
tar -xzf "${CACHE_DIR}/${BASE_TARBALL}" -C "${BOOTFS}"

# Descargar paquetes adicionales requeridos por el payload / receta
EXTRA_APKS_FILE="${WORKDIR}/extra-apks.txt"
[ ! -f "$EXTRA_APKS_FILE" ] && EXTRA_APKS_FILE="${PAYLOAD_DIR}/config/extra-apks.txt"
if [ -f "$EXTRA_APKS_FILE" ]; then
    EXTRA_PKGS=$(grep -v '^#' "$EXTRA_APKS_FILE" | grep -v '^$' | tr '\n' ' ' || true)
    if [ -n "$EXTRA_PKGS" ]; then
        echo "  Descargando paquetes adicionales para el modloop: ${EXTRA_PKGS}..."
        TEMP_ROOTFS="${WORKDIR}/temp_rootfs"
        mkdir -p "${TEMP_ROOTFS}"

        # Descargar los .apk en un directorio SEPARADO del repo principal
        # IMPORTANTE: NO mezclar con apks/ ni regenerar su APKINDEX.
        # El APKINDEX original de Alpine 3.19.1 es firmado y necesario para que
        # el boot diskless pueda instalar alpine-base. Si lo regeneramos sin firmar,
        # APK no puede instalar la base del sistema y /sbin/init nunca se crea.
        EXTRA_PKGS_DIR="${WORKDIR}/extra-pkgs"
        mkdir -p "${EXTRA_PKGS_DIR}"
        apk.static --arch "$ALPINE_ARCH" \
            -X "${ALPINE_MIRROR}/${ALPINE_BRANCH}/main" \
            -X "${ALPINE_MIRROR}/${ALPINE_BRANCH}/community" \
            -U --allow-untrusted --root "${TEMP_ROOTFS}" --initdb add alpine-base >/dev/null 2>&1 || true
        apk.static --arch "$ALPINE_ARCH" \
            -X "${ALPINE_MIRROR}/${ALPINE_BRANCH}/main" \
            -X "${ALPINE_MIRROR}/${ALPINE_BRANCH}/community" \
            -U --allow-untrusted --root "${TEMP_ROOTFS}" \
            fetch -R -o "${EXTRA_PKGS_DIR}" ${EXTRA_PKGS} >/dev/null 2>&1 || true

        rm -rf "${TEMP_ROOTFS}"
    fi
fi

echo "✔ Base de Alpine Linux preparada con $(ls "${BOOTFS}/apks/${ALPINE_ARCH}/"*.apk 2>/dev/null | wc -l) paquetes offline."
