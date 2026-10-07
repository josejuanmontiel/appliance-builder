#!/usr/bin/env bash
set -e

# ==============================================================================
# Paso 1: Descargar y Preparar el Sistema Base de Alpine Linux (Oficial)
# ==============================================================================

WORKDIR="${1:-/workspace/work}"
OUTPUT_DIR="${2:-/workspace/output}"
CACHE_DIR="${3:-/workspace/cache}"
CONFIG_ENV="${4:-/workspace/config.env}"
PAYLOAD_DIR="${5:-/workspace/app-payload}"

[ -f "$CONFIG_ENV" ] && source "$CONFIG_ENV"

ALPINE_VERSION="${ALPINE_VERSION:-3.19.1}"
ALPINE_BRANCH="${ALPINE_BRANCH:-v3.19}"
ALPINE_ARCH="${ALPINE_ARCH:-armhf}"
ALPINE_MIRROR="${ALPINE_MIRROR:-https://dl-cdn.alpinelinux.org/alpine}"

BASE_TARBALL="alpine-rpi-${ALPINE_VERSION}-${ALPINE_ARCH}.tar.gz"
DOWNLOAD_URL="${ALPINE_MIRROR}/${ALPINE_BRANCH}/releases/${ALPINE_ARCH}/${BASE_TARBALL}"
BOOTFS="${WORKDIR}/bootfs"

echo ">> [1/4] Descargando y preparando imagen base de Alpine Linux (${ALPINE_VERSION} - ${ALPINE_ARCH})..."

mkdir -p "${CACHE_DIR}"
if [ ! -f "${CACHE_DIR}/${BASE_TARBALL}" ]; then
    echo "  Descargando ${BASE_TARBALL} desde mirror oficial..."
    curl -fSL -o "${CACHE_DIR}/${BASE_TARBALL}" "${DOWNLOAD_URL}"
else
    echo "  Usando tarball en caché: ${CACHE_DIR}/${BASE_TARBALL}"
fi

echo "  Extrayendo sistema base en ${BOOTFS}..."
# Limpiar bootfs para asegurar que el APKINDEX original firmado de Alpine
# siempre prevalezca sobre cualquier index regenerado de builds anteriores.
rm -rf "${BOOTFS}"
mkdir -p "${BOOTFS}"
tar -xzf "${CACHE_DIR}/${BASE_TARBALL}" -C "${BOOTFS}"

# Descargar paquetes adicionales requeridos por la receta declarativa o payload
EXTRA_APKS_FILE="${WORKDIR}/extra-apks.txt"
[ ! -f "$EXTRA_APKS_FILE" ] && EXTRA_APKS_FILE="${PAYLOAD_DIR}/config/extra-apks.txt"

if [ -f "$EXTRA_APKS_FILE" ]; then
    EXTRA_PKGS=$(grep -v '^#' "$EXTRA_APKS_FILE" | grep -v '^$' | tr '\n' ' ' || true)
    if [ -n "$EXTRA_PKGS" ]; then
        echo "  Descargando paquetes adicionales offline: ${EXTRA_PKGS}..."
        TEMP_ROOTFS="${WORKDIR}/temp_rootfs"
        mkdir -p "${TEMP_ROOTFS}"

        # Descargar los .apk en un directorio SEPARADO del repo principal
        # IMPORTANTE: NO mezclar con apks/ ni regenerar su APKINDEX.
        # El APKINDEX original de Alpine 3.19.1 es firmado y necesario para que
        # el boot diskless pueda instalar alpine-base. Si lo regeneramos sin firmar,
        # APK no puede instalar la base del sistema y /sbin/init nunca se crea.
        EXTRA_PKGS_DIR="${WORKDIR}/extra-pkgs-${ALPINE_ARCH}"
        rm -rf "${EXTRA_PKGS_DIR}"
        mkdir -p "${EXTRA_PKGS_DIR}"
        apk.static --arch "$ALPINE_ARCH" \
            -X "${ALPINE_MIRROR}/${ALPINE_BRANCH}/main" \
            -X "${ALPINE_MIRROR}/${ALPINE_BRANCH}/community" \
            --root "${TEMP_ROOTFS}" \
            --initdb \
            fetch \
            --output "${EXTRA_PKGS_DIR}" \
            --recursive \
            ${EXTRA_PKGS} >/dev/null 2>&1 || true

        rm -rf "${TEMP_ROOTFS}"

        # Copiar los paquetes descargados a bootfs/extra-apks para instalación offline en el appliance
        echo "  Copiando paquetes offline a ${BOOTFS}/extra-apks..."
        mkdir -p "${BOOTFS}/extra-apks"
        cp -r "${EXTRA_PKGS_DIR}/"* "${BOOTFS}/extra-apks/" 2>/dev/null || true
    fi
fi

echo "✔ Base de Alpine Linux preparada con $(ls "${BOOTFS}/apks/${ALPINE_ARCH}/"*.apk 2>/dev/null | wc -l) paquetes base y $(ls "${BOOTFS}/extra-apks/"*.apk 2>/dev/null | wc -l) paquetes offline."
