#!/usr/bin/env bash
set -e

# ==============================================================================
# Paso 2: Preparar modloop y payload de la aplicación
#
# ARQUITECTURA ALPINE DISKLESS:
#   - modloop-rpi = módulos del kernel + firmware WiFi (Alpine original, FIRMADO)
#                  NO se modifica: la firma criptográfica es verificada por el
#                  servicio modloop en cada arranque. Si se reconstruye con
#                  mksquashfs se pierde la firma y WiFi deja de funcionar.
#
#   - apkovl     = configuración + binarios custom (03_build_apkovl.sh)
#                  Los binarios del payload van aquí, NO en el modloop.
# ==============================================================================

WORKDIR="${1:-/workspace/work}"
PAYLOAD_DIR="${2:-/workspace/app-payload}"
CONFIG_ENV="${3:-/workspace/config.env}"

[ -f "$CONFIG_ENV" ] && source "$CONFIG_ENV"

BOOTFS="${WORKDIR}/bootfs"
ORIG_MODLOOP="${BOOTFS}/boot/modloop-rpi"

echo ">> [2/4] Verificando modloop original de Alpine (NO se modifica)..."

if [ ! -f "${ORIG_MODLOOP}" ]; then
    echo "ERROR: modloop-rpi no encontrado en ${ORIG_MODLOOP}" >&2
    exit 1
fi

MODLOOP_SIZE=$(du -h "${ORIG_MODLOOP}" | cut -f1)
echo "✔ modloop-rpi original preservado (${MODLOOP_SIZE}) — firma Alpine intacta"
echo "  Los binarios del payload se inyectan en el apkovl (paso 3)."
